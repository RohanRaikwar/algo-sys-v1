#!/bin/bash
# ═══════════════════════════════════════════════════════
#  STAGING runner — called by Air (.air.staging.toml)
#  Services: tickserver, mdengine, indengine, api_gateway
#  Uses local tickserver (STAGING_MODE=true)
# ═══════════════════════════════════════════════════════

trap 'kill $(jobs -p) 2>/dev/null; exit 0' SIGINT SIGTERM

# Source env files: base .env first, then .env.staging overrides
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if [ -f "$REPO_ROOT/.env" ]; then
    set -a; source "$REPO_ROOT/.env"; set +a
fi
if [ -f "$REPO_ROOT/.env.staging" ]; then
    set -a; source "$REPO_ROOT/.env.staging"; set +a
fi
export STAGING_MODE=true

echo "╔═══════════════════════════════════════════════════╗"
echo "║  STAGING MODE                                    ║"
echo "║  Services: tickserver + mdengine + indengine     ║"
echo "║            + api_gateway                         ║"
echo "║  Data: Local Tickserver (simulated)              ║"
echo "╚═══════════════════════════════════════════════════╝"

./tmp/tickserver  2>&1 | sed 's/^/[tickserver]  /' &
sleep 1
./tmp/mdengine    2>&1 | sed 's/^/[mdengine]    /' &
./tmp/indengine   2>&1 | sed 's/^/[indengine]   /' &
./tmp/api_gateway 2>&1 | sed 's/^/[api_gateway] /' &

wait
