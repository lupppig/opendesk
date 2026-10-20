SCRATCH ?= ./data
export DATABASE_URL ?= postgres://opendesk:opendesk@localhost:55432/opendesk?sslmode=disable

.PHONY: dev-db dev api realtime web admin site docs build test lint web-embed admin-embed e2e

dev-db: ## start local Postgres
	docker compose -f docker-compose.dev.yml -p opendesk-dev up -d --wait

api: ## run the Go API + workers with embedded NATS (listens for the gateway on :4222)
	NATS_LISTEN=0.0.0.0:4222 go run ./cmd/opendesk serve

realtime: ## run the Elixir gateway
	cd realtime && mix phx.server

web: ## run the web app
	cd web && pnpm dev

admin: ## run the instance admin panel (:5174); the API pairs with it in development
	cd admin && pnpm dev

site:
	cd site && pnpm dev

docs:
	cd docs && pnpm dev

build:
	go build -o bin/opendesk ./cmd/opendesk

web-embed: ## build the web app into the Go binary's embed dir
	cd web && pnpm build
	rm -rf internal/webui/dist/assets && cp -R web/dist/. internal/webui/dist/

admin-embed: ## build the admin panel (base /admin/) into the Go binary's embed dir
	cd admin && pnpm build:embed
	rm -rf internal/adminui/dist/assets && cp -R admin/dist/. internal/adminui/dist/

test:
	go test ./...

e2e: ## end-to-end browser run against a running stack (see scripts/e2e/run.sh for the env)
	scripts/e2e/run.sh

lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

# ── deploy ──────────────────────────────────────────────────────────────────
.PHONY: selfhost-smoke selfhost-env selfhost-up selfhost-down images installer-check installer-sha check-migrations tf-check email-worker-test

selfhost-env: ## write .env for the root docker-compose.yml with fresh secrets (never overwrites)
	sh deploy/self-host/gen-env.sh .env $${DOMAIN:-localhost}

selfhost-up: selfhost-env ## build this checkout and run the self-host stack on https://localhost
	docker compose up -d --build --wait

selfhost-down:
	docker compose down

images: ## build both release images locally
	docker build -f deploy/docker/opendesk.Dockerfile -t ghcr.io/useopendesk/opendesk:dev .
	docker build -f deploy/docker/realtime.Dockerfile -t ghcr.io/useopendesk/realtime:dev .

installer-sha: ## regenerate deploy/install/install.sh.sha256 after editing install.sh
	sh deploy/install/gen-sha256.sh

installer-check: ## POSIX parse, shellcheck, idempotency and checksum checks for the installer
	./scripts/check-installer.sh

check-migrations:
	./scripts/check-migrations.sh

tf-check: ## terraform fmt + validate for every root and module
	terraform -chdir=deploy/terraform fmt -check -recursive
	for d in deploy/terraform/environments/* deploy/terraform/modules/*; do \
		terraform -chdir=$$d init -backend=false -input=false >/dev/null && terraform -chdir=$$d validate || exit 1; \
	done

email-worker-test:
	cd deploy/cloudflare/email-worker && pnpm install --frozen-lockfile && pnpm typecheck && pnpm test

selfhost-smoke: ## build and run the root compose under a throwaway project and check it through Caddy
	./scripts/selfhost-smoke.sh
