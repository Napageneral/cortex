---
summary: "Detailed implementation spec for Cortex transformation"
read_when:
  - Implementing any Cortex work items
  - Understanding decisions and priorities
  - Picking up work on segments, search, or AIX integration
---
# Cortex Implementation Spec

This document captures the concrete implementation plan for transforming comms into Cortex. It incorporates decisions from design discussions and defines the work to be done.

---

## Decisions (Locked In)

| Decision | Rationale |
|----------|-----------|
| **Keep `analyses` terminology** | Indicative of LLM doing real intelligence work. No rename to "extractions." |
| **Drop checkpoint tables** | No migration needed. Can reload data easily. |
| **Hybrid search across ALL events** | Skills stored as generic events, search should work across all event types |
| **Single-event segments for metadata** | AIX metadata → single-event segment → facets on ingestion |
| **Defer routing decision logic** | Build the search layer first, tune routing thresholds later |
| **Defer freshness scoring** | Nice to have, not blocking |
| **Defer memory synthesis** | Needs more design |

---

## Phase 1: Foundation

### 1.1 Rename comms → cortex

**Scope:**
- Go module name: `github.com/...` → update if needed
- CLI binary: `comms` → `cortex`
- Package imports throughout codebase
- Documentation references
- Config paths (if any reference "comms")

**Files to update:**
- `go.mod` (module name)
- `cmd/comms/main.go` → `cmd/cortex/main.go`
- All `import` statements
- `Makefile` / build scripts
- README, docs

**Note:** This is a big rename. Consider doing it as a dedicated PR.

### 1.2 Rename conversations → segments

**Schema changes:**
```sql
-- Rename tables
ALTER TABLE conversations RENAME TO segments;
ALTER TABLE conversation_definitions RENAME TO segment_definitions;
ALTER TABLE conversation_events RENAME TO segment_events;

-- Update foreign key references in other tables
-- (May need to recreate tables in SQLite since ALTER TABLE is limited)
```

**Code changes:**
- `internal/chunk/` — rename types, functions
- `internal/db/` — update schema, queries
- All references to "conversation" in code, types, comments

**Files to update:**
- `internal/db/schema.sql`
- `internal/chunk/*.go`
- `internal/compute/engine.go` (references conversations)
- `internal/adapters/*.go` (create conversations)
- CLI commands that reference conversations

### 1.3 Kill checkpoint tables

**Drop these tables:**
- `checkpoints`
- `checkpoint_quality`
- `turn_facets`

**Remove this package:**
- `internal/checkpoints/`

**Update schema:**
```sql
DROP TABLE IF EXISTS turn_facets;
DROP TABLE IF EXISTS checkpoint_quality;
DROP TABLE IF EXISTS checkpoints;
```

**Remove references in:**
- `internal/db/schema.sql`
- Any code that imports `internal/checkpoints`
- CLI commands that use checkpoints

---

## Phase 2: Live Sync

### 2.1 AIX file watcher

**Goal:** Automatically trigger cortex sync when aix.db changes.

**Implementation:**
- Use `fsnotify` package (already common in Go)
- Watch `~/Library/Application Support/aix/aix.db` (macOS)
- On write event, debounce (wait 500ms for writes to settle)
- Trigger sync pipeline

**New file:** `internal/sync/watcher.go`

```go
type AIXWatcher struct {
    aixDBPath string
    onChange  func()
    debounce  time.Duration
}

func (w *AIXWatcher) Start(ctx context.Context) error
func (w *AIXWatcher) Stop()
```

**CLI integration:**
```bash
cortex watch          # Start watcher daemon
cortex watch --once   # Sync once and exit
```

### 2.2 Auto-sync pipeline

**Flow:**
```
AIX db change detected
    ↓
Debounce (500ms)
    ↓
Run aix adapter sync
    ↓
Create single-event segments for new events
    ↓
Run metadata extraction → facets
    ↓
Enqueue embeddings for new segments
```

**Integration points:**
- `internal/adapters/aix.go` — already handles sync
- `internal/chunk/` — create segments
- `internal/compute/` — run analyses, create facets

---

## Phase 3: Skills as Events + Hybrid Search

### 3.1 Nexus skills adapter

**Goal:** Sync skills from Nexus as events in cortex.

**New file:** `internal/adapters/skills.go`

**Flow:**
1. Shell out to `nexus skill list --json`
2. For each skill:
   - Read SKILL.md content
   - Create event with `channel='skill'`, `source_adapter='nexus'`
   - Upsert to `document_heads` with `doc_key='skill:{name}'`
3. Enqueue embeddings

**CLI:**
```bash
cortex sync --source skills    # Sync skills from nexus
cortex sync --all              # Includes skills
```

### 3.2 FTS5 index

**Goal:** Proper BM25 lexical search.

