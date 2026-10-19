#!/bin/sh
# Collects the files the installer downloads into one directory, under the
# names they are published with on a GitHub release, plus SHA256SUMS.
#
#   sh deploy/install/stage-assets.sh <outdir>
#
# release.yml uploads that directory as release assets. The installer fetches
# docker-compose.yml, compose.external-proxy.yml, Caddyfile, env.template and
# opendesk-ctl from the release
# it installs and refuses any file whose checksum is not in SHA256SUMS, so the
# compose file an operator runs is byte for byte the one in this repo at that tag.
#
# OPENDESK_ASSETS_URL=file://<outdir> points the installer at a staged
# directory, which is how scripts/check-installer.sh tests it with no release.
set -eu

main() {
    out=${1:?usage: stage-assets.sh <outdir>}
    root=$(cd "$(dirname "$0")/../.." && pwd)
    mkdir -p "$out"
    cp "$root/docker-compose.yml" "$out/docker-compose.yml"
    cp "$root/compose.external-proxy.yml" "$out/compose.external-proxy.yml"
    cp "$root/deploy/caddy/Caddyfile" "$out/Caddyfile"
    cp "$root/deploy/self-host/.env.template" "$out/env.template"
    cp "$root/deploy/self-host/opendesk-ctl" "$out/opendesk-ctl"
    cp "$root/deploy/install/install.sh" "$out/install.sh"
    cp "$root/deploy/install/install.sh.sha256" "$out/install.sh.sha256"
    (
        cd "$out"
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum docker-compose.yml compose.external-proxy.yml Caddyfile env.template opendesk-ctl install.sh >SHA256SUMS
        else
            shasum -a 256 docker-compose.yml compose.external-proxy.yml Caddyfile env.template opendesk-ctl install.sh >SHA256SUMS
        fi
    )
    echo "staged installer assets in $out"
}

main "$@"
