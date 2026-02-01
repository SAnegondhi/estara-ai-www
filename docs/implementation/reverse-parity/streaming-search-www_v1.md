# Implementation Plan: Streaming Search in www_v1

## Owner
- Author: Claude
- Date: 2026-01-31
- Status: Planned

---

## 1. Problem Statement

The www (Go) backend has a streaming search endpoint `/api/v2/discover/search/stream` that www_v1 (TypeScript/Next.js) lacks. This is the most significant feature gap between the two backends. As a one-time exception to the www_v1 READ-ONLY policy, we will port this endpoint to www_v1 for feature parity.

**Key Feature**: Real-time property streaming via SSE with parallel enrichment and discovery session persistence.

---

## 2. Goals

**In Scope**:
- Port `/api/v2/discover/search/stream` endpoint from www (Go) to www_v1
- Add discovery session database tables via Prisma
- Implement SSE streaming with ordered parallel enrichment
- Create discovery sessions with stored properties on search completion

**Out of Scope**:
- Session archival/cleanup cron jobs (manual via Prisma Studio)
- Chat session linking (requires separate implementation)
- Full discovery session CRUD endpoints (only create for streaming search)
- Session list/detail endpoints (handled by www Go backend)

---

## 3. Background / Context

### www (Go) Implementation

The www (Go) backend implements streaming search in:
- `www/internal/api/handlers/discover/streaming_search.go` - SSE endpoint
- `www/internal/services/property/finder/orchestrator.go` - `SearchWithStreaming()` method
- `www/internal/api/handlers/discover/discovery_sessions.go` - Session CRUD

**Key Features**:
1. GET endpoint with query parameters (EventSource compatible)
2. SSE streaming with events: `progress`, `property`, `complete`, `error`
3. Parallel enrichment with HasData Property API (10 concurrent)
4. Ordered streaming (properties sent in original order)
5. Discovery session created at completion with properties stored in separate table
6. 3-minute timeout, 30-second keepalive

### www_v1 Current State

www_v1 has:
- `/api/v2/discover/search` (POST) - returns all results at once
- SSE infrastructure in `/api/ai/jobs/notifications/route.ts`
- `PropertyFinderOrchestrator` with enrichment capabilities
- `HasDataProvider` with Property API methods
- No discovery session tables or functionality

---

## 4. Proposed Design

### 4.1 Architecture

```
Client (EventSource)
    │
    ▼
GET /api/v2/discover/search/stream?location=Phoenix,%20AZ&token=<jwt>
    │
    ▼
┌─────────────────────────────────────────────────────────┐
│  Streaming Search Route (route.ts)                       │
│  - Parse query params                                    │
│  - Authenticate via token query param                    │
│  - Set SSE headers                                       │
│  - Create ReadableStream                                 │
└─────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────┐
│  PropertyFinderOrchestrator.searchWithStreaming()        │
│  - Call provider search (raw properties)                 │
│  - Yield progress event                                  │
│  - Parallel enrichment (concurrency: 5)                  │
│  - Yield property events (ordered)                       │
│  - Track failed/nodata counts                            │
└─────────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────┐
│  DiscoverySessionService.createForSearch()               │
│  - Create DiscoverySession record                        │
│  - Bulk insert DiscoverySessionProperty records          │
│  - Return session ID                                     │
└─────────────────────────────────────────────────────────┘
    │
    ▼
SSE Events → Client
```

### 4.2 Database Schema

Four new Prisma models matching www (Go) schema:

#### DiscoverySession
```prisma
model DiscoverySession {
  id                 String    @id @default(cuid())
  userId             String
  searchCriteria     Json
  location           String
  propertyCount      Int       @default(0)
  cachedPropertyIds  String[]  @default([])
  name               String?
  notes              String?
  status             String    @default("ACTIVE")  // ACTIVE, ARCHIVED
  chatSessionCount   Int       @default(0)
  evaluationCount    Int       @default(0)
  createdAt          DateTime  @default(now())
  updatedAt          DateTime  @updatedAt
  lastAccessedAt     DateTime  @default(now())
  archivedAt         DateTime?
  expiresAt          DateTime?

  user        User                            @relation(fields: [userId], references: [id], onDelete: Cascade)
  activities  DiscoverySessionActivity[]
  properties  DiscoverySessionProperty[]
  evaluations DiscoverySessionEvaluation[]

  @@index([userId, createdAt(sort: Desc)])
  @@index([userId, status])
  @@index([status])
  @@index([expiresAt])
  @@map("discovery_sessions")
}
```

