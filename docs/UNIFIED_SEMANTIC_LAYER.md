---
summary: "Unified vision for Cortex: the semantic intelligence layer"
read_when:
  - Starting any work on comms/cortex
  - Understanding how semantic context, memory, and routing fit together
  - Working on the broker/workspace interface
---
# Cortex — Unified Semantic Layer

**Cortex** (formerly comms) is the workspace's intelligence layer. It unifies all data sources, extracts structured intelligence, and provides semantic access for context injection and agent routing.

This document is the single source of truth for the architecture. It supersedes prior specs and incorporates decisions from the ARCHITECTURE_EVOLUTION, SEMANTIC_SEARCH_HANDOFF, and MEMORY_AND_SEMANTIC_HANDOFF documents.

---

## Core Insight: One Pipeline

Everything flows through the same pipeline:

```
Adapters → Events → Segments → Analyses → Facets → Semantic Interface
```

No parallel structures. Checkpoints, quality signals, memory — all become analyses and facets on segments. This is the key architectural decision.

---

## Part 1: The Stack

### Broker + Workspace Fusion

The traditional AI stack separates workspace (data) from broker (routing). But intelligent routing requires the workspace to be structured for semantic access. They're co-designed.

**Cortex is the glue.** It lives in the workspace but serves the broker:

```
┌─────────────────────────────────────────────────────────┐
│                     WORKSPACE                           │
│                                                         │
│   Events ──► Segments ──► Analyses ──► Facets          │
│                                                         │
│                    Semantic Interface                   │
│              (search, retrieve, route)                  │
└─────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────┐
│                      BROKER                             │
│                                                         │
│   Context Assembly ←── queries semantic interface       │
│   Agent Routing    ←── queries segment index            │
│   Session Mgmt     ←── reads/writes to workspace        │
└─────────────────────────────────────────────────────────┘
```

The workspace exposes a **semantic interface**. Brokers query it. Different brokers can exist, but they all expect Cortex.

---

## Part 2: Data Model

### Events (Raw Layer)

The `events` table is the single source of record. Immutable, append-only.

Sources:
- iMessage, Gmail, Calendar (via adapters)
- AI sessions — Cursor, Claude Code, Codex (via AIX)
- Documents, skills (via `document_heads` pointer table)
- Future: Slack, Discord, etc.

### Segments (Grouping Layer)

> A **segment** is a contiguous slice of events that belong together.

The `segments` table (renamed from `conversations`) groups events for analysis:

| Strategy | Use Case | Example |
|----------|----------|---------|
| `time_gap` | iMessage conversations | 4-hour gap splits |
| `thread` | Email threads | Gmail thread_id |
| `session` | Full AI sessions | One Cursor session |
| `turn_pair` | User + assistant response | Single interaction |
| `daily` | Cross-channel daily | All events for one day |

Defined by `segment_definitions`, instantiated in `segments`, mapped via `segment_events`.

### Analyses (Extraction Layer)

The `analysis_runs` table tracks extraction:

```sql
-- Now supports both segment-level AND event-level extraction
analysis_runs
├── analysis_type_id  -- which extraction prompt
├── segment_id        -- for segment-level (nullable)
├── event_id          -- for event-level (nullable, NEW)
├── status            -- pending/running/completed/failed
└── output            -- JSON results
```

**Key change:** Adding `event_id` enables event-level extraction for rich AIX metadata.

### Facets (Extracted Data)

Structured, queryable values extracted from events/segments:

| Facet Type | Source | Example |
|------------|--------|---------|
| `person_mention` | Any conversation | "Casey mentioned" |
| `self_preference` | AI sessions | "prefers bullet points" |
| `self_correction` | AI sessions | "don't use ORMs" |
| `workspace_convention` | AI sessions | "use trash not rm" |
| `files_referenced` | AIX metadata | "/src/main.ts:10-50" |
| `capabilities_used` | AIX metadata | "Composer, Agent" |
| `lint_errors` | AIX metadata | "TS2322 in auth.ts" |

### Embeddings (Vector Layer)

The `embeddings` table is the universal vector store:

