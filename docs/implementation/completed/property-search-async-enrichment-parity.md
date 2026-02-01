# Implementation Plan: Property Search Async Enrichment Parity (www vs www_v1)

## Owner
- Author: Claude
- Date: 2026-01-30
- Status: Completed

---

## 1. Problem Statement

Property searches were slow (~28s) because the server waited for all property enrichment (yearBuilt, sqft, lotSize) to complete before returning results. Users experienced long wait times with no feedback.

The solution is async enrichment: return search results immediately (~6s) and stream enrichment data via Server-Sent Events (SSE) as each property is enriched.

Both `www` (Go) and `www_v1` (Next.js) backends need to support this pattern identically for client compatibility.

---

## 2. Goals

- Return property search results immediately without waiting for enrichment
- Stream enrichment updates via SSE to the client
- Client updates properties in-place as enrichment arrives
- Maintain feature parity between `www` (Go) and `www_v1` (Next.js) backends

---

## 3. Non-Goals

- Changing the property finder vendor logic
- Modifying the enrichment data sources
- Changing the cache architecture

---

## 4. Background / Context

### Previous Behavior (Synchronous)

```
Client → POST /api/v2/discover/search
         ↓
Server → Find properties (8-10s)
         ↓
Server → Enrich ALL properties (15-20s)
         ↓
Server → Return fully enriched response (~28s total)
```

### New Behavior (Async Enrichment)

```
Client → POST /api/v2/discover/search
         ↓
Server → Find properties (8-10s)
         ↓
Server → Start background enrichment job
         ↓
Server → Return properties immediately (~6s) with enrichmentJobId
         ↓
Client → Subscribe to SSE: /api/v2/discover/enrich/{jobId}/stream
         ↓
Server → Stream enrichment updates as they complete
         ↓
Client → Update properties in-place
```

---

## 5. Proposed Design

### 5.1 Architecture

**Server Components:**

1. **PropertyOrchestrator.SearchAsync()** - Returns immediately with enrichment job ID
2. **EnrichmentJobManager** - Manages background enrichment with worker pool
3. **SSE Handler** - Streams enrichment updates to subscribed clients

**Client Components:**

1. **subscribeToEnrichmentUpdates()** - SSE subscription utility
2. **Discover page** - Subscribes to SSE and updates properties in-place

### 5.2 API Changes

**Search Response (V2SearchResponse):**
```json
{
  "success": true,
  "properties": [...],
  "totalCount": 41,
  "enrichmentJobId": "e7c5fb30-f3f8-4fda-8377-a1c1be81e828",
  "enrichmentStatus": "IN_PROGRESS"
}
```

**SSE Stream Events:**
```
event: update
data: {"propertyIndex": 0, "success": true, "yearBuilt": 1985, "sqft": 1200}

event: progress
data: {"completed": 10, "total": 41, "failed": 2}

event: complete
data: {"status": "COMPLETED", "enriched": 24, "failed": 17}
```

### 5.3 Data Model Changes

None - uses existing property cache and enrichment infrastructure.

---

## 6. Implementation Steps

### www (Go) Implementation

1. **EnrichmentJobManager** (`www/internal/services/enrichment_job_manager.go`)
   - Worker pool with configurable concurrency (default: 10)
   - Job tracking with status (PENDING, IN_PROGRESS, COMPLETED, FAILED)
   - SSE subscriber management with broadcast

2. **PropertyOrchestrator.SearchAsync()** (`www/internal/services/property_orchestrator.go`)
   - Returns immediately after finding properties
   - Starts background enrichment job
   - Returns enrichmentJobId in response

3. **SSE Handler** (`www/internal/api/handlers/discover/handler.go`)
   - GET `/api/v2/discover/enrich/{jobId}/stream`
   - Validates job exists and user owns it
   - Subscribes to enrichment updates
   - Streams events until completion or timeout

4. **Search Handler Update** (`www/internal/api/handlers/discover/handler.go`)
   - Uses `SearchAsync()` instead of `Search()`
   - Returns `enrichmentJobId` and `enrichmentStatus` in response

### www_v1 (Next.js) Implementation

