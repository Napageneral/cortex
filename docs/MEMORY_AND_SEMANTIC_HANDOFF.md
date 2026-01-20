---
summary: "Unified handoff: memory extraction, semantic context engine, and checkpoint routing"
read_when:
  - You are continuing work on the comms intelligence layer
  - You need to understand how memory, semantic search, and routing fit together
  - You want to implement any of the extraction or search features
---
# Memory & Semantic Intelligence — Unified Handoff

This document consolidates three related workstreams into a single coherent picture:

1. **Memory Extraction** — extracting user/agent/workspace signals from AI sessions
2. **Semantic Context Engine** — hybrid search over documents and facets
3. **Checkpoint Routing** — forking past context windows for subagent dispatch

These are not separate systems. They are **layers of the same intelligence substrate** built on top of comms.

---

## Part 1: Origin Story

### The Spark (LangSmith Agent Builder Article)

The initial inspiration came from LangSmith's "How we built Agent Builder's memory system" article. Key takeaways:

| Insight | LangSmith Approach | Our Translation |
|---------|-------------------|-----------------|
| **File-based memory** | AGENTS.md, tools.json | Already doing this (AGENTS.md, SOUL.md, IDENTITY.md) |
| **COALA memory types** | Procedural/Semantic/Episodic | Map to skills/facts/timeline in our model |
| **Hot path editing** | LLM edits memory during tasks | We chose background processing instead |
| **Human-in-the-loop** | Approve memory changes | Defer — focus on extraction first |
| **Hierarchical scoping** | User/Org/Agent memory levels | Workspace/User/Agent in Nexus |

### The Pivot

The original plan was to create new database tables (`memory_facets`, `synthesized_memory`) and a dedicated memory package. After discussion, this was rejected:

**Why rejected:**
- Comms already has the extraction machinery (events → chunks → analysis → facets)
- Memory should be **derived state**, not a parallel data model
- The `events` table is immutable; memory is mutable analysis on top
- Adding new tables creates maintenance burden without adding capability

**The realization:** Memory is not a separate system. It's a **temporal analysis type** that runs on the same pipeline as everything else.

---

## Part 2: The Unified Architecture

### Core Principle

> **Raw → Extracted → (Synthesized)**
>
> Extraction is parallel and independent. Synthesis is sequential and deferred.

Everything flows through the existing comms machinery:

```
┌────────────────────────────────────────────────────────────────┐
│                         RAW DATA                               │
│  events table (immutable, append-only)                        │
│  ├── iMessage, Gmail, Calendar                                │
│  ├── AI sessions (Cursor, Claude Code, Codex via aix)        │
│  └── Future: Slack, Discord, etc.                            │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────────┐
│                        CHUNKING                                │
│  conversations table                                           │
│  ├── Thread-based (iMessage, Gmail threads)                   │
│  ├── Session-based (AI sessions)                              │
│  └── Daily cross-channel (for temporal memory)                │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────────┐
│                       EXTRACTION                               │
│  analysis_types → analysis_runs → facets                      │
│  ├── self_preference (user preferences)                       │
│  ├── self_correction (user correcting AI)                     │
│  ├── self_knowledge (user expertise/knowledge)                │
│  ├── relationship_context (people/relationships)              │
│  ├── workspace_convention (codebase patterns)                 │
│  ├── checkpoints (forkable context windows)                   │
│  └── ... any future analysis type                             │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────────┐
│                     SEMANTIC INTERFACE                         │
│  Search API: vector + BM25 hybrid                             │
│  ├── Semantic retrieval (by meaning)                          │
│  ├── Temporal retrieval (by time window)                      │
│  └── Checkpoint retrieval (for routing)                       │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────────┐
│                      SYNTHESIS (DEFERRED)                      │
│  Sequential processing — requires prior state                 │
│  ├── Daily summaries                                          │
│  ├── Memory compaction                                        │
│  └── MEMORY.md rendering                                      │
└────────────────────────────────────────────────────────────────┘
```

