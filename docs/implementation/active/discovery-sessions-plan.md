# Discovery Sessions Implementation Plan

**Status**: In Progress
**Started**: 2026-01-30
**Last Updated**: 2026-01-31

---

## Overview

Add Discovery Sessions feature to group property searches with their associated AI Chat conversations and Quick Evaluations. Users can recall entire discovery sessions from history.

**Key Changes:**
1. Reset Search clears local state only (NOT server cache)
2. Each search creates a Discovery Session with unique ID
3. Sessions stored in database under user's ID
4. AI Chat and Evaluations linked to their originating session
5. History tab shows sessions; clicking recalls full context

**Lifecycle:**
- All sessions auto-archived after 30 days
- Users can restore archived sessions
- All sessions auto-deleted after 180 days

---

## Implementation Status

| Phase | Status | Notes |
|-------|--------|-------|
| 1. Database Schema | **COMPLETE** | Tables created, migration applied 2026-01-30 |
| 2. Backend API | **COMPLETE** | All endpoints implemented and tested |
| 3. Background Jobs | PENDING | Auto-archive and cleanup jobs not yet implemented |
| 4. Client API | **COMPLETE** | Types and functions added |
| 5. Discover Page | **COMPLETE** | Session tracking, history tab working |
| 6. Activity Linking | **COMPLETE** | Chat and eval pages updated 2026-01-30 |
| 7. History UI | **COMPLETE** | Session cards and detail page working |
| 8. Archive/Restore | PENDING | Not yet implemented |
| 9. Testing | **PARTIAL** | Manual E2E tested, automated tests pending |

---

## 1. Database Schema Changes

### File: `/www/internal/db/queries/schema/009_discovery.sql`

**Status**: COMPLETE - Migration applied 2026-01-30

```sql
-- Discovery Sessions table
CREATE TABLE IF NOT EXISTS discovery_sessions (
    id TEXT PRIMARY KEY,
    "userId" TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Search context
    "searchCriteria" JSONB NOT NULL,
    location TEXT NOT NULL,
    "propertyCount" INTEGER NOT NULL DEFAULT 0,
    "cachedPropertyIds" TEXT[] NOT NULL DEFAULT '{}',

    -- User metadata
    name TEXT,
    notes TEXT,

    -- Status
    status TEXT NOT NULL DEFAULT 'ACTIVE',  -- ACTIVE, ARCHIVED

    -- Timestamps
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "lastAccessedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "archivedAt" TIMESTAMP(3),  -- When auto-archived (30 days)
    "expiresAt" TIMESTAMP(3),   -- When auto-deleted (180 days from creation)

    -- Metrics
    "chatSessionCount" INTEGER NOT NULL DEFAULT 0,
    "evaluationCount" INTEGER NOT NULL DEFAULT 0
);

-- Discovery Session Properties table (ADDED 2026-01-30)
CREATE TABLE IF NOT EXISTS discovery_session_properties (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "listingId" TEXT NOT NULL,

    -- Property details (24 columns total)
    address TEXT NOT NULL,
    city TEXT NOT NULL,
    state TEXT NOT NULL,
    "zipCode" TEXT,
    price INTEGER NOT NULL,
    "estimatedRent" INTEGER,
    "capRateMin" NUMERIC(5,2),
    "capRateMax" NUMERIC(5,2),
    beds INTEGER NOT NULL DEFAULT 0,
    baths NUMERIC(3,1) NOT NULL DEFAULT 0,
    sqft INTEGER,
    "yearBuilt" INTEGER,
    "propertyType" TEXT,
    "listingDate" TEXT,
    "daysOnMarket" INTEGER,
    "imageUrl" TEXT,
    "listingSearchUrl" TEXT,
    "googleSearchUrl" TEXT,
    latitude NUMERIC(10,7),
    longitude NUMERIC(10,7),
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE("discoverySessionId", "listingId")
);

-- Activity links table
CREATE TABLE IF NOT EXISTS discovery_session_activities (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "activityType" TEXT NOT NULL,  -- 'CHAT_SESSION', 'EVALUATION'
    "activityId" TEXT NOT NULL,
    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE("discoverySessionId", "activityType", "activityId")
);

-- Discovery Session Evaluations table (ADDED 2026-01-31)
-- Stores evaluation results as part of the discovery session
CREATE TABLE IF NOT EXISTS discovery_session_evaluations (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "propertyId" TEXT NOT NULL,  -- References listingId from discovery_session_properties

    -- Property info snapshot (at evaluation time)
    address TEXT NOT NULL,
    price INTEGER NOT NULL,
    "estimatedRent" INTEGER,

    -- Scenario results (JSONB for flexibility)
    scenarios JSONB NOT NULL,  -- Contains conservative, base, optimistic metrics

    -- Derived values
    recommendation TEXT,  -- strong_buy, buy, hold, pass
    "riskLevel" TEXT,     -- low, medium, high
    score INTEGER,        -- 0-100

    -- Status
    status TEXT NOT NULL DEFAULT 'COMPLETED',  -- COMPLETED, FAILED

    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE("discoverySessionId", "propertyId")
);

-- Indexes
CREATE INDEX idx_discovery_session_user_created ON discovery_sessions("userId", "createdAt" DESC);
CREATE INDEX idx_discovery_session_status ON discovery_sessions(status);
CREATE INDEX idx_discovery_session_expires ON discovery_sessions("expiresAt");
CREATE INDEX idx_activity_session ON discovery_session_activities("discoverySessionId");
CREATE INDEX idx_activity_type ON discovery_session_activities("activityType");
CREATE INDEX idx_session_properties_session ON discovery_session_properties("discoverySessionId");
CREATE INDEX idx_session_properties_listing ON discovery_session_properties("listingId");
CREATE INDEX idx_session_evaluations_session ON discovery_session_evaluations("discoverySessionId");
CREATE INDEX idx_session_evaluations_property ON discovery_session_evaluations("propertyId");
```

