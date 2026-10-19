#!/bin/sh
#
#   curl -fsSL https://opendesk.sh/install.sh | sh
#
# Installs opendesk on this machine: writes /opt/opendesk/.env with fresh
# secrets, fetches the release's docker-compose.yml and Caddyfile, pulls the
# images and starts the stack behind automatic HTTPS. Nothing is compiled here.
#
#   sh install.sh --domain desk.example.com --email you@example.com
#   sh install.sh --wizard       ask about email, storage and inbound mail too
#   sh install.sh --proxy external   behind your own reverse proxy, without Caddy
#   sh install.sh --dry-run      print what it would write, touch nothing
#   sh install.sh --help         every flag, and the environment variable for each
#
# What it does, so you can check it before running it:
#
#   * checks for Docker. If it is missing on Linux it offers to run Docker's
#     own install script (downloaded to a file first, then run with sudo after
#     you say yes); on macOS it tells you to install Docker Desktop
#   * creates one directory (default /opt/opendesk) and writes only inside it
#   * generates every secret with openssl (or /dev/urandom), writes .env 0600
#   * resolves the release once and PINS it in .env. Never "latest"
#   * downloads docker-compose.yml, compose.external-proxy.yml, Caddyfile,
#     env.template and opendesk-ctl from that release and refuses any file
#     whose SHA256SUMS entry differs
#   * runs `docker compose pull` and `docker compose up -d`
#   * turns on automatic updates (AUTO_UPDATE in .env; --no-auto-update to
#     skip): a systemd timer, or a cron entry, runs `opendesk-ctl auto-update
#     run` each night. It needs root for the timer, so it asks for sudo
#
# It asks for sudo only for the steps that need it and says which. It is never
# meant to be piped into sudo. Running it again reconfigures in place: secrets
# are adopted, never regenerated (a new CREDENTIALS_KEY would make every stored
# mailbox credential unreadable), and a directory it did not create is refused
# unless you pass --force.
#
# Verify before running, if you would rather:
#
#   curl -fsSLO https://opendesk.sh/install.sh
#   curl -fsSLO https://opendesk.sh/install.sh.sha256
#   sha256sum -c install.sh.sha256        # macOS: shasum -a 256 -c install.sh.sha256
#   less install.sh && sh install.sh
#
# Everything is a function and the last line calls main, so a download cut off
# half way runs nothing.

set -eu

REPO="useopendesk/opendesk"
DOCS="https://docs.opendesk.sh"
DEFAULT_DIR="/opt/opendesk"
DEFAULT_REGISTRY="ghcr.io/useopendesk"
MARKER=".opendesk-install"
EXTERNAL_COMPOSE="compose.external-proxy.yml"

# ── Answers: each is a flag and an OPENDESK_* variable ──────────────────────
DIR="${OPENDESK_DIR:-$DEFAULT_DIR}"
DOMAIN="${OPENDESK_DOMAIN:-}"
EMAIL="${OPENDESK_EMAIL:-}"
VERSION="${OPENDESK_VERSION:-}"
REGISTRY="${OPENDESK_REGISTRY:-$DEFAULT_REGISTRY}"
ASSETS_URL="${OPENDESK_ASSETS_URL:-}"   # default: the GitHub release of VERSION
# caddy (the bundled Caddy, automatic HTTPS) | external (your own reverse proxy).
# Empty keeps what the install has, or caddy.
PROXY="${OPENDESK_PROXY:-}"
NO_START="${OPENDESK_NO_START:-0}"
WIZARD="${OPENDESK_WIZARD:-0}"
ASSUME_YES="${OPENDESK_YES:-0}"
DRY_RUN=0
PRINT_ENV=0
FORCE=0
USE_COLOR=1
B=""; DIM=""; R=""; GREEN=""; AMBER=""; RED=""

# Set by flags or the wizard; empty means "keep what .env has, or the default".
SIGNUP=""
MAIL_DRIVER_A=""
SMTP_HOST_A=""
SMTP_PORT_A=""
SMTP_USER_A=""
SMTP_PASS_A=""
SMTP_TLS_A=""
MAIL_FROM_A=""
STORAGE_A=""
SMTP_INBOUND_A=""
INBOUND_DOMAIN_A=""
AUTO_UPDATE_A=""
case "${OPENDESK_AUTO_UPDATE:-}" in
    1|true|yes) AUTO_UPDATE_A=true ;;
    0|false|no) AUTO_UPDATE_A=false ;;
esac
# AI: none | anthropic | openai | ollama. Keys are best passed as variables,
# not flags, so they stay out of shell history and the process list.
AI_MODE="${OPENDESK_AI:-}"
AI_KEY="${OPENDESK_AI_KEY:-}"
AI_URL="${OPENDESK_AI_URL:-}"
AI_MODEL_A="${OPENDESK_AI_MODEL:-}"

EXISTING=0
SUDO=""
DOCKER_SUDO=0
INTERACTIVE=0

# ── Output ─────────────────────────────────────────────────────────────────

setup_term() {
    if [ ! -t 1 ] || [ -n "${NO_COLOR:-}" ] || [ "${TERM:-}" = "dumb" ]; then
        USE_COLOR=0
    fi
    if [ "$USE_COLOR" = 1 ]; then
        B=$(printf '\033[1m'); DIM=$(printf '\033[2m'); R=$(printf '\033[0m')
        GREEN=$(printf '\033[32m'); AMBER=$(printf '\033[33m'); RED=$(printf '\033[31m')
    else
        B=""; DIM=""; R=""; GREEN=""; AMBER=""; RED=""
    fi
    # Questions are read from the terminal, not stdin: under `curl | sh`,
    # stdin is this script.
    if [ -r /dev/tty ] && [ -w /dev/tty ] && (exec </dev/tty) 2>/dev/null; then
        INTERACTIVE=1
    fi
    [ "$ASSUME_YES" = 1 ] && INTERACTIVE=0
    return 0
}

