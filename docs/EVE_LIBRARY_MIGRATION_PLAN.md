# Eve Library Migration Plan

## Overview

Convert Eve from a standalone application with its own database (`eve.db`) into a stateless Go library that Comms imports directly. This eliminates the intermediate database and aligns iMessage sync with the stateless adapter pattern.

## Current State

```
┌─────────────────────────────────────────────────────────────────┐
│                     CURRENT ARCHITECTURE                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│   chat.db ──→ Eve ETL ──→ eve.db ──→ Comms Eve Adapter          │
│   (Apple)    (writes)    (dupe!)    (reads, writes comms.db)    │
│                                                                 │
│   Problems:                                                     │
│   • Data duplicated in eve.db                                   │
│   • Two-hop sync (chat.db → eve.db → comms.db)                 │
│   • Eve maintains state that Comms also maintains              │
│   • Inconsistent with Gmail/AIX adapter patterns               │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Target State

```
┌─────────────────────────────────────────────────────────────────┐
│                      TARGET ARCHITECTURE                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│   chat.db ──→ eve/imessage package ──→ comms.db                │
│   (Apple)    (stateless library)      (single DB)              │
│                                                                 │
│   Benefits:                                                     │
│   • No duplicate data                                           │
│   • Single-hop sync (chat.db → comms.db)                       │
│   • Zero JSON/CLI overhead                                      │
│   • Same 10-second full ETL performance                        │
│   • Sub-second incremental updates                             │
│   • Consistent adapter pattern                                  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## Component Changes

### 1. Eve Project (`~/nexus/home/projects/eve/`)

#### New: `imessage/` Package (Importable Library)

```
eve/
├── cmd/eve/main.go          # CLI (keep for debugging/standalone use)
├── imessage/                 # NEW: Importable package
│   ├── chatdb.go            # ChatDB reader (from internal/etl/chatdb.go)
│   ├── sync.go              # Main sync orchestration
│   ├── messages.go          # Message extraction
│   ├── handles.go           # Handle/contact extraction  
│   ├── chats.go             # Chat/thread extraction
│   ├── attachments.go       # Attachment extraction
│   ├── reactions.go         # Reaction extraction (NEW - from messages)
│   ├── content.go           # Content decoding (attributedBody, etc.)
│   ├── addressbook.go       # AddressBook hydration
│   └── types.go             # Shared types
├── internal/                 # Keep for CLI-specific code
└── go.mod
```

#### Package API Design

```go
// eve/imessage/types.go
package imessage

import "time"

// SyncResult contains statistics from a sync operation
type SyncResult struct {
    HandlesSynced      int
    ChatsSynced        int
    MessagesSynced     int
    AttachmentsSynced  int
    ReactionsSynced    int
    MaxMessageRowID    int64
    Duration           time.Duration
}

// SyncOptions configures sync behavior
type SyncOptions struct {
    SinceRowID    int64  // Watermark for incremental sync (0 = full)
    IncludeReactions bool // Whether to extract reactions
    HydrateNames     bool // Whether to hydrate from AddressBook
}

// ChatDB provides read-only access to Apple's chat.db
type ChatDB struct {
    db *sql.DB
}
```

```go
// eve/imessage/sync.go
package imessage

import (
    "context"
    "database/sql"
)

// GetChatDBPath returns the default chat.db location
func GetChatDBPath() string

// OpenChatDB opens chat.db with read-only optimized pragmas
func OpenChatDB(path string) (*ChatDB, error)

// Close closes the chat.db connection
func (c *ChatDB) Close() error

// Sync reads from chat.db and writes directly to commsDB
// This is the main entry point for Comms adapter
func Sync(ctx context.Context, chatDB *ChatDB, commsDB *sql.DB, opts SyncOptions) (*SyncResult, error)

// SyncHandles extracts handles from chat.db → comms persons/identities
func SyncHandles(ctx context.Context, chatDB *ChatDB, commsDB *sql.DB) (int, error)

// SyncChats extracts chats from chat.db → comms threads
func SyncChats(ctx context.Context, chatDB *ChatDB, commsDB *sql.DB) (int, error)

// SyncMessages extracts messages from chat.db → comms events
func SyncMessages(ctx context.Context, chatDB *ChatDB, commsDB *sql.DB, sinceRowID int64) (int, int64, error)

// SyncAttachments extracts attachments from chat.db → comms attachments
func SyncAttachments(ctx context.Context, chatDB *ChatDB, commsDB *sql.DB, sinceRowID int64) (int, error)

// SyncReactions extracts reactions from chat.db → comms events (content_type=reaction)
func SyncReactions(ctx context.Context, chatDB *ChatDB, commsDB *sql.DB, sinceRowID int64) (int, error)
```