### File: `/www/internal/db/queries/sql/discovery.sql`

**Status**: COMPLETE

Key queries implemented:
- `CreateDiscoverySession` - Insert with expiresAt = createdAt + 180 days
- `GetDiscoverySession` / `GetDiscoverySessionByUser` - Get session with auth check
- `ListUserDiscoverySessions` - Paginated list (status filter: ACTIVE or ARCHIVED)
- `UpdateDiscoverySessionAccess` - Update lastAccessedAt
- `ArchiveDiscoverySession` / `RestoreDiscoverySession` - Archive management
- `AutoArchiveOldSessions` / `DeleteExpiredSessions` - Cleanup queries
- `CreateActivityLink` - Link chat/evaluation to session
- `IncrementChatSessionCount` / `IncrementEvaluationCount`
- **NEW** `CreateDiscoverySessionProperty` - Insert property with ON CONFLICT DO NOTHING
- **NEW** `ListSessionProperties` - Get all properties for a session
- **NEW** `GetSessionProperty` - Get specific property by listing ID
- **NEW** `DeleteSessionProperties` - Delete all properties for a session
- **NEW** `CountSessionProperties` - Count properties in session
- **NEW 2026-01-31** `CreateDiscoverySessionEvaluation` - Insert/update evaluation with upsert
- **NEW 2026-01-31** `ListSessionEvaluations` - Get all evaluations for a session
- **NEW 2026-01-31** `GetSessionEvaluation` - Get specific evaluation by property ID
- **NEW 2026-01-31** `DeleteSessionEvaluations` - Delete all evaluations for a session
- **NEW 2026-01-31** `CountSessionEvaluations` - Count evaluations in session

---

## 2. Backend API Changes (www)

### Handler: `/www/internal/api/handlers/discover/discovery_sessions.go`

