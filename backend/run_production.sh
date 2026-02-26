#!/bin/bash
# ═══════════════════════════════════════════════════════
#  PRODUCTION runner — called by Air (.air.production.toml)
#  Services: api_gateway, indengine, mdengine
#  Uses real Angel One feed (STAGING_MODE=false)
# ═══════════════════════════════════════════════════════

# Load env vars from repo root .env
if [ -f "../.env" ]; then
  set -a
  source ../.env
  set +a
fi

trap 'kill $(jobs -p) 2>/dev/null; exit 0' SIGINT SIGTERM

echo "╔═══════════════════════════════════════════════════╗"
echo "║  PRODUCTION MODE                                 ║"
echo "║  Services: mdengine + indengine + api_gateway    ║"
echo "║  Data: Angel One Live Feed                       ║"
echo "╚═══════════════════════════════════════════════════╝"

./tmp/mdengine    2>&1 | sed 's/^/[mdengine]    /' &
./tmp/indengine   2>&1 | sed 's/^/[indengine]   /' &
./tmp/api_gateway 2>&1 | sed 's/^/[api_gateway] /' &

wait