# Progress goes to stdout, except under --print-env, where stdout is the .env.
emit() {
    if [ "$PRINT_ENV" = 1 ]; then printf '%s\n' "$*" >&2; else printf '%s\n' "$*"; fi
}
say() { emit "$*"; }
info() { emit "  ${DIM}·${R} $*"; }
ok() { emit "  ${GREEN}✓${R} $*"; }
warn() { emit "  ${AMBER}!${R} $*"; }
note() { emit "    ${DIM}$*${R}"; }
step() { emit ""; emit "${B}$*${R}"; }

die() {
    printf '\n  %s✗%s %s\n' "$RED" "$R" "$1" >&2
    shift
    for _l in "$@"; do printf '    %s\n' "$_l" >&2; done
    printf '\n' >&2
    exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }

ask() {
    # ask VAR "Question" default
    _var=$1; _q=$2; _def=${3:-}
    if [ "$INTERACTIVE" = 0 ]; then
        eval "$_var=\$_def"
        return 0
    fi
    if [ -n "$_def" ]; then
        printf '  %s %s[%s]%s ' "$_q" "$DIM" "$_def" "$R" >/dev/tty
    else
        printf '  %s ' "$_q" >/dev/tty
    fi
    _a=""
    IFS= read -r _a </dev/tty || _a=""
    [ -n "$_a" ] || _a=$_def
    eval "$_var=\$_a"
}

ask_secret() {
    _var=$1; _q=$2
    if [ "$INTERACTIVE" = 0 ]; then
        eval "$_var=''"
        return 0
    fi
    printf '  %s ' "$_q" >/dev/tty
    _old=$(stty -g </dev/tty 2>/dev/null || true)
    stty -echo </dev/tty 2>/dev/null || true
    _a=""
    IFS= read -r _a </dev/tty || _a=""
    [ -n "$_old" ] && stty "$_old" </dev/tty 2>/dev/null
    printf '\n' >/dev/tty
    eval "$_var=\$_a"
}

confirm() {
    # confirm "Question" default(yes|no)
    if [ "$ASSUME_YES" = 1 ]; then return 0; fi
    if [ "$INTERACTIVE" = 0 ]; then
        [ "${2:-no}" = yes ]
        return
    fi
    if [ "${2:-no}" = yes ]; then _h="Y/n"; else _h="y/N"; fi
    printf '  %s %s[%s]%s ' "$1" "$DIM" "$_h" "$R" >/dev/tty
    _a=""
    IFS= read -r _a </dev/tty || _a=""
    case "$_a" in
        y|Y|yes|YES) return 0 ;;
        n|N|no|NO) return 1 ;;
        *) [ "${2:-no}" = yes ] ;;
    esac
}

# ── Platform and Docker ────────────────────────────────────────────────────

detect_platform() {
    OS=$(uname -s 2>/dev/null || echo unknown)
    ARCH=$(uname -m 2>/dev/null || echo unknown)
    case "$OS" in
        Linux|Darwin) ;;
        *) die "This installer supports Linux and macOS, and this is $OS." \
               "On Windows, install Docker Desktop and run it inside WSL." ;;
    esac
    case "$ARCH" in
        x86_64|amd64|aarch64|arm64) ;;
        *) die "Release images are published for amd64 and arm64, and this machine is $ARCH." \
               "Build from source instead: $DOCS/self-hosting/" ;;
    esac
}

need_sudo() {
    if [ "$(id -u)" = 0 ]; then SUDO=""; return 0; fi
    if have sudo; then SUDO="sudo"; return 0; fi
    return 1
}

check_docker() {
    if have docker && docker info >/dev/null 2>&1; then
        docker compose version >/dev/null 2>&1 ||
            die "Docker is running but the Compose plugin is missing." \
                "Install it: https://docs.docker.com/compose/install/linux/"
        return 0
    fi
    if have docker; then
        if need_sudo && [ -n "$SUDO" ] && $SUDO docker info >/dev/null 2>&1; then
            warn "Your user cannot talk to Docker, so this run uses sudo for docker."
            note "To drop that: sudo usermod -aG docker \$USER, then log in again."
            DOCKER_SUDO=1
            return 0
        fi
        die "Docker is installed but not answering." \
            "Start it:  sudo systemctl start docker"
    fi
    install_docker
}

# install_docker runs Docker's documented convenience script, only after
# saying so and being told yes. The script is saved to a file first; nothing
# is piped into a privileged shell.
install_docker() {
    if [ "$OS" = Darwin ]; then
        die "Docker is not installed." \
            "Install Docker Desktop, start it, and run this again:" \
            "https://docs.docker.com/desktop/setup/install/mac-install/"
    fi
    say ""
    warn "Docker is not installed. opendesk runs as containers, so it is required."
    note "The installer can download Docker's install script (https://get.docker.com)"
    note "and run it with sudo. Or install Docker yourself:"
    note "https://docs.docker.com/engine/install/"
    if ! confirm "Install Docker now?" no; then
        die "Docker is required." "Install it and run this again, or re-run with --yes to let this script install it."
    fi
    need_sudo || die "Installing Docker needs root, and sudo is not available." "Run this as root, or install Docker first."
    info "Downloading Docker's install script"
    curl -fsSL https://get.docker.com -o "$TMP/get-docker.sh" || die "Could not download https://get.docker.com"
    info "Installing Docker (uses sudo)"
    $SUDO sh "$TMP/get-docker.sh" >"$TMP/docker-install.log" 2>&1 || {
        tail -20 "$TMP/docker-install.log" >&2
        die "Docker's installer did not finish." "Install Docker by hand and run this again."
    }
    if have systemctl; then $SUDO systemctl enable --now docker >/dev/null 2>&1 || true; fi
    if [ -n "$SUDO" ]; then
        $SUDO usermod -aG docker "$(id -un)" 2>/dev/null || true
        note "Added you to the docker group; it applies from your next login."
        DOCKER_SUDO=1
    fi
    ok "Docker installed"
}

