# Trading System v1 — Architecture

> **Go + React real-time market data & indicator engine** for Indian markets (NSE/BSE) via Angel One SmartAPI.

---

## High-Level Data Flow

```
 Angel One WS / Tick Server (staging)
         │
         ▼
 ┌──────────────────────────────────────────────┐
 │  MS1 — mdengine                              │
 │  WS Ingest → 1s Aggregator → TF Builder     │
 │       │            │              │          │
 │       │            ▼              ▼          │
 │       │      Redis + SQLite   Redis + SQLite │
 │       │        (1s candles)   (TF candles)   │
 │       │            │              │          │
 │       │            ▼              ▼          │
 │       │      PubSub (1s)    PubSub + Stream  │
 └───────┼────────────┼──────────────┼──────────┘
         │            │              │
         │            ▼              ▼
 ┌───────┼────────────────────────────────┐
 │  MS2 — indengine                       │
 │  Streams (XREADGROUP) → Process        │
 │  PubSub  (1s forming) → ProcessPeek    │
 │       │                                │
 │       ▼                                │
 │  Redis PubSub (indicator results)      │
 └───────┼────────────────────────────────┘
         │
         ▼
 ┌─────────────────────────────────────────┐
 │  api_gateway (:9090)                    │
 │  Redis PubSub → Hub → WebSocket        │
 │  SQLite → REST API (/api/candles, etc.) │
 └───────┬─────────────────────────────────┘
         │
         ▼
 ┌───────────────────────────┐
 │  React Frontend (:5173)   │
 │  TradingChart + Dashboard │
 └───────────────────────────┘
```

---

## Service Architecture

| Service | Binary | Port | Role |
|---------|--------|------|------|
| **mdengine** (MS1) | `cmd/mdengine` | `:9091` (metrics) | Market data ingestion, OHLC aggregation, TF resampling, persistence |
| **indengine** (MS2) | `cmd/indengine` | `:9095` (HTTP) | Indicator computation (SMA, EMA, RSI, SMMA) with snapshot/restore |
| **api_gateway** | `cmd/api_gateway` | `:9090` | REST + WebSocket hub → serves React frontend |
| **tickserver** | `cmd/tickserver` | `:9001` | Simulated tick generator for staging/testing |
| **backtest** | `cmd/backtest` | — | Historical replay through indicator engine |
| **api** | `cmd/api` | — | Standalone REST API |

---

## Data Pipeline (MS1 — mdengine)

The pipeline runs 24/7; the WebSocket feed is gated by market hours (9:15 AM – 3:30 PM IST, Mon–Fri).

```
Market-Hours Gated
  TOTP Login → Angel One WS (prod) / tickserver WS (staging)

24/7 Pipeline
  tickCh (10K) → Aggregator (1s OHLC) → candleCh (5K) → Fan-out
                                                          │
                 ┌────────────────────────────────────────┤
                 ▼                                        ▼
          Redis Writer (1s)                      TF Builder (60s/120s/300s)
          SQLite Writer (1s)                            │
          PubSub (pub:candle:1s:*)              ┌───────┤
                                                ▼       ▼
                                        Redis Stream   Redis PubSub
                                        SQLite (TF)    (forming candles)
```

### Key Design Decisions

- **Channel-based pipeline** — All stages communicate via buffered Go channels (10K ticks, 5K candles)
- **Fan-out bus** — Single `candleCh` fanned out to Redis, SQLite, TF Builder via `bus.Fanout` with drop detection
- **Hot path isolation** — TF Builder runs inline (`Run1`) to avoid channel overhead; storage writes are off hot path
- **Staleness rejection** — TF Builder rejects candles >2s stale to prevent corruption
- **Market hours lifecycle** — Fresh TOTP + session at each market open; context deadline at 3:30 PM auto-disconnects WS
- **Staging mode** — Connects to `tickserver` via `wssim` package; env `STAGING_MODE=true`

---

## Indicator Engine (MS2 — indengine)

```
Startup
  1. Restore Snapshot (Redis → SQLite fallback)
  2. Replay Delta (from last StreamID)
  3. Recover Pending (PEL reclaim)
        │
        ▼
Steady State
  Redis Streams (XREADGROUP) → engine.Process()    (completed candles)
  Redis PubSub  (1s forming) → engine.ProcessPeek() (forming previews, read-only)
        │
        ▼
  Results → Redis PubSub (pub:ind:*) → api_gateway → WebSocket → React
  Snapshot every 30s → Redis + SQLite
```

### Indicator Types