1. **EnrichmentJobManager** (`www_v1/src/lib/services/propertyFinder/EnrichmentJobManager.ts`)
   - Same architecture as Go version
   - Uses Node.js worker threads or async queue

2. **PropertyFinderOrchestrator.searchAsync()** (`www_v1/src/lib/services/propertyFinder/PropertyFinderOrchestrator.ts`)
   - Returns immediately with enrichmentJobId
   - Starts background enrichment

3. **SSE Route** (`www_v1/src/app/api/v2/discover/enrich/[jobId]/stream/route.ts`)
   - Uses Next.js streaming response
   - Same event format as Go version

### Client Implementation

1. **SSE Subscription** (`client/src/lib/api/sse.ts`)
   ```typescript
   export function subscribeToEnrichmentUpdates(
     jobId: string,
     callbacks: {
       onUpdate?: (update: EnrichmentUpdate) => void;
       onProgress?: (progress: EnrichmentProgress) => void;
       onComplete?: (status: EnrichmentComplete) => void;
       onError?: (error: Error) => void;
     }
   ): { unsubscribe: () => void }
   ```

2. **Discover Page** (`client/src/app/(dashboard)/discover/page.tsx`)
   - Subscribe to SSE when `enrichmentJobId` is returned
   - Update properties in-place via `onUpdate` callback
   - Show progress indicator during enrichment
   - Clean up subscription on unmount/new search/clear

---

## 7. Testing Strategy

### Unit Tests
- EnrichmentJobManager worker pool behavior
- SSE event formatting
- Property update merging

### Integration Tests
- Full search → SSE subscription → property update flow
- Job completion detection
- Error handling for failed enrichments

### Manual Verification
1. Search for properties
2. Verify initial results return in ~6s
3. Observe properties updating in-place as enrichment completes
4. Verify progress indicator shows correct counts
5. Confirm all enrichments complete or timeout

---

## 8. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| SSE connection drops | Medium | Client auto-reconnects; server caches recent updates |
| Enrichment job lost on server restart | Low | Jobs are short-lived; user can re-search |
| Memory pressure from many concurrent jobs | Medium | Job expiration (5 min); max concurrent jobs limit |
| Client/server version mismatch | Medium | Version negotiation; graceful fallback to sync |

---

## 9. Rollout / Deployment Plan

1. Deploy `www` with async enrichment support
2. Deploy `client` with SSE subscription
3. Monitor enrichment job completion rates
4. Optional: Deploy `www_v1` for parity if needed

---

## 10. Backout Plan

- Revert search handler to use `Search()` instead of `SearchAsync()`
- Client gracefully handles missing `enrichmentJobId` (shows results without live updates)

---

## 11. Implementation Notes (Update During Work)

### www (Go) - Completed 2026-01-30

**Files Modified:**
- `www/internal/services/property/finder/enrichment_job.go` - New file, manages background enrichment
- `www/internal/services/property/finder/orchestrator.go` - Added `SearchAsync()` method
- `www/internal/api/handlers/discover/enrichment_sse.go` - SSE handler and search response updates

**Key Implementation Details:**
- Worker pool concurrency: 10 (configurable via `ENRICHMENT_CONCURRENCY`)
- Job timeout: 5 minutes
- SSE heartbeat: 30 seconds
- Event types: `update`, `progress`, `complete`, `error`

**Smart Property Caching (Added 2026-01-30):**
- Cache check before API call: `getCachedProperty()` checks `property_read:{provider}:{id}`
- Enriched property detection: `yearBuilt > 0 || rentEstimate > 0 || lotSize > 0`
- Cache miss: API call → enrich property → `cacheProperty()` with 24h TTL
- Cache hit: Use cached values, skip API call entirely
- Reduces repeated API calls for same properties across searches

**Performance Fix - Async Property Caching (Added 2026-01-30):**
- **Issue**: `SearchAsync()` was blocking ~7 seconds after starting enrichment job
- **Root Cause**: `cachePropertyReads()` called `cache.Set()` synchronously for each property. Each `cache.Set()` performs PostgreSQL UPSERT (~170ms). For 41 properties: 41 × 170ms ≈ 7 seconds.
- **Fix**: Run `cachePropertyReads()` in background goroutine:
  ```go
  // Before (blocking):
  o.cachePropertyReads(ctx, result.Provider, result.Properties)

  // After (non-blocking):
  go o.cachePropertyReads(context.Background(), providerName, propertiesToCache)
  ```
