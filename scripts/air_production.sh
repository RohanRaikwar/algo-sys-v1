#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════
#  Start PRODUCTION stack with Air (live-reload)
#  Services: api_gateway + indengine + mdengine
#  Env: .env (STAGING_MODE=false)
#  Usage: ./scripts/air_production.sh
# ═══════════════════════════════════════════════════════
set -euo pipefail

cd "$(dirname "$0")/.."
export PATH="$PATH:$HOME/go/bin"

echo "🚀 Loading PRODUCTION environment (.env)..."
if [ -f .env ]; then
    set -a
    source .env
    set +a
fi

# Force production mode
export STAGING_MODE=false

echo ""
echo "╔═══════════════════════════════════════════════════╗"
echo "║  🟢 PRODUCTION — Air Live-Reload                 ║"
echo "║  api_gateway : http://localhost${GATEWAY_ADDR:-:9090}        ║"
echo "║  indengine   : :9095                             ║"
echo "║  mdengine    : Angel One Live Feed               ║"
echo "║  frontend    : run: cd frontend && npm run dev   ║"
echo "║  Press Ctrl+C to stop                            ║"
echo "╚═══════════════════════════════════════════════════╝"
echo ""

cd backend
exec air -c .air.production.toml
