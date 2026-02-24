# Trading System v1 — Architecture

> **Go-based real-time market data & indicator engine** for Indian markets (NSE/BSE) via Angel One SmartAPI.

---

## High-Level Overview (ASCII)

```text
External: Angel One WS
        |
        v
MS1 mdengine: WS Ingest -> 1s Aggregator -> TF Builder
        |                  |                |
        |                  |                v
        |                  |             Redis (TF candles)
        |                  v
        |               Redis (1s candles)
        |                  |
        |                  v
        |               SQLite (1s candles)
        |
        v
MS2 indengine: Indicator Engine <--- Redis Streams
        |
        v
Redis (indicator results)
        |
        +--> MS3 dashboard: WS Hub/REST -> Browser UI
        |
        +--> MS4 api_gateway: REST+WS -> TradingView UI

MS4 also reads historical data from SQLite.
```

---

## End-to-End System Flow (ASCII)

```text
Market Hours -> Angel One (WS+REST)
      |
      v
MS1 mdengine
  WS Ingest -> 1s Aggregator -> TF Builder
      |              |             |
      |              |             +--> Redis (TF candles) -> MS2 indengine
      |              |             +--> SQLite (TF candles)
      |              +--> Redis (1s candles)
      |              +--> SQLite (1s candles)
      |
      v
MS2 indengine -> Redis (indicator results)
      |
      +--> MS3 dashboard (WS/REST) -> Browser UI
      +--> MS4 api_gateway (WS/REST) -> TradingView UI

Strategy/Execution/Portfolio/Notifications
  Redis (candles+indicators) -> Strategy -> Execution -> Angel One REST
  Execution -> Portfolio -> Strategy (feedback)
  Strategy -> Notifications
```

### Notes
- Strategy/Execution/Portfolio/Notifications are logical components in `internal/strategy`, `internal/execution`, `internal/portfolio`, and `internal/notification`.
- This diagram shows the full loop including execution flow.

---

## Service Architecture

| Service | Binary | Port | Role |
|---|---|---|---|
| **mdengine** (MS1) | `cmd/mdengine` | `:9090` (metrics) | Market data ingestion, OHLC aggregation, TF resampling, persistence |
| **indengine** (MS2) | `cmd/indengine` | `:9095` (HTTP) | Indicator computation (SMA, EMA, RSI) with snapshot/restore |
| **dashboard** (MS3) | `cmd/dashboard` | `:8080` | Real-time dashboard with WebSocket hub + REST API |
| **api_gateway** (MS4) | `cmd/api_gateway` | `:9090` | TradingView-style chart frontend with REST + WS |
| **tickserver** | `cmd/tickserver` | `:9001` | Simulated tick generator for testing |
| **mdengine-sim** | `cmd/mdengine-sim` | — | mdengine with simulated WS source |
| **backtest** | `cmd/backtest` | — | Historical replay through indicator engine |

---

## Data Pipeline (MS1 — mdengine)

The core pipeline runs 24/7; only the WebSocket feed is gated by market hours (9:15 AM – 3:30 PM IST, Mon–Fri).

```text
Market-Hours Gated
  Fresh Login (TOTP + Session) -> Angel One WS

24/7 Pipeline
  tickCh (10,000) -> Aggregator (1s OHLC) -> candleCh (5,000) -> Fan-out
                                         |\
                                         | +--> Redis Writer (1s)
                                         | +--> SQLite Writer (1s)
                                         | +--> TF Builder (60s/120s/180s/300s)
                                         |          |
                                         |          +--> Redis Writer (TF)
                                         |          +--> SQLite Writer (TF)
                                         |          +--> Redis PubSub (forming candles)
```

### Key Design Decisions

- **Channel-based pipeline** — All stages communicate via buffered Go channels (10K ticks, 5K candles)
- **Fan-out bus** — Single `candleCh` is fanned out to Redis, SQLite, and TF Builder via `bus.Fanout` with drop detection
- **Hot path isolation** — TF Builder runs inline (`Run1`) to avoid channel overhead; storage writes are off the hot path
- **Staleness rejection** — TF Builder rejects candles > 2s stale to prevent corruption
- **Market hours lifecycle** — Fresh TOTP + session at each market open; context deadline at 3:30 PM auto-disconnects WS

---

## Indicator Engine (MS2 — indengine)

