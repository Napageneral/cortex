---
name: comms
description: Unified communications cartographer - aggregates communications across all channels into a single queryable event store
homepage: https://github.com/Napageneral/comms
metadata: {"nexus":{"emoji":"📡","os":["darwin","linux"],"requires":{"bins":["comms"]},"install":[{"id":"brew","kind":"brew","formula":"Napageneral/tap/comms","bins":["comms"],"label":"Install via Homebrew"},{"id":"go","kind":"shell","script":"go install github.com/Napageneral/comms/cmd/comms@latest","bins":["comms"],"label":"Install via Go"}]}}
---

# Comms — Unified Communications Cartographer

Comms aggregates your communications across all channels (iMessage, Gmail, Slack, AI sessions, etc.) into a single queryable event store with identity resolution.

## Why Comms?

Your communications are fragmented:
- iMessage threads with some people
- Email conversations with others
- Slack for work
- AI chat sessions in Cursor

Comms unifies them into one data layer, so you can ask:
- "What did I discuss with Dad across ALL channels?"
- "Show me everything related to the HTAA project"
- "Who have I communicated with most this year?"

## Quick Start

```bash
# Initialize
comms init

# Configure your identity
comms me set --name "Tyler Brandt" --phone "+17072876731" --email "tnapathy@gmail.com"

# Connect adapters (requires Eve and gogcli installed)
comms connect imessage
comms connect gmail --account tnapathy@gmail.com

# Sync all channels
comms sync

# Query
comms events --person "Dad" --since "2025-01-01"
comms people --top 20
```

## Commands

### Setup

| Command | Description |
|---------|-------------|
| `comms init` | Initialize config and event store |
| `comms me set --name "..." --phone "..." --email "..."` | Configure your identity |
| `comms connect <adapter>` | Configure a channel adapter |
| `comms adapters` | List configured adapters |

### Sync

| Command | Description |
|---------|-------------|
| `comms sync` | Sync all enabled adapters |
| `comms sync --adapter imessage` | Sync specific adapter |
| `comms sync --full` | Force full re-sync |

### Query

| Command | Description |
|---------|-------------|
| `comms events` | List events with filters |
| `comms events --person "Dad"` | Filter by person |
| `comms events --channel imessage` | Filter by channel |
| `comms events --since 2025-01-01` | Filter by date |
| `comms people` | List all people |
| `comms people --top 20` | Top contacts by event count |
| `comms people "Dad"` | Show person details |
| `comms timeline 2026-01` | Events in time period |
| `comms timeline --today` | Today's events |
| `comms db query <sql>` | Raw SQL access |

### Identity Management

| Command | Description |
|---------|-------------|
| `comms identify` | List all people + identities |
| `comms identify --merge "Person A" "Person B"` | Merge two people |
| `comms identify --add "Dad" --email "dad@example.com"` | Add identity |

### Tags

| Command | Description |
|---------|-------------|
| `comms tag list` | List all tags |
| `comms tag add --event <id> --tag "project:htaa"` | Tag an event |
| `comms tag add --filter "person:Dane" --tag "context:business"` | Bulk tag |

## Adapters

### iMessage (via Eve)

Prerequisites:
```bash
brew install Napageneral/tap/eve
eve init && eve sync
```

Connect:
```bash
comms connect imessage
```

### Gmail (via gogcli)

Prerequisites:
```bash
brew install steipete/tap/gogcli
gog auth add your@gmail.com
```

Connect:
```bash
comms connect gmail --account your@gmail.com
```

### AI Sessions (via aix)

Connect:
```bash
comms connect cursor
```

## Output Formats

All commands support `--json` / `-j`:

```bash
comms events --json | jq '.events[] | select(.channel == "imessage")'
comms people --top 10 --json
```

## Configuration

Config: `~/.config/comms/config.yaml`

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

## Bootstrap (for AI agents)

```bash
# Check if installed
which comms && comms version

# Install
brew install Napageneral/tap/comms
# OR: go install github.com/Napageneral/comms/cmd/comms@latest

# Setup
comms init

# Configure identity
comms me set --name "User Name" --email "user@example.com"

# Connect adapters (assumes Eve/gogcli already set up)
comms connect imessage
comms connect gmail --account user@gmail.com

# Sync
comms sync

# Verify
comms db query "SELECT COUNT(*) as count FROM events"
comms people --top 5
```

## Event Schema

Events have these core properties:
- `id` — Unique identifier
- `timestamp` — When it happened
- `channel` — imessage, gmail, slack, cursor, etc.
- `content_types` — ["text"], ["text", "image"], etc.
- `direction` — sent, received, observed
- `participants` — People involved (resolved via identity)

Queryable via:
```bash
comms db query "SELECT * FROM events WHERE channel = 'imessage' LIMIT 10"
```

## Tips for Agents

1. Use `comms people --top 10` to understand who the user communicates with most
2. Use `comms events --person "Name"` to get context on a relationship
3. Use `comms timeline --today` for recent activity
4. Filter by channel to focus on specific contexts
5. Use `--json` output for programmatic access
6. Raw SQL via `comms db query` for complex queries

Example agent workflow:
```bash
# "Tell me about my communication with Dad"
comms people "Dad"                              # Get identity info
comms events --person "Dad" --since 2025-01-01  # Recent events
comms db query "SELECT channel, COUNT(*) FROM events e 
  JOIN event_participants ep ON e.id = ep.event_id 
  JOIN persons p ON ep.person_id = p.id 
  WHERE p.display_name = 'Dad' 
  GROUP BY channel"                             # Channel breakdown
```
