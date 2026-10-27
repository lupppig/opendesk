#!/usr/bin/env bash
#
# End-to-end browser run (Playwright) against a running stack. It starts the
# helpers itself: the fake OpenAI server (:9900), the fake TypeSafe server (Jev, :9910),
# the SMTP sink (:2526) and the widget's customer site (127.0.0.1:8899), then runs every flow.
#
# Start the stack first, with the API pointed at the helpers:
#
#   APP_URL=http://localhost:5173 API_URL=http://localhost:8080 INBOUND_SECRET=dev \
#   AI_PROVIDER=openai OPENAI_BASE_URL=http://127.0.0.1:9900/v1 OPENAI_API_KEY=x AI_MODEL=gpt-fake \
#   TYPESAFE_API_KEY=x TYPESAFE_API_URL=http://127.0.0.1:9910 \
#   MAIL_DRIVER=smtp SMTP_HOST=127.0.0.1 SMTP_PORT=2526 SMTP_TLS=none make api
#   CHECK_ORIGINS=http://localhost:5173 make realtime
#   VITE_API_URL=http://localhost:8080 VITE_GATEWAY_URL=ws://localhost:4000/socket make web
#
# Registration is rate limited per IP in memory: restart the API between runs.
#
# Env: E2E_APP_URL (http://localhost:5173), E2E_API_URL (http://localhost:8080), E2E_ONLY
# (comma-separated flows: account inbox tickets kb widget ai settings danger
# billing), E2E_SKIP_MFA=1, E2E_HEADED=1, E2E_SHOTS=<dir>, E2E_BAIL=0, E2E_WS_DEBUG=1.
# The billing flow needs the API started with billing on against fake-stripe.mjs.
set -euo pipefail

cd "$(dirname "$0")"
root=$(cd ../.. && pwd)
work=$(mktemp -d)
pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$work"
}
trap cleanup EXIT

[[ -d node_modules/playwright ]] || pnpm install --ignore-workspace --frozen-lockfile
node --test mail-links.test.mjs
npx playwright install chromium >/dev/null
[[ -f "$root/web/dist/widget/v1.js" ]] || (cd "$root/web" && pnpm build:widget)

node fake-ai.mjs 9900 >"$work/ai.log" 2>&1 & pids+=($!)
node fake-typesafe.mjs 9910 >"$work/typesafe.log" 2>&1 & pids+=($!)
node smtp-sink.mjs "$work/mail.jsonl" 2526 >"$work/smtp.log" 2>&1 & pids+=($!)
node widget-host.mjs 8899 >"$work/widget.log" 2>&1 & pids+=($!)
sleep 1

E2E_MAIL_SINK="$work/mail.jsonl" node run.mjs
