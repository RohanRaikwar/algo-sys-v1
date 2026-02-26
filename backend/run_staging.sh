#!/bin/bash
# ═══════════════════════════════════════════════════════
#  STAGING runner — called by Air (.air.staging.toml)
#  Services: tickserver, mdengine, indengine, api_gateway
#  Uses local tickserver (STAGING_MODE=true)
# ═══════════════════════════════════════════════════════

trap 'kill $(jobs -p) 2>/dev/null; exit 0' SIGINT SIGTERM

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