dockerc() {
    if [ "$DOCKER_SUDO" = 1 ]; then $SUDO docker "$@"; else docker "$@"; fi
}

composec() {
    (cd "$DIR" && dockerc compose "$@")
}

port_busy() {
    if have ss; then
        ss -ltn 2>/dev/null | grep -qE "[:.]$1[[:space:]]"
    elif have lsof; then
        lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
    else
        return 1
    fi
}

# ── Secrets ────────────────────────────────────────────────────────────────

gen_hex() {
    if have openssl; then
        openssl rand -hex "$1"
    else
        od -An -tx1 -N"$1" /dev/urandom | tr -d ' \n'
    fi
}

require_randomness() {
    if ! have openssl && [ ! -r /dev/urandom ]; then
        die "Neither openssl nor /dev/urandom is available, so secrets cannot be generated." \
            "Install openssl and run this again."
    fi
}

# ── Existing installs ──────────────────────────────────────────────────────

env_get() {
    [ -f "$1" ] || return 0
    sed -n "s/^${2}=//p" "$1" | tail -1
}

adopt_existing() {
    _env="$DIR/.env"
    [ -f "$_env" ] || return 0
    EXISTING=1
    AUTH_SECRET=$(env_get "$_env" AUTH_SECRET)
    CREDENTIALS_KEY=$(env_get "$_env" CREDENTIALS_KEY)
    SECRET_KEY_BASE=$(env_get "$_env" SECRET_KEY_BASE)
    NATS_TOKEN=$(env_get "$_env" NATS_TOKEN)
    INBOUND_SECRET=$(env_get "$_env" INBOUND_SECRET)
    POSTGRES_PASSWORD=$(env_get "$_env" POSTGRES_PASSWORD)
    if [ -z "$DOMAIN" ]; then DOMAIN=$(env_get "$_env" OPENDESK_DOMAIN); fi
    if [ -z "$VERSION" ]; then VERSION=$(env_get "$_env" OPENDESK_VERSION); fi
    if [ -z "$EMAIL" ]; then
        EMAIL=$(env_get "$_env" CADDY_GLOBAL_OPTIONS | sed -n 's/^email //p')
    fi
    if [ -z "$PROXY" ]; then
        case ":$(env_get "$_env" COMPOSE_FILE):" in
            *":$EXTERNAL_COMPOSE:"*) PROXY=external ;;
        esac
    fi
    return 0
}

ensure_secrets() {
    [ -n "${AUTH_SECRET:-}" ] || AUTH_SECRET=$(gen_hex 32)
    [ -n "${CREDENTIALS_KEY:-}" ] || CREDENTIALS_KEY=$(gen_hex 32)
    [ -n "${SECRET_KEY_BASE:-}" ] || SECRET_KEY_BASE=$(gen_hex 48)
    [ -n "${NATS_TOKEN:-}" ] || NATS_TOKEN=$(gen_hex 24)
    [ -n "${INBOUND_SECRET:-}" ] || INBOUND_SECRET=$(gen_hex 24)
    [ -n "${POSTGRES_PASSWORD:-}" ] || POSTGRES_PASSWORD=$(gen_hex 20)
    return 0
}

# ── Questions ──────────────────────────────────────────────────────────────

valid_domain() {
    case "$1" in
        localhost|:[0-9]*) return 0 ;;
        *[!A-Za-z0-9.-]*|.*|*.|*..*|"") return 1 ;;
        *.*) return 0 ;;
        *) return 1 ;;
    esac
}

ask_basics() {
    if [ -z "$DOMAIN" ]; then
        if [ "$INTERACTIVE" = 1 ]; then
            say ""
            if [ "$PROXY" = external ]; then
                note "The hostname people will open, e.g. desk.example.com. Your reverse"
                note "proxy serves it over https and forwards it here."
            else
                note "The hostname people will open, e.g. desk.example.com. Its DNS must point"
                note "at this machine with ports 80 and 443 open, so Caddy can get a certificate."
                note "Use localhost to try it on this machine."
            fi
        fi
        ask DOMAIN "Domain:" "localhost"
    fi
    valid_domain "$DOMAIN" || die "\"$DOMAIN\" is not a hostname." "Pass one with --domain desk.example.com"
    if [ "$PROXY" = external ]; then
        case "$DOMAIN" in
            :*) die "With --proxy external, --domain is the public hostname your proxy serves, e.g. desk.example.com" ;;
        esac
        return 0
    fi
    case "$DOMAIN" in
        localhost|:*) ;;
        *)
            if [ -z "$EMAIL" ] && [ "$INTERACTIVE" = 1 ]; then
                ask EMAIL "Email for certificate notices (optional):" ""
            fi
            ;;
    esac
    return 0
}

