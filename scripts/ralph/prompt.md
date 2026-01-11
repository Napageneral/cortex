# Ralph Agent Instructions — Comms

## Context

You are building `comms`, a unified communications cartographer CLI in Go. It aggregates communications from multiple channels (iMessage, Gmail, Slack, AI sessions) into a single SQLite event store with identity resolution.

## Key Files

- `scripts/ralph/prd.json` — User stories with acceptance criteria
- `scripts/ralph/progress.txt` — Learnings and patterns (READ THIS FIRST)
- `AGENTS.md` — Codebase patterns for this project
- `README.md` — Project overview and schema

## Your Task

1. Read `scripts/ralph/prd.json`
2. Read `scripts/ralph/progress.txt` (especially Codebase Patterns at top)
3. Pick the highest priority story where `passes: false`
4. Implement that ONE story
5. Run `go build ./cmd/comms` to verify it compiles
6. Run `go test ./...` if tests exist
7. Update AGENTS.md with learnings about this codebase
8. Commit: `feat: [US-XXX] - [Title]`
9. Update prd.json: set `passes: true` for completed story
10. Append learnings to progress.txt

## Code Patterns

### Directory Structure
```
comms/
├── cmd/comms/main.go          # CLI entry point (cobra commands)
├── internal/
│   ├── config/config.go       # Config loading/saving
│   ├── db/
│   │   ├── db.go              # Database connection/setup
│   │   ├── schema.sql         # DDL statements
│   │   └── queries.go         # Query functions
│   ├── adapters/
│   │   ├── adapter.go         # Adapter interface
│   │   ├── eve.go             # iMessage adapter (reads Eve db)
│   │   └── gmail.go           # Gmail adapter (calls gogcli)
│   ├── sync/sync.go           # Sync orchestration
│   └── query/query.go         # Query building/execution
├── go.mod
├── Makefile
└── SKILL.md
```

### Go Patterns

```go
// All commands output JSON when --json flag set
type Result struct {
    OK      bool   `json:"ok"`
    Message string `json:"message,omitempty"`
    // ... domain-specific fields
}

// Use modernc.org/sqlite for pure-Go SQLite (no CGO)
import "modernc.org/sqlite"

// XDG-compliant paths
func getConfigDir() string {
    if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
        return filepath.Join(xdg, "comms")
    }
    return filepath.Join(os.UserHomeDir(), ".config", "comms")
}

func getDataDir() string {
    if runtime.GOOS == "darwin" {
        return filepath.Join(os.UserHomeDir(), "Library", "Application Support", "Comms")
    }
    return filepath.Join(os.UserHomeDir(), ".local", "share", "comms")
}
```

### Schema Reference

See README.md for full schema. Key tables:
- `events` — All communication events
- `persons` — People (one has is_me=true)
- `identities` — Phone/email/handle linked to person
- `event_participants` — Who was in each event
- `tags` — Soft tags on events

### Adapter Interface

```go
type Adapter interface {
    Name() string
    Sync(ctx context.Context, db *sql.DB, full bool) (SyncResult, error)
}

type SyncResult struct {
    EventsCreated int
    EventsUpdated int
    PersonsCreated int
    Duration time.Duration
}
```

## Progress Format

APPEND to progress.txt after each story:

```markdown
---
## [Date] - [US-XXX] [Title]
- What was implemented
- Files changed
- **Learnings:**
  - Patterns discovered
  - Gotchas encountered
```

Add reusable patterns to TOP of progress.txt in Codebase Patterns section.

## Stop Condition

If ALL stories in prd.json have `passes: true`, reply:
```
<promise>COMPLETE</promise>
```

Otherwise, end normally after completing one story.

## Critical Rules

1. **ONE story per iteration** — Do not implement multiple stories
2. **Build must pass** — `go build ./cmd/comms` must succeed before committing
3. **No placeholders** — Full implementations only
4. **Update progress.txt** — Capture learnings for future iterations
5. **Update AGENTS.md** — Document codebase patterns
6. **Commit message format** — `feat: [US-XXX] - [Title]`