#### DiscoverySessionActivity
```prisma
model DiscoverySessionActivity {
  id                 String   @id @default(cuid())
  discoverySessionId String
  activityType       String   // CHAT_SESSION, EVALUATION
  activityId         String
  createdAt          DateTime @default(now())

  session DiscoverySession @relation(fields: [discoverySessionId], references: [id], onDelete: Cascade)

  @@unique([discoverySessionId, activityType, activityId])
  @@index([discoverySessionId])
  @@map("discovery_session_activities")
}
```

#### DiscoverySessionProperty
```prisma
model DiscoverySessionProperty {
  id                 String   @id @default(cuid())
  discoverySessionId String
  listingId          String
  address            String
  city               String
  state              String
  zipCode            String?
  price              Int
  estimatedRent      Int?
  capRateMin         Float?
  capRateMax         Float?
  beds               Int      @default(0)
  baths              Float    @default(0)
  sqft               Int?
  yearBuilt          Int?
  propertyType       String?
  listingDate        String?
  daysOnMarket       Int?
  imageUrl           String?
  listingSearchUrl   String?
  googleSearchUrl    String?
  latitude           Float?
  longitude          Float?
  createdAt          DateTime @default(now())

  session DiscoverySession @relation(fields: [discoverySessionId], references: [id], onDelete: Cascade)

  @@unique([discoverySessionId, listingId])
  @@index([discoverySessionId])
  @@index([listingId])
  @@map("discovery_session_properties")
}
```

#### DiscoverySessionEvaluation
```prisma
model DiscoverySessionEvaluation {
  id                 String   @id @default(cuid())
  discoverySessionId String
  propertyId         String
  address            String
  price              Int
  estimatedRent      Int?
  scenarios          Json
  recommendation     String?
  riskLevel          String?
  score              Int?
  status             String   @default("COMPLETED")
  createdAt          DateTime @default(now())

  session DiscoverySession @relation(fields: [discoverySessionId], references: [id], onDelete: Cascade)

  @@unique([discoverySessionId, propertyId])
  @@index([discoverySessionId])
  @@map("discovery_session_evaluations")
}
```

### 4.3 API Contract

#### Request
```
GET /api/v2/discover/search/stream?location=Phoenix,%20AZ&minPrice=200000&maxPrice=500000&minBeds=3&minCapRate=5&token=<jwt>
```

**Query Parameters**:
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| location | string | Yes | City, State format (e.g., "Phoenix, AZ") |
| minPrice | number | No | Minimum property price |
| maxPrice | number | No | Maximum property price |
| minBeds | number | No | Minimum bedrooms |
| propertyType | string | No | Property type filter |
| minCapRate | number | No | Minimum cap rate percentage |
| minGrossYield | number | No | Minimum gross yield percentage |
| token | string | Yes | JWT authentication token |

**Note**: Token passed via query param because EventSource API doesn't support custom headers.

#### SSE Events

**progress** - Sent when total count is known
```json
event: progress
data: {"processed":5,"total":20}

```

**property** - Sent for each enriched property
```json
event: property
data: {"index":0,"property":{"id":"zpid-123","address":"123 Main St","city":"Phoenix","state":"AZ","zipCode":"85001","price":350000,"estimatedRent":2100,"capRateRange":{"min":5.2,"max":6.2},"beds":3,"baths":2,"sqft":1800,"yearBuilt":2015,"propertyType":"single_family","daysOnMarket":14,"imageUrl":"https://...","listingSearchUrl":"https://google.com/search?q=...","googleSearchUrl":"https://google.com/search?q=...","latitude":33.4484,"longitude":-112.0740}}

```

**complete** - Sent at end of stream
```json
event: complete
data: {"total":20,"enriched":18,"failed":1,"noData":1,"discoverySessionId":"clx123..."}

```

**error** - Sent on errors
```json
event: error
data: {"message":"search timed out"}

```

**keepalive** - Comment sent every 30 seconds
```
: keepalive

```

### 4.4 Configuration