---

## Part 3: What Was Built

### 3.1 Extraction Prompts

Five memory-oriented prompts in `prompts/`:

| Prompt | Purpose | Output Facet Type |
|--------|---------|-------------------|
| `self-preference-extraction-v1.prompt.md` | User preferences (formatting, communication style) | `self_preference` |
| `correction-extraction-v1.prompt.md` | User corrections of AI behavior | `self_correction` |
| `self-knowledge-extraction-v1.prompt.md` | User knowledge and expertise | `self_knowledge` |
| `relationship-context-extraction-v1.prompt.md` | Relationships and social context | `relationship_context` |
| `workspace-pattern-extraction-v1.prompt.md` | Codebase conventions and gotchas | `workspace_convention` |

**Key design choice:** Each prompt outputs a `memory_entry` field — a compact sentence suitable for eventual MEMORY.md rendering.

**Scope hints:** Corrections include `scope_hint` to help categorize signals:
- `agent_specific` — task/session specific
- `user_general` — general user preference
- `workspace_specific` — codebase/workspace specific

### 3.2 Checkpoint System

Two checkpoint prompts in `prompts/`:

| Prompt | Purpose |
|--------|---------|
| `checkpoint-context-v1.prompt.md` | Extract context signature at assistant response boundary |
| `checkpoint-feedback-v1.prompt.md` | Capture user satisfaction signals for checkpoint quality |

**Schema additions:**
- `checkpoints` table — tracks forkable points in AI sessions
- `document_heads` table — stable pointers for document-style events

### 3.3 Documentation

| Document | Purpose |
|----------|---------|
| `MEMORY_SYSTEM_SPEC.md` | Extraction-focused spec for memory signals |
| `MEMORY_SYNTHESIS_INVESTIGATION.md` | Deferred design notes for synthesis |
| `MEMORY_PROMPT_TESTS.md` | Test cases for validating extraction prompts |
| `UNIFIED_SEMANTIC_LAYER.md` | High-level vision unifying all three systems |
| `SEMANTIC_CONTEXT_ENGINE_IMPLEMENTATION_PLAN.md` | Concrete implementation plan for search |

---

## Part 4: The Three Retrieval Types

The semantic interface serves three distinct purposes:

### Semantic Retrieval
**Question:** "What's relevant to X?"

- Cuts across time — finds by meaning similarity
- Hybrid search: vector + BM25 lexical
- Works over: documents, skills, extracted facets, events
- **Use case:** Context injection for interaction agents

### Temporal Retrieval
**Question:** "What happened recently?"

- Groups by time — provides narrative continuity
- Time-windowed aggregation of events and facets
- **Use case:** "Here's what's been happening" context

### Checkpoint Retrieval
**Question:** "Where can I fork from?"

- Finds past context windows that can be resumed
- Every assistant message boundary = potential checkpoint
- Includes freshness scoring (has the context drifted?)
- **Use case:** Routing decisions for subagent dispatch

---

## Part 5: Memory Hierarchy (Conceptual)

Three levels of memory, all derived from the same extraction pipeline:

### Workspace Memory
**Location:** `~/nexus/MEMORY.md`

**Contains:**
- Project structure and conventions
- Tools and skills available
- Cross-cutting patterns ("use trash not rm")
- Gotchas ("don't edit state/ directly")

**Extracted from:** `workspace_convention` facets from AI sessions on nexus

### User Memory
**Location:** `~/nexus/state/user/MEMORY.md`

**Contains:**
- Tyler's preferences (communication, formatting, workflow)
- Tyler's knowledge (languages, systems, domains)
- Relationships (who people are, how they relate)

**Extracted from:** `self_preference`, `self_knowledge`, `self_correction`, `relationship_context` facets

### Agent Memory
**Location:** `~/nexus/state/agents/{name}/MEMORY.md`

**Contains:**
- Task-specific learnings for this agent type
- Corrections specific to this agent's domain
- Patterns unique to this agent's workflow

