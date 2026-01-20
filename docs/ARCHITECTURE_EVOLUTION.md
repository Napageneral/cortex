---
summary: "Complete architectural evolution: initial goals → current state → refined design"
read_when:
  - Starting any work on comms/cortex
  - Understanding why architectural decisions were made
  - Picking up work on segments, extraction, or routing
---
# Architecture Evolution — Comms → Cortex

This document consolidates three prior specs and a design session into a unified architectural vision. It serves as the single source of truth for where we are and where we're going.

**Prior documents (superseded by this one):**
- `UNIFIED_SEMANTIC_LAYER.md` — broker/workspace vision
- `SEMANTIC_CONTEXT_ENGINE_IMPLEMENTATION_PLAN.md` — search infrastructure
- `MEMORY_SYSTEM_SPEC.md` — memory extraction from AI sessions

---

## Part 1: Original Goals

### What We Set Out To Build

A **unified communication substrate** that:

1. **Ingests everything** — iMessage, Gmail, calendar, AI sessions (via AIX), documents, skills
2. **Extracts intelligence** — facets, entities, preferences, corrections from raw events
3. **Enables semantic access** — search by meaning, not just keywords
4. **Supports agent routing** — find relevant past context windows to fork from
5. **Builds memory** — synthesize narrative memory from extracted signals

### The Three Retrieval Types

| Type | Question | Use Case |
|------|----------|----------|
| **Semantic** | "What's relevant to X?" | Context injection |
| **Temporal** | "What happened recently?" | Narrative continuity |
| **Checkpoint** | "Where can I fork from?" | Subagent routing |

### The Phased Approach

```
Phase 1: Extraction Infrastructure (foundation)
Phase 2: Semantic Interface (search API)
Phase 3: Checkpoint Index (agent routing)
Phase 4: Synthesis (compressed memory) — DEFERRED
```

---

## Part 2: What We Built

### Core Infrastructure

**Events table** — Single source of truth for all data:
- Messages from all channels (iMessage, Gmail, AI sessions)
- Documents, skills, memory entries (via `document_heads` pointer table)
- Tool invocations extracted from AI sessions

**Analysis pipeline** — Extract structured data from events:
- `analysis_types` — defines extraction prompts and output schema
- `analysis_runs` — tracks execution per conversation
- `facets` — extracted queryable values (entities, topics, preferences, corrections)

**Conversation chunking** — Group events for analysis:
- `conversation_definitions` — chunking strategies (time_gap, thread, session)
- `conversations` — instances with time bounds and event counts
- `conversation_events` — maps events to conversations with position

**Embeddings** — Universal vector store:
- `embeddings` table with `entity_type` and `entity_id`
- Supports documents, conversations, events

### AIX Integration

**What's synced:**
- Messages from Cursor sessions → `events` table
- Sessions → `threads` table
- Terminal commands → extracted as separate tool events

**Performance:** Incremental sync targets <100ms for ≤10 new messages.

### What Got Added (But Should Be Reconsidered)

**Checkpoint tables** (in comms schema):
- `checkpoints` — forkable assistant-response boundaries
- `checkpoint_quality` — quality metrics rollup
- `turn_facets` — per-turn feedback signals

These were designed as separate tables, but they duplicate concepts that should flow through the existing extraction pipeline.

---

## Part 3: The Refined Design

### Key Insight: Build on Events, Not Beside Them

The checkpoint/quality system created parallel structures. The cleaner approach:

1. **Events are the raw source of record** — immutable, append-only
2. **Segments are logical groupings** — temporal slices of events
3. **Analyses are derived information** — run on events OR segments
4. **Everything flows through one pipeline**

### Terminology Change: Segments

"Conversation" is overloaded. We're adopting **segment** for temporal groupings:

> A **segment** is a contiguous slice of events that belong together.

Segments can be:
- Single-event (for rich AI messages with metadata)
- Turn-level (user message + assistant response)
- Session-level (full thread)
- Time-gap-based (iMessage style)

The `conversations` table becomes `segments`. The `conversation_definitions` table becomes `segment_definitions`. Same structure, clearer semantics.

### Multi-Level Extraction

Different granularities need different extraction:

```
Level 0: Event (single message)
         └─ Per-event metadata
            - capabilities used (from AIX message_capabilities)
            - files referenced (from AIX message_files)
            - lints visible (from AIX message_lints)
            - code blocks suggested (from AIX message_codeblocks)
            - tool calls made

Level 1: Turn (user msg + assistant response)
         └─ Turn-level analysis
            - what was asked vs done
            - quality signals (correction, frustration, praise)
            - success/failure assessment

Level 2: Session/Thread
         └─ Session-level analysis
            - overall trajectory
            - what was accomplished

Level 3: Cross-session
         └─ Project/temporal patterns
```

For iMessage, Level 0 is sparse (just text). For AI sessions, Level 0 is **dense** with structured metadata.

### Schema Change: Event-Level Analysis

Current `analysis_runs` is tied to `conversation_id`. To support event-level extraction:

```sql
-- Option A: Add event_id to analysis_runs (simpler)
ALTER TABLE analysis_runs ADD COLUMN event_id TEXT REFERENCES events(id);
-- conversation_id becomes nullable; one or the other is set
```

This lets the same pipeline handle:
- Event-level extraction (AIX metadata → facets)
- Segment-level extraction (quality signals, summaries)

### Kill the Checkpoint Tables

Instead of `checkpoints`, `checkpoint_quality`, `turn_facets`:

