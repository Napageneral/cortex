# 📡 Comms — Unified Communications Cartographer

A unified communications intelligence layer that aggregates, normalizes, and indexes your communications across all channels (iMessage, Gmail, Slack, X DMs, AI sessions, etc.) into a single queryable event store.

## The Problem

Your communications are fragmented across channels:
- iMessage threads
- Gmail conversations  
- Slack workspaces
- X/Twitter DMs
- AI chat sessions (Cursor, ChatGPT)
- Phone calls / meeting transcripts

Each has its own format, storage, and access patterns. You can't easily ask:
- "What did Dad and I talk about across all channels last month?"
- "Show me everything related to the HTAA project"
- "Who have I communicated with most this year?"

## The Solution

Comms provides:
1. **Unified Event Store** — All communications normalized into a single schema
2. **Identity Resolution** — Union-find structure to link identities across channels
3. **Multi-Channel Adapters** — Eve (iMessage), gogcli (Gmail), aix (AI sessions), etc.
4. **Flexible Querying** — Slice by person, time, channel, topic, or any combination
5. **Insight Foundation** — The data layer that powers your personal CRM

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│                     Insight Layer (Markdown)                    │
│  User-defined structure: home/people/, home/timeline/, etc.    │
├────────────────────────────────────────────────────────────────┤
│                     Comms CLI (this project)                    │
│  Orchestrates adapters, owns event store, provides queries     │
├────────────────────────────────────────────────────────────────┤
│                     Unified Event Store (SQLite)                │
│  events, persons, identities, tags tables                       │
├────────────────────────────────────────────────────────────────┤
│                     Channel Adapters                            │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐           │
│  │   Eve   │  │ gogcli  │  │   aix   │  │  ...    │           │
│  │(iMessage)│  │ (Gmail) │  │(Cursor) │  │         │           │
│  └─────────┘  └─────────┘  └─────────┘  └─────────┘           │
└────────────────────────────────────────────────────────────────┘
```

## Quick Start

```bash
# Initialize comms
comms init

# Configure your identity
comms me set --name "Tyler Brandt" --phone "+17072876731" --email "tnapathy@gmail.com"

# Connect adapters
comms connect imessage    # Uses Eve
comms connect gmail       # Uses gogcli (requires OAuth setup)

# Sync all channels
comms sync

# Query your communications
comms events --person "Dad" --since "2025-01-01"
comms people --top 20
comms timeline 2026-01
```

## Commands

### Setup

| Command | Description |
|---------|-------------|
| `comms init` | Initialize config and event store |
| `comms me set` | Configure your identity |
| `comms connect <channel>` | Configure an adapter |
| `comms adapters` | List configured adapters |

### Sync

| Command | Description |
|---------|-------------|
| `comms sync` | Sync all connected adapters |
| `comms sync --adapter imessage` | Sync specific adapter |
| `comms sync --full` | Full repopulation |

### Query

| Command | Description |
|---------|-------------|
| `comms events` | Query events with filters |
| `comms people` | List/search people |
| `comms people <name>` | Show person details |
| `comms timeline <period>` | Events in time period |
| `comms db query <sql>` | Raw SQL access |

### Identity Management

| Command | Description |
|---------|-------------|
| `comms identify` | List all people + identities |
| `comms identify --merge <p1> <p2>` | Union two people |
| `comms identify --add <person> --email <email>` | Add identity |

### Tags

| Command | Description |
|---------|-------------|
| `comms tag list` | List all tags |
| `comms tag add --filter <filter> --tag <tag>` | Apply tag to events |

## Event Schema

```sql
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    timestamp INTEGER NOT NULL,
    channel TEXT NOT NULL,
    content_types TEXT NOT NULL,  -- JSON array: ["text"], ["text", "image"]
    content TEXT,
    direction TEXT NOT NULL,      -- sent, received, observed
    thread_id TEXT,
    reply_to TEXT,
    source_adapter TEXT NOT NULL,
    source_id TEXT NOT NULL,
    UNIQUE(source_adapter, source_id)
);

CREATE TABLE persons (
    id TEXT PRIMARY KEY,
    canonical_name TEXT NOT NULL,
    display_name TEXT,
    is_me INTEGER DEFAULT 0,
    relationship_type TEXT
);

CREATE TABLE identities (
    id TEXT PRIMARY KEY,
    person_id TEXT NOT NULL REFERENCES persons(id),
    channel TEXT NOT NULL,
    identifier TEXT NOT NULL,
    UNIQUE(channel, identifier)
);

CREATE TABLE event_participants (
    event_id TEXT NOT NULL REFERENCES events(id),
    person_id TEXT NOT NULL REFERENCES persons(id),
    role TEXT NOT NULL,  -- sender, recipient, cc, observer
    PRIMARY KEY (event_id, person_id, role)
);

CREATE TABLE tags (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id),
    tag_type TEXT NOT NULL,    -- topic, entity, emotion, project, context
    value TEXT NOT NULL,
    confidence REAL,
    source TEXT NOT NULL       -- user, analysis
);
```

## Configuration

Config: `~/.config/comms/config.yaml` (git-tracked in Nexus)

```yaml
me:
  canonical_name: "Tyler Brandt"
  identities:
    - channel: imessage
      identifier: "+17072876731"
    - channel: email  
      identifier: "tnapathy@gmail.com"

adapters:
  imessage:
    type: eve
    enabled: true
  gmail:
    type: gogcli
    enabled: true
    account: tnapathy@gmail.com
```

Data: `~/Library/Application Support/Comms/comms.db`

## Adapters

### iMessage (via Eve)

```bash
# Prerequisite: Eve installed and synced
brew install Napageneral/tap/eve
eve init && eve sync

# Connect
comms connect imessage
```

### Gmail (via gogcli)

```bash
# Prerequisite: gogcli installed and authenticated
brew install steipete/tap/gogcli
gog auth add tnapathy@gmail.com

# Connect
comms connect gmail --account tnapathy@gmail.com
```

### AI Sessions (via aix)

```bash
# Prerequisite: aix installed and synced
# Connect
comms connect cursor
```

## Development

```bash
cd comms
go mod tidy
make build
./comms init
```

## License

MIT
