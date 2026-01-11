-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

-- Events: All communication events across channels
CREATE TABLE IF NOT EXISTS events (
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

CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_channel ON events(channel);
CREATE INDEX IF NOT EXISTS idx_events_thread ON events(thread_id);

-- Persons: People with unified identity
CREATE TABLE IF NOT EXISTS persons (
    id TEXT PRIMARY KEY,
    canonical_name TEXT NOT NULL,
    display_name TEXT,
    is_me INTEGER DEFAULT 0,
    relationship_type TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_persons_is_me ON persons(is_me);
CREATE INDEX IF NOT EXISTS idx_persons_canonical_name ON persons(canonical_name);

-- Identities: Identifiers (phone, email, handle) linked to persons
CREATE TABLE IF NOT EXISTS identities (
    id TEXT PRIMARY KEY,
    person_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    channel TEXT NOT NULL,
    identifier TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(channel, identifier)
);

CREATE INDEX IF NOT EXISTS idx_identities_person ON identities(person_id);
CREATE INDEX IF NOT EXISTS idx_identities_identifier ON identities(channel, identifier);

-- Event Participants: Who was involved in each event
CREATE TABLE IF NOT EXISTS event_participants (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    role TEXT NOT NULL,  -- sender, recipient, cc, observer
    PRIMARY KEY (event_id, person_id, role)
);

CREATE INDEX IF NOT EXISTS idx_event_participants_event ON event_participants(event_id);
CREATE INDEX IF NOT EXISTS idx_event_participants_person ON event_participants(person_id);

-- Tags: Soft tags on events for categorization
CREATE TABLE IF NOT EXISTS tags (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    tag_type TEXT NOT NULL,    -- topic, entity, emotion, project, context
    value TEXT NOT NULL,
    confidence REAL,
    source TEXT NOT NULL,      -- user, analysis
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tags_event ON tags(event_id);
CREATE INDEX IF NOT EXISTS idx_tags_type_value ON tags(tag_type, value);

-- Sync watermarks: Track last sync per adapter
CREATE TABLE IF NOT EXISTS sync_watermarks (
    adapter TEXT PRIMARY KEY,
    last_sync_at INTEGER NOT NULL,
    last_event_id TEXT
);

-- Insert initial schema version
INSERT OR IGNORE INTO schema_version (version, applied_at)
VALUES (1, strftime('%s', 'now'));