| Setting | Value | Description |
|---------|-------|-------------|
| `STREAMING_SEARCH_TIMEOUT` | 3 minutes | Max stream duration |
| `SSE_KEEPALIVE_INTERVAL` | 30 seconds | Keepalive ping interval |
| `ENRICHMENT_CONCURRENCY` | 5 | Max parallel Property API calls |

---

## 5. Implementation Steps

### Step 1: Add Prisma Schema

**File**: `www_v1/prisma/schema.prisma`

1. Add relation to existing `User` model:
```prisma
model User {
  // ... existing fields
  discoverySessions DiscoverySession[]
}
```

2. Add the 4 new models (DiscoverySession, DiscoverySessionActivity, DiscoverySessionProperty, DiscoverySessionEvaluation) as defined in section 4.2.

### Step 2: Generate and Apply Migration

```bash
cd www_v1
bunx prisma migrate dev --name add_discovery_sessions
bunx prisma generate
```

### Step 3: Add StreamingResult Type

**File**: `www_v1/src/lib/propertyFinder/types.ts`

```typescript
export interface StreamingResult {
  type: 'property' | 'progress' | 'failed' | 'nodata' | 'error';
  property?: Property;
  progress?: { processed: number; total: number };
  error?: Error;
}

export interface StreamingSearchOptions {
  onProgress?: (processed: number, total: number) => void;
  enrichmentConcurrency?: number;
}
```

### Step 4: Add searchWithStreaming() to PropertyFinderOrchestrator

**File**: `www_v1/src/lib/propertyFinder/PropertyFinderOrchestrator.ts`

```typescript
/**
 * Search properties with streaming support
 * Yields results as they are enriched for SSE streaming
 */
async *searchWithStreaming(
  params: PropertySearchParams,
  options: StreamingSearchOptions = {}
): AsyncGenerator<StreamingResult, void, unknown> {
  const { enrichmentConcurrency = 5 } = options;

  // Step 1: Get raw properties from provider
  const result = await this.search({ ...params, forceRefresh: false });
  const rawProperties = result.properties;

  // Step 2: Yield initial progress
  yield {
    type: 'progress',
    progress: { processed: 0, total: rawProperties.length }
  };

  // Step 3: Parallel enrichment with ordered results
  const orderedResults = new Map<number, StreamingResult>();
  let nextIndex = 0;
  let activeCount = 0;
  const queue: Array<{ index: number; property: Property }> =
    rawProperties.map((p, i) => ({ index: i, property: p }));

  // Process queue with concurrency limit
  while (queue.length > 0 || activeCount > 0 || orderedResults.size > 0) {
    // Start new enrichments up to concurrency limit
    while (queue.length > 0 && activeCount < enrichmentConcurrency) {
      const item = queue.shift()!;
      activeCount++;

      this.enrichPropertyAsync(item.property)
        .then(enriched => {
          orderedResults.set(item.index, {
            type: 'property',
            property: enriched
          });
        })
        .catch(err => {
          orderedResults.set(item.index, {
            type: 'failed',
            error: err
          });
        })
        .finally(() => {
          activeCount--;
        });
    }

    // Yield consecutive results
    while (orderedResults.has(nextIndex)) {
      const result = orderedResults.get(nextIndex)!;
      orderedResults.delete(nextIndex);
      yield result;

      // Yield progress after each property
      yield {
        type: 'progress',
        progress: { processed: nextIndex + 1, total: rawProperties.length }
      };

      nextIndex++;
    }

    // Wait a bit before checking again
    await new Promise(resolve => setTimeout(resolve, 50));
  }
}

/**
 * Async property enrichment for streaming
 */
private async enrichPropertyAsync(property: Property): Promise<Property> {
  // Enrich with yearBuilt via Property API if missing
  if (!property.yearBuilt && property.url?.includes('zillow.com')) {
    try {
      const api = createHasDataApi();
      const details = await api.getPropertyByUrl(property.url);
      if (details?.property) {
        property.yearBuilt = details.property.yearBuilt;
        property.squareFeet = details.property.livingArea || property.squareFeet;
        property.rentEstimate = details.property.rentZestimate || property.rentEstimate;
      }
    } catch (err) {
      console.warn('[PropertyFinder] Enrichment failed:', err);
    }
  }

  // Apply investment metrics
  return this.enrichProperties([property])[0];
}
```