**Schema addition:**
```sql
CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
    id,
    content,
    channel,
    content='events',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

-- Triggers to keep FTS in sync
CREATE TRIGGER events_ai AFTER INSERT ON events BEGIN
    INSERT INTO events_fts(rowid, id, content, channel)
    VALUES (new.rowid, new.id, new.content, new.channel);
END;

CREATE TRIGGER events_ad AFTER DELETE ON events BEGIN
    INSERT INTO events_fts(events_fts, rowid, id, content, channel)
    VALUES('delete', old.rowid, old.id, old.content, old.channel);
END;
```

**Backfill existing events:**
```sql
INSERT INTO events_fts(rowid, id, content, channel)
SELECT rowid, id, content, channel FROM events;
```

### 3.3 Hybrid search API

**Goal:** Unified search across ALL events, not just skills.

**Existing:** `internal/search/search.go` has `SearchDocuments()`

**Expand to:**
```go
type SearchRequest struct {
    Query         string
    Channels      []string   // Filter by channel (empty = all)
    EntityTypes   []string   // "event", "segment", "document"
    Limit         int
    MinScore      float64
    UseEmbeddings bool
    UseLexical    bool       // Uses FTS5 BM25
}

type SearchResult struct {
    EntityType     string     // "event", "segment", "document"
    EntityID       string
    Channel        string
    Score          float64
    ScoreBreakdown map[string]float64
    Snippet        string
    Metadata       map[string]any
}

func Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
```

**Scoring:**
```
final_score = α * bm25_score + β * vector_score
```

Default weights: α=0.4, β=0.6 (configurable)

**CLI:**
```bash
cortex search "send email" --limit 10 --channel skill
cortex search "git worktree" --channel cursor --json
cortex search "what happened yesterday" --channel imessage,gmail
```

---

## Phase 4: AIX Metadata + Turn Handling

### 4.1 Single-event segments for AIX metadata

**Goal:** Every AIX message event gets a single-event segment, enabling per-message facets.

**New segment definition:**
```sql
INSERT INTO segment_definitions (
    id, name, strategy, channel, description
) VALUES (
    'aix-single-event',
    'AIX Single Event',
    'single_event',
    'cursor',  -- or NULL for all AIX channels
    'One segment per AIX message for metadata extraction'
);
```

**Implementation:**
- After AIX sync creates events
- Create single-event segments for each new event
- Map via `segment_events` table

### 4.2 AIX metadata → facets

**Goal:** Extract structured facets from AIX metadata on ingestion.

**Analysis types to create:**

| Analysis Type | Input | Output Facets |
|---------------|-------|---------------|
| `cursor_capabilities_v1` | Single-event segment | `capability_used` facets |
| `cursor_lints_v1` | Single-event segment | `lint_error` facets |
| `cursor_files_v1` | Single-event segment | `file_referenced` facets |
| `cursor_codeblocks_v1` | Single-event segment | `code_suggestion` facets |

**Note:** These can be simple "copy" analyses — just reading AIX metadata JSON and creating facets, no LLM needed.

**Implementation:**
- New analysis types in `internal/compute/`
- Register via `cortex compute seed`
- Auto-run on single-event segment creation

### 4.3 Turn-pair segments

**Goal:** Group user message + assistant response for quality analysis.

**New segment definition:**
```sql
INSERT INTO segment_definitions (
    id, name, strategy, channel, description
) VALUES (
    'aix-turn-pair',
    'AIX Turn Pair',
    'turn_pair',
    'cursor',
    'User message paired with following assistant response(s)'
);
```

**Turn boundary detection — Edge cases to handle:**

| Case | How to Handle |
|------|---------------|
| **Normal turn** | user msg → assistant msg | Pair them |
| **Multi-part assistant** | user → assistant → assistant (continuation) | Include all assistant msgs until next user |
| **Tool-only response** | user → tool calls only, no text | Still a turn, include tool results |
| **Interrupted turn** | user → partial assistant (error/cancel) | Mark as incomplete, still create segment |
| **System messages** | system prompts, context injection | Skip, don't create segments for these |
| **Empty assistant** | user → empty response | Create segment, mark as empty |

**Detection algorithm:**
```
For each thread:
    Sort messages by timestamp/sequence
    current_turn = None
    
    For each message:
        If message.role == 'user':
            If current_turn:
                Emit current_turn as segment
            current_turn = new Turn(user_msg=message)
        
        Elif message.role == 'assistant':
            If current_turn and current_turn.has_user:
                current_turn.add_assistant(message)
            Else:
                # Orphan assistant message (edge case)
                Log warning, skip or create solo segment
        
        Elif message.role == 'tool':
            If current_turn:
                current_turn.add_tool_result(message)
    
    # Don't forget last turn
    If current_turn:
        Emit current_turn as segment
```

**Testing requirements:**
- [ ] Normal user → assistant pairs
- [ ] Multi-part assistant responses
- [ ] Tool-only responses
- [ ] Interrupted/incomplete turns
- [ ] System message filtering
- [ ] Empty responses
- [ ] Thread with only user messages (no response yet)

### 4.4 Turn quality analysis

**Goal:** Extract quality signals from turn-pair segments.

**Analysis type:** `turn_quality_v1`

**Input:** Turn-pair segment (user msg + assistant response(s))

