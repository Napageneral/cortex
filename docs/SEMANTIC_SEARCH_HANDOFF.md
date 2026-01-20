---
summary: "Complete handoff doc for semantic search/context engine work"
read_when:
  - Picking up semantic search work in comms
  - Understanding the document_heads / skills indexing system
  - Implementing FTS5 or hybrid search improvements
---
# Semantic Search — Handoff Document

This document consolidates the semantic context engine work across multiple sessions into a single reference for continuing development.

---

## 1. Origin Story

### Initial Motivation

The work started from analyzing [pi-mcp-adapter](https://github.com/nicobailon/pi-mcp-adapter), which solves a token-efficiency problem for MCP tools:

**Problem:** MCP tool definitions are verbose. A single server can burn 10k+ tokens. Connect a few servers and you've burned half your context window before the conversation starts.

**Solution:** One proxy tool (~200 tokens) instead of injecting all tool schemas. The agent discovers tools on-demand via search/describe/call modes.

### Application to Nexus Skills

Nexus has the same problem with skills. Currently:
- Skills are Markdown documents (`SKILL.md`) describing capabilities
- They're eagerly loaded into the Pi agent's system prompt as compact XML
- 250+ skills now, potentially 1k soon
- Formula: `total_chars = 195 + Σ(97 + len(name) + len(description) + len(location))`
- At 1k skills ≈ 68k tokens just for the skill index

**Goal:** Lazy, semantic discovery instead of eager injection.

### Evolution to Generic Engine

The pattern generalizes beyond skills:

| Context Source | What it is |
|----------------|------------|
| Skills | Capability documentation |
| Documents | Specs, notes, project docs |
| Memory | Extracted facets from conversations |
| Checkpoints | Past context windows for routing |

This led to designing a "Semantic Context Engine" — but with a key insight from the Unified Semantic Layer doc: **these are three different retrieval patterns, not one unified document store**.

---

## 2. The Unified Semantic Layer Vision

See: `docs/UNIFIED_SEMANTIC_LAYER.md`

### Three Retrieval Types

| Type | Question | Storage | Use Case |
|------|----------|---------|----------|
| **Semantic** | "What's relevant to X?" | `document_heads` + `embeddings` | Inject skills/docs into context |
| **Temporal** | "What happened recently?" | `events` + `facets` | Narrative continuity |
| **Checkpoint** | "Where can I fork from?" | `checkpoints` table | Routing decisions (NOT injection) |

**Key insight:** Checkpoints aren't documents to inject — they're used for routing decisions. A checkpoint IS a preloaded context window. Forking from one means you don't rebuild context from scratch.

### Three Data Layers

| Layer | Contents | How Created |
|-------|----------|-------------|
| **Raw** | Events from all sources | Adapters sync (aix, gmail, imessage, etc.) |
| **Extracted** | Structured facets | Analysis prompts on conversation chunks |
| **Synthesized** | Compressed insights | (DEFERRED) Sequential synthesis over time |

**Focus:** Raw → Extracted. Synthesis is deferred.

---

## 3. What Was Built

### Schema Changes (`internal/db/schema.sql`)

```sql
-- Document heads: stable pointers for document-style events
CREATE TABLE IF NOT EXISTS document_heads (
    doc_key TEXT PRIMARY KEY,           -- stable id (ex: "skill:gog")
    channel TEXT NOT NULL,              -- "skill" for now
    current_event_id TEXT NOT NULL REFERENCES events(id),
    content_hash TEXT NOT NULL,
    title TEXT,
    description TEXT,
    metadata_json TEXT,
    updated_at INTEGER NOT NULL,
    retrieval_count INTEGER NOT NULL DEFAULT 0,
    last_retrieved_at INTEGER
);

-- Retrieval log: per-query document retrieval tracking
CREATE TABLE IF NOT EXISTS retrieval_log (
    id TEXT PRIMARY KEY,
    doc_key TEXT NOT NULL REFERENCES document_heads(doc_key),
    event_id TEXT NOT NULL REFERENCES events(id),
    query_text TEXT,
    score REAL,
    retrieved_at INTEGER NOT NULL
);
```

### Documents Package (`internal/documents/`)

**`types.go`:**
```go
type DocumentInput struct {
    DocKey        string
    Channel       string
    Title         string
    Description   string
    Content       string
    Metadata      map[string]any
    SourceAdapter string
    Timestamp     int64
}

type DocumentResult struct {
    DocKey          string
    EventID         string
    ContentHash     string
    Created         bool
    Updated         bool
    Skipped         bool
    PreviousEventID string
    Reason          string
}
```

**`upsert.go`:**
- `UpsertDocument(ctx, db, input) (DocumentResult, error)`
- Hashes content (SHA-256) to detect changes
- Inserts new event if content changed
- Updates `document_heads` pointer
- Uses `source_id = "<doc_key>@<content_hash>"` for versioning

### Search Package (`internal/search/`)

**`types.go`:**
```go
type DocumentSearchRequest struct {
    Query         string
    Channels      []string
    Limit         int
    MinScore      float64
    UseEmbeddings bool
    UseLexical    bool
    Model         string
    TrackRetrieval bool
}

type DocumentSearchResult struct {
    DocKey         string
    EventID        string
    Channel        string
    Title          string
    Description    string
    Snippet        string
    Score          float64
    ScoreBreakdown map[string]float64
}
```

**`search.go`:**
- `SearchDocuments(ctx, req) (DocumentSearchResponse, error)`
- Hybrid scoring: vector similarity + lexical term matching
- Default weights: 0.6 vector + 0.4 lexical
- Optional retrieval tracking (updates `document_heads.retrieval_count`)

**`gemini_embedder.go`:**
- Implements `Embedder` interface
- Calls Gemini API for query embeddings

### Compute Engine Updates (`internal/compute/engine.go`)

- Added `"document"` as supported embedding entity type
- `buildDocumentText(docKey)` fetches content for embedding
- `EnqueueDocumentEmbeddings()` queues embedding jobs for documents
- Stores `source_text_hash` for re-embed detection

### CLI (`cmd/comms/main.go`)

```bash
# Enqueue document embeddings
comms compute enqueue document-embeddings

# Search documents
comms documents search "send email" \
  --channel skill \
  --limit 5 \
  --embeddings on \
  --lexical on \
  --track \
  --json
```

---

## 4. Current State & Decisions

### Channel Simplification

Original plan had: `skill | doc | memory | checkpoint | tool`

**Current decision:** Only `skill` for now.

| Channel | Status | Reasoning |
|---------|--------|-----------|
| `skill` | ✅ Build now | Clear adapter (Nexus CLI), immediate value |
| `doc` | ❌ Defer | No doc adapter yet |
| `memory` | ❌ Defer | Memory = facets with time windows, not documents |
| `checkpoint` | ❌ Defer | Already has own table, used for routing not injection |
| `tool` | ❌ Defer | Skills covers this for now |

### Nexus as Skills Adapter

Nexus CLI should be the source of truth for skills:
- Knows where all skills live (workspace, system, hub)
- Has manifest and precedence logic
- Can query skills hub for remote skills

**Needed:** `comms ingest skills` command that:
1. Calls `nexus skill list --json`
2. Reads each SKILL.md content
3. Upserts into `document_heads` with `channel='skill'`
4. Enqueues embedding job
5. Updates FTS5 index

### BM25 via SQLite FTS5

Current lexical search is naive (term matching). Need proper BM25.

**Plan:** SQLite FTS5 (no qmd dependency)

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS document_fts USING fts5(
    doc_key,
    title,
    description,
    content,
    content='',  -- external content mode
    tokenize='porter unicode61'
);
```

### Hybrid Scoring (Steal qmd's Approach)

qmd does:
- `qmd search` = BM25 lexical
- `qmd vsearch` = vector only
- `qmd query` = hybrid with reranking

**Target formula:**
```
final_score = α * bm25_score + β * vector_score + γ * freshness_bonus
```

Components:
1. FTS5 `bm25()` function for lexical score
2. Cosine similarity for vector score
3. Optional freshness bonus based on `updated_at`
4. Optional LLM reranking for top-N candidates

---

## 5. aix Metadata Extraction

### Current State

aix adapter (`internal/adapters/aix.go`) stores:
- Each user message → `events` row (direction=received)
- Each agent response → `events` row (direction=sent)
- Session grouping → `threads` table

### Checkpoints Indexer (`internal/checkpoints/indexer.go`)

Already exists and extracts:
```go
type ContextOutput struct {
    Title        string
    Description  string
    Keywords     []string
    Entities     []string
    Topics       []string
    ToolsInvoked []ToolCall
    FilesTouched []FileTouch
}

type FeedbackOutput struct {
    Feedback []struct {
        Sentiment   string
        Correction  bool
        Frustration bool
        Praise      bool
        // ...
    }
}
```

Stores in:
- `checkpoints` table (semantic metadata)
- `turn_facets` table (per-message feedback)
- `checkpoint_quality` table (rollup scores)
- `embeddings` table (context embeddings)

### Open Question

The user mentioned: "idk if we will even have a checkpoints table either!"

**Need to clarify:** Is the checkpoints system being kept? If so, it's already extracting aix metadata well. If not, that extraction needs to move somewhere.

---

## 6. Implementation Roadmap

### Phase 1: FTS5 Index (Next)

1. Add FTS5 virtual table to schema
2. Create triggers or manual sync to populate FTS from `document_heads`
3. Update `SearchDocuments` to use `bm25()` instead of naive lexical

### Phase 2: Nexus Skills Adapter (Next)

1. New command: `comms ingest skills`
2. Shell out to `nexus skill list --json`
3. Read SKILL.md contents
4. Upsert each skill
5. Enqueue embeddings
6. Update FTS5 index

### Phase 3: Enhanced Hybrid Scoring

1. Implement proper score combination: `α*bm25 + β*vector + γ*freshness`
2. Make weights configurable
3. Add optional LLM reranking for top-N

### Phase 4: Broker Integration (Future)

1. Wire to Nexus broker's `before_agent_start` hook
2. Query comms search API
3. Inject matched skills into agent context

---

## 7. File Reference

### Core Implementation

| File | Purpose |
|------|---------|
| `internal/db/schema.sql` | Schema with `document_heads`, `retrieval_log` |
| `internal/documents/types.go` | `DocumentInput`, `DocumentResult` |
| `internal/documents/upsert.go` | `UpsertDocument()` |
| `internal/search/types.go` | `DocumentSearchRequest`, `DocumentSearchResult` |
| `internal/search/search.go` | `SearchDocuments()` hybrid search |
| `internal/search/gemini_embedder.go` | Query embedding via Gemini |
| `internal/compute/engine.go` | Document embedding jobs |
| `cmd/comms/main.go` | CLI commands |

### Tests

| File | Coverage |
|------|----------|
| `internal/documents/upsert_test.go` | Create, skip, update |
| `internal/search/search_test.go` | Lexical search, vector search |
| `internal/testutil/testdb.go` | In-memory test DB helper |

### Specs

| File | Purpose |
|------|---------|
| `docs/UNIFIED_SEMANTIC_LAYER.md` | Vision doc — three retrieval types |
| `docs/SEMANTIC_CONTEXT_ENGINE_IMPLEMENTATION_PLAN.md` | Original implementation plan |
| `docs/SEMANTIC_SEARCH_HANDOFF.md` | This document |

---

## 8. Open Questions

1. **Checkpoints table fate** — Keep or remove? If removed, where does aix metadata extraction live?

2. **FTS5 sync strategy** — Triggers vs manual sync after upsert?

3. **Reranking approach** — Pure algorithmic (weighted sum) vs LLM rerank for top-N?

4. **Freshness scoring** — What signals? `updated_at`? `retrieval_count`? File state hashes?

5. **Nexus CLI interface** — Does `nexus skill list --json` provide enough info, or need a dedicated export command?

---

## 9. Commands Cheatsheet

```bash
# Initialize comms DB (applies schema)
comms init

# Enqueue document embeddings
comms compute enqueue document-embeddings

# Run compute workers
comms compute run --workers 4

# Search documents
comms documents search "send email" --channel skill --limit 5 --json

# With full options
comms documents search "git worktree" \
  --channel skill \
  --limit 10 \
  --min-score 0.3 \
  --embeddings on \
  --lexical on \
  --track \
  --json
```

---

## 10. Summary

**What we have:**
- Document upsert with content hashing and versioning
- Hybrid search (vector + naive lexical)
- Embedding pipeline for documents
- CLI for testing

**What's next:**
- FTS5 for proper BM25
- Nexus skills adapter
- Enhanced scoring (α*bm25 + β*vector + γ*freshness)
- Broker integration

**Key principle:** Focus on skills first. Other retrieval types (temporal, checkpoint) use their natural patterns, not `document_heads`.