```sql
embeddings
├── entity_type    -- "segment", "document", "event"
├── entity_id      -- reference to source
├── embedding      -- vector blob
└── source_text_hash  -- for re-embed detection
```

---

## Part 3: Multi-Level Extraction

Different granularities need different extraction:

```
Level 0: Event (single message)
         └─ Per-event metadata (AIX: capabilities, files, lints, codeblocks)

Level 1: Turn (user msg + assistant response)
         └─ Turn-level analysis (quality signals, corrections, success/failure)

Level 2: Session/Thread
         └─ Session-level analysis (trajectory, accomplishments)

Level 3: Cross-session
         └─ Project/temporal patterns (memory synthesis - DEFERRED)
```

For iMessage, Level 0 is sparse (just text). For AI sessions, Level 0 is **dense** with structured metadata.

### AIX Metadata → Facets

All AIX metadata syncs and becomes facets:

| AIX Table | Analysis Type | Facets |
|-----------|--------------|--------|
| `message_capabilities` | `cursor_capabilities_v1` | capability names, phases |
| `message_lints` | `lint_errors_v1` | file paths, error types |
| `message_files` | `files_referenced_v1` | file paths, line ranges |
| `message_codeblocks` | `code_suggestions_v1` | file paths, languages |

This happens at sync time or as a post-sync analysis step.

---

## Part 4: Three Retrieval Types

The semantic interface supports three retrieval patterns:

### Semantic Retrieval

**Question:** "What's relevant to X?"

- Cuts across time — finds by meaning similarity
- Hybrid search: vector + BM25 (via FTS5)
- Works over: documents, skills, facets, segments
- **Use case:** Context injection for interaction agents

### Temporal Retrieval

**Question:** "What happened recently?"

- Groups by time — provides narrative continuity
- Time-windowed aggregation of events and facets
- Cross-channel daily segments
- **Use case:** "Here's what's been happening" context

### Segment Routing

**Question:** "Where can I fork from?"

- Finds past context windows that can be resumed
- Uses turn-pair segments from AI sessions
- Includes freshness scoring (has the context drifted?)
- **Use case:** Routing decisions for subagent dispatch

**Key insight:** Routing targets **segments**, not "checkpoints" or "agents". A segment IS a preloaded context window. Forking from one means you don't rebuild context from scratch.

---

## Part 5: The Routing Problem

### Interaction Pattern

```
User sends message
       │
       ▼
┌─────────────────────────────────────────────────────────┐
│              INTERACTION AGENT                          │
│                                                         │
│  Context injected (breadcrumbs):                        │
│  ├── Semantic matches (skills, docs, facets)           │
│  └── Temporal context (recent activity summary)        │
└─────────────────────────────────────────────────────────┘
       │
       │ "I need to DO something"
       ▼
┌─────────────────────────────────────────────────────────┐
│              ROUTING DECISION                           │
│                                                         │
│  Stage 1: Hard filters                                  │
│    - Active segments only                               │
│    - Recency cutoff                                     │
│    - Channel compatibility                              │
│                                                         │
│  Stage 2: Candidate generation (fast)                   │
│    - Top K by embedding similarity                      │
│    - Top K by facet overlap                             │
│    - Union + dedupe                                     │
│                                                         │
│  Stage 3: Scoring (richer)                              │
│    - Weighted: semantic + recency + quality + freshness │
│    - Optional LLM rerank for top N                      │
│                                                         │
│  Stage 4: Decision                                      │
│    - Route to highest scorer OR create new segment      │
└─────────────────────────────────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────────────────────────┐
│              SUBAGENT (Execution)                       │
│                                                         │
│  If routed: starts with segment's context               │
│  If fresh: starts clean                                 │
└─────────────────────────────────────────────────────────┘
```

### Freshness Scoring

When considering a segment for routing:

1. Check which files it touched (from `files_referenced` facets)
2. Compare current file states to when segment was created
3. Calculate drift score (how much has changed?)
4. High drift = stale context = prefer fresh agent

### Scoring Formula

```
final_score = α * semantic_similarity 
            + β * recency_score
            + γ * quality_score
            + δ * freshness_score
```

Quality score comes from `turn_quality_v1` analysis (corrections, frustration, praise signals).

---

## Part 6: Memory as Temporal Analysis

