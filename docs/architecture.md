# Realtime Tick → 1s OHLC Architecture (Go + Angel One WS + Redis + SQLite)

## Goals
- Consume **tick data** from **Angel One WebSocket** (SmartAPI).
- Build **1-second OHLC** candles per instrument with **exact timestamp buckets**.
- Write:
  - **Redis** for realtime reads (latest candle / last N seconds / pub-sub).
  - **SQLite** for durable local persistence (query, replay, audits).
- Be stable under reconnects, bursts, and packet loss.

---

## High-Level Components

### 1) Market Data Ingest (WebSocket Client)
**Responsibilities**
- Connect to Angel One WS.
- Subscribe/unsubscribe tokens (instrument list).
- Parse ticks and normalize schema.
- Handle reconnect + resubscribe.
- Push ticks into an internal pipeline (channels / ring buffer).

**Key ideas**
- One WS connection per “subscription group” (depends on token count limits).
- Keep a **single writer** into the pipeline to reduce locking.

---

### 2) Tick Normalizer (Schema + Time)
**Convert raw WS tick → internal Tick**
- Ensure:
  - `token`, `exchange`, `ltp`, `ltq/volume`, `bid/ask` if available
  - `tickTime`:
    - Prefer exchange timestamp if Angel provides it
    - Otherwise use `time.Now().UTC()` as receive time
- Normalize to UTC and to millisecond precision.

**Tick struct (example fields)**
- `Token string`
- `Exchange string`
- `Price int64` (paise; avoids float drift)
- `Qty int64`
- `TickTS time.Time` (UTC)

---

### 3) 1-Second OHLC Aggregator (Candle Builder)
**Responsibilities**
- Maintain in-memory candle state per token for the **current second bucket**.
- Emit candle when bucket changes (second rolls over).

**Bucket rule**
- `bucket = floor(TickTS.UnixMilli / 1000)` or `TickTS.Unix()` (seconds).
- Candle timestamp = start of that second (UTC).

**Candle output schema**
- `token`
- `exchange`
- `ts` (bucket start time, UTC)
- `open, high, low, close` (int64 paise)
- `volume` (optional if tick volume is usable)
- `ticksCount`

**Concurrency model (recommended)**
- A dedicated goroutine: `AggregatorLoop()`
- Receives ticks through `chan Tick`
- Emits candles through `chan Candle`
- Uses a map: `map[token]CurrentCandleState`

**Late / out-of-order tick policy**
- If tick belongs to an **older bucket**:
  - Option A (simple): drop it
  - Option B (better): keep a small buffer window (e.g., 1–2 seconds) and allow late updates before “finalize”

---

### 4) Redis Realtime Layer (Fast Reads + PubSub)
**What Redis stores**
1. **Latest candle per token**
   - Key: `candle:1s:latest:{exchange}:{token}`
   - Value: JSON/MsgPack
   - TTL: e.g. 5–30 minutes (optional)

2. **Recent candle stream per token**
   - Option A: Redis Stream  
     - Stream: `candle:1s:stream:{exchange}:{token}`
     - `XADD` per candle
     - Trim: `MAXLEN ~ 3600` (keep last hour)
   - Option B: List  
     - `LPUSH` + `LTRIM` (simpler but less rich than streams)

3. **Publish to subscribers**
   - Channel: `pub:candle:1s:{exchange}:{token}` (or one channel for all tokens)
   - Consumers: UI, indicator service, alerting engine

**Why Redis**
- Ultra fast last-N fetch.
- Decouple consumers (indicator engine doesn’t touch SQLite in hot path).

---

### 5) SQLite Persistence Layer (Durable Local Store)
**Responsibilities**
- Insert finalized 1s candles.
- Support query: range fetch, backfill, replay after restart.
- Provide last stored timestamp per token.

**Writing strategy**
- Use **single writer goroutine** (SQLite prefers serialized writers).
- Use **transaction batching**:
  - batch size: 100–1000 candles per commit OR 200ms flush
- Enable:
  - `WAL` journal mode for concurrency
  - Proper indexes

