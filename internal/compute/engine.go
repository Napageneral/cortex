package compute

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Napageneral/comms/internal/gemini"
	"github.com/Napageneral/taskengine/engine"
	"github.com/Napageneral/taskengine/queue"
	"github.com/google/uuid"
)

const (
	JobTypeAnalysis  = "analysis"
	JobTypeEmbedding = "embedding"
)

// Engine wraps the taskengine with comms-specific handlers and adaptive control
type Engine struct {
	db           *sql.DB
	geminiClient *gemini.Client
	queue        *queue.Queue
	engine       *engine.Engine
	writer       *engine.TxBatchWriter
	metrics      *JobMetrics

	analysisModel  string
	embeddingModel string

	// Adaptive control components
	sem               *AdaptiveSemaphore
	adaptiveCtrl      *AdaptiveController
	analysisRPMCtrl   *AutoRPMController
	embedRPMCtrl      *AutoRPMController
	cancelControllers context.CancelFunc

	// Embedding batcher for high-throughput batch API calls
	embeddingBatcher *EmbeddingsBatcher

	// Pre-encoded conversation cache for high-throughput bulk processing
	// Maps conversation_id -> encoded text
	convTextCache   map[string]string
	convTextCacheMu sync.RWMutex
}

// Config for the compute engine
type Config struct {
	WorkerCount    int
	AnalysisModel  string
	EmbeddingModel string
	UseBatchWriter bool // Enable TxBatchWriter for better write performance
	BatchSize      int

	// RPM settings (0 = auto-probe)
	AnalysisRPM int
	EmbedRPM    int
}

// DefaultConfig returns sensible defaults optimized for high-throughput processing
// Matching ChatStats parallelism settings for Tier-3 API keys
func DefaultConfig() Config {
	return Config{
		// 50 workers to match ChatStats ThreadPoolExecutor(max_workers=50)
		// With Tier-3 Gemini keys, this saturates the API nicely
		WorkerCount: 50,
		// Comms defaults (per project policy):
		// - Analysis: Gemini 3 Flash Preview
		// - Embeddings: Gemini Embedding 001
		AnalysisModel:  "gemini-3-flash-preview",
		EmbeddingModel: "gemini-embedding-001",
		UseBatchWriter: true, // Enable by default
		BatchSize:      25,
		AnalysisRPM:    0, // 0 = auto-probe
		EmbedRPM:       0, // 0 = auto-probe
	}
}

// NewEngine creates a compute engine for comms with adaptive control
func NewEngine(db *sql.DB, geminiClient *gemini.Client, cfg Config) (*Engine, error) {
	// Initialize the job queue schema
	if err := queue.Init(db); err != nil {
		return nil, fmt.Errorf("init queue schema: %w", err)
	}

	q := queue.New(db)

	engineCfg := engine.DefaultConfig()
	engineCfg.WorkerCount = cfg.WorkerCount
	engineCfg.LeaseOwner = "comms-compute"

	e := &Engine{
		db:             db,
		geminiClient:   geminiClient,
		queue:          q,
		engine:         engine.New(q, engineCfg),
		metrics:        NewJobMetrics(),
		analysisModel:  cfg.AnalysisModel,
		embeddingModel: cfg.EmbeddingModel,
	}

	// Initialize TxBatchWriter if enabled
	if cfg.UseBatchWriter {
		batchSize := cfg.BatchSize
		if batchSize <= 0 {
			batchSize = 25
		}
		e.writer = engine.NewTxBatchWriter(db, engine.TxBatchWriterConfig{
			BatchSize:     batchSize,
			FlushInterval: 100 * time.Millisecond,
		})
		e.writer.Start()
	}

	// Setup RPM rate limiting
	// If RPM is explicitly set (non-zero), use fixed RPM.
	// If 0, we'll set up auto-probe controllers in Run().
	if cfg.AnalysisRPM > 0 {
		geminiClient.SetAnalysisRPM(cfg.AnalysisRPM)
	}
	if cfg.EmbedRPM > 0 {
		geminiClient.SetEmbedRPM(cfg.EmbedRPM)
	}

	// Create adaptive semaphore for in-flight control
	e.sem = NewAdaptiveSemaphore(cfg.WorkerCount)

	// Create adaptive controller
	e.adaptiveCtrl = NewAdaptiveController(e.sem, DefaultAdaptiveControllerConfig(cfg.WorkerCount))

	// Create auto-RPM controllers if not using fixed RPM
	if cfg.AnalysisRPM <= 0 {
		e.analysisRPMCtrl = NewAutoRPMController(DefaultAutoRPMConfig(), geminiClient.SetAnalysisRPM)
	}
	if cfg.EmbedRPM <= 0 {
		e.embedRPMCtrl = NewAutoRPMController(DefaultAutoRPMConfig(), geminiClient.SetEmbedRPM)
	}

	// Create embedding batcher for high-throughput batch API calls (100 embeddings per request)
	e.embeddingBatcher = NewEmbeddingsBatcher(geminiClient, cfg.EmbeddingModel)

	// Register wrapped handlers with adaptive control
	e.engine.RegisterHandler(JobTypeAnalysis, e.wrapHandler(e.handleAnalysisJob, JobTypeAnalysis))
	e.engine.RegisterHandler(JobTypeEmbedding, e.wrapHandler(e.handleEmbeddingJob, JobTypeEmbedding))

	return e, nil
}