```text
Startup
  Restore Snapshot (Redis -> SQLite fallback)
  Replay Delta (from last StreamID)
  Recover Pending (PEL reclaim)
        |
        v
Indicator Engine

Steady State
  Redis Streams (XREADGROUP) -> Indicator Engine
  Redis PubSub (1s forming) -> ProcessPeek
  Indicator Engine -> Redis (XADD + PubSub)
  Snapshot every 30s
```

### Indicator Types

| Indicator | Implementation | Compute | Peek Support |
|---|---|---|---|
| **SMA** | Ring buffer | O(1) update | ✅ |
| **EMA** | Exponential smoothing | O(1) update | ✅ |
| **RSI** | Wilder's smoothing | O(1) update | ✅ |

### Features
- **Live hot-reload** via `POST /reload` — dynamically add/remove indicators without restart
- **Snapshot/restore** — periodic checkpoints to Redis + SQLite; delta replay on restart
- **PEL reclaimer** — recovers stale consumer group messages at configurable intervals
- **ProcessPeek** — read-only computation on forming candles (no state mutation) for live indicator preview

---

## Storage Layer

### Redis (`internal/store/redis`)

| Component | Purpose |
|---|---|
| **Writer** | Pipelined writes: `SET latest` + `XADD stream` + `PUBLISH pubsub` |
| **Reader** | `XREADGROUP` consumer, `XRANGE` replay, snapshot R/W, stream discovery |
| **BufferedWriter** | Batched writes for high throughput |
| **CircuitBreaker** | Automatic failover on Redis connectivity issues |

**Key patterns:**
- `candle:1s:{exchange}:{token}` — 1s candle stream (~12K max)
- `candle:{tf}s:{exchange}:{token}` — TF candle streams
- `ind:{name}:{tf}s:{exchange}:{token}` — Indicator result streams
- `pub:candle:*` / `pub:ind:*` — PubSub channels for real-time push

### SQLite (`internal/store/sqlite`)

| Table | Schema |
|---|---|
| `candles_1s` | `(exchange, token, ts)` PK — raw 1s OHLCV |
| `candles_tf` | `(exchange, token, tf, ts)` PK — resampled TF candles |
| `indicator_snapshots` | Auto-pruned to last 10 snapshots |

- **WAL mode** with `SYNCHRONOUS=NORMAL` for write performance
- **Transaction batching** — flushes every 100 rows or 200ms, whichever first

---

## Dashboard (MS3) & API Gateway (MS4)

```text
MS3 — Dashboard (:8080)
  Redis PubSub (pub:ind:*, pub:tick:*)
        |
        v
  WebSocket Hub -> Broadcast -> Browser UI
  REST API (/api/candles, /api/indicators, /api/config, /api/metrics)
  Embedded Static (go:embed)

MS4 — API Gateway (:9090)
  REST API (/candles, /indicators, /tokens, /health)
  WebSocket (/ws subscribe protocol)
  TradingView Chart Frontend

Redis Streams -> REST API (MS4)
Redis PubSub  -> WebSocket (MS4)
SQLite History -> REST API (MS4)
```

### Dashboard Features
- **Delta sync** — clients send `last_ts` on reconnect to receive only new data
- **Metrics broadcast** — system metrics (CPU, memory, goroutines) pushed every 2s
- **Market status** — broadcasts open/closed state from `markethours` package
- **Active config** — runtime indicator display filter via `POST /api/indicators/active`
- **Compression** — per-message deflate enabled (~70% bandwidth reduction)

---

## Project Structure