**SQLite table (recommended)**
`candles_1s`
- `token TEXT NOT NULL`
- `exchange TEXT NOT NULL`
- `ts INTEGER NOT NULL` (epoch seconds or epoch ms; pick one and keep consistent)
- `open INTEGER NOT NULL`
- `high INTEGER NOT NULL`
- `low INTEGER NOT NULL`
- `close INTEGER NOT NULL`
- `volume INTEGER`
- `ticks_count INTEGER`
- PRIMARY KEY (`exchange`, `token`, `ts`)

**Indexes**
- `PRIMARY KEY(exchange, token, ts)` supports fast range queries.

---

## Data Flow
1. **AngelOne WebSocket** → Tick messages
2. **WS Ingest** parses → `Tick` → `tickCh`
3. **Aggregator** consumes `tickCh` → builds 1s candles → emits `candleCh`
4. **Redis Writer** reads `candleCh` → updates latest + stream + pubsub
5. **SQLite Writer** reads `candleCh` → batch insert

> Use a **fan-out**: the Aggregator outputs one candle stream that is broadcast to Redis + SQLite writers.

---

## Reliability & Recovery

### Reconnect Handling (WS)
- Exponential backoff reconnect.
- On reconnect: resubscribe tokens.
- Maintain a “connection state” and last heartbeat time.

### Restart Recovery
- On startup:
  - Load last candle timestamp per token from SQLite (or a metadata table).
  - Start live stream from WS (no official replay from WS usually).
  - If you need gap fill: use REST historical API (if available) to backfill.

### Backpressure
- Use buffered channels:
  - `tickCh` large enough for bursts
- If tick bursts exceed capacity:
  - Drop policy (log + metrics)
  - Or use ring buffer / lock-free queue

---

## Performance Targets (Low Latency)
To keep candle publishing under **<50ms** after second close:
- Aggregator in-memory only
- Redis pipelined writes
- SQLite batched commits (async)

Expected pattern:
- Redis update: near-real-time
- SQLite commit: slightly delayed (100–300ms batching)

---

## Suggested Goroutines / Services Layout (Single Binary)

### goroutines
1. `wsReader()` → produces ticks
2. `aggregator()` → produces candles
3. `redisWriter()` → writes latest/stream + pubsub
4. `sqliteWriter()` → batched inserts
5. `metricsServer()` → Prometheus / health endpoints

### Sharding option (if tokens very high)
- Instead of 1 aggregator map for all tokens:
  - Create `N` aggregators (shards) based on token hash
  - Each shard outputs candles → unified writers

---

## Redis Key Design
- Latest:
  - `candle:1s:latest:{exchange}:{token}`
- Stream:
  - `candle:1s:stream:{exchange}:{token}`
- PubSub:
  - `pub:candle:1s:{exchange}:{token}`
- Metadata (optional):
  - `meta:last_ts:{exchange}:{token}` (SQLite remains source of truth)

---

## Observability
**Metrics**
- ticks/sec
- candles/sec
- ws reconnect count
- lag: `now - candle.ts`
- dropped ticks count
- redis write latency
- sqlite commit latency + queue depth

**Logs**
- WS connect/disconnect + reason
- subscription changes
- batch commit errors

**Health checks**
- WS connected?
- last tick received within X seconds?
- writer queues not stuck?

---

## Edge Cases & Rules
1. **No ticks in a second**
   - Either:
     - emit nothing
     - or emit candle with last close repeated (depends on your indicator needs)
2. **Multiple exchanges / segments**
   - include `exchange` in Redis keys + SQLite primary key
3. **Price precision**
   - store price as `int64` (paise) to avoid float drift
4. **Clock sync**
   - server should use NTP
   - rely on exchange timestamps if available

---

## Minimal Conceptual Pipeline
```text
WS -> tickCh -> Aggregator -> candleCh
                     |-> redisWriter(candleCh)
                     |-> sqliteWriter(candleCh)
```

---

## Go Package Layout (Suggested)
- `cmd/mdengine/`
- `internal/ws/` (Angel One socket client + reconnect)
- `internal/model/` (Tick, Candle structs)
- `internal/agg/` (1s OHLC builder)
- `internal/store/redis/`
- `internal/store/sqlite/`
- `internal/bus/` (fanout / broadcaster)
- `internal/metrics/`
