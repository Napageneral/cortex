# AGENTS.md — Comms

> Context for AI agents working on this codebase.

## What This Project Is

Comms is a **unified communications cartographer** — a CLI that aggregates communications from multiple channels (iMessage, Gmail, Slack, AI sessions) into a single SQLite event store with identity resolution.

It's the data layer for a personal CRM. Users query their unified communications; agents can then write insights to markdown files.

## Architecture

```
comms CLI
    ├── cmd/comms/main.go     # Cobra CLI entry point
    └── internal/
        ├── config/           # YAML config handling
        ├── db/               # SQLite database + schema
        ├── adapters/         # Channel adapters (eve, gmail, etc.)
        ├── sync/             # Sync orchestration
        └── query/            # Query building
```

## Key Design Decisions

1. **Pure Go SQLite** — Use `modernc.org/sqlite` (no CGO) for portability
2. **Adapters call external CLIs** — Eve, gogcli are separate tools; we parse their output
3. **Union-find for identities** — Same person across channels linked via identities table
4. **Events are immutable** — Once synced, events don't change (except tags)
5. **Tags are soft** — Can be user-applied or AI-discovered, with confidence scores

## File Locations

- Config: `~/.config/comms/config.yaml`
- Database: `~/Library/Application Support/Comms/comms.db`
- Eve DB (read-only): `~/Library/Application Support/Eve/eve.db`

## Common Operations

### Adding a new command

1. Add cobra command in `cmd/comms/main.go`
2. Implement logic in appropriate `internal/` package
3. Support `--json` flag for machine output (use Result structs with OK bool)
4. Use transaction pattern for multi-step database operations
5. Update SKILL.md with usage

Example pattern for commands:
```go
type Result struct {
    OK      bool   `json:"ok"`
    Message string `json:"message,omitempty"`
    // ... domain-specific fields
}

database, err := db.Open()
if err != nil {
    // handle error
}
defer database.Close()

// Perform operations...

if jsonOutput {
    printJSON(result)
} else {
    fmt.Println("✓ Success message")
}
```

### Adding a new adapter

1. Create `internal/adapters/<name>.go`
2. Implement `Adapter` interface:
   ```go
   type Adapter interface {
       Name() string
       Sync(ctx context.Context, db *sql.DB, full bool) (SyncResult, error)
   }
   ```
3. Add `comms connect <name>` command in main.go
4. Store config in config.yaml via `config.Load()` and `cfg.Save()`
5. Add status check in `checkAdapterStatus()` function
6. Add adapter instantiation in `internal/sync/sync.go` syncAdapter() function

### Running sync

```bash
# Sync all enabled adapters
comms sync

# Sync specific adapter
comms sync --adapter imessage

# Force full re-sync (ignore watermarks)
comms sync --full

# JSON output
comms sync --json
```

### Adding tags

```bash
# List all tags
comms tag list

# Filter tags by type
comms tag list --type project

# Tag a specific event
comms tag add --event <event-id> --tag project:htaa

# Bulk tag events by person
comms tag add --person "Dane" --tag context:business

# Bulk tag with multiple filters
comms tag add --channel imessage --since 2026-01-01 --tag topic:planning
```

### Raw SQL queries

```bash
# Read-only queries (default)
comms db query "SELECT COUNT(*) FROM events"
comms db query "SELECT * FROM persons LIMIT 10"

# Mutation queries (requires --write flag)
comms db query --write "UPDATE persons SET display_name = 'Dad' WHERE canonical_name = 'Father'"

# JSON output for programmatic access
comms db query --json "SELECT channel, COUNT(*) as count FROM events GROUP BY channel"
```

### Running tests

```bash
go test ./...
```

### Building

```bash
make build
./comms version
```

## Common Operations

### Chunking conversations

```bash
# Seed default conversation definitions (creates imessage_3hr and gmail_thread)
comms chunk seed

# List conversation definitions
comms chunk list

# Run chunking for a definition
comms chunk run imessage_3hr       # Time-gap chunking for iMessage (3-hour gaps)
comms chunk run gmail_thread       # Thread-based chunking for Gmail
comms chunk run --definition gmail_thread

# JSON output
comms chunk list --json
comms chunk run imessage_3hr --json
```

## Gotchas