| Old Table | Replacement |
|-----------|-------------|
| `checkpoints` | Segments with turn-pair chunking |
| `checkpoint_quality` | Analysis runs with `turn_quality_v1` type |
| `turn_facets` | Facets from quality analysis |

One pipeline. No special cases.

### AIX Metadata → Facets

All AIX metadata should sync and become facets:

| AIX Table | Analysis Type | Facets |
|-----------|--------------|--------|
| `message_capabilities` | `cursor_capabilities_v1` | capability names, phases |
| `message_lints` | `lint_errors_v1` | file paths, error types, severity |
| `message_files` | `files_referenced_v1` | file paths, line ranges |
| `message_codeblocks` | `code_suggestions_v1` | file paths, languages, content hashes |

This can happen at sync time (automatic) or as a post-sync analysis step.

---

## Part 4: The Routing Problem

### Problem Statement

Given an incoming message, which segment/turn should handle it?

This matters for:
- Agent-to-agent communication
- Resuming work on a project
- Finding relevant context to fork from

### Search Space

```
All segments S where:
  - S.thread is active (not archived)
  - S.end_time within recency window
  - S.channel matches or is compatible
```

### Signals for Ranking

1. **Embedding similarity** — semantic match to segment content
2. **Facet overlap** — shared entities, files, topics
3. **Recency** — more recent segments weighted higher
4. **Thread continuity** — explicit thread_id is strong signal
5. **Quality score** — historical success when forking from this segment

### Pruning Strategy

```
Stage 1: Hard filters
  - Active segments only
  - Recency cutoff
  - Channel compatibility

Stage 2: Candidate generation (fast)
  - Top K by embedding similarity
  - Top K by facet overlap (exact match queries)
  - Union + dedupe

Stage 3: Scoring (richer)
  - Load context for candidates
  - Weighted combination of signals
  - Optional LLM-assisted ranking for top N

Stage 4: Decision
  - Route to highest scorer
  - OR create new segment if all scores below threshold
```

### Context Retrieval for a Segment

When routing to segment S at position P:

1. **The segment itself** — events in S
2. **Prior segments in thread** — full thread history before S
3. **Facets from prior segments** — files touched, errors encountered
4. **Accumulated context** — what the agent "knew" at that point

This is queryable via joins on `segment_events` and `facets`.

---

## Part 5: Rename to Cortex

"Comms" is limiting — this isn't just communications anymore.

**New name: Cortex**

The system is:
```
adapters → events → segments → analyses → facets → semantic interface
```

It's the workspace's intelligence layer. The broker's data backend. What makes a workspace "smart."

---

## Part 6: Implementation Priorities

### Immediate (Schema + Sync)

1. **Rename tables** — `conversations` → `segments`, update all references
2. **Add event_id to analysis_runs** — enable event-level extraction
3. **Sync ALL AIX metadata** — capabilities, lints, files, codeblocks
4. **Drop checkpoint tables** — migrate any existing data to segments + analyses
5. **AIX watcher** — fsnotify on AIX db file for live sync

### Next (Extraction + Search)

6. **Turn-pair chunking strategy** — `strategy: "turn_pair"` for AI sessions
7. **Event-level extraction for AIX** — auto-populate facets from metadata
8. **Quality analysis type** — `turn_quality_v1` for correction/frustration/praise signals
9. **Hybrid search API** — vector + BM25 over segments and facets

### Later (Routing + Memory)

10. **Routing infrastructure** — candidate generation, scoring, threshold decisions
11. **Freshness scoring** — file state hashes for context staleness
12. **Memory synthesis** — sequential compaction (still deferred)

---

## Part 7: Open Questions

### Resolved

- **Checkpoint tables?** → Kill them, use segments + analyses
- **Event vs segment extraction?** → Both, via nullable event_id on analysis_runs
- **Naming for temporal groups?** → Segments

### Still Open

1. **Turn boundary detection** — How do we reliably pair user message + assistant response? AIX has structure, but edge cases exist (tool-only responses, multi-turn assistant).

2. **Facet deduplication** — Same file appears in 50 segments. Do we dedupe in facets table or at query time?

3. **Routing policy configuration** — Should routing strategies be configurable per channel/use case, or is one policy enough?

4. **Cross-session continuity** — When does related work in different sessions count as "same project"?

---

## Summary

| Concept | What It Is | Status |
|---------|------------|--------|
| **Events** | Raw source of record | ✅ Built |
| **Segments** | Temporal groupings of events | ✅ Built (as conversations), needs rename |
| **Analyses** | Derived information on events/segments | ✅ Built, needs event_id support |
| **Facets** | Queryable extracted values | ✅ Built |
| **Embeddings** | Vector store | ✅ Built |
| **AIX sync** | Messages + sessions | ✅ Built, metadata incomplete |
| **AIX watcher** | Live sync on file change | ❌ Not built |
| **Checkpoint tables** | Parallel structure | ❌ To be removed |
| **Routing** | Find segment to fork from | ❌ Not built |
| **Synthesis** | Compressed memory | ⏸️ Deferred |

**The core insight:** Events → Segments → Analyses → Facets. One pipeline. Everything flows through it. Checkpoints, quality signals, metadata — all become analyses and facets on segments.

---

## Handoff Notes

This document supersedes the three prior specs. When continuing work:

1. **Read this first** — it's the unified truth
2. **Schema changes are the foundation** — do those before feature work
3. **AIX metadata is high value** — prioritize syncing all of it
4. **Routing is the interesting problem** — but it needs segments + facets solid first

The prior specs have valuable detail on specific subsystems (search API, memory prompts, checkpoint scoring). Reference them for implementation specifics, but this document defines the architecture.