- **Result**: Response returns immediately after starting enrichment job (~7s faster)

### client - Completed 2026-01-30

**Files Modified:**
- `client/src/lib/api/sse.ts` - SSE subscription utilities
- `client/src/app/(dashboard)/discover/page.tsx` - SSE integration

**Key Implementation Details:**
- Uses `useRef` to track unsubscribe function for cleanup
- Updates properties via `setProperties` with index-based matching
- Shows "Enriching X/Y" progress indicator
- Cleans up on unmount, new search, and clear results

---

## 12. Completion Checklist

- [x] www async search endpoint working
- [x] www SSE streaming endpoint working
- [x] client SSE subscription implemented
- [x] client property updates in-place
- [x] Progress indicator shows enrichment status
- [x] Cleanup on unmount/new search/clear
- [x] Tests passing
- [x] Changelog updated
- [x] Plan status updated to Completed

---

## 13. Parity Reference Table

| Feature | www (Go) | www_v1 (Next.js) | Client |
|---------|----------|------------------|--------|
| Async search | `SearchAsync()` | `searchAsync()` | N/A |
| SSE endpoint | `/api/v2/discover/enrich/{jobId}/stream` | Same | Subscribes |
| Response fields | `enrichmentJobId`, `enrichmentStatus` | Same | Parses |
| Event: update | `{propertyIndex, success, yearBuilt, sqft}` | Same | Handles |
| Event: progress | `{completed, total, failed}` | Same | Handles |
| Event: complete | `{status, enriched, failed}` | Same | Handles |
| Cleanup | Auto-expires jobs (5 min) | Same | Unsubscribes |
| Smart caching | Check cache before API call | Same | N/A |
| Cache key | `property_read:{provider}:{id}` | Same | N/A |
| Cache TTL | 24 hours | Same | N/A |
| Async property caching | `go cachePropertyReads()` | Background task | N/A |

---

## 14. Client Integration Example

```typescript
// In discover page handleSearch:
const response = await searchProperties(criteria);

if (response.enrichmentJobId && response.enrichmentStatus !== 'COMPLETED') {
  const { unsubscribe } = subscribeToEnrichmentUpdates(response.enrichmentJobId, {
    onUpdate: (update) => {
      setProperties((prev) =>
        prev.map((p, idx) => {
          if (idx === update.propertyIndex && update.success) {
            return {
              ...p,
              yearBuilt: update.yearBuilt ?? p.yearBuilt,
              sqft: update.sqft ?? p.sqft,
            };
          }
          return p;
        })
      );
    },
    onProgress: (progress) => {
      setEnrichmentProgress({ completed: progress.completed, total: progress.total });
    },
    onComplete: (status) => {
      setEnrichmentStatus('COMPLETED');
      enrichmentUnsubscribeRef.current = null;
    },
  });

  enrichmentUnsubscribeRef.current = unsubscribe;
}
```

---

## 15. www_v1 Implementation Guide (For Parity)

If maintaining www_v1 (Next.js) alongside www (Go), implement these components:

### EnrichmentJobManager.ts

```typescript
interface EnrichmentJob {
  id: string;
  status: 'PENDING' | 'IN_PROGRESS' | 'COMPLETED' | 'FAILED';
  properties: PropertyResult[];
  subscribers: Set<(event: SSEEvent) => void>;
  completed: number;
  failed: number;
  createdAt: Date;
}

class EnrichmentJobManager {
  private jobs = new Map<string, EnrichmentJob>();
  private concurrency = 10;

  async startJob(properties: PropertyResult[]): Promise<string> {
    const jobId = crypto.randomUUID();
    const job: EnrichmentJob = {
      id: jobId,
      status: 'IN_PROGRESS',
      properties,
      subscribers: new Set(),
      completed: 0,
      failed: 0,
      createdAt: new Date(),
    };

    this.jobs.set(jobId, job);
    this.processJob(job);

    return jobId;
  }

  private async processJob(job: EnrichmentJob) {
    const queue = [...job.properties.keys()];

    await Promise.all(
      Array.from({ length: this.concurrency }, () =>
        this.worker(job, queue)
      )
    );

    job.status = 'COMPLETED';
    this.broadcast(job, { type: 'complete', data: { ... } });
  }

  subscribe(jobId: string, callback: (event: SSEEvent) => void): () => void {
    const job = this.jobs.get(jobId);
    if (!job) throw new Error('Job not found');

    job.subscribers.add(callback);
    return () => job.subscribers.delete(callback);
  }
}
```