**Extracted from:** Agent-scoped corrections and patterns (via `scope_hint`)

---

## Part 6: What's Left To Build

### Phase 1: Extraction Infrastructure (PRIORITY)

- [ ] Register memory analysis types via `comms compute seed`
- [ ] Add daily conversation definition (strategy = daily, channel = NULL)
- [ ] Run extraction on AI sessions and verify facet quality
- [ ] Add facet cleanup on re-run (avoid duplication)

### Phase 2: Semantic Interface

- [ ] Implement `document_heads` upsert flow
- [ ] Wire up embeddings for `entity_type = document`
- [ ] Build hybrid search API (`internal/search/`)
- [ ] Add `comms search` CLI command

### Phase 3: Checkpoint Index

- [ ] Backfill checkpoints from existing aix sessions
- [ ] Implement freshness scoring (file state hashes)
- [ ] Build checkpoint query API for routing

### Phase 4: Synthesis (DEFERRED)

- [ ] Daily/weekly memory synthesis
- [ ] MEMORY.md file rendering
- [ ] Episodic continuity (prior memory injection)
- [ ] Conflict resolution (recency, confidence weighting)

---

## Part 7: Key Decisions Made

| Decision | Rationale |
|----------|-----------|
| **No new memory tables** | Use existing facets table with memory-oriented facet types |
| **Extraction-first** | Synthesis is expensive and can be added later |
| **Background processing** | Don't burden agents with memory updates during tasks |
| **Scope hints** | Let extraction classify signals for later routing |
| **memory_entry field** | Prompts produce MEMORY.md-ready lines |
| **Daily chunks** | Cross-channel temporal windows for memory analysis |

---

## Part 8: Open Questions

1. **Compaction threshold** — How many similar facets before generalizing? (Start with 3)
2. **Memory file size** — When to split MEMORY.md? (Start with 5KB limit)
3. **Cross-scope promotion** — Should patterns appearing in multiple agent memories promote to user level?
4. **Forgetting** — Time-based decay? Explicit forget command?
5. **Backfill handling** — When backdated events arrive, how much do we reprocess?
6. **Checkpoint staleness** — What drift threshold makes a checkpoint too stale to fork?

---

## Part 9: Files Reference

### Prompts (`prompts/`)
- `self-preference-extraction-v1.prompt.md`
- `correction-extraction-v1.prompt.md`
- `self-knowledge-extraction-v1.prompt.md`
- `relationship-context-extraction-v1.prompt.md`
- `workspace-pattern-extraction-v1.prompt.md`
- `memory-synthesis-v1.prompt.md` (for future use)
- `checkpoint-context-v1.prompt.md`
- `checkpoint-feedback-v1.prompt.md`

### Documentation (`docs/`)
- `MEMORY_SYSTEM_SPEC.md` — extraction spec
- `MEMORY_SYNTHESIS_INVESTIGATION.md` — deferred synthesis design
- `MEMORY_PROMPT_TESTS.md` — test cases
- `UNIFIED_SEMANTIC_LAYER.md` — unified vision
- `SEMANTIC_CONTEXT_ENGINE_IMPLEMENTATION_PLAN.md` — search implementation

### Schema (`internal/db/schema.sql`)
- `checkpoints` table
- `document_heads` table
- `embeddings` table (existing, reused)
- `facets` table (existing, memory facet types added)

---

## Part 10: Quick Start for Continuing Agent

1. **Read this doc** — you now have full context
2. **Read `UNIFIED_SEMANTIC_LAYER.md`** — the unified vision
3. **Run `nexus status`** — understand current state
4. **Pick a Phase 1 task** — extraction infrastructure is priority
5. **Test with `MEMORY_PROMPT_TESTS.md`** — validate extraction quality

**Most impactful next step:** Register the memory analysis types in `comms compute seed` and run extraction on existing AI sessions to validate the prompts produce high-quality facets.

---

*This document consolidates discussions from memory system design, semantic context engine planning, and checkpoint-based routing into a single coherent handoff.*
