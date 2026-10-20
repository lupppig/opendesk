#!/usr/bin/env bash
#
# Checks the one-command installer served at https://opendesk.sh/install.sh,
# against the exact bytes `curl -fsSL https://opendesk.sh/install.sh | sh` runs:
#
#   * it parses as POSIX sh in dash (what /bin/sh is on Debian and Ubuntu)
#   * shellcheck -s sh has nothing to say about it, or about opendesk-ctl
#   * --help and --print-env work with no terminal and no Docker
#   * a real --no-start install writes a .env docker compose accepts, and a
#     second run keeps every secret (idempotency is the data-loss guard)
#   * --proxy external drops Caddy and is kept between runs; --proxy caddy undoes it
#   * a release's images.json pins both images to their digests
#   * a release after v0.2.0 needs a SHA256SUMS signature by the release key
#   * a release file that does not match SHA256SUMS stops the install
#   * install.sh.sha256 matches install.sh
#
# Run by `make installer-check` and the Installer job in CI.
set -euo pipefail

cd "$(dirname "$0")/.."
SCRIPT=deploy/install/install.sh
SUMFILE=deploy/install/install.sh.sha256

fail() { printf '\n\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }
pass() { printf '\033[32m✓\033[0m %s\n' "$*"; }

[[ -f $SCRIPT ]] || fail "$SCRIPT is missing"

if command -v dash >/dev/null 2>&1; then
  dash -n "$SCRIPT" || fail "the installer is not valid POSIX sh (dash -n)"
  dash -n deploy/self-host/opendesk-ctl || fail "opendesk-ctl is not valid POSIX sh"
  pass "parses as POSIX sh (dash)"
else
  sh -n "$SCRIPT" || fail "the installer does not parse"
  pass "parses (dash not installed; the POSIX check was approximate)"
fi

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -s sh "$SCRIPT" deploy/self-host/opendesk-ctl deploy/self-host/gen-env.sh \
    deploy/install/stage-assets.sh deploy/install/gen-sha256.sh ||
    fail "shellcheck found problems"
  pass "shellcheck clean ($(shellcheck --version | awk '/^version:/ {print $2}'))"
else
  echo "· shellcheck not installed; skipped"
fi

if command -v sha256sum >/dev/null 2>&1; then
  (cd deploy/install && sha256sum -c --status install.sh.sha256) ||
    fail "$SUMFILE does not match install.sh; run: make installer-sha"
else
  (cd deploy/install && shasum -a 256 -c --status install.sh.sha256) ||
    fail "$SUMFILE does not match install.sh; run: make installer-sha"
fi
pass "install.sh.sha256 matches"

sh "$SCRIPT" --help >/dev/null </dev/null || fail "--help failed"
pass "--help works"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
sh deploy/install/stage-assets.sh "$work/assets" >/dev/null
export OPENDESK_ASSETS_URL="file://$work/assets"

sh "$SCRIPT" --print-env --no-color --domain desk.example.com --email ops@example.com \
  --version v0.0.0-test --dir "$work/none" >"$work/env" 2>/dev/null </dev/null || fail "--print-env failed"
for key in OPENDESK_VERSION OPENDESK_DOMAIN OPENDESK_URL OPENDESK_GATEWAY_URL AUTH_SECRET \
  CREDENTIALS_KEY SECRET_KEY_BASE NATS_TOKEN INBOUND_SECRET POSTGRES_PASSWORD; do
  value=$(sed -n "s/^${key}=//p" "$work/env" | tail -1)
  [[ -n $value ]] || fail "--print-env left $key empty"
done
grep -qx 'OPENDESK_URL=https://desk.example.com' "$work/env" || fail "OPENDESK_URL was not derived from --domain"
grep -qx 'CADDY_GLOBAL_OPTIONS=email ops@example.com' "$work/env" || fail "--email did not reach CADDY_GLOBAL_OPTIONS"
[[ ! -e $work/none ]] || fail "--print-env created the install directory"
pass "--print-env renders every secret and writes nothing"

sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v0.0.0-test --dir "$work/inst" >/dev/null </dev/null ||
  fail "a --no-start install failed"
for f in .env docker-compose.yml Caddyfile opendesk-ctl .opendesk-install; do
  [[ -f $work/inst/$f ]] || fail "the install did not write $f"
done
mode=$(stat -c '%a' "$work/inst/.env" 2>/dev/null || stat -f '%Lp' "$work/inst/.env")
[[ $mode == 600 ]] || fail ".env is mode $mode, not 600"
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  (cd "$work/inst" && docker compose config -q) || fail "docker compose rejects the installed files"
  (cd "$work/inst" && COMPOSE_PROFILES=minio,smtp-inbound S3_SECRET_ACCESS_KEY=x docker compose config -q) ||
    fail "docker compose rejects the installed files with every profile"
  pass "docker compose accepts the install, with and without profiles"
fi
before=$(grep -E '^(AUTH_SECRET|CREDENTIALS_KEY|SECRET_KEY_BASE|NATS_TOKEN|INBOUND_SECRET|POSTGRES_PASSWORD)=' "$work/inst/.env")
echo "MY_SETTING=kept" >>"$work/inst/.env"
sed -i.orig 's/^SIGNUP_ENABLED=.*/SIGNUP_ENABLED=false/' "$work/inst/.env"
sh "$SCRIPT" --yes --no-start --no-color --version v0.0.0-test2 --dir "$work/inst" >/dev/null </dev/null ||
  fail "a second install failed"
after=$(grep -E '^(AUTH_SECRET|CREDENTIALS_KEY|SECRET_KEY_BASE|NATS_TOKEN|INBOUND_SECRET|POSTGRES_PASSWORD)=' "$work/inst/.env")
[[ $before == "$after" ]] || fail "a second run changed a secret"
grep -qx 'MY_SETTING=kept' "$work/inst/.env" || fail "a second run dropped a setting it does not own"
grep -qx 'SIGNUP_ENABLED=false' "$work/inst/.env" || fail "a second run reset an operator's setting"
grep -qx 'OPENDESK_DOMAIN=localhost' "$work/inst/.env" || fail "a second run forgot the domain"
grep -qx 'OPENDESK_VERSION=v0.0.0-test2' "$work/inst/.env" || fail "--version did not move the pin"
pass "a second run keeps secrets and settings and moves the version pin"

# AI answers: --ollama adds the profile next to the operator's own, and a
# later Anthropic key removes it again without leaving the local model behind.
sed -i.orig 's/^COMPOSE_PROFILES=.*/COMPOSE_PROFILES=smtp-inbound/' "$work/inst/.env"
sh "$SCRIPT" --yes --no-start --no-color --ollama --version v0.0.0-test2 --dir "$work/inst" >/dev/null </dev/null ||
  fail "an --ollama install failed"
grep -qx 'COMPOSE_PROFILES=smtp-inbound,ollama' "$work/inst/.env" || fail "--ollama did not add the profile next to the existing one"
grep -qx 'OPENAI_BASE_URL=http://ollama:11434/v1' "$work/inst/.env" || fail "--ollama did not point the provider at the container"
grep -qx 'EMBEDDINGS_DRIVER=openai' "$work/inst/.env" || fail "--ollama did not enable local embeddings"
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  (cd "$work/inst" && docker compose config -q) || fail "docker compose rejects the --ollama install"
fi
OPENDESK_AI_KEY=sk-ant-test sh "$SCRIPT" --yes --no-start --no-color --version v0.0.0-test2 --dir "$work/inst" >/dev/null </dev/null ||
  fail "an install with OPENDESK_AI_KEY failed"
grep -qx 'COMPOSE_PROFILES=smtp-inbound' "$work/inst/.env" || fail "switching to Anthropic did not remove the ollama profile"
grep -qx 'AI_PROVIDER=anthropic' "$work/inst/.env" || fail "OPENDESK_AI_KEY did not select Anthropic"
grep -qx 'ANTHROPIC_API_KEY=sk-ant-test' "$work/inst/.env" || fail "OPENDESK_AI_KEY was not written"
grep -qx 'AI_MODEL=' "$work/inst/.env" || fail "switching provider left the previous model behind"
sh "$SCRIPT" --yes --no-start --no-color --ai-url http://10.0.0.5:11434/v1 --ai-model qwen3 --version v0.0.0-test2 --dir "$work/inst" >/dev/null </dev/null ||
  fail "an install with --ai-url failed"
grep -qx 'AI_PROVIDER=openai' "$work/inst/.env" && grep -qx 'OPENAI_BASE_URL=http://10.0.0.5:11434/v1' "$work/inst/.env" &&
  grep -qx 'AI_MODEL=qwen3' "$work/inst/.env" && grep -qx 'ANTHROPIC_API_KEY=' "$work/inst/.env" ||
  fail "--ai-url did not configure an OpenAI-compatible provider"
after=$(grep -E '^(AUTH_SECRET|CREDENTIALS_KEY|SECRET_KEY_BASE|NATS_TOKEN|INBOUND_SECRET|POSTGRES_PASSWORD)=' "$work/inst/.env")
[[ $before == "$after" ]] || fail "an AI change touched a secret"
pass "AI answers (--ollama, an Anthropic key, --ai-url) set the provider and merge compose profiles"

# Proxy mode: --proxy external adds the override next to the operator's own
# compose files and drops Caddy, a run without the flag keeps it, and
# --proxy caddy takes it out again.
sed -i.orig 's/^COMPOSE_FILE=.*//' "$work/inst/.env"
echo "COMPOSE_FILE=docker-compose.yml:docker-compose.override.yml" >>"$work/inst/.env"
printf 'services: {}\n' >"$work/inst/docker-compose.override.yml"
sh "$SCRIPT" --yes --no-start --no-color --proxy external --domain desk.example.com --version v0.0.0-test2 --dir "$work/inst" >/dev/null </dev/null ||
  fail "a --proxy external install failed"
[[ -f $work/inst/compose.external-proxy.yml ]] || fail "the install did not write compose.external-proxy.yml"
grep -qx 'COMPOSE_FILE=docker-compose.yml:docker-compose.override.yml:compose.external-proxy.yml' "$work/inst/.env" ||
  fail "--proxy external did not add the override next to the existing compose files"
grep -qx 'OPENDESK_URL=https://desk.example.com' "$work/inst/.env" && grep -qx 'OPENDESK_GATEWAY_URL=wss://desk.example.com/socket' "$work/inst/.env" ||
  fail "--proxy external did not keep the public https and wss addresses"
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  (cd "$work/inst" && docker compose config --services) | grep -qx caddy && fail "--proxy external still runs Caddy"
  (cd "$work/inst" && docker compose config) | grep -q 'host_ip: 127.0.0.1' ||
    fail "--proxy external does not publish opendesk on 127.0.0.1"
fi
sh "$SCRIPT" --yes --no-start --no-color --version v0.0.0-test2 --dir "$work/inst" >/dev/null </dev/null ||
  fail "a second --proxy external run failed"
grep -q '^COMPOSE_FILE=.*:compose.external-proxy.yml$' "$work/inst/.env" || fail "a run without --proxy forgot the external proxy"
if sh "$SCRIPT" --yes --no-start --no-color --proxy external --domain :80 --version v0.0.0-test2 --dir "$work/inst" >/dev/null 2>&1 </dev/null; then
  fail "--proxy external accepted :80 as the public hostname"
fi
sh "$SCRIPT" --yes --no-start --no-color --proxy caddy --version v0.0.0-test2 --dir "$work/inst" >/dev/null </dev/null ||
  fail "a --proxy caddy install failed"
grep -qx 'COMPOSE_FILE=docker-compose.yml:docker-compose.override.yml' "$work/inst/.env" ||
  fail "--proxy caddy did not take the override out and keep the rest"
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  (cd "$work/inst" && docker compose config --services) | grep -qx caddy || fail "--proxy caddy does not run Caddy"
fi
after=$(grep -E '^(AUTH_SECRET|CREDENTIALS_KEY|SECRET_KEY_BASE|NATS_TOKEN|INBOUND_SECRET|POSTGRES_PASSWORD)=' "$work/inst/.env")
[[ $before == "$after" ]] || fail "a proxy change touched a secret"
pass "--proxy external and --proxy caddy switch the bundled Caddy off and on, and are kept between runs"

mkdir -p "$work/other" && touch "$work/other/something"
if sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v0.0.0-test --dir "$work/other" >/dev/null 2>&1 </dev/null; then
  fail "the installer took over a non-empty directory it did not create"
fi
pass "refuses a non-empty directory it did not create"

# A release's images.json pins the images to their digests.
pinned="$work/pinned-assets"
cp -r "$work/assets" "$pinned"
app=sha256:$(printf 'app' | sha256sum | cut -d' ' -f1)
gw=sha256:$(printf 'gw' | sha256sum | cut -d' ' -f1)
printf '{\n  "version": "v0.0.0-test",\n  "opendesk": "%s",\n  "realtime": "%s"\n}\n' "$app" "$gw" >"$pinned/images.json"
(cd "$pinned" && sha256sum images.json >>SHA256SUMS)
OPENDESK_ASSETS_URL="file://$pinned" sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v0.0.0-test \
  --dir "$work/pinned" >/dev/null </dev/null || fail "a pinned install failed"
grep -qx "OPENDESK_IMAGE_PIN=@$app" "$work/pinned/.env" || fail "the opendesk image was not pinned to its digest"
grep -qx "REALTIME_IMAGE_PIN=@$gw" "$work/pinned/.env" || fail "the realtime image was not pinned to its digest"
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  (cd "$work/pinned" && docker compose config) | grep -q "image: ghcr.io/useopendesk/opendesk:v0.0.0-test@$app" ||
    fail "docker compose does not run the pinned opendesk image"
fi
grep -qx "OPENDESK_IMAGE_PIN=" "$work/inst/.env" || fail "an install without images.json pinned something"
pass "pins the images to the digests in the release's images.json"

# A release after v0.2.0 installs only with a SHA256SUMS signature that holds.
signed="$work/signed-assets"
cp -r "$work/pinned-assets" "$signed"
openssl ecparam -name prime256v1 -genkey -noout -out "$work/test.key" 2>/dev/null
openssl ec -in "$work/test.key" -pubout -out "$work/test.pub" 2>/dev/null
export OPENDESK_RELEASE_PUBKEY_FILE="$work/test.pub"
if OPENDESK_ASSETS_URL="file://$signed" sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v9.9.9 \
  --dir "$work/unsigned" >/dev/null 2>&1 </dev/null; then
  fail "a release after v0.2.0 installed without a signature"
fi
openssl dgst -sha256 -sign "$work/test.key" -out "$signed/SHA256SUMS.sig" "$signed/SHA256SUMS"
OPENDESK_ASSETS_URL="file://$signed" sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v9.9.9 \
  --dir "$work/signed" >/dev/null </dev/null || fail "a signed release did not install"
openssl ecparam -name prime256v1 -genkey -noout -out "$work/other.key" 2>/dev/null
openssl dgst -sha256 -sign "$work/other.key" -out "$signed/SHA256SUMS.sig" "$signed/SHA256SUMS"
if OPENDESK_ASSETS_URL="file://$signed" sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v9.9.9 \
  --dir "$work/forged" >/dev/null 2>&1 </dev/null; then
  fail "a release signed with another key installed"
fi
noimg="$work/noimg-assets"
cp -r "$work/assets" "$noimg"
openssl dgst -sha256 -sign "$work/test.key" -out "$noimg/SHA256SUMS.sig" "$noimg/SHA256SUMS"
if OPENDESK_ASSETS_URL="file://$noimg" sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v9.9.9 \
  --dir "$work/noimg" >/dev/null 2>&1 </dev/null; then
  fail "a signed release without image digests installed"
fi
unset OPENDESK_RELEASE_PUBKEY_FILE
openssl ec -in deploy/install/release-signing.pub -pubin -noout 2>/dev/null || fail "deploy/install/release-signing.pub is not a public key"
for f in "$SCRIPT" deploy/self-host/opendesk-ctl; do
  grep -q "$(sed -n 2p deploy/install/release-signing.pub)" "$f" || fail "$f does not embed deploy/install/release-signing.pub"
done
pass "installs a release after v0.2.0 only with a signature that holds"

echo "# tampered" >>"$work/assets/docker-compose.yml"
if sh "$SCRIPT" --yes --no-start --no-color --domain localhost --version v0.0.0-test --dir "$work/tampered" >/dev/null 2>&1 </dev/null; then
  fail "the installer accepted a docker-compose.yml that does not match SHA256SUMS"
fi
[[ ! -e $work/tampered/.env ]] || fail "a failed checksum still wrote files"
pass "refuses release files that do not match SHA256SUMS"

echo "installer checks passed"