- Eve's `eve.db` is the warehouse, `eve-queue.db` is the job queue — we only read `eve.db`
- gogcli requires authentication first: `gog auth add email@example.com`
- Identity merging is destructive — updates all event_participants references
- Use `//go:embed` directive to embed schema.sql into the binary
- SQLite PRAGMA foreign_keys must be enabled on each connection
- XDG_CONFIG_HOME defaults to ~/.config, XDG_DATA_HOME defaults to ~/.local/share on Linux
- macOS uses ~/Library/Application Support instead of XDG for data
- Only one person can have is_me=1 in the persons table
- Use transactions when performing multiple related database operations
- UUIDs from google/uuid package for generating IDs
- Always defer tx.Rollback() after beginning a transaction (safe even if committed)
- Adapter configuration is stored in config.yaml and persisted via config.Save()
- Use os.Stat() to check if external files/databases exist before configuring adapters
- Provide helpful error messages with setup instructions when prerequisites are missing
- When opening external databases (like Eve), use read-only mode: `file:path?mode=ro`
- Eve schema: contacts/contact_identifiers for people, messages/chats for events
- Use `ON CONFLICT DO UPDATE` for upsert operations in SQLite
- Sync watermarks enable incremental sync by tracking last_sync_at timestamp per adapter
- Sync command orchestrates all adapters via internal/sync package
- One adapter failing during sync doesn't stop others from running
- Use context.Background() for sync operations (can be enhanced later with timeouts)
- Identity merge operation: cannot merge 'me' person into another person (must be target)
- When merging persons, handle duplicate event_participants with ON CONFLICT DO NOTHING
- GetPersonByName matches both canonical_name and display_name for flexibility
- Search uses LIKE with LOWER() for case-insensitive fuzzy matching
- Join with event_participants to show event count per person (useful for sorting)
- Custom identifiers support 'channel:identifier' format (e.g., 'slack:U123456')
- Query building: Use dynamic SQL with conditions based on provided filters
- Date parsing: Use time.Parse with layout "2006-01-02" for YYYY-MM-DD format
- Query filters are combinable - all provided filters are ANDed together
- Use DISTINCT in SELECT when joining with event_participants to avoid duplicate events
- Load related data (participants) in separate queries to avoid N+1 issues at display layer
- Truncate long content in text output (200 chars) for readability
- People command reuses identify package functions (ListAll, Search, GetPersonByName)
- Support both list mode (no args) and detail mode (person name arg) in people command
- --top N flag limits results to top N by event count (already sorted by identify.ListAll)
- Format identities inline for list view (channel:identifier), detailed for detail view
- Timeline queries use DATE(timestamp, 'unixepoch', 'localtime') for day grouping in SQLite
- Timeline supports flexible date parsing: YYYY (year), YYYY-MM (month), YYYY-MM-DD (day)
- Time range calculation: Use time.Date with AddDate for precise start/end boundaries
- Week range: Calculate Monday of current week (Sunday = 7 for weekday calculation)
- Timeline aggregation: Group events by date, then by sender/channel/direction for statistics
- Map data structures for timeline stats enable easy aggregation and display
- Tag types are enumerated: topic, entity, emotion, project, context
- Tags use format 'type:value' (e.g., 'project:htaa', 'context:business')
- Bulk tagging uses same filter pattern as events query (person, channel, since, until)
- Tags can have confidence scores for analysis-discovered tags (0.0-1.0)
- Tag source tracks origin: 'user' for manual tags, 'analysis' for AI-discovered tags
- Duplicate tag detection prevents same tag being added to same event multiple times
- DB query command: default read-only, use --write flag for mutations (INSERT, UPDATE, DELETE)
- Query mutation detection checks uppercase SQL for keywords: INSERT, UPDATE, DELETE, DROP, CREATE, ALTER, TRUNCATE
- DB query results returned as []map[string]interface{} for flexible handling of dynamic schemas
- Convert []byte values to strings when scanning SQL results for text fields
- Use rows.Columns() to get column names dynamically for any query
- Text output formats results as tab-separated table with row count
- Gmail adapter calls gogcli via exec.Command with --json flag for structured output
- Gmail sync uses 'after:YYYY/MM/DD' query syntax for incremental sync based on watermark
- Gmail message timestamps are in milliseconds, divide by 1000 to get Unix seconds
- Email participant parsing handles both "Name <email>" and plain "email" formats
- Gmail threads contain multiple messages, each message synced as separate event
- Gmail direction determined by SENT label in message.LabelIDs array
- Gmail body extraction recursively searches payload.Parts for text/plain or text/html
- Email addresses stored as identities with channel='email', used for person resolution
- Type assertions required when reading config.Options map[string]interface{}
- exec.LookPath checks if command is available in PATH before creating adapter
- Schema evolution: Use CREATE TABLE IF NOT EXISTS for safe migrations on existing databases
- Schema versioning: INSERT OR IGNORE won't update existing version rows, but new installations get latest version
- Threads table provides generic container abstraction for chats, email threads, channels, and sessions
- Thread membership is NOT stored at thread level - derived from event_participants per-event (handles dynamic membership)
- Attachments table stores media metadata with ON DELETE CASCADE - deleting event auto-deletes attachments
- Attachment storage_uri supports multiple backends: file://, s3://, https:// URLs
- Attachment content_hash enables deduplication - same file attached to multiple events
- Attachment media_type (image/video/audio/document/sticker/link) provides queryable categorization
- Attachment metadata_json stores format-specific data (dimensions, duration) without schema changes
- Person facts table stores identity graph data with category/fact_type/fact_value structure
- Person facts include confidence scores, source attribution, and evidence quotes
- Person facts use UNIQUE constraint on (person_id, category, fact_type, fact_value) to prevent duplicates
- Hard identifier facts flagged with is_hard_identifier=1 for O(F) collision detection
- Conditional indexes (WHERE clause) enable efficient hard identifier queries without full table scan
- Schema version managed via INSERT OR IGNORE - only applies on fresh installations
- Conversation abstraction: events are immutable, conversations are views over events via mapping table
- Conversations can span multiple threads/channels (channel=NULL, thread_id=NULL for cross-boundary convos)
- conversation_definitions define chunking strategy: time_gap, thread, session, daily, persona_pair, custom
- config_json stores strategy-specific parameters (e.g., gap_seconds for time_gap)
- conversation_events.position tracks order within conversation (1-indexed)
- Same events can belong to multiple conversations with different definitions (e.g., 90min vs 3hr gaps)
- Events do NOT have conversation_id FK - use conversation_events mapping for flexibility
- Analysis framework: analysis_types define LLM-based analyses, analysis_runs track execution, facets store extracted values
- Analysis types support two output modes: structured (extracts to facets table) and freeform (stores in output_text)
- facets_config_json defines extraction rules using json_path for structured analyses
- Facets are queryable values extracted from LLM output: entities, topics, emotions, PII, etc.
- No raw LLM JSON stored - facets ARE the parsed output (reconstructable if needed)
- UNIQUE(analysis_type_id, conversation_id) ensures one analysis run per conversation per type
- Facets have CASCADE delete on analysis_run_id and conversation_id for cleanup
- Embeddings table: stores vector embeddings for any entity (event, conversation, facet, person, thread)
- Embeddings indexed by (entity_type, entity_id, model) for fast lookup and deduplication
- embedding_blob stores binary vector as little-endian float64 array
- source_text_hash enables change detection for re-embedding when source changes
- Multiple embedding models can coexist: same entity embedded with different models stored separately
- Eve adapter syncs threads from chats table before syncing messages
- Thread ID format: adapter_name:source_id (e.g., "imessage:chat123")
- Thread name prefers chat_name from Eve, falls back to chat_identifier
- Events already linked to threads via thread_id field (no schema change needed)
- SyncResult includes ThreadsCreated and ThreadsUpdated counts for tracking
- Eve adapter syncs attachment metadata from Eve attachments table
- Attachment ID format: adapter_name:guid (e.g., "imessage:att123")
- Attachment media_type derived from mime_type: image/video/audio/document/sticker
- Attachment storage_uri uses eve:// scheme as placeholder (actual file path not in Eve)
- Attachment metadata_json stores uti and is_sticker flag from Eve
- deriveMediaType function categorizes mime_types into queryable media_type enum
- SyncResult includes AttachmentsCreated and AttachmentsUpdated counts for tracking
- Attachment sync happens after messages sync, uses same watermark for incremental sync
- Chunking strategies: time_gap (gaps in time), thread (thread boundaries), session, daily, persona_pair, custom
- TimeGapChunker supports two scopes: "thread" (chunk within each thread) or "channel" (chunk across all events)
- ThreadChunker creates one conversation per unique thread_id - perfect for Gmail with native threading
- Conversations are created through conversation_definitions that specify strategy and config
- chunk.CreateDefinition is idempotent - returns existing definition ID if name already exists
- Chunker interface allows pluggable strategies - implement Chunk(ctx, db, definitionID) method
- conversation_events.position is 1-indexed for ordering events within a conversation
- Same events can belong to multiple conversations with different chunking definitions
- Conversations can span multiple threads/channels by setting thread_id=NULL and channel=NULL
- Time gap measured in seconds - 10800 = 3 hours, 5400 = 90 minutes
- Events are queried in timestamp order and grouped by thread (or globally) before chunking
- Each conversation transaction inserts conversation record and all conversation_events mappings atomically
- Thread-based chunking requires events to have thread_id set - filters WHERE thread_id IS NOT NULL
- GetChunkerForDefinition uses strategy field to instantiate correct chunker type (time_gap, thread, etc.)
- Person facts use category/fact_type/fact_value structure for flexible PII storage
- Fact constants defined in internal/identify/facts.go: FactTypeEmailPersonal, FactTypePhoneMobile, etc.
- HardIdentifiers slice lists all fact types that trigger immediate merge consideration
- SoftIdentifierWeights map defines weight for each soft identifier (0.15-0.25 range)
- InsertFact uses ON CONFLICT to handle duplicates - updates confidence if higher
- FindFactCollisions implements O(F) collision detection via GROUP BY fact_value HAVING COUNT > 1
- Facts have three boolean flags: is_sensitive, is_identifier, is_hard_identifier
- isIdentifierType returns true for both hard identifiers and soft identifiers (those with weights)
- Analysis types seeded via INSERT OR IGNORE in schema.sql for idempotent registration
- pii_extraction_v1 registered as structured analysis type with 27 facet extraction rules
- facets_config_json maps JSON paths ($.persons[*].pii.category.field.value) to fact types
- Prompt templates use {conversation_text} placeholder for dynamic content injection
- gemini-2.0-flash model configured for cost-effective PII extraction
- Single quotes in SQL strings require escaping with double quotes ('')
- Analysis framework enables LLM-based extraction without custom parsing code
- Facet-to-fact sync: SyncFacetsToPersonFacts processes completed pii_extraction runs
- FacetToFactMapping maps 27 facet types to (category, fact_type) pairs
- Two sync paths: facet-based (from facets table) and direct JSON (ProcessPIIExtractionOutput)
- Confidence mapping: high=0.9, medium=0.7, low=0.4 from LLM output to numeric scores
- Source type detection: self_disclosed, mentioned, inferred, extracted
- Unattributed facts created when facet person_id is NULL (ambiguous attribution)
- Third-party persons auto-created from new_identity_candidates in extraction output
- Person lookup: primary contact via identities JOIN, non-primary via fuzzy name LIKE match
- Evidence strings combined with semicolons when multiple evidence pieces exist
- isSensitiveFactType flags SSN, passport, drivers_license as is_sensitive=1
- SyncStats tracks: facets processed, facts created/updated, unattributed created, third parties created

## Schema Quick Reference

```sql
events          -- All communication events
persons         -- People (one has is_me=1)
identities      -- Phone/email/handle -> person
event_participants  -- Who was in each event
threads         -- Chat/channel/thread metadata
attachments     -- Media/file metadata for events
tags            -- Soft tags on events
person_facts    -- Rich identity graph data (PII extraction results)
unattributed_facts  -- Ambiguous data that couldn't be attributed (resolvable later)
merge_events    -- Identity merge proposals and execution tracking
conversation_definitions  -- HOW to chunk events into conversations
conversations   -- Chunked groups of events (time-gap, thread-based, etc.)
conversation_events  -- Mapping table: which events belong to which conversations
analysis_types  -- Analysis definitions (prompt, output schema, facet extraction rules)
analysis_runs   -- Execution tracking per (analysis_type, conversation) pair
facets          -- Extracted queryable values from structured analyses
embeddings      -- Vector embeddings for entities (events, conversations, facets, persons, threads)
```

See `internal/db/schema.sql` for full DDL.