### Step 5: Create Discovery Session Service

**File**: `www_v1/src/lib/services/discoverySessionService.ts`

```typescript
import { prisma } from '@/lib/prisma';
import type { V2PropertyResult } from '@/app/api/v2/discover/search/route';

interface SearchCriteria {
  location: string;
  minPrice?: number;
  maxPrice?: number;
  minBeds?: number;
  propertyTypes?: string[];
  minCapRate?: number;
  minGrossYield?: number;
}

/**
 * Create a discovery session for a streaming search
 * Stores session metadata and all properties in the database
 */
export async function createDiscoverySessionForSearch(
  userId: string,
  criteria: SearchCriteria,
  location: string,
  properties: V2PropertyResult[]
): Promise<string | null> {
  if (!userId || properties.length === 0) {
    return null;
  }

  try {
    // Calculate expiry (180 days from now)
    const expiresAt = new Date();
    expiresAt.setDate(expiresAt.getDate() + 180);

    // Create session with properties in a transaction
    const session = await prisma.$transaction(async (tx) => {
      // Create the session
      const newSession = await tx.discoverySession.create({
        data: {
          userId,
          searchCriteria: criteria,
          location,
          propertyCount: properties.length,
          cachedPropertyIds: properties.map(p => p.id),
          status: 'ACTIVE',
          expiresAt,
        },
      });

      // Bulk insert properties
      if (properties.length > 0) {
        await tx.discoverySessionProperty.createMany({
          data: properties.map(p => ({
            discoverySessionId: newSession.id,
            listingId: p.id,
            address: p.address,
            city: p.city,
            state: p.state,
            zipCode: p.zipCode || null,
            price: p.price,
            estimatedRent: p.estimatedRent || null,
            capRateMin: p.capRateRange?.min || null,
            capRateMax: p.capRateRange?.max || null,
            beds: p.beds,
            baths: p.baths,
            sqft: p.sqft || null,
            yearBuilt: p.yearBuilt || null,
            propertyType: p.propertyType || null,
            listingDate: p.listingDate || null,
            daysOnMarket: p.daysOnMarket || null,
            imageUrl: p.imageUrl || null,
            listingSearchUrl: p.listingSearchUrl || null,
            googleSearchUrl: p.googleSearchUrl || null,
            latitude: p.latitude || null,
            longitude: p.longitude || null,
          })),
          skipDuplicates: true,
        });
      }

      return newSession;
    });

    console.log(`[DiscoverySession] Created session ${session.id} with ${properties.length} properties`);
    return session.id;
  } catch (error) {
    console.error('[DiscoverySession] Failed to create session:', error);
    return null;
  }
}
```

### Step 6: Create Streaming Search Route

**File**: `www_v1/src/app/api/v2/discover/search/stream/route.ts`

