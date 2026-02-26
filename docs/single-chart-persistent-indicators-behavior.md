# Single Chart + Persistent Indicators Behavior Spec

Date: 2026-02-25
Scope: Frontend chart behavior, indicator profile behavior, and subscription behavior.

## 1) Goal

Move to a **single chart** where:
- User can switch chart timeframe (1m, 5m, etc.) from one TF selector.
- Indicator selection is **global** (one profile), not per chart TF.
- The same indicator profile persists when chart TF changes.
- No forced default indicator selection.
- Indicator creation is dynamic with:
  - dynamic indicator type (SMA/EMA/SMMA),
  - dynamic window/period,
  - dynamic indicator TF per entry (MTF support).

## 2) Current Behavior (as-is)

- Chart TF is selected globally (`selectedTF`).
- Indicators are currently stored **per chart TF** (`activeEntriesByTF`).
- Switching chart TF changes which indicator profile is loaded.
- If active config is missing, frontend auto-populates defaults from server config per TF.
- Subscribe flow depends on selected chart TF + that TF-specific indicators.

Behavior impact today:
- User can see different indicators for 1m vs 5m even without intentionally changing settings.
- User intent is fragmented across TF-specific profiles.

## 3) Target Behavior (to-be)

### 3.1 Chart
- Exactly one chart component is used for price + overlays.
- Changing TF updates candle timeframe only.
- Chart remains continuous; indicator selection does not reset.
- Indicator lines must stay correctly mapped to the active chart candle timeline when TF changes.
- Indicator timestamps must align to chart candle buckets (no visual drift).

### 3.2 Indicator Profile
- One global profile: `activeEntries[]`.
- Same profile is used for all chart TFs.
- Each entry keeps its own indicator TF (`entry.tf`) and window in `entry.name` (e.g., `SMA_20`, `EMA_9`).
- Duplicate indicator entries (`name + tf`) are blocked.

### 3.3 Defaults
- No auto-selected indicators by default.
- If user has never configured indicators, profile starts empty.
- Candle stream must still work with empty indicator profile.

### 3.4 Settings UX
- Settings modal edits one global profile (not "for current TF").
- Add indicator fields:
  - `type` (SMA/EMA/SMMA)
  - `window` / period (2..500)
  - `indicator TF` (from configured TF list)
  - optional line color
- Apply saves full global profile.

### 3.5 Persistence
- Global profile persists in local storage.
- Legacy per-TF local profiles are migrated into one deduplicated global list.
- Backend persists profile via `/api/indicators/active` using flat `entries` payload.

### 3.6 Subscription Behavior
- On token or chart TF change, send `SUBSCRIBE(symbol, selectedTF, activeEntries)`.
- `activeEntries` can be empty; candle subscription must still proceed.
- On settings apply, re-subscribe with updated profile.
- If profile unchanged and token/TF unchanged, no redundant re-subscribe.

### 3.7 Timestamp and Line Mapping Accuracy
- Use one canonical timestamp basis (UTC epoch seconds) for candles and indicators.
- Do not hard-shift timestamps with fixed timezone offsets in chart pipeline.
- For indicator TF vs chart TF alignment:
  - If indicator TF is finer than chart TF: resample/snap indicator points to chart TF buckets.
  - If indicator TF is coarser than chart TF: step-fill between confirmed indicator points across chart candles.
- Always render indicator points in strictly ascending chart time to satisfy chart library constraints.

### 3.8 New Indicator Add Behavior (TradingView-like)
- When user adds a new indicator and clicks Apply:
  - indicator should appear on chart with historical line first,
  - then continue updating in live mode without requiring manual refresh.
- Expected data flow:
  - frontend sends updated `SUBSCRIBE`,
  - backend returns `SNAPSHOT` with candle history + indicator histories,
  - frontend renders history immediately and merges subsequent live indicator updates.
- If snapshot does not contain enough indicator history, frontend should backfill via `/api/indicators/history` for that indicator entry.

## 4) Data Model

### Indicator Entry
```ts
interface IndicatorEntry {
  name: string; // "SMA_20", "EMA_9", "SMMA_14"
  tf: number;   // indicator timeframe in seconds
  color?: string;
}
```

### App Store (target)
```ts
selectedTF: number;
selectedToken: string | null;
activeEntries: IndicatorEntry[]; // global profile
```

## 5) API Contract

### GET `/api/indicators/active`
Preferred response:
```json
{ "entries": [ { "name": "SMA_20", "tf": 300 } ] }
```

Backward compatibility input accepted by frontend:
```json
{ "byTF": { "60": [ ... ], "300": [ ... ] } }
```
Frontend flattens legacy shape into one global profile.

### POST `/api/indicators/active`
Request body:
```json
{ "entries": [ { "name": "EMA_21", "tf": 60, "color": "#22c55e" } ] }
```

## 6) Acceptance Criteria

1. Switching chart TF does not change selected indicators.
2. Indicators added in settings remain visible when moving between 1m and 5m.
3. Empty indicator profile shows candles normally (no subscribe failure).
4. No automatic default indicators appear unless user explicitly adds them.
5. Indicator entries support mixed TF in one profile (e.g., `SMA_20@5m`, `EMA_9@1m`).
6. Profile survives refresh/reload.
7. Indicator lines remain time-aligned to chart candles after any TF switch.
8. Adding a new indicator shows historical line first, then live updates continue automatically.
9. No fixed timezone offset artifact causes candle/indicator misalignment.

## 7) Edge Cases

- Duplicate add attempts (`name + tf`) are ignored.
- Removing all indicators leaves chart candle-only but fully functional.
- If backend returns unknown or malformed entries, frontend ignores invalid rows.
- If user previously had per-TF profiles, migration merges + deduplicates.

## 8) Rollout Notes

1. Update frontend store and consumers from `activeEntriesByTF` to `activeEntries`.
2. Keep backend `entries` payload compatibility.
3. Ensure websocket subscribe path allows empty indicator list.
4. Remove frontend fallback auto-population from `/api/config.indicators`.

---

This document defines the required behavior before implementation changes continue.
