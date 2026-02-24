#!/usr/bin/env bash
# Run both microservices in development mode
set -euo pipefail

cd "$(dirname "$0")/.."

# Load env
if [ -f .env ]; then
    set -a
    source .env
    set +a
fi

SERVICE="${1:-both}"

# Go commands run from backend/
GO_RUN="go run -C backend"

case "$SERVICE" in
    mdengine|ms1)
        echo "Starting Market Data Engine (MS1)..."
        $GO_RUN ./cmd/mdengine/
        ;;
    indengine|ms2)
        echo "Starting Indicator Engine (MS2)..."
        $GO_RUN ./cmd/indengine/
        ;;
    dashboard|gateway|ms3)
        echo "Starting API Gateway (backend) at http://localhost${GATEWAY_ADDR:-:9090} ..."
        $GO_RUN ./cmd/api_gateway/
        ;;
    api_gateway|ms4)
        echo "Starting API Gateway (MS4) at http://localhost${GATEWAY_ADDR:-:9090} ..."
        $GO_RUN ./cmd/api_gateway/
        ;;
    both)
        echo "Starting MS1 (Market Data Engine) + MS2 (Indicator Engine)..."
        $GO_RUN ./cmd/mdengine/ &
        MS1_PID=$!
        echo "MS1 PID: $MS1_PID"
        
        sleep 3
        
        $GO_RUN ./cmd/indengine/ &
        MS2_PID=$!
        echo "MS2 PID: $MS2_PID"
        
        trap "kill $MS1_PID $MS2_PID 2>/dev/null; exit" SIGINT SIGTERM
        wait -n
        kill $MS1_PID $MS2_PID 2>/dev/null
        ;;
    all)
        echo "Starting MS1 + MS2 + API Gateway..."
        $GO_RUN ./cmd/mdengine/ &
        MS1_PID=$!
        sleep 3
        $GO_RUN ./cmd/indengine/ &
        MS2_PID=$!
        sleep 2
        $GO_RUN ./cmd/api_gateway/ &
        GW_PID=$!
        echo "MS1=$MS1_PID  MS2=$MS2_PID  GW=$GW_PID"
        trap "kill $MS1_PID $MS2_PID $GW_PID 2>/dev/null; exit" SIGINT SIGTERM
        wait -n
        kill $MS1_PID $MS2_PID $GW_PID 2>/dev/null
        ;;
    tickserver)
        echo "Starting Demo Tick Server on ${TICK_SERVER_ADDR:-:9001} ..."
        $GO_RUN ./cmd/tickserver/
        ;;
    mdengine-sim|sim-engine)
        echo "Starting Market Data Engine (SIM) → ${SIM_WS_URL:-ws://localhost:9001/ws} ..."
        $GO_RUN ./cmd/mdengine-sim/
        ;;
    sim)
        echo "Starting SIM stack: tickserver + mdengine-sim + indengine + dashboard..."
        $GO_RUN ./cmd/tickserver/ &
        TICK_PID=$!
        echo "tickserver PID: $TICK_PID"
        sleep 2

        $GO_RUN ./cmd/mdengine-sim/ &
        SIM_PID=$!
        echo "mdengine-sim PID: $SIM_PID"
        sleep 3

        $GO_RUN ./cmd/indengine/ &
        IND_PID=$!
        echo "indengine PID: $IND_PID"
        sleep 2

        $GO_RUN ./cmd/api_gateway/ &
        GW_PID=$!
        echo "api_gateway PID: $GW_PID"

        echo ""
        echo "══════════════════════════════════════════════════"
        echo "  SIM STACK RUNNING"
        echo "  Tick Server  : ws://localhost:${TICK_SERVER_ADDR:-9001}/ws"
        echo "  API Gateway  : http://localhost${GATEWAY_ADDR:-:9090}"
        echo "  Frontend     : http://localhost:5173  (run: cd frontend && npm run dev)"
        echo "  Press Ctrl+C to stop all"
        echo "══════════════════════════════════════════════════"

        trap "kill $TICK_PID $SIM_PID $IND_PID $GW_PID 2>/dev/null; exit" SIGINT SIGTERM
        wait -n
        kill $TICK_PID $SIM_PID $IND_PID $GW_PID 2>/dev/null
        ;;
    *)
        echo "Usage: $0 [mdengine|ms1|indengine|ms2|dashboard|gateway|ms3|both|all|tickserver|mdengine-sim|sim]"
        exit 1
        ;;
esac