```typescript
import { NextRequest } from 'next/server';
import jwt from 'jsonwebtoken';
import {
  PropertyFinderOrchestrator,
  type PropertySearchParams,
} from '@/lib/propertyFinder';
import { createDiscoverySessionForSearch } from '@/lib/services/discoverySessionService';

export const dynamic = 'force-dynamic';
export const maxDuration = 180; // 3 minutes

// Constants
const STREAMING_SEARCH_TIMEOUT = 3 * 60 * 1000; // 3 minutes
const SSE_KEEPALIVE_INTERVAL = 30 * 1000; // 30 seconds

// State name mapping
const stateNames: Record<string, string> = {
  AL: 'Alabama', AK: 'Alaska', AZ: 'Arizona', AR: 'Arkansas', CA: 'California',
  CO: 'Colorado', CT: 'Connecticut', DE: 'Delaware', FL: 'Florida', GA: 'Georgia',
  HI: 'Hawaii', ID: 'Idaho', IL: 'Illinois', IN: 'Indiana', IA: 'Iowa',
  KS: 'Kansas', KY: 'Kentucky', LA: 'Louisiana', ME: 'Maine', MD: 'Maryland',
  MA: 'Massachusetts', MI: 'Michigan', MN: 'Minnesota', MS: 'Mississippi', MO: 'Missouri',
  MT: 'Montana', NE: 'Nebraska', NV: 'Nevada', NH: 'New Hampshire', NJ: 'New Jersey',
  NM: 'New Mexico', NY: 'New York', NC: 'North Carolina', ND: 'North Dakota', OH: 'Ohio',
  OK: 'Oklahoma', OR: 'Oregon', PA: 'Pennsylvania', RI: 'Rhode Island', SC: 'South Carolina',
  SD: 'South Dakota', TN: 'Tennessee', TX: 'Texas', UT: 'Utah', VT: 'Vermont',
  VA: 'Virginia', WA: 'Washington', WV: 'West Virginia', WI: 'Wisconsin', WY: 'Wyoming',
  DC: 'District of Columbia',
};

function parseLocation(location: string): { city: string; state: string } | null {
  const commaMatch = location.match(/^([^,]+),\s*(.+)$/);
  if (commaMatch) {
    const city = commaMatch[1].trim();
    let state = commaMatch[2].trim();
    if (state.length === 2) {
      state = stateNames[state.toUpperCase()] || state;
    }
    return { city, state };
  }
  return null;
}

interface V2PropertyResult {
  id: string;
  address: string;
  city: string;
  state: string;
  zipCode: string;
  price: number;
  estimatedRent?: number;
  capRateRange?: { min: number; max: number };
  beds: number;
  baths: number;
  sqft: number;
  yearBuilt?: number;
  propertyType: string;
  listingDate?: string;
  daysOnMarket?: number;
  imageUrl?: string;
  listingSearchUrl: string;
  googleSearchUrl: string;
  latitude?: number;
  longitude?: number;
}

export async function GET(request: NextRequest) {
  const { searchParams } = new URL(request.url);

  // Parse token from query param (EventSource doesn't support headers)
  const token = searchParams.get('token');
  if (!token) {
    return new Response(JSON.stringify({ error: 'Authentication required' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  // Verify JWT
  let userId: string;
  try {
    const payload = jwt.verify(token, process.env.CLIENT_JWT_SECRET!) as { userId: string };
    userId = payload.userId;
  } catch (error) {
    return new Response(JSON.stringify({ error: 'Invalid token' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  // Parse location
  const location = searchParams.get('location');
  if (!location) {
    return new Response(JSON.stringify({ error: 'location is required' }), {
      status: 400,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  const parsedLocation = parseLocation(location);
  if (!parsedLocation) {
    return new Response(JSON.stringify({ error: 'Invalid location format' }), {
      status: 400,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  // Parse other query params
  const minPrice = searchParams.get('minPrice') ? parseInt(searchParams.get('minPrice')!) : undefined;
  const maxPrice = searchParams.get('maxPrice') ? parseInt(searchParams.get('maxPrice')!) : undefined;
  const minBeds = searchParams.get('minBeds') ? parseInt(searchParams.get('minBeds')!) : undefined;
  const propertyType = searchParams.get('propertyType') || undefined;
  const minCapRate = searchParams.get('minCapRate') ? parseFloat(searchParams.get('minCapRate')!) : undefined;
  const minGrossYield = searchParams.get('minGrossYield') ? parseFloat(searchParams.get('minGrossYield')!) : undefined;

  console.log(`[StreamingSearch] Starting for ${location}, userId: ${userId}`);

  // Create SSE stream
  const encoder = new TextEncoder();
  let keepaliveInterval: NodeJS.Timeout | null = null;
  let timeoutId: NodeJS.Timeout | null = null;
  let isStreamClosed = false;

  const stream = new ReadableStream({
    async start(controller) {
      const properties: V2PropertyResult[] = [];
      let totalCount = 0;
      let enrichedCount = 0;
      let failedCount = 0;
      let noDataCount = 0;
      let propertyIndex = 0;

      // Helper to send SSE event
      const sendEvent = (eventType: string, data: unknown) => {
        if (isStreamClosed) return;
        try {
          const message = `event: ${eventType}\ndata: ${JSON.stringify(data)}\n\n`;
          controller.enqueue(encoder.encode(message));
        } catch (err) {
          console.error('[StreamingSearch] Failed to send event:', err);
        }
      };

      // Setup keepalive
      keepaliveInterval = setInterval(() => {
        if (isStreamClosed) return;
        try {
          controller.enqueue(encoder.encode(': keepalive\n\n'));
        } catch (err) {
          // Stream may be closed
        }
      }, SSE_KEEPALIVE_INTERVAL);

      // Setup timeout
      timeoutId = setTimeout(() => {
        sendEvent('error', { message: 'search timed out' });
        cleanup();
      }, STREAMING_SEARCH_TIMEOUT);

      const cleanup = () => {
        isStreamClosed = true;
        if (keepaliveInterval) clearInterval(keepaliveInterval);
        if (timeoutId) clearTimeout(timeoutId);
        try {
          controller.close();
        } catch (err) {
          // Already closed
        }
      };

      // Handle client disconnect
      request.signal.addEventListener('abort', () => {
        console.log('[StreamingSearch] Client disconnected');
        cleanup();
      });

      try {
        // Build search params
        const searchParams: PropertySearchParams = {
          location,
          listingType: 'for-sale',
          minPrice,
          maxPrice,
          bedrooms: minBeds,
          propertyTypes: propertyType ? [propertyType] : undefined,
        };

        // Create orchestrator and start streaming search
        const orchestrator = new PropertyFinderOrchestrator();

        for await (const result of orchestrator.searchWithStreaming(searchParams)) {
          if (isStreamClosed) break;

          switch (result.type) {
            case 'progress':
              if (result.progress) {
                totalCount = result.progress.total;
                sendEvent('progress', result.progress);
              }
              break;

            case 'property':
              if (result.property) {
                const p = result.property;

                // Skip invalid prices
                if (p.price <= 0) continue;

                // Calculate cap rate
                const capRate = p.rentEstimate && p.price > 0
                  ? ((p.rentEstimate * 12) / p.price) * 100
                  : undefined;

                // Apply investment filters
                if (minCapRate && capRate && capRate < minCapRate) continue;
                if (minGrossYield && capRate && capRate < minGrossYield) continue;

                // Convert to V2PropertyResult
                const prop: V2PropertyResult = {
                  id: p.id || `${p.address}-${p.zipCode}`,
                  address: p.address,
                  city: p.city || '',
                  state: p.state || '',
                  zipCode: p.zipCode || '',
                  price: p.price,
                  estimatedRent: p.rentEstimate,
                  capRateRange: capRate ? { min: capRate - 0.5, max: capRate + 0.5 } : undefined,
                  beds: p.bedrooms,
                  baths: p.bathrooms,
                  sqft: p.squareFeet || 0,
                  yearBuilt: p.yearBuilt,
                  propertyType: p.propertyType || 'single_family',
                  daysOnMarket: p.daysOnMarket,
                  imageUrl: p.imageUrl,
                  listingSearchUrl: buildListingSearchUrl(p),
                  googleSearchUrl: buildGoogleSearchUrl(p),
                  latitude: p.latitude,
                  longitude: p.longitude,
                };

                properties.push(prop);
                enrichedCount++;

                sendEvent('property', { index: propertyIndex, property: prop });
                propertyIndex++;
              }
              break;

            case 'failed':
              failedCount++;
              break;

            case 'nodata':
              noDataCount++;
              break;

            case 'error':
              sendEvent('error', { message: result.error?.message || 'Unknown error' });
              break;
          }
        }

        // Create discovery session
        let discoverySessionId: string | null = null;
        if (userId && properties.length > 0) {
          discoverySessionId = await createDiscoverySessionForSearch(
            userId,
            {
              location,
              minPrice,
              maxPrice,
              minBeds,
              propertyTypes: propertyType ? [propertyType] : undefined,
              minCapRate,
              minGrossYield,
            },
            location,
            properties
          );
        }

        // Send complete event
        sendEvent('complete', {
          total: totalCount,
          enriched: enrichedCount,
          failed: failedCount,
          noData: noDataCount,
          discoverySessionId,
        });

        console.log(`[StreamingSearch] Complete: ${enrichedCount} enriched, ${failedCount} failed, session: ${discoverySessionId}`);
      } catch (error) {
        console.error('[StreamingSearch] Error:', error);
        sendEvent('error', { message: error instanceof Error ? error.message : 'Search failed' });
      } finally {
        cleanup();
      }
    },
  });

  return new Response(stream, {
    headers: {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache, no-transform',
      'Connection': 'keep-alive',
      'X-Accel-Buffering': 'no',
      'Access-Control-Allow-Origin': process.env.NEXT_PUBLIC_CLIENT_URL || '*',
      'Access-Control-Allow-Credentials': 'true',
    },
  });
}

// Helper functions
function buildListingSearchUrl(property: { address: string; city?: string; state?: string; zipCode?: string }): string {
  const fullAddress = [property.address, property.city || '', property.state || '', property.zipCode || '']
    .filter(Boolean)
    .join(' ');
  return `https://www.google.com/search?q=${encodeURIComponent(fullAddress + ' real estate listing')}`;
}