| Indicator | Implementation | Compute | Peek | Hot Path Optimized |
|-----------|---------------|---------|------|--------------------|
| **SMA** | Ring buffer | O(1) update | ✅ | ✅ |
| **EMA** | Exponential smoothing | O(1) update | ✅ | ✅ |
| **RSI** | Wilder's smoothing | O(1) update | ✅ | ✅ |
| **SMMA** | Smoothed MA | O(1) update | ✅ | ✅ |

### Features

- **O(1) TF lookup** — `tfIndex` map replaces linear scan in `Process`/`ProcessPeek`
- **Live hot-reload** — `POST /reload` or Redis `config:indicators` channel
- **Snapshot/restore** — periodic checkpoints; delta replay on restart
- **PEL reclaimer** — recovers stale consumer group messages at configurable intervals
- **ProcessPeek** — read-only computation on forming candles (no state mutation)
- **Zero-copy JSON** — hand-crafted `IndicatorResult.JSON()` avoids reflection

---

## Storage Layer

### Redis (`internal/store/redis`)

| Component | File | Purpose |
|-----------|------|---------|
| **Writer** | `writer.go` | Pipelined writes: `SET` + `XADD` + `PUBLISH` |
| **Reader** | `reader.go` | `XREADGROUP` consumer, `XRANGE` replay, snapshot R/W, PubSub |
| **BufferedWriter** | `bufferedwriter.go` | Batched writes for high throughput |
| **CircuitBreaker** | `circuitbreaker.go` | Automatic failover on Redis connectivity issues |

**Key patterns:**
- `candle:1s:{exchange}:{token}` — 1s candle stream (~12K max)
- `candle:{tf}s:{exchange}:{token}` — TF candle streams
- `ind:{name}:{tf}s:{exchange}:{token}` — Indicator result streams
- `pub:candle:*` / `pub:ind:*` — PubSub channels for real-time push

**Hot-path optimizations:**
- `itoa()` replaces `fmt.Sprintf` for integer keys
- `unsafe.Pointer` zero-copy `[]byte→string` for JSON payloads
- String concatenation instead of `fmt.Sprintf` for channel names
- `TFCandle.PubSubChannel()` pre-builds PubSub channel name

### SQLite (`internal/store/sqlite`)

| Table | Schema |
|-------|--------|
| `candles_1s` | `(exchange, token, ts)` PK — raw 1s OHLCV |
| `candles_tf` | `(exchange, token, tf, ts)` PK — resampled TF candles |
| `indicator_snapshots` | Auto-pruned to last 10 snapshots |

- **WAL mode** with `SYNCHRONOUS=NORMAL` for write performance
- **Transaction batching** — flushes every 100 rows or 200ms

---

## API Gateway (`internal/gateway`)

```
Redis PubSub ──→ Hub.broadcast() ──→ WebSocket clients
                      │
                      ▼
              Hand-crafted JSON envelope
              (no json.Marshal reflection)
                      │
                      ▼
              client.writePump()
              (write coalescing: batches
               queued messages into single
               WS frame, \n-separated)
```

| File | Responsibility |
|------|---------------|
| `hub.go` | Hub, PubSub subscription loop, broadcast, active config |
| `client.go` | WS client lifecycle, write coalescing, readPump |
| `handlers.go` | REST endpoints (`/api/candles`, `/api/indicators`, `/api/config`, `/health`) |
| `metrics.go` | System metrics collection, CPU/memory sampling |

### Features

- **Delta sync** — clients send `last_ts` on reconnect to receive only new data
- **Write coalescing** — batches queued WS messages into a single frame with `\n` separators
- **Hand-crafted envelope** — `broadcast()` builds JSON without `json.Marshal` (~25× faster)
- **Metrics broadcast** — system metrics (CPU, memory, goroutines) pushed every 2s
- **Market status** — open/closed state from `markethours` package
- **Compression** — per-message deflate enabled (~70% bandwidth reduction)

---

## React Frontend (`frontend/`)

Built with **Vite + React + TypeScript + Zustand** (no TailwindCSS).

```
src/
├── App.tsx                    # Root layout, routes useWebSocket
├── hooks/
│   └── useWebSocket.ts        # WS connection, reconnect, batch message parsing
├── store/
│   ├── useWSStore.ts          # WS connection state, metrics, latency
│   ├── useCandleStore.ts      # Candle + indicator data, TF aggregation
│   └── useAppStore.ts         # App config, active indicator entries
├── components/
│   ├── chart/
│   │   └── TradingChart.tsx   # Lightweight Charts candlestick + indicators
│   ├── layout/
│   │   ├── Header.tsx         # Top bar with token/TF selector
│   │   ├── StatusBar.tsx      # Connection status + metrics
│   │   └── ReconnectBanner.tsx
│   ├── health/                # System health display
│   ├── settings/              # Indicator configuration modal
│   └── ErrorBoundary.tsx
├── types/
│   └── ws.ts                  # WSEnvelope, CandlePayload, IndicatorPayload
├── utils/
│   └── helpers.ts             # parseChannel, formatters
└── services/
    └── api.ts                 # REST API client
```