wizard() {
    [ "$INTERACTIVE" = 1 ] || {
        warn "The wizard needs a terminal; using flags and defaults instead."
        return 0
    }
    step "Platform email"
    note "Verification, password reset and invitation emails. Without SMTP they are"
    note "printed to the opendesk log, which is fine for trying it out."
    if confirm "Send email through an SMTP server?" no; then
        MAIL_DRIVER_A=smtp
        ask SMTP_HOST_A "SMTP host:" ""
        ask SMTP_PORT_A "SMTP port:" "587"
        ask SMTP_TLS_A "TLS (starttls, tls, none):" "starttls"
        ask SMTP_USER_A "SMTP username:" ""
        ask_secret SMTP_PASS_A "SMTP password:"
        ask MAIL_FROM_A "From address:" "opendesk <notifications@$DOMAIN>"
    else
        MAIL_DRIVER_A=log
    fi

    step "Attachments"
    if confirm "Store attachments in a bundled S3-compatible store instead of a volume?" no; then
        STORAGE_A=minio
    else
        STORAGE_A=local
    fi

    step "Inbound email"
    note "Mailboxes can forward to <token>@<inbound domain>. To receive that mail"
    note "directly, this host needs port 25 open and an MX record for the domain."
    ask INBOUND_DOMAIN_A "Inbound domain:" "in.$DOMAIN"
    if confirm "Receive mail on port 25 here?" no; then
        SMTP_INBOUND_A=1
    else
        SMTP_INBOUND_A=0
    fi

    step "AI"
    note "Drafts, summaries and answers from the knowledge base. Optional; nothing"
    note "is sent anywhere without a provider. Choose: none, anthropic (an API key),"
    note "openai (any OpenAI-compatible URL, e.g. your own Ollama or vLLM), or"
    note "ollama (models run in a container on this machine; 8 GB RAM or more)."
    ask AI_MODE "AI provider:" "${AI_MODE:-none}"
    case "$AI_MODE" in
        anthropic)
            [ -n "$AI_KEY" ] || ask_secret AI_KEY "Anthropic API key:"
            ;;
        openai)
            [ -n "$AI_URL" ] || ask AI_URL "Base URL (OpenAI-compatible):" "http://localhost:11434/v1"
            [ -n "$AI_KEY" ] || ask_secret AI_KEY "API key (enter for none):"
            [ -n "$AI_MODEL_A" ] || ask AI_MODEL_A "Model:" ""
            ;;
        ollama)
            [ -n "$AI_MODEL_A" ] || ask AI_MODEL_A "Model to pull:" "llama3.1:8b"
            ;;
    esac

    step "Sign-ups"
    if confirm "Allow anyone to sign up? (Close it after you create your account)" yes; then
        SIGNUP=true
    else
        SIGNUP=false
    fi

    step "Updates"
    note "Each night opendesk-ctl can install a new release of the same major version:"
    note "it backs up first and goes back to the version before if the new one fails."
    if confirm "Install new releases automatically?" yes; then
        AUTO_UPDATE_A=true
    else
        AUTO_UPDATE_A=false
    fi
    return 0
}

# ── Release ────────────────────────────────────────────────────────────────

