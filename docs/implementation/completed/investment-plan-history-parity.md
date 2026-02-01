# Implementation Plan: Investment Plan History Parity (www vs www_v1)

## Owner
- Author: Codex
- Date: 2026-01-28
- Status: Completed

---

## 1. Problem Statement

The client Investment Planning page (`/invest`) renders correctly when using `www_v1` as the backend (3 plan cards), but fails when using `www`:

- Only 1 plan is returned/displayed.
- "Load More" appears unexpectedly.
- Client logs React duplicate key warnings (re-fetch/appending duplicates).
- Plan cards are missing expected fields (locations/strategy/capital/metrics).

This is caused by `www` returning a history response that diverges from the `www_v1` API contract and a pagination parsing bug.

---

## 2. Goals

✅ In scope:
- Fix pagination parsing for `/api/ai/investment-planning/history` in `www`.
- Align history query + response shape with `www_v1` behavior (ordering, filtering, locations, metrics mapping).
- Remove the client-facing "Load More" + duplicate key symptom by returning correct `limit/page/totalPages`.

---

## 3. Non-Goals

- Not changing client code (read-only for this task).
- Not redesigning investment planning generation logic.
- Not altering the investment plan job queue/status endpoints unless required for history parity.

---

## 4. Background / Context

`www_v1` history endpoint:
- Filters `AnalysisCache` by `userId`, `feature = 'investment_planning'`, `supersededBy = null`.
- Orders by `lastAccessedAt` (descending).
- Builds `locations` from `analysis_cache.location` (or parses from `key`), and reads `strategy/availableCapital` from `metricsData` (fallback `metadata`).
- Returns nested `metrics` object with `projectedReturn` derived from `expectedAnnualReturn`.

`www` history endpoint currently:
- Parses `page/limit` incorrectly (uses `fmt.Sscanf` return value).
- Reads locations/strategy/capital from `metadata` only.
- Does not select `location` or `lastAccessedAt`, and orders by `createdAt`.

---

## 5. Proposed Design

### 5.1 Architecture

Small, localized fixes in `www/internal/api/handlers/ai/handler.go`:
- Replace pagination parsing with `strconv.Atoi`-based helper.
- Update SQL to select the fields needed for parity (`location`, `lastAccessedAt`, `metadata`, `metricsData`, `investorProfile`, `supersededBy`).
- Add small helper(s) to map `analysis_cache` rows into the `InvestmentPlanHistoryItem` contract expected by the client.

### 5.2 API Changes

No endpoint changes; behavior/response correctness improvements only.

### 5.3 Data Model Changes

None.

---

## 6. Implementation Steps

1. Create `www/docs` structure + this plan.
2. Fix query param parsing for `page`/`limit`.
3. Update history SQL query to match `www_v1` ordering/filtering.
4. Map `location/metricsData` into response fields consistent with `www_v1`.
5. Add tests for parsing/mapping helpers.
6. Add changelog entry.

---

## 7. Testing Strategy

- Unit tests:
  - `page/limit` parsing helper (bounds, defaults).
  - Metrics mapping helper (supports both `www_v1` keys like `expectedAnnualReturn` and Go-worker keys like `avgCashOnCash`/`projectedValue`).
- Manual verification:
  - Call `/api/ai/investment-planning/history?limit=20&page=1` and confirm:
    - `plans.length` matches expected count.
    - `pagination.totalPages` correct.
    - No duplicates when requesting `page=2`.

---

## 8. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| DB row shapes differ across environments | Medium | Defensive JSON parsing; tolerate missing fields |
| Existing cached rows lack `location` or `metricsData` | Low/Medium | Fallback parsing from `key`; default values |

---

## 9. Rollout / Deployment Plan

- Deploy as a backward-compatible API fix.
- No feature flags.

---

## 10. Backout Plan

- Revert the commit(s) touching `www/internal/api/handlers/ai/handler.go` and associated tests/docs.

---

## 11. Implementation Notes (Update During Work)

- Implemented in `www/internal/api/handlers/ai/handler.go`:
  - Fixed pagination parsing by switching from `fmt.Sscanf` to `strconv.Atoi` with bounds.
  - Updated SQL to match `www_v1` behavior: filter `supersededBy IS NULL`, order by `lastAccessedAt`, optional `search` filter on `location`.
  - Response mapping now prefers `analysis_cache.location` and `metricsData` (with fallbacks) to populate `locations`, `strategy`, `availableCapital`, and card `metrics`.
- Added unit tests in `www/internal/api/handlers/ai/handler_history_test.go`.

---

## 12. Completion Checklist

- [x] Endpoint returns correct pagination
- [x] Cards render correctly in client using `www` backend
- [x] Tests added
- [x] Changelog updated
- [x] Plan status updated to Completed and moved to `completed/`