**Status**: COMPLETE

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api/v2/discover/sessions` | POST | COMPLETE | Create session (called by search) |
| `/api/v2/discover/sessions` | GET | COMPLETE | List user's sessions (query: status=ACTIVE\|ARCHIVED) |
| `/api/v2/discover/sessions/{id}` | GET | COMPLETE | Get session with properties, activities & evaluations |
| `/api/v2/discover/sessions/{id}` | DELETE | COMPLETE | Archive session |
| `/api/v2/discover/sessions/{id}/restore` | POST | COMPLETE | Restore archived session |
| `/api/v2/discover/sessions/{id}/link` | POST | COMPLETE | Link activity to session |
| `/api/v2/discover/sessions/{id}/evaluations` | POST | **NEW 2026-01-31** | Save evaluations to session |

### Handler Updates: `/www/internal/api/handlers/discover/handler.go`

**Status**: COMPLETE

- `V2SearchResponse` includes `discoverySessionId`
- `CreateDiscoverySessionForSearch()` now accepts and stores properties
- Properties stored in `discovery_session_properties` table (not just cached IDs)

### Background Jobs: `/www/internal/jobs/discovery_cleanup.go`

**Status**: PENDING

```go
// TODO: Implement daily cron job
func (j *DiscoveryCleanupJob) Run(ctx context.Context) error {
    // 1. Auto-archive sessions older than 30 days
    // 2. Delete sessions past expiration (180 days)
}
```

---

## 3. Frontend Changes (client)

### API Client: `/client/src/lib/api/client.ts`

**Status**: COMPLETE

```typescript
// Types added
export interface DiscoverySession { ... }
export interface DiscoverySessionDetail extends DiscoverySession {
  cachedPropertyIds: string[];
  properties: PropertyResult[];  // ADDED - full property data
  activities: DiscoverySessionActivity[];
  evaluations?: SessionEvaluation[];  // ADDED 2026-01-31
}

// NEW 2026-01-31: Evaluation types
export interface SessionEvaluation {
  id: string;
  propertyId: string;
  address: string;
  price: number;
  estimatedRent?: number;
  scenarios: { conservative, base, optimistic };
  recommendation?: string;
  riskLevel?: string;
  score?: number;
  status: string;
  createdAt: string;
}

// Functions added
export async function listDiscoverySessions(params?: {...});
export async function getDiscoverySession(id: string): Promise<DiscoverySessionDetail>;
export async function linkActivityToDiscoverySession(sessionId: string, type: string, activityId: string);
export async function archiveDiscoverySession(id: string);
export async function restoreDiscoverySession(id: string);
export async function saveEvaluationsToDiscoverySession(sessionId: string, evaluations: SaveEvaluationInput[]); // NEW 2026-01-31
```

### Session Detail Page: `/client/src/app/(dashboard)/discover/session/[id]/page.tsx`

**Status**: COMPLETE

Features implemented:
- Full PropertyTable component with sorting/filtering
- Selection checkboxes for evaluating specific properties
- "Quick Evaluate" button (opens evaluation with selected properties)
- "AI Chat" button (opens chat with discovery context)
- Linked activities shown in compact section at bottom
- Session metadata (location, property count, dates) in header

### Activity Linking: Chat/Eval Pages

**Status**: COMPLETE (Evaluations), PENDING (Chat)

- [x] `/client/src/app/(dashboard)/evaluate/[propertyId]/page.tsx` - Saves evaluation data to discovery session via `saveEvaluationsToDiscoverySession()` (2026-01-31)
- [ ] `/client/src/app/(dashboard)/evaluate/chat/page.tsx` - Link chat session (TODO)

---

## 4. Data Flow

### Creating Session (on search)
```
Client: POST /api/v2/discover/search
    ↓
Server: Search properties
    ↓
Server: Store properties in discovery_session_properties table  ← UPDATED
    ↓
Server: Create discovery_sessions record
    ↓
Server: Return properties + discoverySessionId
    ↓
Client: Store in state + localStorage
```

### Recalling Session
```
User clicks session card in History
    ↓
Navigate to /discover/session/{id}
    ↓
Client: GET /sessions/{id}
    ↓
Server: Fetch session, hydrate properties from discovery_session_properties  ← UPDATED
    ↓