resolve_version() {
    if [ -n "$VERSION" ] && [ "$VERSION" != "latest" ]; then
        return 0
    fi
    info "Finding the newest release"
    _json=$(curl -fsSL -H 'Accept: application/vnd.github+json' \
        "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null || true)
    VERSION=$(printf '%s' "$_json" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
    [ -n "$VERSION" ] || die "Could not read the newest release from GitHub." \
        "Pass the version you want:  --version v1.0.0" \
        "Releases: https://github.com/$REPO/releases"
    return 0
}

sha256_of() {
    if have sha256sum; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# fetch_assets downloads the release's files into $TMP and checks each against
# the release's SHA256SUMS. A file that is missing from it, or differs, stops
# the install before anything is written.
fetch_assets() {
    _base=${ASSETS_URL:-https://github.com/$REPO/releases/download/$VERSION}
    _base=${_base%/}
    info "Fetching the $VERSION release files"
    curl -fsSL "$_base/SHA256SUMS" -o "$TMP/SHA256SUMS" ||
        die "Could not download $_base/SHA256SUMS." "Is $VERSION a published release?"
    for _f in docker-compose.yml "$EXTERNAL_COMPOSE" Caddyfile env.template opendesk-ctl; do
        curl -fsSL "$_base/$_f" -o "$TMP/$_f" || die "Could not download $_base/$_f"
        _want=$(awk -v f="$_f" '$2 == f || $2 == "*" f { print $1 }' "$TMP/SHA256SUMS")
        [ -n "$_want" ] || die "$_f is not listed in the release's SHA256SUMS."
        [ "$(sha256_of "$TMP/$_f")" = "$_want" ] ||
            die "$_f does not match the checksum release $VERSION published." \
                "Nothing was changed. Try again; if it repeats, open an issue at https://github.com/$REPO/issues"
    done
    ok "Release files verified against SHA256SUMS"
}

# ── Rendering ──────────────────────────────────────────────────────────────

derive() {
    case "$PROXY:$DOMAIN" in
        caddy::*) URL="http://localhost${DOMAIN#:80}"; GW="ws://localhost${DOMAIN#:80}/socket" ;;
        *) URL="https://$DOMAIN"; GW="wss://$DOMAIN/socket" ;;
    esac
    GLOBAL_OPTS=""
    [ -n "$EMAIL" ] && GLOBAL_OPTS="email $EMAIL"
    compose_files
    return 0
}

# compose_files sets COMPOSE_FILES to the COMPOSE_FILE line for .env: the
# external proxy file added to (or removed from) the list the install has, so
# an operator's own override files stay. Empty leaves .env as it is.
compose_files() {
    COMPOSE_FILES=""
    _have=$(env_get "$DIR/.env" COMPOSE_FILE)
    _list=$(printf '%s' ":$_have:" | sed "s/:$EXTERNAL_COMPOSE:/:/g; s/^:*//; s/:*$//")
    if [ "$PROXY" = external ]; then
        COMPOSE_FILES="${_list:-docker-compose.yml}:$EXTERNAL_COMPOSE"
    elif [ "$_list" != "$_have" ]; then
        COMPOSE_FILES="${_list:-docker-compose.yml}"
    fi
    return 0
}

# proxy_ports reads where opendesk and realtime listen for an external proxy,
# as compose.external-proxy.yml publishes them.
proxy_ports() {
    BIND=$(env_get "$DIR/.env" OPENDESK_BIND)
    APP_PORT=$(env_get "$DIR/.env" OPENDESK_PORT)
    SOCKET_PORT=$(env_get "$DIR/.env" REALTIME_PORT)
    BIND=${BIND:-127.0.0.1}
    APP_PORT=${APP_PORT:-8080}
    SOCKET_PORT=${SOCKET_PORT:-4000}
    return 0
}

# The values this run owns. Everything else in .env keeps what the operator
# set, so a second run reconfigures rather than clobbers.
owned_values() {
    cat <<EOF
OPENDESK_VERSION=$VERSION
OPENDESK_DOMAIN=$DOMAIN
OPENDESK_URL=$URL
OPENDESK_GATEWAY_URL=$GW
CADDY_GLOBAL_OPTIONS=$GLOBAL_OPTS
OPENDESK_CADDYFILE=./Caddyfile
AUTH_SECRET=$AUTH_SECRET
CREDENTIALS_KEY=$CREDENTIALS_KEY
SECRET_KEY_BASE=$SECRET_KEY_BASE
NATS_TOKEN=$NATS_TOKEN
INBOUND_SECRET=$INBOUND_SECRET
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
EOF
    [ -n "$COMPOSE_FILES" ] && printf 'COMPOSE_FILE=%s\n' "$COMPOSE_FILES"
    [ "$REGISTRY" = "$DEFAULT_REGISTRY" ] || printf 'OPENDESK_IMAGE=%s/opendesk\nREALTIME_IMAGE=%s/realtime\n' "$REGISTRY" "$REGISTRY"
    [ -n "$SIGNUP" ] && printf 'SIGNUP_ENABLED=%s\n' "$SIGNUP"
    [ -n "$AUTO_UPDATE_A" ] && printf 'AUTO_UPDATE=%s\n' "$AUTO_UPDATE_A"
    if [ -n "$MAIL_DRIVER_A" ]; then
        printf 'MAIL_DRIVER=%s\n' "$MAIL_DRIVER_A"
        if [ "$MAIL_DRIVER_A" = smtp ]; then
            printf 'SMTP_HOST=%s\nSMTP_PORT=%s\nSMTP_TLS=%s\nSMTP_USERNAME=%s\nSMTP_PASSWORD=%s\nMAIL_FROM=%s\n' \
                "$SMTP_HOST_A" "$SMTP_PORT_A" "$SMTP_TLS_A" "$SMTP_USER_A" "$SMTP_PASS_A" "$MAIL_FROM_A"
        fi
    fi
    [ -n "$INBOUND_DOMAIN_A" ] && printf 'INBOUND_DOMAIN=%s\n' "$INBOUND_DOMAIN_A"
    PROFILES=$(env_get "$DIR/.env" COMPOSE_PROFILES)
    PROFILES_SET=0
    if [ "$STORAGE_A" = minio ]; then
        profile_on minio
        _s3key=$(env_get "$DIR/.env" S3_SECRET_ACCESS_KEY)
        [ -n "$_s3key" ] || _s3key=$(gen_hex 24)
        printf 'STORAGE_DRIVER=s3\nS3_ENDPOINT=http://s3:9000\nS3_FORCE_PATH_STYLE=true\nS3_BUCKET=opendesk\nS3_REGION=us-east-1\nS3_ACCESS_KEY_ID=opendesk\nS3_SECRET_ACCESS_KEY=%s\n' "$_s3key"
    elif [ "$STORAGE_A" = local ]; then
        profile_off minio
        printf 'STORAGE_DRIVER=local\n'
    fi
    if [ "$SMTP_INBOUND_A" = 1 ]; then
        profile_on smtp-inbound
        printf 'INBOUND_SMTP_ADDR=:2525\n'
    elif [ "$SMTP_INBOUND_A" = 0 ]; then
        profile_off smtp-inbound
        printf 'INBOUND_SMTP_ADDR=\n'
    fi
    ai_values
    [ "$PROFILES_SET" = 1 ] && printf 'COMPOSE_PROFILES=%s\n' "$PROFILES"
    return 0
}

# Compose profiles are a comma list in .env; answers switch single entries and
# leave the rest as the operator had them.
profile_on() {
    case ",$PROFILES," in *",$1,"*) ;; *) PROFILES="${PROFILES:+$PROFILES,}$1" ;; esac
    PROFILES_SET=1
}
profile_off() {
    PROFILES=$(printf '%s' ",$PROFILES," | sed "s/,$1,/,/g; s/^,//; s/,$//")
    PROFILES_SET=1
}

# ai_values writes the provider settings for the AI answer, if one was given.
# Self-hosted instances never bill, so there is nothing about Stripe here.
ai_values() {
    # Every answer writes the whole set, so switching provider never leaves a
    # model name from the previous one behind.
    case "$AI_MODE" in
        "") return 0 ;;
        none)
            profile_off ollama
            ai_set none "" "" "https://api.openai.com/v1" "" "" ""
            ;;
        anthropic)
            profile_off ollama
            ai_set anthropic "$AI_KEY" "" "https://api.openai.com/v1" "$AI_MODEL_A" "" ""
            ;;
        openai)
            profile_off ollama
            ai_set openai "" "${AI_KEY:-none}" "$AI_URL" "$AI_MODEL_A" "$AI_MODEL_A" ""
            ;;
        ollama)
            profile_on ollama
            _m=${AI_MODEL_A:-llama3.1:8b}
            ai_set openai "" ollama "http://ollama:11434/v1" "$_m" "$_m" openai
            printf 'EMBEDDINGS_MODEL=nomic-embed-text\n'
            return 0
            ;;
    esac
    printf 'EMBEDDINGS_MODEL=\n'
    return 0
}

# ai_set provider anthropic_key openai_key openai_url model fast_model embeddings_driver
ai_set() {
    printf 'AI_PROVIDER=%s\nANTHROPIC_API_KEY=%s\nOPENAI_API_KEY=%s\nOPENAI_BASE_URL=%s\nAI_MODEL=%s\nAI_FAST_MODEL=%s\nEMBEDDINGS_DRIVER=%s\n' \
        "$1" "$2" "$3" "$4" "$5" "$6" "$7"
}