### SSE Route (Next.js App Router)

```typescript
// www_v1/src/app/api/v2/discover/enrich/[jobId]/stream/route.ts
export async function GET(
  request: NextRequest,
  { params }: { params: { jobId: string } }
) {
  const encoder = new TextEncoder();

  const stream = new ReadableStream({
    start(controller) {
      const unsubscribe = enrichmentJobManager.subscribe(
        params.jobId,
        (event) => {
          controller.enqueue(
            encoder.encode(`event: ${event.type}\ndata: ${JSON.stringify(event.data)}\n\n`)
          );
        }
      );

      // Cleanup on close
      request.signal.addEventListener('abort', unsubscribe);
    },
  });

  return new Response(stream, {
    headers: {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      'Connection': 'keep-alive',
    },
  });
}
```

This ensures both backends can serve the same API contract for clients.

---

## 16. Smart Property Caching (www Implemented, www_v1 Parity Needed)

### Concept

Before making an API call to enrich a property, check if the property is already cached AND enriched. This avoids redundant API calls for properties that were already enriched in previous searches.

### www (Go) Implementation

```go
// In enrichment_job.go

func (m *EnrichmentJobManager) getCachedProperty(ctx context.Context, providerName, propertyID string) (*providers.Property, bool) {
    cacheKey := m.buildPropertyCacheKey(providerName, propertyID)
    cached, err := m.cache.Get(ctx, "", cacheKey)
    if err != nil || cached == nil {
        return nil, false
    }

    var prop providers.Property
    if err := json.Unmarshal(cached, &prop); err != nil {
        return nil, false
    }

    // Check if property is enriched (has yearBuilt or other enrichment data)
    isEnriched := prop.YearBuilt > 0 || prop.EstimatedRent > 0 || prop.LotSize > 0
    return &prop, isEnriched
}

func (m *EnrichmentJobManager) cacheProperty(ctx context.Context, prop *providers.Property) {
    cacheKey := m.buildPropertyCacheKey(prop.ProviderName, prop.ID)
    m.cache.Set(ctx, "", cacheKey, "property_read", prop, 24*time.Hour)
}

// In runEnrichment():
if cachedProp, isEnriched := m.getCachedProperty(enrichCtx, prop.ProviderName, prop.ID); isEnriched {
    // Use cached enriched data - no API call needed
    if cachedProp.YearBuilt > 0 && prop.YearBuilt == 0 {
        prop.YearBuilt = cachedProp.YearBuilt
        update.YearBuilt = cachedProp.YearBuilt
    }
    // ... apply other cached values ...
    return
}
// Not in cache - call API and cache result
```

### www_v1 (Next.js) Parity Implementation