```
trading-systemv1/
├── cmd/                          # Service entry points
│   ├── mdengine/                 #   MS1: Market Data Engine
│   ├── indengine/                #   MS2: Indicator Engine
│   ├── dashboard/                #   MS3: Real-time Dashboard
│   │   └── static/               #     Embedded web UI (HTML/CSS/JS)
│   ├── api_gateway/              #   MS4: TradingView-style Gateway
│   │   └── static/               #     Chart frontend
│   ├── tickserver/               #   Simulated tick generator
│   ├── mdengine-sim/             #   mdengine with simulated feed
│   ├── backtest/                 #   Historical replay
│   └── api/                      #   Standalone REST API
│
├── config/                       # Centralized config (env vars)
│   └── config.go
│
├── internal/                     # Private packages
│   ├── marketdata/
│   │   ├── ws/                   #   Angel One WS ingestion
│   │   ├── wssim/                #   Simulated WS ingestion
│   │   ├── agg/                  #   1s OHLC aggregator
│   │   ├── bus/                  #   Fan-out channel bus
│   │   ├── tfbuilder/            #   Multi-TF resampler
│   │   └── replay/               #   Historical data replayer
│   ├── indicator/
│   │   ├── engine.go             #   Multi-TF indicator engine
│   │   ├── sma.go / ema.go / rsi.go  # Indicator implementations
│   │   ├── snapshot.go           #   Snapshot serialization
│   │   ├── restorer.go           #   Engine restoration from snapshot
│   │   └── reload.go             #   Hot-reload logic
│   ├── store/
│   │   ├── redis/                #   Redis writer/reader/circuit breaker
│   │   └── sqlite/               #   SQLite writer/reader (WAL mode)
│   ├── gateway/                  #   API Gateway server/router/WS/store
│   ├── model/                    #   Domain types (Tick, Candle, TFCandle, etc.)
│   ├── metrics/                  #   Prometheus metrics + health server
│   ├── markethours/              #   Market schedule (9:15–3:30 IST + holidays)
│   ├── logger/                   #   Structured logging
│   ├── ringbuf/                  #   Lock-free ring buffer
│   ├── execution/                #   Order execution engine
│   ├── strategy/                 #   Trading strategy framework
│   ├── portfolio/                #   Portfolio management
│   └── notification/             #   Alert/notification system
│
├── pkg/                          # Public packages
│   └── smartconnect/             #   Angel One SmartAPI Go client
│       └── websocket.go          #     WebSocket V3 with binary parsing
│
├── data/                         # Runtime data (SQLite DB, logs)
├── scripts/                      # Utility scripts
└── docs/                         # Documentation
```

---

## Data Models (`internal/model`)

| Type | Fields | Used By |
|---|---|---|
| `Tick` | Token, Exchange, Price (paise), Qty, TickTS | WS Ingest → Aggregator |
| `Candle` | Token, Exchange, TS, OHLCV, TicksCount | Aggregator → Fan-out |
| `TFCandle` | Token, Exchange, TF, TS, OHLCV, Count, Forming | TF Builder → Redis/SQLite/Indicators |
| `IndicatorResult` | Name, Token, Exchange, TF, Value, TS, Ready, Live | Indicator Engine → Redis → Dashboard |
| `Instrument` | Token, Exchange, Symbol metadata | Configuration |
| `Order` / `Position` | Execution and portfolio tracking | Strategy execution |

---

## Dependencies

| Dependency | Version | Purpose |
|---|---|---|
| `gorilla/websocket` | v1.5.3 | WS connections (Angel One + Dashboard + Gateway) |
| `go-redis/redis` | v8.11.5 | Redis Streams, PubSub, KV |
| `mattn/go-sqlite3` | v1.14.24 | SQLite with WAL mode (CGo) |
| `pquerna/otp` | v1.5.0 | TOTP generation for Angel One login |
| `prometheus/client_golang` | v1.20.5 | Prometheus metrics export |

---

## Configuration

All services are configured via **environment variables** (`.env` file):

| Variable | Default | Service |
|---|---|---|
| `ANGEL_API_KEY` | — (required) | mdengine |
| `ANGEL_CLIENT_CODE` | — (required) | mdengine |
| `ANGEL_PASSWORD` | — (required) | mdengine |
| `ANGEL_TOTP_SECRET` | — (required) | mdengine |
| `REDIS_ADDR` | `localhost:6379` | all |
| `REDIS_PASSWORD` | `""` | all |
| `SQLITE_PATH` | `data/candles.db` | mdengine, indengine |
| `SUBSCRIBE_TOKENS` | `1:99926000` | mdengine, indengine, dashboard |
| `ENABLED_TFS` | `60,120,180,300` | mdengine, indengine, dashboard |
| `INDICATOR_CONFIGS` | `SMA:9,SMA:20,...` | indengine |
| `METRICS_ADDR` | `:9090` | mdengine |
| `DASHBOARD_ADDR` | `:8080` | dashboard |
| `GATEWAY_ADDR` | `:9090` | api_gateway |
