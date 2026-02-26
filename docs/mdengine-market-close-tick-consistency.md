# ADR-006: MD Engine Market-Close Handling and Tick Consistency (Angel SDK)

## Status
Proposed

## Date
2026-02-26

## Context
- `mdengine` runs the compute/persistence pipeline continuously, but production tick ingest depends on Angel One SmartAPI WebSocket availability during market hours.
- Current market-hours policy is IST trading session based:
  - Open: 09:15 IST
  - Close: 15:30 IST
  - Trading days: Mon-Fri excluding configured holidays
- A recurring product concern is data consistency when the market closes:
  - WebSocket disconnect at close can look like a failure.
  - Downstream consumers may misinterpret missing ticks as pipeline outage.
  - End-of-session candle finalization must remain deterministic.

## Decision
Adopt an explicit market-session contract for `mdengine` and Angel SDK lifecycle.

### 1) Session Lifecycle Contract (Angel SDK + WS)
- At market open, create a fresh SmartConnect session (TOTP + login), derive auth/feed tokens, and connect WS.
- During market hours, allow broker SDK reconnect behavior for transient network issues.
- At market close (`15:30 IST`), terminate WS context intentionally and treat disconnect as expected.
- After close, do not attempt continuous re-login/reconnect until next `NextOpen()` window.

### 2) Tick Consistency Contract at Market Close
- `No synthetic ticks` after market close.
- Last received exchange tick is final market tick for the session.
- Tick silence during closed hours is a valid state, not an ingest error.
- Consumers must gate freshness alerts with market status (open/closed).

### 3) Candle/TF Finalization Contract
- At close transition, finalize all in-progress 1s/TF states that are eligible under current watermark rules.
- Persist finalized candles through normal Redis/SQLite paths.
- Stop forming-candle progression until next valid market tick on next open.

### 4) Observability Contract
- Record market state transitions explicitly in logs/metrics:
  - `market_closed_waiting_for_next_open`
  - `market_open_session_started`
  - `ws_disconnected_market_close`
- WS disconnected during closed hours should not page as Sev-1 unless pipeline health checks also fail.

### 5) Angel One SDK Data Availability Window
- Angel One SmartAPI WebSocket tick data is expected only during open market hours.
- After `15:30 IST` (market close), WS may remain connected/disconnect, but no new live tick flow is expected.
- This is a normal market-closed condition, not a market-data pipeline defect by itself.
- Live tick flow should start again at next trading session open (`09:15 IST`, Mon-Fri, non-holiday) after fresh login/session and subscription.
- If data does not resume after open, treat it as an operational issue (auth/session/subscription/connectivity), not as expected close behavior.

## Options Considered

| Option | Pros | Cons | Complexity | Decision |
|---|---|---|---|---|
| Keep WS and reconnect 24/7 | Simple mental model | Wasted auth/reconnect churn after close; noisy alerts | Low | Rejected |
| Market-hours gated WS + no synthetic ticks + explicit close contract | Matches exchange reality; deterministic EOD behavior | Requires consumer-side market-state awareness | Medium | Chosen |
| Emit synthetic heartbeat ticks after close | Constant downstream cadence | Distorts price/tick semantics; complicates indicators | Medium | Rejected |

## Rationale
1. Exchange-truth semantics are more important than constant tick cadence.
2. Explicit close-state modeling reduces false incidents and avoids reconnect storms.
3. Deterministic close behavior improves replay/backfill correctness for next-day startup.

## Trade-offs Accepted
- Consumers cannot assume continuous tick flow across 24h.
- Some dashboards/alerts must include market-hours awareness.

## Consequences
- Positive:
  - Cleaner operational behavior at close.
  - Better distinction between expected idle and real outages.
  - Consistent end-of-day candle boundaries.
- Negative:
  - Downstream services must handle closed-session idle periods correctly.
- Mitigation:
  - Standardize market-state signals and alerting rules.
  - Keep markethours/holiday config current.

## Revisit Triggers
- Broker introduces official after-hours or auction tick streams to be consumed.
- Product requires synthetic session markers for backtest/live parity.
- Multi-exchange expansion with non-uniform market windows.

## Scope Note
This ADR is documentation-only for architecture alignment. It does not introduce code changes in this update.