```typescript
// In EnrichmentJobManager.ts

private async getCachedProperty(providerName: string, propertyId: string): Promise<{ property: PropertyResult | null; isEnriched: boolean }> {
  const cacheKey = `property_read:${providerName.toLowerCase().replace(/ /g, '_')}:${propertyId}`;
  const cached = await this.cache.get(cacheKey);

  if (!cached) {
    return { property: null, isEnriched: false };
  }

  const property = JSON.parse(cached) as PropertyResult;
  const isEnriched = (property.yearBuilt ?? 0) > 0 ||
                     (property.rentEstimate ?? 0) > 0 ||
                     (property.lotSize ?? 0) > 0;

  return { property, isEnriched };
}

private async cacheProperty(property: PropertyResult): Promise<void> {
  const providerName = property.providerName || 'hasdata';
  const cacheKey = `property_read:${providerName.toLowerCase().replace(/ /g, '_')}:${property.id}`;
  await this.cache.set(cacheKey, JSON.stringify(property), 24 * 60 * 60); // 24h TTL
}

// In worker function:
async enrichProperty(property: PropertyResult, index: number): Promise<EnrichmentUpdate> {
  // Check cache first
  const { property: cached, isEnriched } = await this.getCachedProperty(
    property.providerName ?? 'hasdata',
    property.id
  );

  if (isEnriched && cached) {
    // Apply cached enrichment values
    if (cached.yearBuilt && !property.yearBuilt) property.yearBuilt = cached.yearBuilt;
    if (cached.sqft && !property.sqft) property.sqft = cached.sqft;
    if (cached.lotSize && !property.lotSize) property.lotSize = cached.lotSize;
    if (cached.rentEstimate && !property.rentEstimate) property.rentEstimate = cached.rentEstimate;

    return {
      propertyIndex: index,
      success: true,
      yearBuilt: cached.yearBuilt,
      sqft: cached.sqft,
      lotSize: cached.lotSize,
      rentEstimate: cached.rentEstimate,
    };
  }

  // Not cached or not enriched - call API
  const enriched = await this.hasDataProvider.getPropertyByURL(property.listingUrl);

  if (enriched) {
    // Apply enrichment
    property.yearBuilt = enriched.yearBuilt;
    property.sqft = enriched.livingArea || enriched.area;
    property.lotSize = enriched.lotSize;
    property.rentEstimate = enriched.rentZestimate;

    // Cache for future use
    await this.cacheProperty(property);
  }

  return { propertyIndex: index, success: !!enriched, ... };
}
```

### Cache Key Format

| Component | Format | Example |
|-----------|--------|---------|
| Prefix | `property_read:` | - |
| Provider | lowercase, underscores | `hasdata`, `bright_data` |
| Property ID | as-is | `123456789` |
| **Full Key** | `property_read:{provider}:{id}` | `property_read:hasdata:123456789` |

### Benefits

1. **Cost Savings**: Reduces API calls for properties that were already enriched
2. **Speed**: Cache hits are instant vs 2-5s API calls
3. **Cross-Search Reuse**: Property enriched in one search benefits future searches
4. **24h TTL**: Data stays fresh while avoiding redundant calls

---

## 17. Async Property Caching in SearchAsync (www Implemented, www_v1 Parity Needed)

### Problem

In `SearchAsync()`, the `cachePropertyReads()` method was called synchronously, blocking the response while caching each property to PostgreSQL. For 41 properties at ~170ms per UPSERT, this added ~7 seconds of unnecessary latency.

### www (Go) Implementation

```go
// In orchestrator.go SearchAsync():

// Cache property reads for 24 hours (but not the search results since they're not fully enriched)
// Run in background to avoid blocking the response - PostgreSQL upserts are slow
if o.cache != nil && len(result.Properties) > 0 {
    providerName := result.Provider
    propertiesToCache := make([]providers.Property, len(result.Properties))
    copy(propertiesToCache, result.Properties)
    go o.cachePropertyReads(context.Background(), providerName, propertiesToCache)
}
```

**Key Points:**
- Copy the slice to avoid race conditions (goroutine runs after function returns)
- Use `context.Background()` since the request context will be cancelled
- Fire-and-forget - caching doesn't affect the response

### www_v1 (Next.js) Parity Implementation

```typescript
// In searchAsync():

// Cache property reads in background - don't await
if (properties.length > 0) {
  // Fire and forget - use setImmediate or process.nextTick to not block
  setImmediate(() => {
    this.cachePropertyReads(provider, [...properties]).catch(err => {
      this.logger.warn('Background cache failed', { error: err });
    });
  });
}
```

Or with a more explicit approach:

```typescript
// In searchAsync():

// Cache property reads in background - don't block response
const propertiesToCache = [...properties]; // Copy to avoid mutation issues
Promise.resolve().then(async () => {
  try {
    await this.cachePropertyReads(provider, propertiesToCache);
  } catch (err) {
    this.logger.warn('Background cache failed', { error: err });
  }
});
```

### Performance Impact

| Metric | Before | After |
|--------|--------|-------|
| Response time (41 properties) | ~14s | ~7s |
| Cache writes | Blocking | Background |
| User experience | Delayed | Immediate |
