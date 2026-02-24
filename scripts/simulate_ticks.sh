#!/usr/bin/env bash
# Simulate raw tick data by publishing 3 ticks per second on a single Redis PubSub channel.
# Usage: bash scripts/simulate_ticks.sh [duration_seconds]

set -euo pipefail

DURATION=${1:-120}   # default 2 minutes
TOKEN="99926000"
EXCHANGE="NSE"
CHANNEL="pub:tick:${EXCHANGE}:${TOKEN}"

# Base price around Nifty 50 (in paise, so 25570.00 = 2557000)
BASE_PRICE=2557000
SPREAD=500  # ±5 rupees random walk

echo "🔄 Simulating raw ticks for ${DURATION}s (3 ticks/sec) on channel: ${CHANNEL}"
echo "   Base price: $(echo "scale=2; $BASE_PRICE/100" | bc) INR"
echo ""

price=$BASE_PRICE
count=0
end=$((SECONDS + DURATION))

while [ $SECONDS -lt $end ]; do
    # Publish 3 ticks in this second
    for i in 1 2 3; do
        # Random walk: price ±SPREAD paise
        delta=$(( (RANDOM % (SPREAD * 2 + 1)) - SPREAD ))
        price=$(( price + delta ))

        # Ensure price stays reasonable
        if [ $price -lt 2500000 ]; then price=2500000; fi
        if [ $price -gt 2650000 ]; then price=2650000; fi

        # Random quantity
        qty=$(( RANDOM % 500 + 1 ))

        ts=$(date -u +"%Y-%m-%dT%H:%M:%S.%3NZ")
        count=$((count + 1))

        # Build tick JSON (raw tick: token, exchange, price, qty, tick_ts)
        tick="{\"token\":\"${TOKEN}\",\"exchange\":\"${EXCHANGE}\",\"price\":${price},\"qty\":${qty},\"tick_ts\":\"${ts}\"}"

        # Publish to single PubSub channel
        redis-cli PUBLISH "${CHANNEL}" "${tick}" > /dev/null 2>&1

        price_rupees=$(echo "scale=2; $price/100" | bc)
        printf "\r  [%5d] %s  ltp=₹%-10s qty=%-5s" "$count" "$ts" "$price_rupees" "$qty"

        # Sleep ~333ms (3 ticks per second)
        sleep 0.333
    done
done

echo ""
echo "✅ Simulation complete: $count ticks published on ${CHANNEL}"