#### Key Implementation Notes

**Reactions**: In chat.db, reactions are stored as messages with:
- `type` in range 2000-2005 (love, like, dislike, laugh, emphasis, question)
- `associated_message_guid` pointing to the original message

```go
// eve/imessage/reactions.go
func (c *ChatDB) GetReactions(sinceRowID int64) ([]Reaction, error) {
    query := `
        SELECT
            m.ROWID,
            m.guid,
            m.associated_message_guid,
            m.handle_id,
            m.date,
            m.is_from_me,
            m.type as reaction_type,
            cmj.chat_id,
            c.chat_identifier
        FROM message m
        INNER JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
        INNER JOIN chat c ON c.ROWID = cmj.chat_id
        WHERE m.ROWID > ?
          AND m.type >= 2000 AND m.type <= 2005
          AND m.associated_message_guid IS NOT NULL
        ORDER BY m.ROWID
    `
    // ...
}
```

**Writing to Comms Schema**: The sync functions write directly to Comms tables:
- `handles` → `persons` + `identities`
- `chats` → `threads`
- `messages` → `events` + `event_participants`
- `attachments` → `attachments`
- `reactions` → `events` (with `content_types: ["reaction"]`)

---

### 2. Comms Project (`~/nexus/home/projects/comms/`)

#### Replace: `internal/adapters/eve.go` → `internal/adapters/imessage.go`

```go
// comms/internal/adapters/imessage.go
package adapters

import (
    "context"
    "database/sql"
    "time"

    "github.com/tyler/eve/imessage"  // Import Eve as library
)

// IMessageAdapter syncs iMessage events directly from chat.db
type IMessageAdapter struct {
    chatDBPath string
}

func NewIMessageAdapter() (*IMessageAdapter, error) {
    path := imessage.GetChatDBPath()
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return nil, fmt.Errorf("chat.db not found at %s (Full Disk Access required)", path)
    }
    return &IMessageAdapter{chatDBPath: path}, nil
}

func (a *IMessageAdapter) Name() string {
    return "imessage"
}

func (a *IMessageAdapter) Sync(ctx context.Context, commsDB *sql.DB, full bool) (SyncResult, error) {
    startTime := time.Now()
    result := SyncResult{Perf: map[string]string{}}

    // Open chat.db via Eve library
    chatDB, err := imessage.OpenChatDB(a.chatDBPath)
    if err != nil {
        return result, fmt.Errorf("failed to open chat.db: %w", err)
    }
    defer chatDB.Close()

    // Get watermark for incremental sync
    var sinceRowID int64
    if !full {
        row := commsDB.QueryRow(
            "SELECT COALESCE(MAX(CAST(source_id AS INTEGER)), 0) FROM events WHERE source_adapter = ?",
            a.Name(),
        )
        row.Scan(&sinceRowID)
    }

    // Call Eve library directly - no JSON, no CLI, no IPC
    opts := imessage.SyncOptions{
        SinceRowID:       sinceRowID,
        IncludeReactions: true,
        HydrateNames:     true,
    }
    
    syncResult, err := imessage.Sync(ctx, chatDB, commsDB, opts)
    if err != nil {
        return result, err
    }

    // Map Eve result to Comms result
    result.PersonsCreated = syncResult.HandlesSynced
    result.ThreadsCreated = syncResult.ChatsSynced
    result.EventsCreated = syncResult.MessagesSynced + syncResult.ReactionsSynced
    result.AttachmentsCreated = syncResult.AttachmentsSynced
    result.Duration = time.Since(startTime)
    result.Perf["total"] = result.Duration.String()

    return result, nil
}
```

