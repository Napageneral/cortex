---
summary: "Concrete implementation plan for semantic context engine in comms"
read_when:
  - Starting semantic context engine implementation in comms
  - Deciding schema changes for documents + unified events
  - Building vector/BM25 search API
---
# Semantic Context Engine -- Implementation Plan (Comms)

This plan captures the decisions and concrete steps to implement a unified semantic
context engine inside **comms**, using the existing events table as the canonical
document store and the existing embeddings table as the universal vector store.

Related:
- `~/nexus/home/projects/nexus/worktrees/bulk-sync/docs/semantic-context-engine-spec.md`
- `~/nexus/home/projects/nexus/worktrees/bulk-sync/docs/skills-discovery-spec.md`
- `docs/MEMORY_SYSTEM_SPEC.md`

---

## Decisions (Locked In)

1. **Unified events table**  
   Use `events` as the single source of truth for *all* document-like objects:
   skills, docs, memory, checkpoints, tools, and comms events.

2. **Append-only events**  
   Documents are versioned by inserting new events (immutability preserved).

3. **Stable document keys via document_heads**  
   Add a `document_heads` table to map a stable `doc_key` to the current event id,
   and to track retrieval stats.

4. **Embeddings as universal store**  
   Reuse the existing `embeddings` table with:
   - `entity_type = "document"`
   - `entity_id = doc_key`

5. **Hybrid search**  
   Implement vector search + BM25 (qmd) with a unified API, plus lexical fallback.

6. **Embedding-first taxonomy**  
   Use embeddings for label drift; defer canonical label normalization to a later
   clustering job.

---

## Schema Changes

### 1) document_heads (new)

```sql
CREATE TABLE IF NOT EXISTS document_heads (
    doc_key TEXT PRIMARY KEY,           -- stable id (ex: "skill:gog")
    channel TEXT NOT NULL,              -- "skill", "doc", "memory", "checkpoint", "tool"
    current_event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    title TEXT,
    description TEXT,
    metadata_json TEXT,
    updated_at INTEGER NOT NULL,
    retrieval_count INTEGER NOT NULL DEFAULT 0,
    last_retrieved_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_document_heads_channel ON document_heads(channel);
CREATE INDEX IF NOT EXISTS idx_document_heads_event ON document_heads(current_event_id);
```

### 2) retrieval_log (optional, new)

```sql
CREATE TABLE IF NOT EXISTS retrieval_log (
    id TEXT PRIMARY KEY,
    doc_key TEXT NOT NULL REFERENCES document_heads(doc_key) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    query_text TEXT,
    score REAL,
    retrieved_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_retrieval_log_doc ON retrieval_log(doc_key);
CREATE INDEX IF NOT EXISTS idx_retrieval_log_ts ON retrieval_log(retrieved_at);
```

### 3) Events table usage (no schema change)

We keep the existing `events` table unchanged and expand semantics:
- `channel`: add values like `skill`, `doc`, `memory`, `checkpoint`, `tool`
- `direction`: allow `created`, `updated`, `deleted` for document events
- `content_types`: add `["document"]` / `["skill"]` / `["memory"]` etc.

**Uniqueness constraint** on `(source_adapter, source_id)` remains.  
To allow multiple versions of a document, use:

```
source_adapter = "documents"
source_id = "<doc_key>@<content_hash>"
```

The stable identity is **doc_key**, stored in `document_heads`.

---

## Document Model

```ts
type DocumentInput = {
  docKey: string;              // "skill:gog"
  channel: string;             // "skill" | "doc" | "memory" | ...
  title?: string;
  description?: string;
  content: string;             // full markdown or serialized content
  metadata?: map[string]any;
  sourceAdapter?: string;      // default "documents"
  timestamp?: int64;           // unix seconds
};
```

On upsert:
1. Hash content → `content_hash`
2. Check `document_heads` for same hash → skip if unchanged
3. Insert a new **event**
4. Update `document_heads` to point at new event
5. Enqueue embedding job for `entity_type=document`, `entity_id=doc_key`

---

## Implementation Plan (Comms)

### Phase 1 -- Schema + Document Upsert

**Files**
- `internal/db/schema.sql` (add `document_heads`, `retrieval_log`)
- `internal/db/db.go` (ensure schema changes included)
- `internal/documents/` (new package)

**New package**
`internal/documents`
- `upsert.go`: `UpsertDocument(ctx, db, DocumentInput) (DocumentResult, error)`
- `hash.go`: content hashing utility (SHA-256)
- `load.go`: optional readers for skills/docs/memory

**Notes**
- Use transactions for upsert + event insert
- Use `uuid.NewString()` for event ids

### Phase 2 -- Embeddings for documents

**Files**
- `internal/compute/engine.go`
- `internal/compute/embeddings_batcher.go`

**Changes**
- Add `document` as a supported embedding entity type
- Add `buildDocumentText(docKey)` helper (read from events or document_heads)
- Ensure `source_text_hash` stored for re-embed checks

**Behavior**
- If document_heads.content_hash changes, enqueue embedding job
- Use existing batch embedder

### Phase 3 -- Hybrid Search API

**Files**
- `internal/search/` (new package)
  - `search.go`: `Search(ctx, SearchRequest) (SearchResult, error)`
  - `scoring.go`: hybrid scoring logic
  - `fallback.go`: lexical fallback if qmd/embeddings absent

**SearchRequest**
```ts
type SearchRequest struct {
  Query string
  ChannelFilter []string
  Limit int
  MinScore float64
}
```

**SearchResult**
```ts
type SearchResult struct {
  DocKey string
  EventID string
  Score float64
  ScoreBreakdown map[string]float64
  Snippet string
  Title string
  Description string
  Channel string
}
```

### Phase 4 -- BM25/QMD Integration

**Approach**
- Invoke qmd CLI as a backend for lexical + vector search
- Store index under:
  - `~/Library/Application Support/Comms/qmd/` (macOS)
  - `~/.local/share/comms/qmd/` (linux)

**Integration Points**
- `internal/search/qmd.go`: wrapper for qmd indexing + search
- `internal/documents/index.go`: push doc content into qmd index

**Fallback**
- If qmd missing, use:
  - Lexical search over events.content (LIKE)
  - Vector-only search from embeddings table (Go cosine)

### Phase 5 -- Retrieval Metrics

**Updates**
- Increment `document_heads.retrieval_count`
- Update `last_retrieved_at`
- Optional insert into `retrieval_log`

### Phase 6 -- CLI + JSON output

**Command**
`comms search`

Flags:
- `--channel skill,doc,memory`
- `--limit N`
- `--min-score 0.4`
- `--json`

---

## Mapping to Future Semantic Context Engine

This comms search API becomes the backend for the broker hook:
```
before_agent_start -> comms search -> returns doc matches
```

Engine-level logic (budgeting, injection formatting) stays in Nexus; comms provides
the **semantic retrieval substrate**.

---

## Tests

### Unit Tests
- `internal/documents/upsert_test.go`
- `internal/search/scoring_test.go`
- `internal/compute/embedding_blob_test.go` (add blob deserialize)

### Integration Tests
- Seed 5 documents (skills + docs)
- Embed (stubbed vectors)
- Run search and assert top hit order

### Golden Cases
- "send email" => skill:gog
- "post to slack" => skill:slack
- "semantic routing spec" => doc:semantic-agent-routing

---

## Open Questions (Track)

1. Should we add a partial unique index to enforce comms event uniqueness without
   constraining document versioning?
2. Do we want `document_heads` to carry denormalized `content` for faster fetch?
3. Should qmd store both vector + lexical in one index, or split?

