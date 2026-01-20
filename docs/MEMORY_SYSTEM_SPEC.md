---
summary: "Spec for memory extraction (AI sessions) with synthesis deferred"
read_when:
  - You are implementing memory extraction in comms
  - You want to understand the memory hierarchy (workspace/user/agent)
  - You need to build the synthesis pipeline
---
# Memory System — Extraction Focus

This spec defines how to extract **memory signals** from comms, primarily from AI
session data (AIX). Synthesis is deferred. Memory itself is a narrative layer that
we are not defining yet; for now we focus on reliable, parallel extraction.

Related: LangSmith Agent Builder memory article (inspiration), semantic-agent-routing-spec.md (checkpoint system)

---

## Goals (Now)

- **High-quality extraction:** Capture raw memory signals from AI sessions (AIX).
- **Parallelizable:** Extraction runs independently per chunk (fast + scalable).
- **Temporal readiness:** Daily cross-channel chunks are available for later memory work.
- **Semantic utility:** Extracted facets feed semantic context injection now.
- **Attribution:** Every signal traces back to source events/conversations.

## Non-goals (Now)

- Defining the final memory representation (narrative format is TBD)
- Implementing synthesis/compaction logic (explicitly deferred)
- Real-time memory updates during agent execution
- Replacing person_facts (identity resolution is orthogonal)

---

## Definitions & Terminology

- **extracted signal**: A facet derived from events (preferences, corrections, etc.)
- **memory target**: Conceptual destination (workspace/user/agent) for future memory
- **synthesis**: Future process that turns signals + context into narrative memory
- **temporal chunk**: A daily conversation that aggregates events across channels

---

## Architecture Overview (Current)

```
Raw Events (comms DB)
    ↓
Conversation Chunking (existing)
    ↓
Extraction (analysis types)
    ↓
Facets (memory signal types)

Synthesis is deferred.
```

---

## Current Extraction Focus (AIX)

These prompts are **optimized for AI session data**. They extract:

- **Agent memory signals** — corrections and task patterns from AI sessions
- **User memory signals** — preferences, knowledge, and corrections expressed in AI chats
- **Workspace signals** — conventions and gotchas when working on the workspace

The value right now is clean extraction of these signals from AIX sessions so the
semantic context engine can retrieve them immediately.

---

## Memory Targets (Future)

These are conceptual targets for memory synthesis. Right now we only extract
signals that will eventually feed these layers.

### Level 1: Workspace Memory

**Location:** `~/nexus/MEMORY.md`

**Contains:**
- How this nexus is configured
- Tools and skills available
- Project structures and conventions
- Cross-cutting patterns ("all code uses TypeScript", "prefer trash over rm")

**Source events:**
- Agent sessions working on nexus itself
- Configuration changes
- Skill installations

**Signals extracted from:** AI sessions and daily temporal chunks

### Level 2: User Memory

**Location:** `~/nexus/state/user/MEMORY.md`

**Contains:**
- Tyler's preferences (communication style, formatting, tooling)
- Knowledge about Tyler (what he knows, what he cares about)
- Relationships and context (who people are, how they relate)
- Patterns from all communications (iMessage, Gmail, AI sessions)

**Source events:**
- All comms events where Tyler is a participant
- AI sessions where Tyler provides feedback/corrections
- Self-disclosures in messages

**Signals extracted from:** AI sessions and daily temporal chunks

### Level 3: Agent Memory

**Location:** `~/nexus/state/agents/{name}/MEMORY.md`

**Contains:**
- Task-specific learnings for this agent
- Corrections made during this agent's sessions
- Patterns unique to this agent's domain

**Source events:**
- AI sessions tagged with this agent name (from aix session.name)
- Explicit corrections in those sessions

**Signals extracted from:** agent-tagged AI sessions (AIX)

---

## Data Model

Memory should use the **existing comms extraction stack** — no new tables.

**Raw → Extracted only (for now):**
- Raw events live in `events`
- Chunking groups events into `conversations`
- Analyses run on conversations (`analysis_types` / `analysis_runs`)
- Extracted signals are stored as **facets** (`facets`) with memory-oriented facet types

**Memory signal facet types** (stored in `facets.facet_type`):
- `self_preference` — user preferences
- `self_knowledge` — what the user knows/understands
- `self_correction` — user corrections of AI behavior
- `relationship_context` — relationships and social context
- `workspace_convention` — workspace-level patterns

**Temporal memory** is modeled by **daily conversations**:
- Create a conversation definition that chunks by day across all channels
- Run memory analysis types on those daily conversations
- The resulting facets are time-windowed by `conversations.start_time`/`end_time`

**Synthesis is deferred.** When we eventually add it, it can build on top of
facets and analysis runs without introducing new tables.

---

## Analysis Types for Memory Extraction

### 1. self-preference-extraction-v1

**Purpose:** Extract user preferences from conversations (formatting, communication style, tool preferences, etc.)

**Input:** Conversation chunk with user as participant

**Output facets:**
- `self_preference` — "prefers bullet points", "hates corporate speak", etc.

**Scope assignment:** Always `user` (preferences are user-level)

### 2. correction-extraction-v1

**Purpose:** Extract instances where user corrects AI behavior

**Input:** AI session conversation (from aix adapter)

**Output facets:**
- `self_correction` — "when X, do Y instead", "don't do Z"

**Scope assignment:**
- If session has agent name → `agent` scope
- Otherwise → `user` scope

### 3. self-knowledge-extraction-v1

**Purpose:** Extract what user knows, understands, or cares about

**Input:** Conversation chunk with user as participant

**Output facets:**
- `self_knowledge` — "knows Go", "understands distributed systems", "cares about DX"

**Scope assignment:** Always `user`

### 4. relationship-context-extraction-v1