Client: Display full session context with PropertyTable
```

---

## 5. Verification Results

### Manual E2E Test (2026-01-30)

| Test | Result | Notes |
|------|--------|-------|
| Search creates session | PASS | Session ID `f5e8fcc0-6177-40b7-a46f-c891633c36ce` created |
| Properties stored | PASS | 39 properties stored in `discovery_session_properties` |
| Session detail loads | PASS | 23KB response with full property data |
| History shows sessions | PASS | Sessions list with metadata |
| Session recall | PASS | Properties load in PropertyTable |

### Test Session Details
```
Session ID: f5e8fcc0-6177-40b7-a46f-c891633c36ce
Location: Peoria, IL
Properties: 39
User: cmkfiqzoi0000km3volasff4g
Created: 2026-01-30 13:15:49
```

---

## 6. Files Modified/Created

### New Files (COMPLETE)
- [x] `/www/internal/db/queries/schema/009_discovery.sql` - Schema with all 3 tables
- [x] `/www/internal/db/queries/sql/discovery.sql` - All queries including property CRUD
- [x] `/www/internal/api/handlers/discover/discovery_sessions.go` - Session handler
- [x] `/client/src/app/(dashboard)/discover/session/[id]/page.tsx` - Detail page

### New Files (PENDING)
- [ ] `/www/internal/jobs/discovery_cleanup.go` - Auto-archive and deletion jobs
- [ ] `/client/src/components/discover/DiscoverySessionCard.tsx` - Session card component

### Modified Files (COMPLETE)
- [x] `/www/internal/api/router.go` - Routes added
- [x] `/www/internal/api/handlers/discover/handler.go` - Session ID + properties
- [x] `/client/src/lib/api/client.ts` - Types and API functions

### Modified Files (COMPLETE)
- [x] `/client/src/app/(dashboard)/evaluate/chat/page.tsx` - Links chat session to discovery session
- [x] `/client/src/app/(dashboard)/evaluate/[propertyId]/page.tsx` - Links evaluation to discovery session

### Modified Files (PENDING)
- [ ] `/www/cmd/server/main.go` - Register cleanup job

---

## 7. Remaining Work

### High Priority
1. ~~**Activity Linking**: Update chat and eval pages to link activities to discovery session~~ DONE for evaluations (2026-01-31)
2. **Chat Linking**: Update chat page to link chat session to discovery session
3. **Background Jobs**: Implement auto-archive (30 days) and cleanup (180 days) jobs
4. ~~**Apply Migration**: Run migration for `discovery_session_evaluations` table~~ DONE (2026-01-31)

### Medium Priority
5. **Archive/Restore UI**: Add toggle for archived sessions in history tab
6. **DiscoverySessionCard**: Create reusable card component for history list

### Low Priority
7. **Automated Tests**: Add unit and integration tests
8. **Expiration Warning**: Show warning in UI when session < 30 days from deletion

---

## 8. Migrations

### Migration 2026-01-31: Discovery Session Evaluations

**Status**: APPLIED - Migration run via dbq on 2026-01-31
**Migration File**: `www/internal/db/migrations/20260131_add_discovery_session_evaluations.sql`

```sql
-- Discovery Session Evaluations - Stores evaluation results as part of the discovery session
CREATE TABLE IF NOT EXISTS discovery_session_evaluations (
    id TEXT PRIMARY KEY,
    "discoverySessionId" TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
    "propertyId" TEXT NOT NULL,

    -- Property info snapshot
    address TEXT NOT NULL,
    price INTEGER NOT NULL,
    "estimatedRent" INTEGER,

    -- Scenario results (JSONB)
    scenarios JSONB NOT NULL,

    -- Derived values
    recommendation TEXT,
    "riskLevel" TEXT,
    score INTEGER,

    -- Status
    status TEXT NOT NULL DEFAULT 'COMPLETED',

    "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE("discoverySessionId", "propertyId")
);

CREATE INDEX IF NOT EXISTS idx_session_evaluations_session ON discovery_session_evaluations("discoverySessionId");
CREATE INDEX IF NOT EXISTS idx_session_evaluations_property ON discovery_session_evaluations("propertyId");
```

**To apply**: Run the SQL above against the main database.

---

## 9. Changelog References

- `www/docs/changelogs/2026-01.md` - Discovery Session Properties Storage entry
- `estara-ai-docs/changelogs/current-www.md` - Backend changes
- `estara-ai-docs/changelogs/current-client.md` - Frontend changes