# render_env fills the release's env.template: owned keys get this run's
# values, other template keys keep the existing .env's value or the template
# default, and keys the operator added that the template does not know are
# kept at the end.
render_env() {
    owned_values >"$TMP/owned"
    if [ -f "$DIR/.env" ]; then cp "$DIR/.env" "$TMP/existing"; else : >"$TMP/existing"; fi
    awk '
        FILENAME == ARGV[1] { file = 1 }
        FILENAME == ARGV[2] { file = 2 }
        FILENAME == ARGV[3] { file = 3 }
        file == 1 { i = index($0, "="); if (i > 1) own[substr($0, 1, i - 1)] = substr($0, i + 1); next }
        file == 2 { if ($0 ~ /^[A-Z_][A-Z0-9_]*=/) { i = index($0, "="); old[substr($0, 1, i - 1)] = substr($0, i + 1) } next }
        file == 3 {
            if ($0 ~ /^[A-Z_][A-Z0-9_]*=/) {
                i = index($0, "="); k = substr($0, 1, i - 1)
                if (k in own) { print k "=" own[k]; done[k] = 1; next }
                if (k in old) { print k "=" old[k]; done[k] = 1; next }
                done[k] = 1
            } else if ($0 ~ /^# ?[A-Z_][A-Z0-9_]*=/) {
                k = $0; sub(/^# ?/, "", k); i = index(k, "="); k = substr(k, 1, i - 1)
                if (k in own) { print k "=" own[k]; done[k] = 1; next }
                if (k in old) { print k "=" old[k]; done[k] = 1; next }
            }
            print
        }
        END {
            extra = 0
            for (k in own) if (!(k in done)) { if (!extra++) print "\n# ── Set by the installer ──"; print k "=" own[k]; done[k] = 1 }
            extra = 0
            for (k in old) if (!(k in done)) { if (!extra++) print "\n# ── Kept from the previous .env ──"; print k "=" old[k] }
        }
    ' "$TMP/owned" "$TMP/existing" "$TMP/env.template"
}

# ── Writing ────────────────────────────────────────────────────────────────

prepare_dir() {
    if [ -d "$DIR" ] && [ -n "$(ls -A "$DIR" 2>/dev/null)" ] && [ ! -f "$DIR/$MARKER" ] && [ "$EXISTING" = 0 ] && [ "$FORCE" = 0 ]; then
        die "$DIR is not empty and this installer did not create it." \
            "Pass --dir to pick another path, or --force to install into it anyway."
    fi
    if ! mkdir -p "$DIR" 2>/dev/null; then
        need_sudo || die "Cannot create $DIR and sudo is not available." "Pick a directory you can write: --dir \$HOME/opendesk"
        info "Creating $DIR (uses sudo)"
        $SUDO mkdir -p "$DIR"
        $SUDO chown "$(id -u):$(id -g)" "$DIR"
    fi
    if [ ! -w "$DIR" ]; then
        need_sudo || die "$DIR is not writable and sudo is not available."
        info "Taking ownership of $DIR (uses sudo)"
        $SUDO chown "$(id -u):$(id -g)" "$DIR"
    fi
    chmod 750 "$DIR" 2>/dev/null || true
}

write_file() {
    # write_file path mode < content
    (umask 077; cat >"$1.tmp")
    chmod "$2" "$1.tmp"
    keep_owner "$1.tmp"
    mv -f "$1.tmp" "$1"
}

# keep_owner: when root runs this (the nightly update does) in a directory
# another user owns, the files it writes stay that user's.
keep_owner() {
    [ "$(id -u)" = 0 ] || return 0
    # shellcheck disable=SC2012
    _owner=$(ls -nd "$DIR" | awk '{ print $3 ":" $4 }')
    [ "$_owner" = "0:0" ] || chown "$_owner" "$@" 2>/dev/null || true
}

backup_file() {
    [ -f "$1" ] || return 0
    cp -p "$1" "$1.bak"
    keep_owner "$1.bak"
}

write_all() {
    render_env >"$TMP/env.rendered"
    backup_file "$DIR/.env"
    write_file "$DIR/.env" 0600 <"$TMP/env.rendered"
    backup_file "$DIR/docker-compose.yml"
    write_file "$DIR/docker-compose.yml" 0644 <"$TMP/docker-compose.yml"
    write_file "$DIR/$EXTERNAL_COMPOSE" 0644 <"$TMP/$EXTERNAL_COMPOSE"
    write_file "$DIR/Caddyfile" 0644 <"$TMP/Caddyfile"
    write_file "$DIR/opendesk-ctl" 0755 <"$TMP/opendesk-ctl"
    printf 'installed_at=%s\nversion=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$VERSION" | write_file "$DIR/$MARKER" 0644
    ok "Wrote .env, docker-compose.yml, Caddyfile and opendesk-ctl in $DIR"
}

# ── Running ────────────────────────────────────────────────────────────────

# A new .env next to an old database volume cannot work: the volume keeps the
# password of the install that created it. Say so before starting, rather than
# leaving opendesk failing to log in to Postgres.
check_old_volume() {
    [ "$EXISTING" = 0 ] || return 0
    if dockerc volume inspect opendesk_postgres-data >/dev/null 2>&1; then
        die "This machine already has an opendesk database (Docker volume opendesk_postgres-data)," \
            "but $DIR has no .env, so its password and CREDENTIALS_KEY are unknown." \
            "To keep that data: put the old install's .env into $DIR and run this again." \
            "To start over and DELETE it: docker volume rm opendesk_postgres-data opendesk_opendesk-data"
    fi
    return 0
}

pull_and_start() {
    if [ "$PROXY" = external ]; then
        if [ "$EXISTING" = 0 ]; then
            for _p in "$APP_PORT" "$SOCKET_PORT"; do
                if port_busy "$_p"; then
                    warn "Port $_p is already in use; opendesk publishes it for your proxy."
                    note "Set OPENDESK_PORT / REALTIME_PORT in $DIR/.env to free ones."
                fi
            done
        fi
    else
        for _p in 80 443; do
            if port_busy "$_p"; then
                warn "Port $_p is already in use; Caddy needs it. Stop whatever listens there,"
                note "or set HTTP_PORT / HTTPS_PORT in $DIR/.env."
            fi
        done
    fi
    info "Pulling images (this takes a minute the first time)"
    composec pull --quiet >"$TMP/pull.log" 2>&1 || {
        tail -20 "$TMP/pull.log" >&2
        die "Pulling the images failed." "Check the network, then run this again; it picks up where it stopped."
    }
    ok "Images pulled"
    info "Starting the stack"
    composec up -d --remove-orphans >"$TMP/up.log" 2>&1 || {
        tail -20 "$TMP/up.log" >&2
        die "The stack did not start." "See: cd $DIR && docker compose logs"
    }
    ok "Containers started"
    if [ "$PROXY" = external ] && [ -n "$(composec --profile bundled-proxy ps -aq caddy 2>/dev/null)" ]; then
        # The bundled Caddy of an install that used it until now: it would keep
        # ports 80 and 443 your proxy needs.
        composec --profile bundled-proxy rm -sf caddy >/dev/null 2>&1 || true
        ok "Removed the bundled Caddy"
    fi
}

wait_healthy() {
    info "Waiting for opendesk (the first boot applies the database migrations)"
    _i=0
    while [ "$_i" -lt 90 ]; do
        if composec exec -T opendesk /opendesk healthcheck >/dev/null 2>&1; then
            ok "opendesk is up"
            return 0
        fi
        _i=$((_i + 1))
        sleep 2
    done
    warn "opendesk has not answered in three minutes."
    note "Watch it: cd $DIR && ./opendesk-ctl logs opendesk"
    return 1
}

# sync_updates installs or removes the nightly update schedule to match
# AUTO_UPDATE in .env (opendesk-ctl auto-update sync).
sync_updates() {
    AUTO_ON=0
    [ "$(env_get "$DIR/.env" AUTO_UPDATE)" = true ] && AUTO_ON=1
    if [ "$AUTO_ON" = 1 ]; then info "Scheduling automatic updates (the systemd timer uses sudo)"; fi
    "$DIR/opendesk-ctl" auto-update sync || warn "Automatic updates are not scheduled; see: $DIR/opendesk-ctl auto-update status"
}

finale() {
    say ""
    say "  ${GREEN}${B}opendesk $VERSION is installed.${R}"
    say ""
    if [ "$NO_START" = 1 ]; then
        say "  Start it:      cd $DIR && docker compose up -d"
    else
        say "  Open:          ${B}$URL${R}"
        if [ "$PROXY" = external ]; then
            note "once your reverse proxy serves $DOMAIN over https and forwards:"
            note "  /socket, /socket/*  to http://$BIND:$SOCKET_PORT (WebSocket)"
            note "  everything else     to http://$BIND:$APP_PORT"
            note "Examples for nginx, Traefik, Caddy and Nginx Proxy Manager:"
            note "$DOCS/self-hosting/reverse-proxy/"
        fi
        case "$PROXY:$DOMAIN" in
            external:*) ;;
            caddy:localhost) note "Caddy's local certificate authority signs localhost; your browser asks once." ;;
            caddy::*) ;;
            *) note "The certificate is issued on the first request once DNS for $DOMAIN points here." ;;
        esac
        say "  First account: sign up at $URL; it becomes the workspace owner."
    fi
    say ""
    if [ "$AI_MODE" = ollama ] && [ "$NO_START" = 0 ]; then
        note "Ollama is downloading the models in the background (several GB):"
        note "cd $DIR && docker compose logs -f ollama-init"
        say ""
    fi
    say "  Files:         $DIR (.env holds the secrets, mode 0600)"
    say "  Status:        $DIR/opendesk-ctl status"
    if [ "$PROXY" = external ]; then
        say "  Logs:          $DIR/opendesk-ctl logs [opendesk|realtime|postgres]"
    else
        say "  Logs:          $DIR/opendesk-ctl logs [opendesk|realtime|postgres|caddy]"
    fi
    say "  Back up:       $DIR/opendesk-ctl backup"
    if [ "${AUTO_ON:-0}" = 1 ]; then
        say "  Updates:       installed each night ($DIR/opendesk-ctl auto-update status, or off)"
    else
        say "  Upgrade:       $DIR/opendesk-ctl upgrade"
    fi
    say ""
    if [ "$EXISTING" = 0 ]; then
        warn "Copy $DIR/.env somewhere safe. CREDENTIALS_KEY in it cannot be recovered,"
        note "and a database backup without it cannot decrypt stored mailbox credentials."
        say ""
    fi
    note "Guide: $DOCS/self-hosting/"
    say ""
}

