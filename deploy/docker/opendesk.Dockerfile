# syntax=docker/dockerfile:1.7
#
# The opendesk image: one static Go binary (API + workers + embedded NATS) with
# the web app and the instance admin panel (/admin/) embedded, so a self-hoster
# runs one container for everything that is not the realtime gateway. The hosted service runs the same image with
# OPENDESK_ROLE=api or worker and SERVE_WEB unset.
#
#   docker build -f deploy/docker/opendesk.Dockerfile -t opendesk .
#
# Both build stages run on $BUILDPLATFORM: the web build is architecture
# independent and Go cross-compiles, so a multi-arch build never runs a
# compiler under QEMU.

ARG NODE_VERSION=24
ARG GO_VERSION=1.27

# ── web: the React app, widget and help center ──────────────────────────────
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
# The pnpm version comes from web/package.json "packageManager" when it is set,
# which is what keeps the lockfile and the installer in step. PNPM_VERSION is
# the fallback for a package.json without one.
ARG PNPM_VERSION=12.10.1
ENV PNPM_HOME=/pnpm PATH=/pnpm:$PATH CI=true
WORKDIR /src
# brand/ holds the design tokens the app imports; contracts/ the API types.
COPY brand/ brand/
COPY contracts/ contracts/
COPY web/package.json web/pnpm-lock.yaml* web/pnpm-workspace.yaml* web/.npmrc* web/
RUN set -eu; \
    if grep -q '"packageManager"' web/package.json; then \
      npm install -g --silent corepack@latest && corepack enable; \
    else \
      npm install -g --silent "pnpm@${PNPM_VERSION}"; \
    fi
RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    cd web && pnpm config set store-dir /pnpm/store && pnpm install --frozen-lockfile
COPY web/ web/
RUN cd web && pnpm build && test -f dist/index.html

# ── admin: the instance admin panel, built to be served at /admin/ ──────────
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS admin
ARG PNPM_VERSION=12.10.1
ENV PNPM_HOME=/pnpm PATH=/pnpm:$PATH CI=true
WORKDIR /src
COPY brand/ brand/
COPY contracts/ contracts/
COPY admin/package.json admin/pnpm-lock.yaml* admin/pnpm-workspace.yaml* admin/.npmrc* admin/
RUN set -eu; \
    if grep -q '"packageManager"' admin/package.json; then \
      npm install -g --silent corepack@latest && corepack enable; \
    else \
      npm install -g --silent "pnpm@${PNPM_VERSION}"; \
    fi
RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    cd admin && pnpm config set store-dir /pnpm/store && pnpm install --frozen-lockfile
COPY admin/ admin/
RUN cd admin && pnpm build:embed && test -f dist/index.html

# ── go: a static binary with the web build embedded ─────────────────────────
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS go
ARG TARGETOS TARGETARCH
ARG VERSION=dev COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod go mod download
COPY . .
# internal/webui embeds dist/; replace whatever the checkout has with the build.
RUN rm -rf internal/webui/dist && mkdir -p internal/webui/dist
COPY --from=web /src/web/dist/ internal/webui/dist/
RUN rm -rf internal/adminui/dist && mkdir -p internal/adminui/dist
COPY --from=admin /src/admin/dist/ internal/adminui/dist/
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
    --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/useopendesk/opendesk/internal/version.Version=${VERSION} -X github.com/useopendesk/opendesk/internal/version.Commit=${COMMIT}" \
      -o /out/opendesk ./cmd/opendesk
# /data is the volume: attachments (STORAGE_DRIVER=local) and the embedded NATS
# JetStream store. Docker seeds a fresh named volume from the image, so the
# directory has to exist here owned by the runtime user, or the nonroot process
# cannot write to it.
RUN mkdir -p /out/data/files /out/data/nats

# ── runtime ─────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev COMMIT=none
LABEL org.opencontainers.image.title="opendesk" \
      org.opencontainers.image.description="The open-source helpdesk: API, workers and web app" \
      org.opencontainers.image.source="https://github.com/useopendesk/opendesk" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=go /out/opendesk /opendesk
COPY --from=go --chown=65532:65532 /out/data /data
# The image is for running opendesk, so it is production unless told otherwise.
ENV DATA_DIR=/data HTTP_ADDR=:8080 OPENDESK_ENV=production
USER 65532:65532
VOLUME /data
# 8080 HTTP. 4222 the embedded NATS, for the realtime gateway (NATS_LISTEN).
EXPOSE 8080 4222
HEALTHCHECK --interval=15s --timeout=4s --start-period=60s --retries=3 \
  CMD ["/opendesk", "healthcheck"]
ENTRYPOINT ["/opendesk"]
CMD ["serve"]