**Purpose:** Extract context about relationships mentioned in conversations

**Input:** Conversation chunk

**Output facets:**
- `relationship_context` — "Casey is girlfriend", "Dad owns Napa General Store"

**Scope assignment:** Always `user`

### 5. workspace-pattern-extraction-v1

**Purpose:** Extract workspace-level conventions from AI sessions working on the workspace itself

**Input:** AI session in nexus workspace

**Output facets:**
- `workspace_convention` — "uses AGENTS.md", "skills live in skills/"

**Scope assignment:** Always `workspace`

---

## Extraction Pipeline (Current)

### Trigger: Heartbeat or Cron

Extraction can run on heartbeat/cron, or manually via `comms compute enqueue/run`.

### Pipeline Stages

```
1. CHUNK
   - Daily conversation definition (strategy = daily, channel = NULL)
   - AI session conversations (aix → threads)

2. ANALYZE
   - Run memory analysis types on daily conversations
   - Run correction/workspace analysis on AI sessions

3. STORE
   - Write memory entries as facets in `facets`
   - Use facet_type to indicate category (self_preference, relationship_context, etc.)
```

## Synthesis (Deferred)

Sequential synthesis is **not implemented yet**. The prompt exists for future use:
`prompts/memory-synthesis-v1.prompt.md`.

When added later, it should read facets and produce compressed summaries, but it
should not require new tables.

---

## Agent Name Attribution

### From aix sessions

The aix adapter syncs sessions with a `name` field (e.g., "Cursor Session" or a custom name). This becomes the basis for agent attribution.

```sql
-- In aix adapter, session name is stored in threads table
SELECT name FROM threads WHERE source_adapter = 'cursor' AND id = ?
```

### Mapping to agent scope

When extracting memory from an AI session:

1. Get thread name from aix
2. Use that thread name to select agent-specific sessions for analysis
3. Persist agent attribution in analysis metadata (future) if needed

### Explicit tagging

Users can tag sessions with agent names via:
- Session title in Cursor
- Future: `nexus session tag <session_id> --agent <name>`

---

## MEMORY.md Format (Illustrative, Future)

These examples are placeholders to communicate intent. The actual memory format
will be defined when synthesis is designed.

### Structure

```markdown
# Memory

Last updated: 2026-01-20T15:30:00Z

## Preferences

- Prefers bullet points over paragraphs
- Hates corporate speak and HR-style language
- Wants direct feedback, real criticism, pushback when wrong

## Knowledge

- Proficient in Go, TypeScript, Python
- Building Nexus — AI workspace/OS for personal productivity
- Understands distributed systems, embeddings, LLM orchestration

## Patterns

- When making changes, always check for linter errors after
- Use `trash` instead of `rm` for recoverable deletes

## Relationships

- **Casey Adams** — girlfriend, they live together
- **Dad (Jim Brandt)** — owns Napa General Store

## Context

- Lives in Austin, TX (Central timezone)
- Works on Nexus full-time
```

### Agent-specific additions

Agent MEMORY.md files include task-specific learnings:

```markdown
# Memory — email-assistant

Last updated: 2026-01-20T15:30:00Z

## Task Patterns

- Always check unread count before summarizing
- Group emails by sender for recurring correspondents
- Flag anything from Casey as high priority

## Corrections

- Don't draft replies to cold outreach — just archive
- Use casual tone for personal emails, formal for work

## Domain Knowledge

- Tyler's work email: tnapathy@gmail.com
- Important senders: Casey, Dad, specific work contacts
```

---

## Conflict Resolution

### Recency wins (future synthesis)

When synthesis is added, contradictions should be resolved by preferring newer
facets over older ones. Until then, contradictory facets will coexist in raw
extraction output.

### Future: Confidence scoring

Track confidence per memory entry based on:
- Frequency (mentioned multiple times)
- Recency (recent > old)
- Explicitness (direct statement > inference)

---

## Implementation Phases

### Phase 1 — Analysis Types + Chunking

- [ ] Create analysis type prompts (see prompts/)
- [ ] Register analysis types via `comms compute seed`
- [ ] Add daily conversation definition (strategy = daily, channel = NULL)

### Phase 2 — Extraction Pipeline

- [ ] Run memory extraction on daily conversations (cross-channel)
- [ ] Run correction/workspace analysis on AI sessions (aix adapter)
- [ ] Query facets for temporal context ("what happened yesterday?")

### Phase 3 — Semantic Retrieval (Next)

- [ ] Add search surface for facets (vector + lexical)
- [ ] Return extracted memory signals as semantic breadcrumbs

### Phase 4 — Synthesis (Deferred)

- [ ] Daily/weekly summaries
- [ ] MEMORY.md rendering
- [ ] Episodic continuity (prior memory injection)

---

## CLI Commands (Deferred)

Memory synthesis CLI commands (e.g., `nexus reflect`, `nexus memory`) should be added
after extraction quality is solid and temporal retrieval is in place.

---

## Open Questions

1. **Compaction threshold:** How many similar facets before we generalize? (Start with 3)

2. **Memory file size:** At what size do we need to split MEMORY.md into sections? (Start with 5KB limit)

3. **Cross-scope promotion:** Should patterns that appear in multiple agent memories get promoted to user level? (Probably yes, future work)

4. **Forgetting:** When do we remove memory? Time-based decay? Explicit "forget" command?

---

## Summary

This spec builds a memory layer on top of comms:

1. **New analysis types** extract memory-relevant facets from conversations
2. **Scope assignment** routes facets to workspace/user/agent level
3. **Synthesis pipeline** compacts and generalizes facets into MEMORY.md
4. **Background execution** via heartbeat/cron keeps memory fresh

The result: agents have access to evolving, structured memory about the workspace, user, and their own task-specific learnings — all derived automatically from conversation history.