# ── Arguments ──────────────────────────────────────────────────────────────

usage() {
    cat <<USAGE
opendesk installer

  curl -fsSL https://opendesk.sh/install.sh | sh
  curl -fsSL https://opendesk.sh/install.sh | sh -s -- --domain desk.example.com --email you@example.com

Options                                     environment variable
  --domain HOST     Public hostname         OPENDESK_DOMAIN    [asked, or localhost]
  --email ADDRESS   ACME account email      OPENDESK_EMAIL
  --proxy MODE      caddy: the bundled Caddy gets certificates on 80 and 443.
                    external: your own reverse proxy in front; opendesk and
                    realtime listen on 127.0.0.1:8080 and :4000 for it
                                            OPENDESK_PROXY     [kept, or caddy]
  --dir PATH        Install directory       OPENDESK_DIR       [$DEFAULT_DIR]
  --version TAG     Release to install      OPENDESK_VERSION   [newest]
  --no-start        Write the files and pull nothing; start later yourself
                                            OPENDESK_NO_START=1
  --wizard          Also ask about email, storage, inbound mail and sign-ups
                                            OPENDESK_WIZARD=1
  -y, --yes         Ask nothing; take defaults (and install Docker if missing)
                                            OPENDESK_YES=1
  --registry PREFIX Image namespace         OPENDESK_REGISTRY  [$DEFAULT_REGISTRY]
  --no-auto-update  Do not install new releases each night (opendesk-ctl
                    auto-update on turns it on later)
                                            OPENDESK_AUTO_UPDATE=false
  --auto-update     Install new releases each night [default]
                                            OPENDESK_AUTO_UPDATE=true

AI (optional; asked by --wizard)
  --ai-key KEY      Anthropic API key       OPENDESK_AI_KEY (prefer the variable)
  --ai-url URL      An OpenAI-compatible endpoint instead (OpenAI, vLLM, your
                    own Ollama); --ai-key is then its key
                                            OPENDESK_AI_URL
  --ai-model NAME   Model name              OPENDESK_AI_MODEL
  --ollama          Run models locally in an ollama container
                                            OPENDESK_AI=ollama
  --no-ai           Turn AI off             OPENDESK_AI=none
  --dry-run         Print the .env it would write and stop
  --print-env       Same, without the banner (for scripts)
  --force           Install into a non-empty directory this installer did not create
  --no-color        Plain output
  -h, --help        This

Running it again on an existing install keeps every secret and setting, and
moves to --version (or the newest release) if you pass one.

Docs: $DOCS/self-hosting/
USAGE
}