#### New: Watch Mode Support

```go
// comms/internal/watch/watcher.go
package watch

import (
    "context"
    "log"
    "time"

    "github.com/fsnotify/fsnotify"
    "github.com/tyler/eve/imessage"
)

// ChatDBWatcher watches for changes to chat.db and triggers sync
type ChatDBWatcher struct {
    chatDBPath string
    onChange   func()
    watcher    *fsnotify.Watcher
}

func NewChatDBWatcher(onChange func()) (*ChatDBWatcher, error) {
    path := imessage.GetChatDBPath()
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, err
    }
    
    // Watch the Messages directory (chat.db + WAL files)
    dir := filepath.Dir(path)
    if err := watcher.Add(dir); err != nil {
        return nil, err
    }
    
    return &ChatDBWatcher{
        chatDBPath: path,
        onChange:   onChange,
        watcher:    watcher,
    }, nil
}

func (w *ChatDBWatcher) Start(ctx context.Context) {
    // Debounce rapid changes
    var debounceTimer *time.Timer
    
    for {
        select {
        case <-ctx.Done():
            return
        case event := <-w.watcher.Events:
            if strings.Contains(event.Name, "chat.db") {
                if debounceTimer != nil {
                    debounceTimer.Stop()
                }
                debounceTimer = time.AfterFunc(500*time.Millisecond, w.onChange)
            }
        case err := <-w.watcher.Errors:
            log.Printf("watch error: %v", err)
        }
    }
}
```

#### CLI Integration

```go
// In comms/cmd/comms/main.go

// Add watch command
watchCmd := &cobra.Command{
    Use:   "watch",
    Short: "Watch for new messages and sync incrementally",
    Run: func(cmd *cobra.Command, args []string) {
        adapter, _ := adapters.NewIMessageAdapter()
        
        syncFn := func() {
            result, err := adapter.Sync(ctx, database, false)
            if err != nil {
                log.Printf("sync error: %v", err)
                return
            }
            if result.EventsCreated > 0 {
                fmt.Printf("Synced %d new events\n", result.EventsCreated)
            }
        }
        
        // Initial sync
        syncFn()
        
        // Watch for changes
        watcher, _ := watch.NewChatDBWatcher(syncFn)
        watcher.Start(ctx)
    },
}
```

---

## Migration Steps

### Phase 1: Create Eve Library Package (Eve repo)

```
□ 1.1 Create eve/imessage/ directory structure
□ 1.2 Move/refactor internal/etl/chatdb.go → imessage/chatdb.go
□ 1.3 Move/refactor internal/etl/handles.go → imessage/handles.go  
□ 1.4 Move/refactor internal/etl/chats.go → imessage/chats.go
□ 1.5 Move/refactor internal/etl/messages.go → imessage/messages.go
□ 1.6 Move/refactor internal/etl/attachments.go → imessage/attachments.go
□ 1.7 Move/refactor internal/etl/content.go → imessage/content.go
□ 1.8 Move/refactor internal/etl/addressbook.go → imessage/addressbook.go
□ 1.9 Create imessage/reactions.go (extract reactions from messages)
□ 1.10 Create imessage/sync.go (main entry point writing to Comms schema)
□ 1.11 Create imessage/types.go (exported types)
□ 1.12 Update go.mod to make package importable
□ 1.13 Write unit tests for each component
```

### Phase 2: Update Comms to Use Eve Library

```
□ 2.1 Add Eve as Go module dependency in comms/go.mod
□ 2.2 Create internal/adapters/imessage.go (new adapter using Eve library)
□ 2.3 Update sync orchestration to use new adapter
□ 2.4 Implement watch mode (fsnotify)
□ 2.5 Add `comms watch` CLI command
□ 2.6 Write integration tests
```

### Phase 3: Deprecate Old Path

```
□ 3.1 Mark internal/adapters/eve.go as deprecated
□ 3.2 Update documentation
□ 3.3 Remove eve.db creation from Eve
□ 3.4 Update Eve CLI to use chat.db directly for queries
□ 3.5 Clean up unused Eve internal/etl code
□ 3.6 Delete internal/adapters/eve.go after validation period
```

