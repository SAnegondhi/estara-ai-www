# Streaming Search with Inline Enrichment

**Status**: In Progress
**Started**: 2026-01-30
**Last Updated**: 2026-01-30

---

## Overview

Replace the current two-phase search (search → async enrichment) with a single streaming SSE endpoint that returns fully enriched properties as they're processed.

**Current Flow (Problems)**:
1. POST /search returns 40 properties immediately (NO yearBuilt)
2. Client subscribes to SSE for enrichment updates
3. Race condition: job may complete before subscription
4. Complex client-side state management

**New Flow (Better)**:
1. Client connects to SSE endpoint
2. Server calls HasData Listing API → gets properties
3. For each property: enrich via Property API → send via SSE
4. Properties arrive fully enriched, displayed progressively

---

## Implementation Plan

### Phase 1: Backend - Streaming Search Endpoint

**New File**: `www/internal/api/handlers/discover/streaming_search.go`

```go
// GET /api/v2/discover/search/stream?location=Peoria,IL&minPrice=50000&maxPrice=200000
func (h *Handler) StreamingSearch(w http.ResponseWriter, r *http.Request) {
    // 1. Parse query params
    // 2. Set SSE headers
    // 3. Call orchestrator.SearchWithStreaming()
    // 4. For each enriched property, send SSE event
    // 5. Send complete event with summary
}
```

**SSE Events**:
- `property`: Single enriched property
- `progress`: {processed: N, total: M}
- `complete`: {total, enriched, failed, noData, discoverySessionId}
- `error`: {message}

### Phase 2: Orchestrator - Streaming Method

**File**: `www/internal/services/property/finder/orchestrator.go`

```go
// SearchWithStreaming performs search and enrichment, sending results via channel
func (o *Orchestrator) SearchWithStreaming(
    ctx context.Context,
    params providers.SearchParams,
    results chan<- StreamingResult,
) error {
    // 1. Call HasData Listing API
    // 2. For each property (parallel with semaphore):
    //    a. Check property cache first
    //    b. Call Property API for enrichment
    //    c. Cache result (async)
    //    d. Send to results channel
    // 3. Close channel when done
}

type StreamingResult struct {
    Type     string           // "property", "progress", "complete", "error"
    Property *providers.Property
    Progress *ProgressUpdate
    Complete *CompleteStatus
    Error    error
}
```

### Phase 3: Client - SSE Subscription

**File**: `client/src/lib/api/client.ts`

```typescript
export function subscribeToStreamingSearch(
  params: SearchCriteria,
  callbacks: {
    onProperty: (property: PropertyResult) => void;
    onProgress: (progress: { processed: number; total: number }) => void;
    onComplete: (status: SearchCompleteStatus) => void;
    onError: (error: Error) => void;
  }
): { unsubscribe: () => void }
```

**File**: `client/src/app/(dashboard)/discover/page.tsx`

```typescript
const handleSearch = async (criteria: SearchCriteria) => {
  setIsLoading(true);
  setProperties([]);

  subscribeToStreamingSearch(criteria, {
    onProperty: (property) => {
      setProperties(prev => [...prev, property]);
    },
    onProgress: ({ processed, total }) => {
      setProgress({ processed, total });
    },
    onComplete: (status) => {
      setDiscoverySessionId(status.discoverySessionId);
      setIsLoading(false);
    },
    onError: (error) => {
      setError(error.message);
      setIsLoading(false);
    }
  });
};
```

---

## Implementation Status

| Component | Status | Notes |
|-----------|--------|-------|
| Streaming Search Handler | **COMPLETE** | `streaming_search.go` created |
| Orchestrator Streaming Method | **COMPLETE** | `SearchWithStreaming` added |
| Client SSE Subscription | **COMPLETE** | `subscribeToStreamingSearch` added |
| Discover Page Integration | **COMPLETE** | `handleSearch` updated |
| Discovery Session Creation | **COMPLETE** | Created in SSE complete handler |
| Remove Old Enrichment Code | PENDING | Cleanup after testing/migration |

---

## Files to Create/Modify

### New Files
- [x] `www/internal/api/handlers/discover/streaming_search.go` - **CREATED**

### Modified Files
- [x] `www/internal/api/router.go` - Added `GET /api/v2/discover/search/stream` route
- [x] `www/internal/services/property/finder/orchestrator.go` - Added `SearchWithStreaming` method
- [x] `client/src/lib/api/client.ts` - Added `subscribeToStreamingSearch` function
- [x] `client/src/app/(dashboard)/discover/page.tsx` - Updated to use streaming search

---

## SSE Event Format

```
event: property
data: {"id":"123","address":"123 Main St","price":150000,"yearBuilt":1985,...}

event: progress
data: {"processed":5,"total":40}

event: property
data: {"id":"124","address":"456 Oak Ave","price":175000,"yearBuilt":1992,...}

event: complete
data: {"total":40,"enriched":35,"failed":2,"noData":3,"discoverySessionId":"abc-123"}
```

---

## Considerations

1. **Timeout**: Long-running SSE connection needs appropriate timeout (2-3 minutes)
2. **Concurrency**: Use semaphore to limit parallel Property API calls (10 concurrent)
3. **Caching**: Cache enriched properties for future searches
4. **Error Handling**: Continue processing if individual property enrichment fails
5. **Discovery Session**: Create session after all properties are processed (with enriched data)
6. **Client Reconnection**: If SSE disconnects, client should show partial results

---

## Rollback Plan

If issues arise:
1. Keep old POST /search endpoint functional
2. Client can fall back to old flow
3. Feature flag to switch between flows