need_value() {
    if [ $# -lt 2 ] || [ -z "$2" ]; then
        die "$1 needs a value"
    fi
    printf '%s' "$2"
}

parse_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --domain) DOMAIN=$(need_value "$@"); shift ;;
            --domain=*) DOMAIN=${1#*=} ;;
            --email) EMAIL=$(need_value "$@"); shift ;;
            --email=*) EMAIL=${1#*=} ;;
            --dir) DIR=$(need_value "$@"); shift ;;
            --dir=*) DIR=${1#*=} ;;
            --version) VERSION=$(need_value "$@"); shift ;;
            --version=*) VERSION=${1#*=} ;;
            --proxy) PROXY=$(need_value "$@"); shift ;;
            --proxy=*) PROXY=${1#*=} ;;
            --registry) REGISTRY=$(need_value "$@"); shift ;;
            --ai-key) AI_KEY=$(need_value "$@"); shift ;;
            --ai-url) AI_URL=$(need_value "$@"); shift ;;
            --ai-model) AI_MODEL_A=$(need_value "$@"); shift ;;
            --ollama) AI_MODE=ollama ;;
            --no-ai) AI_MODE=none ;;
            --no-start) NO_START=1 ;;
            --no-auto-update) AUTO_UPDATE_A=false ;;
            --auto-update) AUTO_UPDATE_A=true ;;
            --wizard) WIZARD=1 ;;
            -y|--yes) ASSUME_YES=1 ;;
            --dry-run) DRY_RUN=1 ;;
            --print-env) PRINT_ENV=1; DRY_RUN=1 ;;
            --force) FORCE=1 ;;
            --no-color) USE_COLOR=0 ;;
            -h|--help) usage; exit 0 ;;
            *) die "Unknown option $1" "Run with --help for the list." ;;
        esac
        shift
    done
    # --ai-url means an OpenAI-compatible endpoint (its key, if any, in
    # --ai-key); --ai-key alone means Anthropic.
    if [ -z "$AI_MODE" ]; then
        if [ -n "$AI_URL" ]; then AI_MODE=openai; elif [ -n "$AI_KEY" ]; then AI_MODE=anthropic; fi
    fi
    case "$PROXY" in
        ""|caddy|external) ;;
        *) die "--proxy must be caddy or external" ;;
    esac
    case "$AI_MODE" in
        ""|none|anthropic|openai|ollama) ;;
        *) die "OPENDESK_AI must be none, anthropic, openai or ollama" ;;
    esac
    case "$AI_URL" in
        ""|http://*|https://*) ;;
        *) die "--ai-url must be an http(s) URL, e.g. http://192.168.1.10:11434/v1" ;;
    esac
    case "$EMAIL" in
        ""|*@*.*) ;;
        *) die "\"$EMAIL\" is not an email address." ;;
    esac
    case "$DIR" in
        /*) ;;
        *) DIR="$(pwd)/$DIR" ;;
    esac
    return 0
}

# ── main ───────────────────────────────────────────────────────────────────

main() {
    parse_args "$@"
    setup_term
    TMP=$(mktemp -d 2>/dev/null || printf '/tmp/opendesk-install.%s' "$$")
    mkdir -p "$TMP"
    trap 'rm -rf "$TMP"' EXIT INT TERM

    have curl || die "curl is required." "Install it (apt install curl, dnf install curl) and run this again."
    detect_platform
    require_randomness

    if [ "$PRINT_ENV" = 0 ]; then
        say ""
        say "  ${B}opendesk${R} ${DIM}installer${R}"
    fi

    adopt_existing
    if [ "$EXISTING" = 1 ] && [ "$PRINT_ENV" = 0 ]; then
        info "Found an install in $DIR; keeping its secrets and settings"
    fi
    [ -n "$PROXY" ] || PROXY=caddy
    proxy_ports
    ask_basics
    [ "$WIZARD" = 1 ] && wizard
    resolve_version
    ensure_secrets
    derive
    fetch_assets

    if [ "$DRY_RUN" = 1 ]; then
        if [ "$PRINT_ENV" = 0 ]; then
            step "This is the .env it would write to $DIR. Nothing was changed."
        fi
        render_env
        return 0
    fi

    if [ "$NO_START" = 0 ]; then
        check_docker
        check_old_volume
    fi
    step "Installing opendesk $VERSION into $DIR"
    prepare_dir
    write_all
    if [ "$NO_START" = 0 ]; then
        pull_and_start
        wait_healthy || true
        sync_updates
    fi
    finale
}

main "$@"
