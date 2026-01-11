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

## Schema Quick Reference

```sql
events          -- All communication events
persons         -- People (one has is_me=1)
identities      -- Phone/email/handle -> person
event_participants  -- Who was in each event
tags            -- Soft tags on events
```

See `internal/db/schema.sql` for full DDL.
