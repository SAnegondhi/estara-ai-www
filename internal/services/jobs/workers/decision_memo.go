package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/memo"
)

// DecisionMemoWorker processes decision memo generation jobs.
type DecisionMemoWorker struct {
	memoService *memo.Service
	logger      *slog.Logger
}

// DecisionMemoWorkerConfig holds configuration for the worker.
type DecisionMemoWorkerConfig struct {
	MemoService *memo.Service
}

// NewDecisionMemoWorker creates a new decision memo worker.
func NewDecisionMemoWorker(cfg DecisionMemoWorkerConfig) *DecisionMemoWorker {
	return &DecisionMemoWorker{
		memoService: cfg.MemoService,
		logger:      slog.Default().With("component", "decision_memo_worker"),
	}
}

// GetHandler returns the job handler function.
func (w *DecisionMemoWorker) GetHandler() queue.JobHandler {
	return func(ctx context.Context, job *queue.Job, progress chan<- queue.ProgressEvent) (*queue.JobResult, error) {
		return w.Process(ctx, job, progress)
	}
}

// Process executes a decision memo job.
func (w *DecisionMemoWorker) Process(
	ctx context.Context,
	job *queue.Job,
	progress chan<- queue.ProgressEvent,
) (*queue.JobResult, error) {
	startTime := time.Now()
	w.logger.Info("processing decision memo job",
		"job_id", job.ID,
		"user_id", job.UserID,
	)

	// Parse properties from payload
	propsRaw, ok := job.Payload["properties"]
	if !ok {
		return w.failedResult(job, fmt.Errorf("missing properties in payload"))
	}

	var propsJSON []byte
	switch v := propsRaw.(type) {
	case json.RawMessage:
		propsJSON = v
	case string:
		propsJSON = []byte(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return w.failedResult(job, fmt.Errorf("failed to marshal properties: %w", err))
		}
		propsJSON = b
	}

	var properties []memo.BatchPropertyInput
	if err := json.Unmarshal(propsJSON, &properties); err != nil {
		return w.failedResult(job, fmt.Errorf("invalid properties: %w", err))
	}

	if len(properties) == 0 {
		return w.failedResult(job, fmt.Errorf("no properties provided"))
	}

	strategy, _ := job.Payload["strategy"].(string)
	if strategy == "" {
		strategy = "balanced"
	}
	forceRefresh, _ := job.Payload["forceRefresh"].(bool)
	userID, _ := job.Payload["userId"].(string)
	if userID == "" {
		userID = job.UserID
	}

	opts := memo.GenerateOptions{
		Strategy:     strategy,
		UserID:       userID,
		ForceRefresh: forceRefresh,
	}

	// Progress callback bridges memo.Service progress to job queue progress events
	onProgress := func(pct float64, message string) {
		w.reportProgress(progress, job.ID, pct, message)
	}

	memos, err := w.memoService.GenerateMemos(ctx, properties, opts, onProgress)
	if err != nil {
		w.logger.Error("memo generation failed", "error", err, "job_id", job.ID)
		return w.failedResult(job, fmt.Errorf("memo generation failed: %w", err))
	}

	// Convert memos to map for job result
	memosJSON, err := json.Marshal(memos)
	if err != nil {
		return w.failedResult(job, fmt.Errorf("failed to serialize memos: %w", err))
	}

	var resultData map[string]interface{}
	if err := json.Unmarshal(memosJSON, &resultData); err != nil {
		// Fallback: store as array under "memos" key
		resultData = map[string]interface{}{
			"memos": memos,
		}
	}
	// The Unmarshal above would fail since memos is an array, not object.
	// Always use the memos key.
	resultData = map[string]interface{}{
		"memos":      memos,
		"count":      len(memos),
		"strategy":   strategy,
		"properties": len(properties),
	}

	w.logger.Info("decision memo generation complete",
		"job_id", job.ID,
		"memos", len(memos),
		"duration", time.Since(startTime),
	)

	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusCompleted,
		Data:        resultData,
		CompletedAt: time.Now(),
		Duration:    time.Since(startTime),
	}, nil
}

func (w *DecisionMemoWorker) reportProgress(progress chan<- queue.ProgressEvent, jobID string, pct float64, message string) {
	if progress == nil {
		return
	}
	select {
	case progress <- queue.ProgressEvent{
		JobID:    jobID,
		Progress: pct,
		Stage:    message,
		Message:  message,
	}:
	default:
		// Don't block if channel is full
	}
}

func (w *DecisionMemoWorker) failedResult(job *queue.Job, err error) (*queue.JobResult, error) {
	return &queue.JobResult{
		JobID:       job.ID,
		Status:      queue.JobStatusFailed,
		Error:       err.Error(),
		CompletedAt: time.Now(),
	}, err
}
