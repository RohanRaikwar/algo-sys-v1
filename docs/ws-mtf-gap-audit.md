# WS + MTF Backend/Frontend Gap Audit

Date: 2026-02-25

Scope:
- Backend WebSocket/gateway flow (`backend/internal/gateway`, `backend/pkg/smartconnect`)
- Frontend real-time chart + MTF handling (`frontend/src/hooks`, `frontend/src/store`, `frontend/src/components/chart`)
- Reference check against architecture/pattern docs requested by you

## Findings (ordered by severity)

### 1) HIGH: Cross-symbol contamination risk in subscriptions/state
Issue:
- Frontend sends repeated `SUBSCRIBE` messages but never sends `UNSUBSCRIBE`.
- Backend keeps old subscriptions per client.
- Frontend indicator state keys are not symbol-scoped (`NAME:TF` only), so stale values from prior symbol subscriptions can bleed into the current chart.

Evidence:
- `frontend/src/components/chart/hooks/useChartSubscription.ts:37`
- `frontend/src/hooks/useWebSocket.ts:27`
- `backend/internal/gateway/client.go:197`
- `frontend/src/hooks/useWebSocket.ts:212`
- `frontend/src/components/chart/hooks/useIndicatorLines.ts:55`

Impact:
- Wrong indicator overlays when switching token/symbol.

---

### 2) HIGH: Candle chart can stay stale after token switch
Issue:
- Candle render fingerprint omits token/symbol identity.
- If two symbols have same candle count/time structure, chart may skip full `setData()` and only do `update()`, leaving stale series.

Evidence:
- `frontend/src/components/chart/hooks/useCandleSeries.ts:44`
- `frontend/src/components/chart/hooks/useCandleSeries.ts:52`

Impact:
- User may see previous token’s historical candles after switching token.

---

### 3) HIGH: SmartConnect reconnect delay math bug
Issue:
- Retry delay uses `^` (bitwise XOR) where exponentiation was intended.

Evidence:
- `backend/pkg/smartconnect/websocket.go:409`

Impact:
- Backoff behavior is incorrect and may reconnect too fast/slow unpredictably.

---

### 4) HIGH: Potential panic in binary parsing guard
Issue:
- Quote/snap-quote parse branch checks `len(b) >= 99` but reads up to index `123`.

Evidence:
- `backend/pkg/smartconnect/websocket.go:485`
- `backend/pkg/smartconnect/websocket.go:501`

Impact:
- Possible runtime panic on short quote payloads.

---

### 5) HIGH: Gateway concurrency safety gaps
Issue:
- Unsynchronized access/mutation around shared state maps/slices in gateway paths.

Evidence:
- `backend/internal/gateway/hub.go:173` (`len(h.clients)` logged after unlock)
- `backend/internal/gateway/subscribe.go:362`
- `backend/internal/gateway/subscribe.go:380`

Impact:
- Data races under concurrent client/subscription churn.

---

### 6) MEDIUM: Hardcoded IST offset in chart timestamps
Issue:
- Frontend force-adds `+5:30` offset to parsed timestamps.

Evidence:
- `frontend/src/utils/helpers.ts:50`
- `frontend/src/components/chart/hooks/useCandleSeries.ts:29`
- `frontend/src/store/useCandleStore.ts:143`

Impact:
- Time-axis misalignment/portability problems across locales/time settings.

---

### 7) MEDIUM: Render-time side effect in `App.tsx`
Issue:
- Store mutation (`setConfig`) occurs in render error path.

Evidence:
- `frontend/src/App.tsx:78`

Impact:
- Risk of repeated rerender loops and hard-to-debug UI behavior.

---

### 8) MEDIUM: Gap-recovery protocol exists but is not wired in frontend
Issue:
- Backend emits `channel_seq` and exposes `/api/missed`.
- Frontend does not track per-channel sequence or call missed-message backfill.

Evidence:
- `backend/internal/gateway/broadcaster.go:59`
- `backend/internal/gateway/handlers.go:273`
- `frontend/src/hooks/useWebSocket.ts:132`

Impact:
- Message loss during bursts/reconnect can silently corrupt chart continuity.

---

### 9) MEDIUM: Open WS/CORS security defaults
Issue:
- `CheckOrigin` accepts all origins.
- REST CORS allows `*`.

Evidence:
- `backend/internal/gateway/handlers.go:18`
- `backend/internal/gateway/handlers.go:24`

Impact:
- Elevated cross-origin abuse risk in production.

---

### 10) MEDIUM: Incomplete exchange mapping in `api_gateway`
Issue:
- Token exchange mapping supports only `1/2/3`; unknown exchange types default to `NSE`.

Evidence:
- `backend/cmd/api_gateway/main.go:111`
- `backend/cmd/api_gateway/main.go:119`

Impact:
- Misrouted channels/subscriptions for unsupported exchange type codes.

## Validation Run

Backend:
- `go test ./...` (pass)
- `go vet ./...` (pass)

Frontend:
- `npm run build` (pass)
- `npx vitest run` (pass, 12 tests)

Note:
- Passing tests/build do not cover the protocol/state integrity gaps above.

## Recommended Fix Order

1. Subscription lifecycle + symbol scoping (Findings 1 and 2)
2. SmartConnect reconnect/backoff + binary parser guard (Findings 3 and 4)
3. Gateway race-proofing shared state (Finding 5)
4. Sequence-gap recovery wiring in frontend (Finding 8)
5. Time normalization cleanup (Finding 6)
6. Security tightening and exchange mapping completion (Findings 9 and 10)
