# Backend Readiness Audit: Single Chart + Persistent Indicators

Date: 2026-02-25  
Spec baseline: `docs/single-chart-persistent-indicators-behavior.md`

## Verdict

Backend is **partially ready**.  
Core subscription/snapshot APIs exist, but there are blocking gaps for the requested behavior.

## What Is Already Ready

1. Active indicator config API exists with flat `entries` shape.
   - `GET/POST /api/indicators/active`
   - Ref: `backend/internal/gateway/handlers.go`
2. WebSocket subscribe supports per-indicator TF override.
   - `IndicatorSpec.TF`
   - Ref: `backend/internal/gateway/subscribe.go`
3. Snapshot response includes indicator history keyed by `NAME:TF`.
   - Ref: `backend/internal/gateway/subscribe.go`
4. Empty indicator list does not block candle subscription.
   - Ref: `backend/internal/gateway/client.go`
5. Historical indicator backfill endpoint exists.
   - `GET /api/indicators/history`
   - Ref: `backend/internal/gateway/handlers.go`
6. Backend timestamps are UTC-based in core model.
   - Ref: `backend/internal/model/tfcandle.go`

## Gaps (Ordered by Severity)

### 1) HIGH: Duplicate indicator names across different TF are not handled correctly

Issue:
- Mapping is keyed by indicator name only (`name -> tf`), so entries like `SMA_20@60` and `SMA_20@300` collide.
- One TF can overwrite the other, breaking multi-TF same-indicator use.

Refs:
- `backend/internal/gateway/subscribe.go` (`ResolveIndicatorTFs`)
- `backend/internal/gateway/client.go` (`matchesChannel` indicator TF lookup)
- `backend/internal/gateway/subscribe.go` (snapshot loop over `sub.IndNames`)

Spec impact:
- Violates mixed-TF profile behavior.

### 2) HIGH: Default indicators are pre-selected at startup

Issue:
- Hub builds default active entries as indicator × TF.
- `parseIndicatorNames` falls back to default list when env is empty/invalid.

Refs:
- `backend/internal/gateway/hub.go` (`defaultEntries` init)
- `backend/internal/gateway/hub.go` (`activeConfig: Entries: defaultEntries`)
- `backend/cmd/api_gateway/main.go` (`parseIndicatorNames` defaults)

Spec impact:
- Violates “no forced default selected indicators”.

### 3) MEDIUM: History-first then live is not guaranteed for all add-indicator cases

Issue:
- Server waits for readiness only when indicator type/period is newly published (`hasNew == true`).
- If indicator already known but stream is cold for current symbol/TF, snapshot can return empty history before live starts.

Refs:
- `backend/internal/gateway/client.go` (`publishNewIndicators`, conditional `waitForIndicators`)

Spec impact:
- Can violate TradingView-like “add indicator -> see history first -> then live”.

### 4) MEDIUM: Active indicator profile is not durable across gateway restart

Issue:
- Active config is stored in-memory only.
- Restart drops runtime profile and falls back to startup defaults.

Refs:
- `backend/internal/gateway/config_store.go` (`Get/Set` only in memory)

Spec impact:
- Can conflict with persistent behavior expectation if frontend is not sole source of truth.

## Criteria Mapping

1. TF switch should not reset indicators: **Backend supports** (subscription model is stateless per request).  
2. Indicators persist across TF: **Mostly frontend concern**, backend supports subscribe payload model.  
3. Empty profile still shows candles: **Supported**.  
4. No auto-default indicators: **Not supported** (gap #2).  
5. Mixed TF per profile including same indicator name on multiple TF: **Not supported** (gap #1).  
6. Profile survives reload: **Frontend local persistence yes, backend durability no** (gap #4).  
7. Line-to-candle alignment: **Backend timestamps are clean UTC; frontend must avoid fixed offset shifts**.  
8. Add indicator shows history then live: **Partially supported** (gap #3).  

## Recommended Backend Changes

1. Replace name-only TF map with composite identity (`name + tf`) in subscription state and channel matching.
2. Start with empty `activeConfig.Entries` (no preselected defaults).
3. Keep indicator engine capability list separate from active display config.
4. On subscribe, optionally wait for per-(name, tf, symbol) stream readiness even when indicator is already known.
5. Persist active config to Redis/SQLite and restore on gateway start.

## Validation Status

Executed:
- `go test -C backend ./...` -> pass

Note:
- Passing tests do not cover all behavior-spec acceptance criteria above.
