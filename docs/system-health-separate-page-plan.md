# Move System Health to Separate Page (Plain Plan)

## Goal
Move `SystemHealth` out of the main dashboard screen and show it on a separate page.

## Current State
- `SystemHealth` is rendered in the same page as `TradingChart`.
- File: `frontend/src/App.tsx`
- Current layout in `Dashboard()`:
  - `TradingChart`
  - `SystemHealth`

## Target State
- Main dashboard page:
  - `TradingChart` only
- New health page:
  - `SystemHealth` only
- User can switch between pages from header navigation.

## Minimal Implementation Steps
1. Add routing:
   - Install and use `react-router-dom`.
   - Define routes:
     - `/` -> dashboard (chart view)
     - `/health` -> system health page
2. Split page components:
   - Keep `DashboardPage` with chart content.
   - Create `HealthPage` with `<SystemHealth />`.
3. Update header navigation:
   - Add links/buttons: `Dashboard`, `Health`.
4. Keep shared UI shell:
   - Keep `ReconnectBanner`, `Header`, `StatusBar`, `SettingsModal` in the app shell.
   - Render route content inside `<main>`.
5. Validate behavior:
   - Chart page loads without health cards.
   - Health page shows metrics cards.
   - Mobile view has cleaner layout on chart page.

## Files to Update (when implementing)
- `frontend/src/main.tsx` (router bootstrap if needed)
- `frontend/src/App.tsx` (routes + page split)
- `frontend/src/components/layout/Header.tsx` (navigation links)
- Optional: new page files under `frontend/src/pages/`

## Notes
- No backend changes needed.
- `SystemHealth` already reads from `useWSStore`, so it will continue to work on a separate page.