**Output facets:**
- `turn_sentiment`: positive/neutral/negative
- `turn_correction`: boolean (user corrected AI)
- `turn_frustration`: boolean (user expressed frustration)
- `turn_praise`: boolean (user praised AI)
- `turn_acceptance`: boolean (user accepted result)

**Prompt:** Already exists as `checkpoint-feedback-v1.prompt.md` — rename/adapt.

---

## Phase 5: Temporal Query

### 5.1 Daily cross-channel segments

**Goal:** Create segments spanning all events for a calendar day.

**Segment definition:**
```sql
INSERT INTO segment_definitions (
    id, name, strategy, channel, time_window_seconds, description
) VALUES (
    'daily-all-channels',
    'Daily All Channels',
    'time_window',
    NULL,  -- all channels
    86400, -- 24 hours
    'All events across all channels for one calendar day'
);
```

**Implementation:**
- Chunker creates one segment per day
- Segment boundaries at midnight (user's timezone)
- Include events from all channels

### 5.2 Temporal query API

**Goal:** Query events/segments by time window.

**API:**
```go
type TemporalQuery struct {
    Start     time.Time
    End       time.Time
    Channels  []string   // Filter (empty = all)
    GroupBy   string     // "day", "hour", "segment"
    Limit     int
}

type TemporalResult struct {
    Period     string     // "2026-01-20" or segment ID
    EventCount int
    Channels   []string   // Channels represented
    Summary    string     // If segment has been analyzed
    Events     []Event    // If requested
}

func QueryTemporal(ctx context.Context, q TemporalQuery) ([]TemporalResult, error)
```

**CLI:**
```bash
cortex timeline --since yesterday
cortex timeline --date 2026-01-15 --channel cursor
cortex timeline --start 2026-01-01 --end 2026-01-20 --group-by day
```

---

## Deferred (Needs More Design)

### Segment search for routing
- How to efficiently search across potentially thousands of segments?
- What signals beyond embedding similarity?
- How to handle segment recency vs relevance tradeoff?

### Freshness scoring
- What file state do we track? Content hash? Mtime?
- How to efficiently compute drift for candidate segments?
- What threshold makes a segment "too stale"?

### Routing decision logic
- What threshold for routing to existing vs creating fresh?
- How to handle ambiguous cases (multiple good matches)?
- Should routing be deterministic or have randomness?

### Memory synthesis
- Sequential processing requirement
- What goes into synthesis input (prior memory + new facets)?
- How to handle conflicts/contradictions?
- Storage format (MEMORY.md vs database)?

---

## Implementation Order

```
Week 1: Foundation
├── 1.1 Rename comms → cortex
├── 1.2 Rename conversations → segments
└── 1.3 Kill checkpoint tables

Week 2: Live Sync + Skills
├── 2.1 AIX file watcher
├── 2.2 Auto-sync pipeline
├── 3.1 Nexus skills adapter
└── 3.2 FTS5 index

Week 3: Search + Metadata
├── 3.3 Hybrid search API (expanded)
├── 4.1 Single-event segments
├── 4.2 AIX metadata → facets
└── 4.3 Turn-pair segments (with edge case testing)

Week 4: Quality + Temporal
├── 4.4 Turn quality analysis
├── 5.1 Daily cross-channel segments
└── 5.2 Temporal query API

Future: Routing + Memory
├── Segment search optimization
├── Freshness scoring
├── Routing decision logic
└── Memory synthesis
```

---

## Testing Requirements

### Foundation
- [ ] All imports resolve after rename
- [ ] CLI `cortex` works
- [ ] Schema migration succeeds
- [ ] Existing data preserved after rename

### Live Sync
- [ ] Watcher detects aix.db changes
- [ ] Debounce prevents rapid re-syncs
- [ ] Sync completes without errors
- [ ] New events appear in cortex

### Search
- [ ] FTS5 returns BM25-ranked results
- [ ] Vector search returns similarity-ranked results
- [ ] Hybrid combines both appropriately
- [ ] Search works across all channels
- [ ] Search filters by channel correctly

### Turn Handling
- [ ] Normal turns detected correctly
- [ ] Multi-part assistant responses grouped
- [ ] Tool-only turns handled
- [ ] Incomplete turns marked appropriately
- [ ] Edge cases don't crash

### Temporal
- [ ] Daily segments created at correct boundaries
- [ ] Temporal query returns correct date ranges
- [ ] Cross-channel aggregation works

---

## File Reference

### New files to create
- `cmd/cortex/main.go` (rename from comms)
- `internal/sync/watcher.go`
- `internal/adapters/skills.go`
- `internal/chunk/turn_pair.go`
- `internal/search/temporal.go`

### Files to significantly modify
- `internal/db/schema.sql`
- `internal/chunk/*.go` (rename conversations)
- `internal/search/search.go` (expand API)
- `internal/compute/engine.go` (new analysis types)
- `internal/adapters/aix.go` (single-event segments)

### Files to delete
- `internal/checkpoints/` (entire directory)
- Checkpoint-related prompts (or repurpose)

---

*This spec is the source of truth for Cortex implementation. Update it as decisions change.*