function buildGoogleSearchUrl(property: { address: string; city?: string; state?: string; zipCode?: string }): string {
  const fullAddress = [property.address, property.city || '', property.state || '', property.zipCode || '']
    .filter(Boolean)
    .join(' ');
  return `https://www.google.com/search?q=${encodeURIComponent(fullAddress + ' for sale')}`;
}
```

---

## 6. File Changes Summary

| File | Action | Description |
|------|--------|-------------|
| `www_v1/prisma/schema.prisma` | MODIFY | Add User.discoverySessions relation + 4 discovery session models |
| `www_v1/src/lib/propertyFinder/types.ts` | MODIFY | Add `StreamingResult` and `StreamingSearchOptions` types |
| `www_v1/src/lib/propertyFinder/PropertyFinderOrchestrator.ts` | MODIFY | Add `searchWithStreaming()` async generator method |
| `www_v1/src/lib/services/discoverySessionService.ts` | CREATE | Discovery session creation service |
| `www_v1/src/app/api/v2/discover/search/stream/route.ts` | CREATE | SSE streaming search endpoint |

---

## 7. Testing Strategy

### Unit Tests
- `searchWithStreaming()` ordering guarantee
- Discovery session creation with properties
- Investment filter application

### Manual Verification

1. **Database Migration**:
```bash
cd www_v1
bunx prisma migrate dev --name add_discovery_sessions
bunx prisma generate
```

2. **Build Verification**:
```bash
cd www_v1
bunx tsc --noEmit
bun run build
```

3. **Manual SSE Test**:
```bash
# Start www_v1 server
cd www_v1 && bun run dev