### Features

- **Real-time chart** — Lightweight Charts with candlestick + indicator overlays
- **1s forming candle aggregation** — aggregates 1s candles into selected TF in-browser
- **Batch WS processing** — handles `\n`-separated batched messages from write-coalescing
- **Auto-reconnect** — exponential backoff with delta sync on reconnect
- **Dynamic indicators** — runtime indicator selection via settings modal
- **Latency display** — server→client latency computed from envelope timestamps

---

## Latency-Optimized Pipeline

The system is optimized for **sub-10ms** end-to-end latency (tick → indicator on dashboard):

| Stage | Optimization | Impact |
|-------|-------------|--------|
| `TFCandle.JSON()` | Hand-crafted builder (no `json.Marshal`) | ~10× faster |
| `IndicatorResult.JSON()` | Manual `strconv.AppendFloat/Int` | ~10× faster |
| `engine.Process()` | O(1) `tfIndex` map (was linear scan) | Constant time |
| `Subscribe1sForPeek` | Pre-built TF strings, string concat | ~5× faster |
| `WriteIndicatorBatch` | Zero-copy string, `PubSubChannel()` | ~5× faster |
| `RunFormingTFCandles` | String concat, unsafe string | ~3× faster |
| `Hub.broadcast()` | Hand-crafted JSON envelope | ~25× faster |
| `writePump` | Write coalescing (batch queued msgs) | Fewer syscalls |
| Hot-path logging | Removed per-tick `log.Printf` | ~100μs saved |

---

## Project Structure

```
trading-systemv1/
├── backend/
│   ├── cmd/                          # Service entry points
│   │   ├── mdengine/                 #   MS1: Market Data Engine
│   │   ├── indengine/                #   MS2: Indicator Engine
│   │   ├── api_gateway/              #   API Gateway + WS Hub
│   │   ├── tickserver/               #   Simulated tick generator
│   │   ├── backtest/                 #   Historical replay
│   │   └── api/                      #   Standalone REST API
│   │
│   ├── config/                       # Centralized config (env vars)
│   │
│   ├── internal/
│   │   ├── marketdata/
│   │   │   ├── ws/                   #   Angel One WS V3 ingestion
│   │   │   ├── wssim/                #   Simulated WS ingestion (staging)
│   │   │   ├── agg/                  #   1s OHLC aggregator
│   │   │   ├── bus/                  #   Fan-out channel bus
│   │   │   ├── tfbuilder/            #   Multi-TF resampler
│   │   │   └── replay/               #   Historical data replayer
│   │   ├── indicator/
│   │   │   ├── engine.go             #   Multi-TF indicator engine (O(1) lookup)
│   │   │   ├── sma.go                #   Simple Moving Average (ring buffer)
│   │   │   ├── ema.go                #   Exponential Moving Average
│   │   │   ├── rsi.go                #   Relative Strength Index (Wilder)
│   │   │   ├── smma.go               #   Smoothed Moving Average
│   │   │   ├── snapshot.go           #   Snapshot serialization
│   │   │   ├── restorer.go           #   Engine restoration from snapshot
│   │   │   └── reload.go             #   Hot-reload logic
│   │   ├── store/
│   │   │   ├── redis/                #   Writer, Reader, BufferedWriter, CircuitBreaker
│   │   │   └── sqlite/               #   SQLite writer/reader (WAL mode)
│   │   ├── gateway/                  #   API Gateway (refactored from cmd/)
│   │   │   ├── hub.go                #     Hub + PubSub + broadcast
│   │   │   ├── client.go             #     WS client + writePump (coalescing)
│   │   │   ├── handlers.go           #     REST endpoints + /health
│   │   │   └── metrics.go            #     SystemMetrics + CPU sampling
│   │   ├── model/                    #   Domain types (Tick, Candle, TFCandle, etc.)
│   │   ├── metrics/                  #   Prometheus metrics + health server
│   │   ├── markethours/              #   Market schedule (9:15–3:30 IST + holidays)
│   │   ├── logger/                   #   Structured logging
│   │   ├── ringbuf/                  #   Lock-free ring buffer
│   │   ├── execution/                #   Order execution engine
│   │   │   ├── executor.go           #     Signal consumer (interface)
│   │   │   ├── paper.go              #     Paper trading executor
│   │   │   └── journal.go            #     Trade journal (SQLite)
│   │   ├── strategy/                 #   Trading strategy framework
│   │   │   ├── engine.go             #     Multi-strategy router
│   │   │   └── sma_crossover.go      #     SMA 9/21 crossover + RSI filter
│   │   ├── portfolio/                #   Portfolio management
│   │   │   ├── portfolio.go          #     Position tracking
│   │   │   ├── risk.go               #     Risk limits + drawdown tracking
│   │   │   └── pnl.go                #     Realized + unrealized P&L
│   │   └── notification/             #   Alert/notification system
│   │       ├── notifier.go           #     Interface + LogNotifier
│   │       ├── telegram.go           #     Telegram Bot API
│   │       └── webhook.go            #     Generic HTTP webhook
│   │
│   └── pkg/                          # Public packages
│       └── smartconnect/             #   Angel One SmartAPI Go client
│
├── frontend/                         # React + Vite + TypeScript
│   └── src/
│       ├── components/               #   Chart, Layout, Health, Settings
│       ├── hooks/                    #   useWebSocket (batch parsing)
│       ├── store/                    #   Zustand (WS, Candle, App)
│       ├── types/                    #   WS envelope types
│       └── utils/                    #   Helpers
│
├── data/                             # Runtime data (SQLite DB)
├── scripts/                          # run_dev.sh (sim/all/individual)
├── .env                              # Configuration
└── .env.staging                      # Staging overrides
```