Memory is NOT a separate system. It's temporal event analysis within the same pipeline.

### How It Works

```
analysis_type: "daily_memory_v1"
input: ALL events from ALL channels for day N
output: facets capturing what happened that day
```

Same machinery as everything else:
- Events → daily segments (cross-channel)
- Segments → analysis runs
- Analysis runs → facets

### Memory Extraction Prompts (Built)

| Prompt | Facet Type |
|--------|------------|
| `self-preference-extraction-v1` | `self_preference` |
| `correction-extraction-v1` | `self_correction` |
| `self-knowledge-extraction-v1` | `self_knowledge` |
| `relationship-context-extraction-v1` | `relationship_context` |
| `workspace-pattern-extraction-v1` | `workspace_convention` |

Each outputs a `memory_entry` field suitable for eventual MEMORY.md rendering.

### Synthesis (DEFERRED)

Sequential memory synthesis is deferred:
- Expensive (time, cost, complexity)
- Not needed for context injection or routing
- Can be added once extraction is solid

---

## Part 7: What's Built

| Component | Status |
|-----------|--------|
| Events table | ✅ Built |
| Segments (conversations) | ✅ Built, needs rename |
| Analysis pipeline | ✅ Built, needs event_id support |
| Facets table | ✅ Built |
| Embeddings table | ✅ Built |
| AIX sync (messages + sessions) | ✅ Built |
| AIX metadata sync | ⚠️ Partial |
| Document heads + search | ✅ Built |
| Memory extraction prompts | ✅ Built (5 prompts) |
| Checkpoint prompts | ✅ Built (2 prompts) |
| FTS5 BM25 search | ❌ Not built |
| Hybrid search API | ⚠️ Partial |
| Routing infrastructure | ❌ Not built |
| Synthesis | ⏸️ Deferred |

---

## Part 8: Implementation Priorities

### Phase 1: Schema + Sync (Foundation)

1. Rename `conversations` → `segments` (all references)
2. Add `event_id` to `analysis_runs` (enable event-level extraction)
3. Sync ALL AIX metadata (capabilities, lints, files, codeblocks)
4. Drop checkpoint tables (migrate to segments + analyses)
5. Add AIX watcher (fsnotify on db file for live sync)

### Phase 2: Extraction + Search

6. Turn-pair chunking strategy for AI sessions
7. Event-level extraction for AIX metadata → facets
8. Quality analysis type (`turn_quality_v1`)
9. FTS5 index for BM25
10. Hybrid search API (vector + BM25)
11. `cortex search` CLI command

### Phase 3: Routing

12. Candidate generation (embedding + facet overlap)
13. Freshness scoring (file state hashes)
14. Routing decision logic
15. Broker integration (`before_agent_start` hook)

### Phase 4: Memory + Synthesis (DEFERRED)

16. Daily cross-channel segments
17. Memory extraction on daily segments
18. Sequential synthesis
19. MEMORY.md rendering

---

## Part 9: Open Questions

### Resolved

| Question | Decision |
|----------|----------|
| Checkpoint tables? | Kill them — use segments + analyses |
| Event vs segment extraction? | Both — via nullable event_id |
| Terminology for temporal groups? | Segments |
| Memory as separate system? | No — temporal analysis type |
| Name? | Cortex |

### Still Open

1. **Turn boundary detection** — How to reliably pair user + assistant in edge cases?
2. **Facet deduplication** — Dedupe in table or at query time?
3. **Routing policy config** — One policy or configurable per channel?
4. **Cross-session continuity** — When is related work in different sessions "same project"?
5. **Freshness thresholds** — What drift % makes a segment too stale?

---

## Summary

**Cortex is:**
```
adapters → events → segments → analyses → facets → semantic interface
```

**One pipeline. Everything flows through it.**

- Events are the raw source of record
- Segments are temporal groupings (turns, sessions, days)
- Analyses extract structured data from events OR segments
- Facets are the queryable output
- Semantic interface serves the broker

**Three retrieval types:**
- Semantic (by meaning) → context injection
- Temporal (by time) → narrative continuity  
- Segment routing (past context windows) → subagent dispatch

**Build extraction first. Everything else follows.**