// wrapHandler wraps a job handler with adaptive control (semaphore + observation)
func (e *Engine) wrapHandler(base func(context.Context, *queue.Job) error, jobType string) func(context.Context, *queue.Job) error {
	return func(ctx context.Context, job *queue.Job) error {
		// Acquire semaphore (respects adaptive concurrency limit)
		if err := e.sem.Acquire(ctx); err != nil {
			return err
		}
		defer e.sem.Release()

		start := time.Now()
		err := base(ctx, job)
		elapsed := time.Since(start)

		// Feed the adaptive controller
		e.adaptiveCtrl.Observe(elapsed, err)

		// Feed the RPM controllers by job type
		switch jobType {
		case JobTypeAnalysis:
			if e.analysisRPMCtrl != nil {
				e.analysisRPMCtrl.Observe(err)
			}
		case JobTypeEmbedding:
			if e.embedRPMCtrl != nil {
				e.embedRPMCtrl.Observe(err)
			}
		}

		return err
	}
}

// Close shuts down the engine gracefully
func (e *Engine) Close() error {
	// Stop controllers
	if e.cancelControllers != nil {
		e.cancelControllers()
	}
	// Close embedding batcher (flushes pending)
	if e.embeddingBatcher != nil {
		e.embeddingBatcher.Close()
	}
	if e.writer != nil {
		return e.writer.Close()
	}
	return nil
}

// JobMetrics returns the job metrics collector
func (e *Engine) JobMetrics() *JobMetrics {
	return e.metrics
}

// Run starts the compute engine and processes jobs until done or context cancelled
func (e *Engine) Run(ctx context.Context) (*engine.Stats, error) {
	// Create a cancellable context for the controllers
	ctrlCtx, cancel := context.WithCancel(ctx)
	e.cancelControllers = cancel

	// Start the adaptive controller
	e.adaptiveCtrl.Start(ctrlCtx)

	// Start RPM auto-controllers
	if e.analysisRPMCtrl != nil {
		e.analysisRPMCtrl.Start(ctrlCtx)
	}
	if e.embedRPMCtrl != nil {
		e.embedRPMCtrl.Start(ctrlCtx)
	}

	return e.engine.Run(ctx)
}

// ControllerStats returns snapshots of all controller states
func (e *Engine) ControllerStats() map[string]any {
	stats := make(map[string]any)
	stats["adaptive_controller"] = json.RawMessage(e.adaptiveCtrl.SnapshotJSON())
	if e.analysisRPMCtrl != nil {
		stats["analysis_rpm_controller"] = json.RawMessage(e.analysisRPMCtrl.SnapshotJSON())
	}
	if e.embedRPMCtrl != nil {
		stats["embed_rpm_controller"] = json.RawMessage(e.embedRPMCtrl.SnapshotJSON())
	}
	if e.embeddingBatcher != nil {
		stats["embedding_batcher"] = e.embeddingBatcher.Metrics()
	}
	return stats
}

// EffectiveRPM returns the current effective RPM for analysis and embedding
func (e *Engine) EffectiveRPM() (analysisRPM, embedRPM int) {
	if e.analysisRPMCtrl != nil {
		analysisRPM = e.analysisRPMCtrl.CurrentRPM()
	}
	if e.embedRPMCtrl != nil {
		embedRPM = e.embedRPMCtrl.CurrentRPM()
	}
	return
}