# Test streaming search (get JWT token first)
curl -N "http://localhost:3000/api/v2/discover/search/stream?location=Phoenix,%20AZ&minPrice=200000&token=<jwt_token>"
```

4. **Expected Output**:
```
event: progress
data: {"processed":0,"total":25}

event: property
data: {"index":0,"property":{"id":"zpid-123",...}}

event: property
data: {"index":1,"property":{"id":"zpid-456",...}}

: keepalive

event: progress
data: {"processed":25,"total":25}

event: complete
data: {"total":25,"enriched":22,"failed":2,"noData":1,"discoverySessionId":"clx123..."}
```

5. **Verify Discovery Session**:
```bash
bunx prisma studio
# Navigate to discovery_sessions table
# Verify session created with propertyCount matching enriched count
# Navigate to discovery_session_properties table
# Verify properties stored with all fields
```

---

## 8. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Database migration on shared DB | Medium | Migration is additive (new tables); no existing table changes |
| Property enrichment timeouts | Medium | 3-minute timeout; send partial results in complete event |
| Rate limiting from HasData | Medium | Limit concurrency to 5; existing provider handles retries |
| Large property result sets | Low | Session properties stored in separate table; Prisma bulk insert |
| JWT in query param exposure | Low | Use HTTPS in production; token is short-lived |

---

## 9. Rollout Plan

1. **Development**:
   - Implement on feature branch
   - Test locally with development database

2. **Staging**:
   - Apply migration to staging database
   - Verify endpoint functionality
   - Test with client app

3. **Production**:
   - Apply migration during low-traffic window
   - Deploy code changes
   - Monitor for errors

---

## 10. Backout Plan

1. **Code Rollback**: Revert the commit containing the new endpoint
2. **Database**: Keep tables (they're additive and don't affect existing functionality)
3. **Client**: Falls back to non-streaming search endpoint

---

## 11. Completion Checklist

- [ ] Prisma schema updated with 4 new models
- [ ] Migration applied successfully
- [ ] `StreamingResult` type added
- [ ] `searchWithStreaming()` method implemented
- [ ] `discoverySessionService.ts` created
- [ ] Streaming search route created
- [ ] Manual testing passed
- [ ] Build passes (`bunx tsc --noEmit && bun run build`)
- [ ] Changelog updated
