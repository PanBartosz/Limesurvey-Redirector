#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

project="lsr-e2e"
compose=(docker compose --project-name "$project" --env-file e2e/test.env -f compose.yaml -f e2e/compose.yaml)

cleanup() {
  "${compose[@]}" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup
"${compose[@]}" up -d --build

for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18100/healthz >/dev/null; then
    break
  fi
  sleep 1
 done

PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install --prefix e2e/.runner playwright >/dev/null
node e2e/run.cjs