// PreloadConversations pre-encodes all conversation texts into memory cache.
// This eliminates per-job DB reads during bulk processing, matching ChatStats'
// pre-encoding strategy for maximum throughput.
// Call this before Run() for best results with large batches.
func (e *Engine) PreloadConversations(ctx context.Context) (int, error) {
	log.Printf("[preload] Starting conversation pre-encoding...")
	start := time.Now()

	// Get all conversation IDs
	rows, err := e.db.QueryContext(ctx, `SELECT id FROM conversations`)
	if err != nil {
		return 0, fmt.Errorf("query conversations: %w", err)
	}

	var convIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		convIDs = append(convIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	if len(convIDs) == 0 {
		return 0, nil
	}

	// Pre-encode all conversations
	cache := make(map[string]string, len(convIDs))
	for _, convID := range convIDs {
		text, err := e.buildConversationText(ctx, convID)
		if err != nil {
			log.Printf("[preload] Warning: failed to encode conversation %s: %v", convID, err)
			continue
		}
		cache[convID] = text
	}

	// Swap in the cache atomically
	e.convTextCacheMu.Lock()
	e.convTextCache = cache
	e.convTextCacheMu.Unlock()

	elapsed := time.Since(start)
	log.Printf("[preload] Pre-encoded %d conversations in %v (%.1f/sec)",
		len(cache), elapsed, float64(len(cache))/elapsed.Seconds())

	return len(cache), nil
}

// ClearConversationCache clears the pre-encoded conversation cache
func (e *Engine) ClearConversationCache() {
	e.convTextCacheMu.Lock()
	e.convTextCache = nil
	e.convTextCacheMu.Unlock()
}

// getConversationTextCached returns cached conversation text if available
func (e *Engine) getConversationTextCached(convID string) (string, bool) {
	e.convTextCacheMu.RLock()
	defer e.convTextCacheMu.RUnlock()
	if e.convTextCache == nil {
		return "", false
	}
	text, ok := e.convTextCache[convID]
	return text, ok
}

// QueueStats returns current queue statistics
func (e *Engine) QueueStats() (*queue.Stats, error) {
	return e.queue.GetStats()
}

// EnqueueAnalysis queues analysis jobs for all un-analyzed conversations
func (e *Engine) EnqueueAnalysis(ctx context.Context, analysisTypeName string, conversationIDs ...string) (int, error) {
	// Get the analysis type
	var analysisTypeID string
	err := e.db.QueryRowContext(ctx, `
		SELECT id FROM analysis_types WHERE name = ?
	`, analysisTypeName).Scan(&analysisTypeID)
	if err != nil {
		return 0, fmt.Errorf("analysis type not found: %w", err)
	}

	var convIDs []string

	// If specific conversation IDs provided, use those (already filtered by caller)
	if len(conversationIDs) > 0 {
		convIDs = conversationIDs
	} else {
		// Find conversations without analysis runs for this type
		// Collect all IDs first, then close rows before enqueueing (SQLite deadlock avoidance)
		rows, err := e.db.QueryContext(ctx, `
			SELECT c.id FROM conversations c
			WHERE NOT EXISTS (
				SELECT 1 FROM analysis_runs ar
				WHERE ar.conversation_id = c.id
				AND ar.analysis_type_id = ?
			)
		`, analysisTypeID)
		if err != nil {
			return 0, fmt.Errorf("query conversations: %w", err)
		}

		for rows.Next() {
			var convID string
			if err := rows.Scan(&convID); err != nil {
				rows.Close()
				return 0, err
			}
			convIDs = append(convIDs, convID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, err
		}
		rows.Close()
	}

	// Now enqueue (rows is closed, no deadlock)
	count := 0
	for _, convID := range convIDs {
		payload := AnalysisJobPayload{
			ConversationID: convID,
			AnalysisTypeID: analysisTypeID,
		}

		if err := e.queue.Enqueue(queue.EnqueueOptions{
			Type:    JobTypeAnalysis,
			Key:     fmt.Sprintf("analysis:%s:%s", analysisTypeID, convID),
			Payload: payload,
		}); err != nil {
			log.Printf("failed to enqueue analysis for %s: %v", convID, err)
			continue
		}
		count++
	}

	return count, nil
}

// EnqueueEmbeddings queues embedding jobs for all un-embedded conversations
func (e *Engine) EnqueueEmbeddings(ctx context.Context) (int, error) {
	// Find conversations without embeddings
	// Collect IDs first, close rows, then enqueue (SQLite deadlock avoidance)
	rows, err := e.db.QueryContext(ctx, `
		SELECT c.id FROM conversations c
		WHERE NOT EXISTS (
			SELECT 1 FROM embeddings em
			WHERE em.entity_type = 'conversation'
			AND em.entity_id = c.id
		)
	`)
	if err != nil {
		return 0, fmt.Errorf("query conversations: %w", err)
	}

	var convIDs []string
	for rows.Next() {
		var convID string
		if err := rows.Scan(&convID); err != nil {
			rows.Close()
			return 0, err
		}
		convIDs = append(convIDs, convID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	// Now enqueue (rows is closed, no deadlock)
	count := 0
	for _, convID := range convIDs {
		payload := EmbeddingJobPayload{
			EntityType: "conversation",
			EntityID:   convID,
		}

		if err := e.queue.Enqueue(queue.EnqueueOptions{
			Type:    JobTypeEmbedding,
			Key:     fmt.Sprintf("embedding:conversation:%s", convID),
			Payload: payload,
		}); err != nil {
			log.Printf("failed to enqueue embedding for %s: %v", convID, err)
			continue
		}
		count++
	}

	return count, nil
}

// EnqueueFacetEmbeddings queues embedding jobs for all un-embedded facets
func (e *Engine) EnqueueFacetEmbeddings(ctx context.Context) (int, error) {
	// Find facets without embeddings
	rows, err := e.db.QueryContext(ctx, `
		SELECT f.id FROM facets f
		WHERE NOT EXISTS (
			SELECT 1 FROM embeddings em
			WHERE em.entity_type = 'facet'
			AND em.entity_id = f.id
		)
	`)
	if err != nil {
		return 0, fmt.Errorf("query facets: %w", err)
	}

	var facetIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		facetIDs = append(facetIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	count := 0
	for _, id := range facetIDs {
		payload := EmbeddingJobPayload{
			EntityType: "facet",
			EntityID:   id,
		}

		if err := e.queue.Enqueue(queue.EnqueueOptions{
			Type:    JobTypeEmbedding,
			Key:     fmt.Sprintf("embedding:facet:%s", id),
			Payload: payload,
		}); err != nil {
			log.Printf("failed to enqueue facet embedding for %s: %v", id, err)
			continue
		}
		count++
	}

	return count, nil
}

// EnqueuePersonEmbeddings queues embedding jobs for all un-embedded persons
func (e *Engine) EnqueuePersonEmbeddings(ctx context.Context) (int, error) {
	// Find persons without embeddings
	rows, err := e.db.QueryContext(ctx, `
		SELECT p.id FROM persons p
		WHERE NOT EXISTS (
			SELECT 1 FROM embeddings em
			WHERE em.entity_type = 'person'
			AND em.entity_id = p.id
		)
	`)
	if err != nil {
		return 0, fmt.Errorf("query persons: %w", err)
	}

	var personIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		personIDs = append(personIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	count := 0
	for _, id := range personIDs {
		payload := EmbeddingJobPayload{
			EntityType: "person",
			EntityID:   id,
		}

		if err := e.queue.Enqueue(queue.EnqueueOptions{
			Type:    JobTypeEmbedding,
			Key:     fmt.Sprintf("embedding:person:%s", id),
			Payload: payload,
		}); err != nil {
			log.Printf("failed to enqueue person embedding for %s: %v", id, err)
			continue
		}
		count++
	}

	return count, nil
}

// AnalysisJobPayload for analysis jobs
type AnalysisJobPayload struct {
	ConversationID string `json:"conversation_id"`
	AnalysisTypeID string `json:"analysis_type_id"`
}

// EmbeddingJobPayload for embedding jobs
type EmbeddingJobPayload struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
}

// handleAnalysisJob processes an analysis job
func (e *Engine) handleAnalysisJob(ctx context.Context, job *queue.Job) error {
	overallStart := time.Now()
	var (
		dbReadDur     time.Duration
		textBuildDur  time.Duration
		apiDur        time.Duration
		parseDur      time.Duration
		dbWriteDur    time.Duration
		outcome       = "error"
		blockedReason string
	)
	defer func() {
		e.metrics.RecordAnalysis(AnalysisMetricEvent{
			DBRead:        dbReadDur,
			TextBuild:     textBuildDur,
			APICall:       apiDur,
			Parse:         parseDur,
			DBWrite:       dbWriteDur,
			Overall:       time.Since(overallStart),
			Outcome:       outcome,
			BlockedReason: blockedReason,
		})
	}()

	var payload AnalysisJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	// Get analysis type config
	t0 := time.Now()
	var promptTemplate, outputType, analysisTypeName string
	var facetsConfigJSON sql.NullString
	err := e.db.QueryRowContext(ctx, `
		SELECT name, prompt_template, output_type, facets_config_json
		FROM analysis_types WHERE id = ?
	`, payload.AnalysisTypeID).Scan(&analysisTypeName, &promptTemplate, &outputType, &facetsConfigJSON)
	if err != nil {
		return fmt.Errorf("get analysis type: %w", err)
	}
	dbReadDur = time.Since(t0)

	// Build conversation text (check cache first for pre-encoded text)
	t1 := time.Now()
	var convText string
	if cached, ok := e.getConversationTextCached(payload.ConversationID); ok {
		convText = cached
	} else {
		var err error
		convText, err = e.buildConversationText(ctx, payload.ConversationID)
		if err != nil {
			return fmt.Errorf("build conversation text: %w", err)
		}
	}
	textBuildDur = time.Since(t1)

	// Build prompt
	prompt := strings.ReplaceAll(promptTemplate, "{{{conversation_text}}}", convText)

	// Check if analysis already exists (idempotency)
	var existingRunID, existingStatus string
	err = e.db.QueryRowContext(ctx, `
		SELECT id, status FROM analysis_runs 
		WHERE analysis_type_id = ? AND conversation_id = ?
	`, payload.AnalysisTypeID, payload.ConversationID).Scan(&existingRunID, &existingStatus)

	var runID string
	now := time.Now().Unix()

	if err == nil {
		// Existing record found
		if existingStatus == "completed" || existingStatus == "blocked" {
			// Already processed successfully
			outcome = "skipped"
			return nil
		}
		// Update existing record to running
		runID = existingRunID
		_, err = e.db.ExecContext(ctx, `
			UPDATE analysis_runs SET status = 'running', started_at = ?, error_message = NULL
			WHERE id = ?
		`, now, runID)
		if err != nil {
			return fmt.Errorf("update analysis run: %w", err)
		}
	} else {
		// Create new record
		runID = uuid.New().String()
		_, err = e.db.ExecContext(ctx, `
			INSERT INTO analysis_runs (id, analysis_type_id, conversation_id, status, started_at, created_at)
			VALUES (?, ?, ?, 'running', ?, ?)
		`, runID, payload.AnalysisTypeID, payload.ConversationID, now, now)
		if err != nil {
			return fmt.Errorf("create analysis run: %w", err)
		}
	}

	// Call Gemini
	req := &gemini.GenerateContentRequest{
		Contents: []gemini.Content{{
			Role:  "user",
			Parts: []gemini.Part{{Text: prompt}},
		}},
		SafetySettings: []gemini.SafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_NONE"},
		},
	}

	if outputType == "structured" {
		req.GenerationConfig = &gemini.GenerationConfig{
			// ThinkingLevel: "minimal" dramatically reduces per-call latency for
			// structured extraction tasks by minimizing the model's "thinking" phase.
			// This is critical for high-throughput bulk processing.
			ThinkingConfig:   &gemini.ThinkingConfig{ThinkingLevel: "minimal"},
			ResponseMimeType: "application/json",
		}

		// Add response schema for known analysis types (improves output reliability)
		if schema := getResponseSchema(analysisTypeName); schema != nil {
			req.GenerationConfig.ResponseSchema = schema
		}
	}

	t2 := time.Now()
	resp, err := e.geminiClient.GenerateContent(ctx, e.analysisModel, req)
	apiDur = time.Since(t2)
	if err != nil {
		// Mark as failed
		e.db.ExecContext(ctx, `
			UPDATE analysis_runs SET status = 'failed', error_message = ?, completed_at = ?
			WHERE id = ?
		`, err.Error(), time.Now().Unix(), runID)
		return fmt.Errorf("gemini API: %w", err)
	}

	// Extract and parse output
	t3 := time.Now()
	outputText := extractText(resp)
	parseDur = time.Since(t3)

	if outputText == "" {
		// Check if blocked
		if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
			blockedReason = resp.PromptFeedback.BlockReason
			outcome = "blocked"
			e.db.ExecContext(ctx, `
				UPDATE analysis_runs SET status = 'blocked', blocked_reason = ?, completed_at = ?
				WHERE id = ?
			`, resp.PromptFeedback.BlockReason, time.Now().Unix(), runID)
			return nil // Not an error, just blocked
		}
		e.db.ExecContext(ctx, `
			UPDATE analysis_runs SET status = 'failed', error_message = 'empty output', completed_at = ?
			WHERE id = ?
		`, time.Now().Unix(), runID)
		return fmt.Errorf("empty model output")
	}

	// Persist results
	t4 := time.Now()
	if outputType == "structured" && facetsConfigJSON.Valid {
		if err := e.extractAndPersistFacets(ctx, runID, payload.ConversationID, outputText, facetsConfigJSON.String); err != nil {
			log.Printf("warning: facet extraction failed: %v", err)
		}
	}

	// Mark complete
	_, err = e.db.ExecContext(ctx, `
		UPDATE analysis_runs SET status = 'completed', output_text = ?, completed_at = ?
		WHERE id = ?
	`, outputText, time.Now().Unix(), runID)
	dbWriteDur = time.Since(t4)

	if err == nil {
		outcome = "ok"
	}
	return err
}

// handleEmbeddingJob processes an embedding job using the batch API
func (e *Engine) handleEmbeddingJob(ctx context.Context, job *queue.Job) error {
	overallStart := time.Now()
	var (
		textBuildDur time.Duration
		apiDur       time.Duration
		dbWriteDur   time.Duration
		outcome      = "error"
	)
	defer func() {
		e.metrics.RecordEmbedding(EmbeddingMetricEvent{
			TextBuild: textBuildDur,
			APICall:   apiDur,
			DBWrite:   dbWriteDur,
			Overall:   time.Since(overallStart),
			Outcome:   outcome,
		})
	}()

	var payload EmbeddingJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	// Get text to embed (check cache first for conversations)
	t0 := time.Now()
	var text string
	var err error
	switch payload.EntityType {
	case "conversation":
		if cached, ok := e.getConversationTextCached(payload.EntityID); ok {
			text = cached
		} else {
			text, err = e.buildConversationText(ctx, payload.EntityID)
		}
	case "facet":
		text, err = e.buildFacetText(ctx, payload.EntityID)
	case "person":
		text, err = e.buildPersonText(ctx, payload.EntityID)
	default:
		return fmt.Errorf("unsupported entity type: %s", payload.EntityType)
	}
	if err != nil {
		return fmt.Errorf("get entity text: %w", err)
	}
	textBuildDur = time.Since(t0)

	// Skip if no text content (Gemini requires non-empty text)
	text = strings.TrimSpace(text)
	if text == "" {
		// Just complete successfully - no embedding for empty content
		outcome = "skipped"
		return nil
	}

	// Submit to batcher for batched API call (up to 100 embeddings per request)
	t1 := time.Now()
	embedding, err := e.embeddingBatcher.Submit(ctx, payload.EntityType, payload.EntityID, text)
	apiDur = time.Since(t1)
	if err != nil {
		return fmt.Errorf("gemini batch embed: %w", err)
	}

	if len(embedding) == 0 {
		return fmt.Errorf("empty embedding response")
	}

	// Convert to blob
	blob := float64SliceToBlob(embedding)
	embID := uuid.New().String()
	now := time.Now().Unix()
	model := e.embeddingModel
	dimension := len(embedding)

	// Persist using batch writer if available
	t2 := time.Now()
	apply := func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO embeddings (id, entity_type, entity_id, model, embedding_blob, dimension, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(entity_type, entity_id, model) DO UPDATE SET
				embedding_blob = excluded.embedding_blob,
				dimension = excluded.dimension
		`, embID, payload.EntityType, payload.EntityID, model, blob, dimension, now)
		return err
	}

	var writeErr error
	if e.writer != nil {
		writeErr = e.writer.Submit(ctx, apply)
	} else {
		// Fallback: direct write
		tx, err := e.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback()
		if err := apply(tx); err != nil {
			writeErr = err
		} else {
			writeErr = tx.Commit()
		}
	}
	dbWriteDur = time.Since(t2)

	if writeErr == nil {
		outcome = "ok"
	}
	return writeErr
}

// buildConversationText builds text representation of a conversation
// Format matches Eve's encoding: "Name: message text [Image] [Attachment: file.pdf]"
// Attachments are encoded as [Image], [Video], [Audio], [Sticker], or [Attachment: filename]
func (e *Engine) buildConversationText(ctx context.Context, convID string) (string, error) {
	// Query events with aggregated attachment info
	rows, err := e.db.QueryContext(ctx, `
		SELECT 
			e.id,
			e.content, 
			e.timestamp, 
			p.canonical_name, 
			e.direction,
			(
				SELECT GROUP_CONCAT(
					CASE 
						WHEN a.media_type = 'image' THEN 'image'
						WHEN a.media_type = 'video' THEN 'video'
						WHEN a.media_type = 'audio' THEN 'audio'
						WHEN a.media_type = 'sticker' THEN 'sticker'
						ELSE COALESCE(a.filename, 'file')
					END, '|'
				)
				FROM attachments a WHERE a.event_id = e.id
			) as attachments
		FROM conversation_events ce
		JOIN events e ON ce.event_id = e.id
		LEFT JOIN event_participants ep ON e.id = ep.event_id AND ep.role = 'sender'
		LEFT JOIN persons p ON ep.person_id = p.id
		WHERE ce.conversation_id = ?
		ORDER BY ce.position
	`, convID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var eventID string
		var content sql.NullString
		var timestamp int64
		var senderName sql.NullString
		var direction string
		var attachments sql.NullString

		if err := rows.Scan(&eventID, &content, &timestamp, &senderName, &direction, &attachments); err != nil {
			return "", err
		}

		name := "Unknown"
		if senderName.Valid && senderName.String != "" {
			name = senderName.String
		}

		// Build message parts
		var parts []string

		// Add text content if present
		if content.Valid && content.String != "" {
			parts = append(parts, content.String)
		}

		// Add attachments
		if attachments.Valid && attachments.String != "" {
			for _, att := range strings.Split(attachments.String, "|") {
				switch att {
				case "image":
					parts = append(parts, "[Image]")
				case "video":
					parts = append(parts, "[Video]")
				case "audio":
					parts = append(parts, "[Audio]")
				case "sticker":
					parts = append(parts, "[Sticker]")
				default:
					parts = append(parts, fmt.Sprintf("[Attachment: %s]", att))
				}
			}
		}

		// Only write line if there's content
		if len(parts) > 0 {
			sb.WriteString(fmt.Sprintf("%s: %s\n", name, strings.Join(parts, " ")))
		}
	}

	return sb.String(), rows.Err()
}

// buildFacetText builds text representation of a facet for embedding
// Format: "facet_type: value" (e.g., "entity: Paris", "topic: travel")
func (e *Engine) buildFacetText(ctx context.Context, facetID string) (string, error) {
	var facetType, value string
	err := e.db.QueryRowContext(ctx, `
		SELECT facet_type, value FROM facets WHERE id = ?
	`, facetID).Scan(&facetType, &value)
	if err != nil {
		return "", fmt.Errorf("get facet: %w", err)
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	return fmt.Sprintf("%s: %s", facetType, value), nil
}

// buildPersonText builds text representation of a person for embedding
// Includes name and all known identities/facts
func (e *Engine) buildPersonText(ctx context.Context, personID string) (string, error) {
	// Get person name
	var canonicalName string
	var displayName sql.NullString
	err := e.db.QueryRowContext(ctx, `
		SELECT canonical_name, display_name FROM persons WHERE id = ?
	`, personID).Scan(&canonicalName, &displayName)
	if err != nil {
		return "", fmt.Errorf("get person: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("person: ")
	sb.WriteString(canonicalName)
	if displayName.Valid && displayName.String != "" && displayName.String != canonicalName {
		sb.WriteString(" (")
		sb.WriteString(displayName.String)
		sb.WriteString(")")
	}

	// Get identities
	rows, err := e.db.QueryContext(ctx, `
		SELECT channel, identifier FROM identities WHERE person_id = ?
	`, personID)
	if err != nil {
		return sb.String(), nil // Return what we have
	}
	defer rows.Close()

	var identities []string
	for rows.Next() {
		var channel, identifier string
		if err := rows.Scan(&channel, &identifier); err != nil {
			continue
		}
		identities = append(identities, fmt.Sprintf("%s: %s", channel, identifier))
	}

	if len(identities) > 0 {
		sb.WriteString(" | identities: ")
		sb.WriteString(strings.Join(identities, ", "))
	}

	return sb.String(), nil
}

// extractAndPersistFacets parses structured output and saves facets
func (e *Engine) extractAndPersistFacets(ctx context.Context, runID, convID, outputText, facetsConfig string) error {
	// Parse the JSON output
	jsonText := extractJSONObject(outputText)
	if jsonText == "" {
		return fmt.Errorf("no JSON object found")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}

	// Parse facets config
	var config struct {
		Mappings []struct {
			JsonPath  string `json:"json_path"`
			FacetType string `json:"facet_type"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal([]byte(facetsConfig), &config); err != nil {
		return fmt.Errorf("parse facets config: %w", err)
	}

	now := time.Now().Unix()

	// Collect all facets to insert
	type facetRow struct {
		id        string
		facetType string
		value     string
	}
	var facets []facetRow

	for _, mapping := range config.Mappings {
		values := extractValues(parsed, mapping.JsonPath)
		for _, val := range values {
			facets = append(facets, facetRow{
				id:        uuid.New().String(),
				facetType: mapping.FacetType,
				value:     val,
			})
		}
	}

	if len(facets) == 0 {
		return nil
	}

	// Batch insert using TxBatchWriter if available
	apply := func(tx *sql.Tx) error {
		for _, f := range facets {
			_, err := tx.Exec(`
				INSERT INTO facets (id, analysis_run_id, conversation_id, facet_type, value, created_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, f.id, runID, convID, f.facetType, f.value, now)
			if err != nil {
				log.Printf("insert facet error: %v", err)
			}
		}
		return nil
	}

	if e.writer != nil {
		return e.writer.Submit(ctx, apply)
	}

	// Fallback: direct write
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func extractText(resp *gemini.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	for _, c := range resp.Candidates {
		for _, p := range c.Content.Parts {
			if strings.TrimSpace(p.Text) != "" {
				return p.Text
			}
		}
	}
	return ""
}

func extractJSONObject(text string) string {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// extractValues extracts values from a JSON path like "entities[].name"
func extractValues(data map[string]any, path string) []string {
	parts := strings.Split(path, ".")
	return extractValuesRecursive(data, parts)
}

func extractValuesRecursive(data any, parts []string) []string {
	if len(parts) == 0 {
		if s, ok := data.(string); ok {
			return []string{s}
		}
		return nil
	}

	part := parts[0]
	remaining := parts[1:]

	// Handle array notation
	if strings.HasSuffix(part, "[]") {
		key := strings.TrimSuffix(part, "[]")
		m, ok := data.(map[string]any)
		if !ok {
			return nil
		}
		arr, ok := m[key].([]any)
		if !ok {
			return nil
		}
		var results []string
		for _, item := range arr {
			results = append(results, extractValuesRecursive(item, remaining)...)
		}
		return results
	}

	// Regular key access
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	return extractValuesRecursive(m[part], remaining)
}

func float64SliceToBlob(values []float64) []byte {
	blob := make([]byte, len(values)*8)
	for i, v := range values {
		bits := math.Float64bits(v)
		for j := 0; j < 8; j++ {
			blob[i*8+j] = byte(bits >> (j * 8))
		}
	}
	return blob
}

// getResponseSchema returns the Gemini response schema for known analysis types
// This enforces JSON structure at the API level for more reliable output parsing
func getResponseSchema(analysisTypeName string) any {
	switch analysisTypeName {
	case "convo-all-v1":
		// Schema for conversation analysis: summary, entities, topics, emotions, humor
		return map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"summary": map[string]any{
					"type": "STRING",
				},
				"entities": map[string]any{
					"type": "ARRAY",
					"items": map[string]any{
						"type": "STRING",
					},
				},
				"topics": map[string]any{
					"type": "ARRAY",
					"items": map[string]any{
						"type": "STRING",
					},
				},
				"emotions": map[string]any{
					"type": "ARRAY",
					"items": map[string]any{
						"type": "STRING",
					},
				},
				"humor": map[string]any{
					"type": "ARRAY",
					"items": map[string]any{
						"type": "STRING",
					},
				},
			},
			"required": []string{"summary", "entities", "topics", "emotions", "humor"},
		}
	default:
		return nil
	}
}