---

## Data Models (`internal/model`)

| Type | Fields | Used By |
|------|--------|---------|
| `Tick` | Token, Exchange, Price (paise), Qty, TickTS | WS Ingest → Aggregator |
| `Candle` | Token, Exchange, TS, OHLCV, TicksCount | Aggregator → Fan-out |
| `TFCandle` | Token, Exchange, TF, TS, OHLCV, Count, Forming | TF Builder → Redis/SQLite/Indicators |
| `IndicatorResult` | Name, Token, Exchange, TF, Value, TS, Ready, Live | Indicator Engine → Redis → Dashboard |
| `Instrument` | Token, Exchange, Symbol metadata | Configuration |
| `Order` / `Position` | Execution and portfolio tracking | Strategy execution |

---

## Dependencies

| Dependency | Version | Purpose |
|------------|---------|---------|
| `gorilla/websocket` | v1.5.3 | WS connections (Angel One + api_gateway) |
| `go-redis/redis` | v8.11.5 | Redis Streams, PubSub, KV |
| `mattn/go-sqlite3` | v1.14.24 | SQLite with WAL mode (CGo) |
| `pquerna/otp` | v1.5.0 | TOTP generation for Angel One login |
| `prometheus/client_golang` | v1.20.5 | Prometheus metrics export |
| `lightweight-charts` | — | TradingView candlestick charting (frontend) |
| `zustand` | — | React state management (frontend) |

---

## Configuration

All services configured via **environment variables** (`.env` / `.env.staging`):

| Variable | Default | Service |
|----------|---------|---------|
| `ANGEL_API_KEY` | — (required) | mdengine |
| `ANGEL_CLIENT_CODE` | — (required) | mdengine |
| `ANGEL_PASSWORD` | — (required) | mdengine |
| `ANGEL_TOTP_SECRET` | — (required) | mdengine |
| `REDIS_ADDR` | `localhost:6379` | all |
| `REDIS_PASSWORD` | `""` | all |
| `SQLITE_PATH` | `data/candles.db` | mdengine, indengine |
| `SUBSCRIBE_TOKENS` | `1:99926000` | mdengine, indengine, api_gateway |
| `ENABLED_TFS` | `60,120,180,300` | mdengine, indengine, api_gateway |
| `INDICATOR_CONFIGS` | `SMA:9,SMA:20,...` | indengine |
| `METRICS_ADDR` | `:9091` | mdengine |
| `GATEWAY_ADDR` | `:9090` | api_gateway |
| `STAGING_MODE` | `false` | mdengine |
| `SIM_WS_URL` | `ws://localhost:9001/ws` | mdengine (staging) |

---

## Running

```bash
# Load env
set -a && source .env && set +a

# Individual services
go run -C backend ./cmd/tickserver/     # Simulated ticks
go run -C backend ./cmd/mdengine/       # Market data engine
go run -C backend ./cmd/indengine/      # Indicator engine
go run -C backend ./cmd/api_gateway/    # API Gateway

# Frontend
cd frontend && npm run dev              # Vite dev server :5173

# Or use the script
./scripts/run_dev.sh sim                # Full sim stack
./scripts/run_dev.sh all                # MS1 + MS2 + api_gateway
```