### Phase 4: Cleanup

```
□ 4.1 Remove eve.db file from system
□ 4.2 Remove Eve warehouse schema/migrations
□ 4.3 Final documentation update
```

---

## Testing Strategy

### Unit Tests (Eve imessage package)

```go
// eve/imessage/sync_test.go
func TestSyncHandles(t *testing.T) {
    // Create test chat.db with known handles
    // Create empty comms.db
    // Call SyncHandles
    // Verify persons/identities created correctly
}

func TestSyncMessages(t *testing.T) {
    // Create test chat.db with known messages
    // Call SyncMessages
    // Verify events created with correct mapping
}

func TestSyncReactions(t *testing.T) {
    // Create test chat.db with reaction messages (type 2000-2005)
    // Call SyncReactions
    // Verify events created with content_types: ["reaction"]
}

func TestIncrementalSync(t *testing.T) {
    // Sync once (full)
    // Add new messages to chat.db
    // Sync again with watermark
    // Verify only new messages synced
}
```

### Integration Tests (Comms)

```go
// comms/internal/adapters/imessage_test.go
func TestIMessageAdapterFullSync(t *testing.T) {
    // Use real chat.db (or test copy)
    // Run full sync
    // Verify events, threads, persons created
}

func TestIMessageAdapterIncrementalSync(t *testing.T) {
    // Run full sync
    // Note counts
    // Run incremental sync
    // Verify no duplicates, correct watermark handling
}
```

### Performance Benchmarks

```go
// eve/imessage/sync_bench_test.go
func BenchmarkFullSync(b *testing.B) {
    // Measure full sync time
    // Compare to old Eve ETL (target: same or faster)
}

func BenchmarkIncrementalSync(b *testing.B) {
    // Measure incremental sync with 0-10 new messages
    // Target: < 100ms
}
```

### Manual Testing Checklist

```
□ Full sync on fresh comms.db (compare message counts to chat.db)
□ Incremental sync (send message, verify appears in comms.db)
□ Reactions sync (react to message, verify reaction event created)
□ Attachments sync (send image, verify attachment record)
□ Group chats (verify thread and participants)
□ Contact names (verify AddressBook hydration works)
□ Watch mode (send message, verify auto-sync within 1 second)
□ Performance: full sync < 15 seconds for 100k messages
□ Performance: incremental sync < 100ms
```

---

## Rollback Plan

If issues are discovered:

1. **Comms**: Revert to `internal/adapters/eve.go` (reading from eve.db)
2. **Eve**: Keep eve.db sync working in parallel during transition
3. **Both adapters can coexist** during testing period

---

## Success Criteria

- [ ] Full sync performance: ≤ 15 seconds (same as current Eve)
- [ ] Incremental sync performance: < 100ms for ≤10 new messages
- [ ] Watch mode latency: < 1 second from message sent to comms.db
- [ ] Zero data loss: all messages, reactions, attachments synced
- [ ] No eve.db: Eve operates statelessly
- [ ] Clean separation: Eve is an importable library

---

## Timeline Estimate

| Phase | Effort | Dependencies |
|-------|--------|--------------|
| Phase 1: Eve Library | 4-6 hours | None |
| Phase 2: Comms Integration | 2-3 hours | Phase 1 |
| Phase 3: Deprecation | 1 hour | Phase 2 validated |
| Phase 4: Cleanup | 30 min | Phase 3 complete |
| **Total** | **8-10 hours** | |

---

## Open Questions

1. **Eve CLI queries**: Should Eve CLI commands like `eve search` query chat.db directly or call into Comms?
   - Recommendation: Query chat.db directly for speed, but could optionally query comms.db for unified search

2. **Whoami**: Currently Eve has `eve whoami` that reads from AddressBook. Should this become part of the imessage package?
   - Recommendation: Yes, expose as `imessage.GetWhoami()` 

3. **Conversation chunking**: Currently done in Comms. Keep it there or move to Eve?
   - Recommendation: Keep in Comms (it's channel-agnostic)

4. **Error handling**: What happens if chat.db is locked by Messages.app?
   - Recommendation: Retry with backoff, same as current Eve behavior
