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
3. Register in adapter factory
4. Add `comms connect <name>` support

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

## Schema Quick Reference

```sql
events          -- All communication events
persons         -- People (one has is_me=1)
identities      -- Phone/email/handle -> person
event_participants  -- Who was in each event
tags            -- Soft tags on events
```

See `internal/db/schema.sql` for full DDL.
