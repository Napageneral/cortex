package chunk

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Chunker defines the interface for conversation chunking strategies
type Chunker interface {
	// Chunk creates conversations based on the strategy
	Chunk(ctx context.Context, db *sql.DB, definitionID string) (ChunkResult, error)
}

// ChunkResult tracks the outcome of a chunking operation
type ChunkResult struct {
	ConversationsCreated int
	EventsProcessed      int
	Duration             time.Duration
}

// Event represents a minimal event for chunking
type Event struct {
	ID        string
	Timestamp int64
	ThreadID  string
	Channel   string
}

// TimeGapConfig defines configuration for time-gap chunking
type TimeGapConfig struct {
	GapSeconds int64  `json:"gap_seconds"` // Time gap in seconds
	Scope      string `json:"scope"`       // "thread" or "channel"
}

// TimeGapChunker implements time-gap based conversation chunking
type TimeGapChunker struct {
	config TimeGapConfig
}

// NewTimeGapChunker creates a new time-gap chunker
func NewTimeGapChunker(config TimeGapConfig) *TimeGapChunker {
	return &TimeGapChunker{config: config}
}

// Chunk implements the Chunker interface for time-gap chunking
func (c *TimeGapChunker) Chunk(ctx context.Context, db *sql.DB, definitionID string) (ChunkResult, error) {
	startTime := time.Now()
	result := ChunkResult{}

	// Get definition details to determine scope
	var defName, channel string
	err := db.QueryRowContext(ctx, `
		SELECT name, channel FROM conversation_definitions WHERE id = ?
	`, definitionID).Scan(&defName, &channel)
	if err != nil {
		return result, fmt.Errorf("failed to fetch definition: %w", err)
	}

	// Query events based on scope
	var query string
	var args []interface{}

	if c.config.Scope == "thread" {
		// Group by thread_id
		if channel != "" {
			query = `
				SELECT id, timestamp, thread_id, channel
				FROM events
				WHERE channel = ? AND thread_id IS NOT NULL
				ORDER BY thread_id, timestamp ASC
			`
			args = []interface{}{channel}
		} else {
			query = `
				SELECT id, timestamp, thread_id, channel
				FROM events
				WHERE thread_id IS NOT NULL
				ORDER BY thread_id, timestamp ASC
			`
		}
	} else {
		// Group by channel only
		if channel != "" {
			query = `
				SELECT id, timestamp, thread_id, channel
				FROM events
				WHERE channel = ?
				ORDER BY timestamp ASC
			`
			args = []interface{}{channel}
		} else {
			query = `
				SELECT id, timestamp, thread_id, channel
				FROM events
				ORDER BY timestamp ASC
			`
		}
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	// Group events by thread (or globally if scope is channel)
	eventsByGroup := make(map[string][]Event)

	for rows.Next() {
		var e Event
		var threadID sql.NullString

		err := rows.Scan(&e.ID, &e.Timestamp, &threadID, &e.Channel)
		if err != nil {
			return result, fmt.Errorf("failed to scan event: %w", err)
		}

		if threadID.Valid {
			e.ThreadID = threadID.String
		}

		// Group key depends on scope
		groupKey := "global"
		if c.config.Scope == "thread" && e.ThreadID != "" {
			groupKey = e.ThreadID
		} else if c.config.Scope == "channel" {
			groupKey = e.Channel
		}

		eventsByGroup[groupKey] = append(eventsByGroup[groupKey], e)
		result.EventsProcessed++
	}

	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("error iterating events: %w", err)
	}

	// Process each group and create conversations based on time gaps
	for groupKey, events := range eventsByGroup {
		if len(events) == 0 {
			continue
		}

		// Split events into conversations based on time gaps
		conversations := c.splitByTimeGap(events)

		// Insert conversations into database
		for _, conv := range conversations {
			err := c.insertConversation(ctx, db, definitionID, groupKey, conv, channel)
			if err != nil {
				return result, fmt.Errorf("failed to insert conversation: %w", err)
			}
			result.ConversationsCreated++
		}
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// conversation represents a chunked group of events
type conversation struct {
	events        []Event
	startTime     int64
	endTime       int64
	threadID      string
	channel       string
}

// splitByTimeGap splits events into conversations based on time gaps
func (c *TimeGapChunker) splitByTimeGap(events []Event) []conversation {
	if len(events) == 0 {
		return nil
	}

	conversations := []conversation{}
	currentConv := conversation{
		events:    []Event{events[0]},
		startTime: events[0].Timestamp,
		endTime:   events[0].Timestamp,
		threadID:  events[0].ThreadID,
		channel:   events[0].Channel,
	}

	for i := 1; i < len(events); i++ {
		timeSinceLastEvent := events[i].Timestamp - currentConv.endTime

		if timeSinceLastEvent > c.config.GapSeconds {
			// Gap exceeded, finalize current conversation and start new one
			conversations = append(conversations, currentConv)
			currentConv = conversation{
				events:    []Event{events[i]},
				startTime: events[i].Timestamp,
				endTime:   events[i].Timestamp,
				threadID:  events[i].ThreadID,
				channel:   events[i].Channel,
			}
		} else {
			// Continue current conversation
			currentConv.events = append(currentConv.events, events[i])
			currentConv.endTime = events[i].Timestamp
		}
	}

	// Don't forget the last conversation
	conversations = append(conversations, currentConv)
	return conversations
}

// insertConversation inserts a conversation and its event mappings
func (c *TimeGapChunker) insertConversation(ctx context.Context, db *sql.DB, definitionID, groupKey string, conv conversation, scopeChannel string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	conversationID := uuid.New().String()
	now := time.Now().Unix()

	// Determine thread_id and channel values for the conversation record
	var threadIDValue interface{} = nil
	if conv.threadID != "" && c.config.Scope == "thread" {
		threadIDValue = conv.threadID
	}

	var channelValue interface{} = nil
	if scopeChannel != "" {
		channelValue = scopeChannel
	} else if conv.channel != "" {
		channelValue = conv.channel
	}

	// Insert conversation
	_, err = tx.ExecContext(ctx, `
		INSERT INTO conversations (
			id, definition_id, channel, thread_id,
			start_time, end_time, event_count,
			first_event_id, last_event_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, conversationID, definitionID, channelValue, threadIDValue,
		conv.startTime, conv.endTime, len(conv.events),
		conv.events[0].ID, conv.events[len(conv.events)-1].ID, now)

	if err != nil {
		return fmt.Errorf("failed to insert conversation: %w", err)
	}

	// Insert conversation_events mappings
	for position, event := range conv.events {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO conversation_events (conversation_id, event_id, position)
			VALUES (?, ?, ?)
		`, conversationID, event.ID, position+1) // position is 1-indexed

		if err != nil {
			return fmt.Errorf("failed to insert conversation_event mapping: %w", err)
		}
	}

	return tx.Commit()
}

// CreateDefinition creates a conversation definition in the database
func CreateDefinition(ctx context.Context, db *sql.DB, name, channel, strategy string, config interface{}, description string) (string, error) {
	// Check if definition already exists
	var existingID string
	err := db.QueryRowContext(ctx, "SELECT id FROM conversation_definitions WHERE name = ?", name).Scan(&existingID)
	if err == nil {
		// Definition already exists
		return existingID, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("failed to check for existing definition: %w", err)
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}

	definitionID := uuid.New().String()
	now := time.Now().Unix()

	var channelValue interface{} = nil
	if channel != "" {
		channelValue = channel
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO conversation_definitions (
			id, name, channel, strategy, config_json, description, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, definitionID, name, channelValue, strategy, string(configJSON), description, now, now)

	if err != nil {
		return "", fmt.Errorf("failed to insert definition: %w", err)
	}

	return definitionID, nil
}

// ThreadConfig defines configuration for thread-based chunking
type ThreadConfig struct {
	// No additional config needed - one conversation per thread_id
}

// ThreadChunker implements thread-based conversation chunking
// Each unique thread_id becomes one conversation
type ThreadChunker struct {
	config ThreadConfig
}

// NewThreadChunker creates a new thread-based chunker
func NewThreadChunker(config ThreadConfig) *ThreadChunker {
	return &ThreadChunker{config: config}
}

// Chunk implements the Chunker interface for thread-based chunking
func (c *ThreadChunker) Chunk(ctx context.Context, db *sql.DB, definitionID string) (ChunkResult, error) {
	startTime := time.Now()
	result := ChunkResult{}

	// Get definition details
	var defName, channel string
	err := db.QueryRowContext(ctx, `
		SELECT name, channel FROM conversation_definitions WHERE id = ?
	`, definitionID).Scan(&defName, &channel)
	if err != nil {
		return result, fmt.Errorf("failed to fetch definition: %w", err)
	}

	// Query events based on channel scope
	var query string
	var args []interface{}

	if channel != "" {
		query = `
			SELECT id, timestamp, thread_id, channel
			FROM events
			WHERE channel = ? AND thread_id IS NOT NULL
			ORDER BY thread_id, timestamp ASC
		`
		args = []interface{}{channel}
	} else {
		query = `
			SELECT id, timestamp, thread_id, channel
			FROM events
			WHERE thread_id IS NOT NULL
			ORDER BY thread_id, timestamp ASC
		`
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	// Group events by thread_id
	eventsByThread := make(map[string][]Event)

	for rows.Next() {
		var e Event
		var threadID sql.NullString

		err := rows.Scan(&e.ID, &e.Timestamp, &threadID, &e.Channel)
		if err != nil {
			return result, fmt.Errorf("failed to scan event: %w", err)
		}

		if threadID.Valid {
			e.ThreadID = threadID.String
		}

		if e.ThreadID != "" {
			eventsByThread[e.ThreadID] = append(eventsByThread[e.ThreadID], e)
			result.EventsProcessed++
		}
	}

	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("error iterating events: %w", err)
	}

	// Create one conversation per thread
	for threadID, events := range eventsByThread {
		if len(events) == 0 {
			continue
		}

		conv := conversation{
			events:    events,
			startTime: events[0].Timestamp,
			endTime:   events[len(events)-1].Timestamp,
			threadID:  threadID,
			channel:   events[0].Channel,
		}

		err := c.insertConversation(ctx, db, definitionID, threadID, conv, channel)
		if err != nil {
			return result, fmt.Errorf("failed to insert conversation: %w", err)
		}
		result.ConversationsCreated++
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// insertConversation inserts a conversation and its event mappings
func (c *ThreadChunker) insertConversation(ctx context.Context, db *sql.DB, definitionID, threadID string, conv conversation, scopeChannel string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	conversationID := uuid.New().String()
	now := time.Now().Unix()

	// Thread-based chunking always has a thread_id
	var channelValue interface{} = nil
	if scopeChannel != "" {
		channelValue = scopeChannel
	} else if conv.channel != "" {
		channelValue = conv.channel
	}

	// Insert conversation
	_, err = tx.ExecContext(ctx, `
		INSERT INTO conversations (
			id, definition_id, channel, thread_id,
			start_time, end_time, event_count,
			first_event_id, last_event_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, conversationID, definitionID, channelValue, threadID,
		conv.startTime, conv.endTime, len(conv.events),
		conv.events[0].ID, conv.events[len(conv.events)-1].ID, now)

	if err != nil {
		return fmt.Errorf("failed to insert conversation: %w", err)
	}

	// Insert conversation_events mappings
	for position, event := range conv.events {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO conversation_events (conversation_id, event_id, position)
			VALUES (?, ?, ?)
		`, conversationID, event.ID, position+1) // position is 1-indexed

		if err != nil {
			return fmt.Errorf("failed to insert conversation_event mapping: %w", err)
		}
	}

	return tx.Commit()
}

// GetChunkerForDefinition creates a chunker instance for a given definition
func GetChunkerForDefinition(ctx context.Context, db *sql.DB, definitionID string) (Chunker, error) {
	var strategy, configJSON string
	err := db.QueryRowContext(ctx, `
		SELECT strategy, config_json FROM conversation_definitions WHERE id = ?
	`, definitionID).Scan(&strategy, &configJSON)

	if err != nil {
		return nil, fmt.Errorf("failed to fetch definition: %w", err)
	}

	switch strategy {
	case "time_gap":
		var config TimeGapConfig
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal time_gap config: %w", err)
		}
		return NewTimeGapChunker(config), nil
	case "thread":
		var config ThreadConfig
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal thread config: %w", err)
		}
		return NewThreadChunker(config), nil
	default:
		return nil, fmt.Errorf("unsupported strategy: %s", strategy)
	}
}

// ListDefinitions lists all conversation definitions
func ListDefinitions(ctx context.Context, db *sql.DB) ([]Definition, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, channel, strategy, config_json, description, created_at, updated_at
		FROM conversation_definitions
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query definitions: %w", err)
	}
	defer rows.Close()

	definitions := []Definition{}
	for rows.Next() {
		var d Definition
		var channel sql.NullString

		err := rows.Scan(&d.ID, &d.Name, &channel, &d.Strategy, &d.ConfigJSON, &d.Description, &d.CreatedAt, &d.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan definition: %w", err)
		}

		if channel.Valid {
			d.Channel = channel.String
		}

		definitions = append(definitions, d)
	}

	return definitions, rows.Err()
}

// Definition represents a conversation definition
type Definition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Channel     string `json:"channel,omitempty"`
	Strategy    string `json:"strategy"`
	ConfigJSON  string `json:"config_json"`
	Description string `json:"description,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}
