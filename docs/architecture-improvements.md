# Architecture Improvements for High-Clarity MTF Charts and Real-Time Indicators

## Purpose
Define concrete architecture improvements to deliver high-clarity multi-timeframe (MTF) charts with near-real-time indicator updates, while preserving accuracy under jitter, out-of-order ticks, and bursts.

## Current Baseline (Summary)
- `mdengine` ingests ticks, builds 1s candles, resamples to TFs, writes Redis and SQLite, and publishes PubSub/Streams.
- `indengine` consumes Redis Streams for completed candles and PubSub for forming candles, computes indicators, and publishes results.
- `api_gateway` bridges Redis PubSub to WebSocket clients and serves REST for historical candles.

## Target Outcomes
- Correct event-time candles even with late or out-of-order ticks.
- Atomic, coherent updates across multiple TFs.
- Sub-100ms typical latency for forming candle and indicator previews.
- Deterministic backfill and gap detection for clients.

## Proposed Improvements

### 1) Event-Time Correctness and Out-of-Order Handling
- Use exchange-provided timestamp as the canonical tick time when available.
- Add a reorder buffer in `mdengine` (e.g., 200–500ms) to handle out-of-order ticks before finalizing candles.
- Close candles by watermark based on event time, not processing time.
- Define a strict late-tick policy.
- Finalized candle is immutable; late ticks after watermark can be dropped or logged for audit.

### 2) Atomic MTF Frames
- Emit a single “frame” message per symbol containing all TF updates for the same logical timestamp.
- Include both candles and indicators in the same frame for client coherence.
- Publish frame once and atomically, rather than per-TF publishes.
- Add a frame ID and per-TF sequence numbers for reliable gap detection and replay.

### 3) Fast Path vs Durable Path
- Keep the durable path to Redis Streams and SQLite for backfill and recovery.
- Add a fast path in `mdengine` to push forming candle and preview indicators directly to `api_gateway` or a dedicated low-latency PubSub channel.
- Persist asynchronously to avoid slowing the hot path.

### 4) Indicator Preview Enhancements
- Expand `ProcessPeek` to run at sub-second intervals, not only on 1s forming candles.
- Tie preview results to the same frame ID and event-time watermark so the UI knows when values are final.

### 5) Client-Side Gap Detection and Backfill
- Every WebSocket message includes a sequence ID and frame ID.
- Clients detect gaps and request the missing range from REST or Redis Streams.
- On reconnect, clients send `last_frame_id` and `last_seq` to resume without full reload.

### 6) Chart Quality and Level-of-Detail (LOD)
- Add server-side downsampling based on the client’s zoom level.
- For large ranges, send min/max/open/close aggregates per bucket.
- For narrow ranges, send full 1s or tick-derived data.

### 7) Horizontal Scaling
- Shard `mdengine` and `indengine` by instrument token using a consistent hash.
- Each shard owns a subset of symbols and publishes frame messages independently.
- Redis Streams consumer groups align with shard boundaries to avoid cross-shard contention.

### 8) Observability and SLOs
- Track end-to-end latency from tick ingest to WebSocket emit.
- Record watermark delay, reorder buffer occupancy, and late-tick counts.
- Publish p50, p95, p99 latencies as part of existing metrics broadcast.

## Message Schema Additions

### Candle Payload (per TF)
- `event_ts` (epoch ms, exchange time)
- `frame_id` (monotonic per symbol)
- `seq` (monotonic per symbol and TF)
- `is_final` (boolean, true when candle is finalized)

### Indicator Payload
- `event_ts`
- `frame_id`
- `seq`
- `is_final`

### Frame Envelope
- `symbol`
- `frame_id`
- `event_ts`
- `candles` (array of TF candles)
- `indicators` (array of indicator results)

## Rollout Plan
1. Add event-time watermarking and reorder buffer in `mdengine`.
2. Introduce `frame_id` and `seq` fields across candle and indicator messages.
3. Add atomic frame publishing in `mdengine` and `indengine`.
4. Add client gap detection and backfill using existing REST endpoints.
5. Add LOD and downsampling in `api_gateway` for chart ranges.

## Risks and Mitigations
- Increased memory use due to reorder buffer.
- Mitigation: keep buffer size small and configurable; measure impact.
- Atomic frame format change requires coordinated deploy.
- Mitigation: support dual publish format during migration.

## Success Criteria
- Forming candles update within 100ms for the majority of ticks.
- Final candles match exchange event time with minimal drift.
- No visible cross-TF skew on the chart for the same timestamp.
- Client reconnections recover without full reload.
