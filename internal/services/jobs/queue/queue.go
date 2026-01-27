package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrJobNotFound is returned when a job doesn't exist
	ErrJobNotFound = errors.New("job not found")
	// ErrQueueFull is returned when the queue is at capacity
	ErrQueueFull = errors.New("queue is full")
	// ErrQueueClosed is returned when operations are attempted on a closed queue
	ErrQueueClosed = errors.New("queue is closed")
)

const (
	defaultQueueCapacity   = 1000
	defaultResultRetention = time.Hour * 24
	redisJobKeyPrefix      = "job:"
	redisQueueKey          = "job_queue"
	redisResultKeyPrefix   = "job_result:"
)

// Queue manages job scheduling and execution
type Queue struct {
	mu          sync.RWMutex
	jobs        map[string]*Job
	pending     []*Job // Priority queue (sorted by priority then creation time)
	results     map[string]*JobResult
	subscribers map[string][]chan ProgressEvent
	handlers    map[JobType]JobHandler
	redis       *redis.Client
	config      QueueConfig
	closed      bool
	logger      *slog.Logger
}

// QueueConfig holds configuration for the queue
type QueueConfig struct {
	Capacity        int
	ResultRetention time.Duration
	UseRedisBackup  bool
}

// NewQueue creates a new job queue
func NewQueue(redisClient *redis.Client, cfg QueueConfig) *Queue {
	if cfg.Capacity == 0 {
		cfg.Capacity = defaultQueueCapacity
	}
	if cfg.ResultRetention == 0 {
		cfg.ResultRetention = defaultResultRetention
	}

	q := &Queue{
		jobs:        make(map[string]*Job),
		pending:     make([]*Job, 0),
		results:     make(map[string]*JobResult),
		subscribers: make(map[string][]chan ProgressEvent),
		handlers:    make(map[JobType]JobHandler),
		redis:       redisClient,
		config:      cfg,
		logger:      slog.Default().With("component", "job_queue"),
	}

	// Restore jobs from Redis if available
	if cfg.UseRedisBackup && redisClient != nil {
		go q.restoreFromRedis()
	}

	return q
}

// RegisterHandler registers a handler for a specific job type
func (q *Queue) RegisterHandler(jobType JobType, handler JobHandler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[jobType] = handler
	q.logger.Info("registered job handler", "type", jobType)
}

// Enqueue adds a job to the queue
func (q *Queue) Enqueue(job *Job) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return "", ErrQueueClosed
	}

	if len(q.pending) >= q.config.Capacity {
		return "", ErrQueueFull
	}

	// Store job
	q.jobs[job.ID] = job
	q.pending = append(q.pending, job)
	q.sortPending()

	// Backup to Redis
	if q.config.UseRedisBackup && q.redis != nil {
		go q.backupJobToRedis(job)
	}

	q.logger.Debug("job enqueued",
		"job_id", job.ID,
		"type", job.Type,
		"user_id", job.UserID,
		"queue_size", len(q.pending),
	)

	return job.ID, nil
}

// Dequeue retrieves the next job to process
func (q *Queue) Dequeue(ctx context.Context) (*Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil, ErrQueueClosed
	}

	if len(q.pending) == 0 {
		return nil, nil
	}

	// Get highest priority job
	job := q.pending[0]
	q.pending = q.pending[1:]

	// Update job status
	job.Status = JobStatusProcessing
	now := time.Now()
	job.StartedAt = &now
	job.UpdatedAt = now
	job.Attempts++

	// Update in Redis
	if q.config.UseRedisBackup && q.redis != nil {
		go q.backupJobToRedis(job)
	}

	q.logger.Debug("job dequeued",
		"job_id", job.ID,
		"type", job.Type,
		"attempt", job.Attempts,
	)

	return job, nil
}

// GetJob retrieves a job by ID
func (q *Queue) GetJob(jobID string) (*Job, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	return job, nil
}

// GetResult retrieves the result of a completed job
func (q *Queue) GetResult(jobID string) (*JobResult, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	result, ok := q.results[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	return result, nil
}

// Complete marks a job as completed with results
func (q *Queue) Complete(jobID string, result *JobResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}

	job.Status = JobStatusCompleted
	job.UpdatedAt = time.Now()
	result.JobID = jobID
	result.Status = JobStatusCompleted
	result.CompletedAt = time.Now()

	if job.StartedAt != nil {
		result.Duration = result.CompletedAt.Sub(*job.StartedAt)
	}

	q.results[jobID] = result

	// Notify subscribers
	q.notifySubscribers(jobID, ProgressEvent{
		JobID:    jobID,
		Progress: 100,
		Stage:    "completed",
		Message:  "Job completed successfully",
		Data:     result.Data,
	})
	q.closeSubscribers(jobID)

	// Update Redis
	if q.config.UseRedisBackup && q.redis != nil {
		go q.backupJobToRedis(job)
		go q.backupResultToRedis(result)
	}

	q.logger.Info("job completed",
		"job_id", jobID,
		"duration", result.Duration,
	)

	return nil
}

// Fail marks a job as failed
func (q *Queue) Fail(jobID string, err error) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	// Check if we should retry
	if job.Attempts < job.MaxRetries {
		job.Status = JobStatusPending
		job.Error = errMsg
		job.UpdatedAt = time.Now()
		q.pending = append(q.pending, job)
		q.sortPending()

		q.logger.Warn("job failed, will retry",
			"job_id", jobID,
			"attempt", job.Attempts,
			"max_retries", job.MaxRetries,
			"error", errMsg,
		)
	} else {
		job.Status = JobStatusFailed
		job.Error = errMsg
		job.UpdatedAt = time.Now()

		result := &JobResult{
			JobID:       jobID,
			Status:      JobStatusFailed,
			Error:       errMsg,
			CompletedAt: time.Now(),
		}
		if job.StartedAt != nil {
			result.Duration = result.CompletedAt.Sub(*job.StartedAt)
		}
		q.results[jobID] = result

		// Notify subscribers of failure
		q.notifySubscribers(jobID, ProgressEvent{
			JobID:    jobID,
			Progress: 0,
			Stage:    "failed",
			Message:  errMsg,
		})
		q.closeSubscribers(jobID)

		q.logger.Error("job failed permanently",
			"job_id", jobID,
			"attempts", job.Attempts,
			"error", errMsg,
		)
	}

	// Update Redis
	if q.config.UseRedisBackup && q.redis != nil {
		go q.backupJobToRedis(job)
	}

	return nil
}

// Cancel cancels a pending or processing job
func (q *Queue) Cancel(jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}

	if job.Status == JobStatusCompleted || job.Status == JobStatusFailed {
		return fmt.Errorf("cannot cancel job with status %s", job.Status)
	}

	job.Status = JobStatusCancelled
	job.UpdatedAt = time.Now()

	// Remove from pending if still there
	for i, p := range q.pending {
		if p.ID == jobID {
			q.pending = append(q.pending[:i], q.pending[i+1:]...)
			break
		}
	}

	// Notify subscribers
	q.notifySubscribers(jobID, ProgressEvent{
		JobID:    jobID,
		Progress: 0,
		Stage:    "cancelled",
		Message:  "Job was cancelled",
	})
	q.closeSubscribers(jobID)

	q.logger.Info("job cancelled", "job_id", jobID)

	return nil
}

// Subscribe returns a channel for job progress updates
func (q *Queue) Subscribe(jobID string) <-chan ProgressEvent {
	q.mu.Lock()
	defer q.mu.Unlock()

	ch := make(chan ProgressEvent, 100)
	q.subscribers[jobID] = append(q.subscribers[jobID], ch)

	q.logger.Debug("subscriber added", "job_id", jobID)

	return ch
}

// ReportProgress reports progress for a job
func (q *Queue) ReportProgress(jobID string, event ProgressEvent) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	event.JobID = jobID
	q.notifySubscribers(jobID, event)
}

// GetUserJobs returns all jobs for a user
func (q *Queue) GetUserJobs(userID string, status *JobStatus) []*Job {
	q.mu.RLock()
	defer q.mu.RUnlock()

	jobs := make([]*Job, 0)
	for _, job := range q.jobs {
		if job.UserID == userID {
			if status == nil || job.Status == *status {
				jobs = append(jobs, job)
			}
		}
	}

	// Sort by creation time (newest first)
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})

	return jobs
}

// Stats returns queue statistics
func (q *Queue) Stats() map[string]interface{} {
	q.mu.RLock()
	defer q.mu.RUnlock()

	statusCounts := make(map[JobStatus]int)
	typeCounts := make(map[JobType]int)

	for _, job := range q.jobs {
		statusCounts[job.Status]++
		typeCounts[job.Type]++
	}

	return map[string]interface{}{
		"total_jobs":     len(q.jobs),
		"pending_jobs":   len(q.pending),
		"results_cached": len(q.results),
		"subscribers":    len(q.subscribers),
		"by_status":      statusCounts,
		"by_type":        typeCounts,
	}
}

// Close shuts down the queue
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.closed = true

	// Close all subscriber channels
	for jobID := range q.subscribers {
		q.closeSubscribers(jobID)
	}

	q.logger.Info("queue closed")
	return nil
}

// CleanupOldResults removes results older than retention period
func (q *Queue) CleanupOldResults() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-q.config.ResultRetention)
	removed := 0

	for jobID, result := range q.results {
		if result.CompletedAt.Before(cutoff) {
			delete(q.results, jobID)
			delete(q.jobs, jobID)
			removed++
		}
	}

	if removed > 0 {
		q.logger.Info("cleaned up old results", "removed", removed)
	}

	return removed
}

// sortPending sorts the pending queue by priority (desc) then creation time (asc)
func (q *Queue) sortPending() {
	sort.Slice(q.pending, func(i, j int) bool {
		if q.pending[i].Priority != q.pending[j].Priority {
			return q.pending[i].Priority > q.pending[j].Priority
		}
		return q.pending[i].CreatedAt.Before(q.pending[j].CreatedAt)
	})
}

// notifySubscribers sends an event to all subscribers of a job
func (q *Queue) notifySubscribers(jobID string, event ProgressEvent) {
	subs, ok := q.subscribers[jobID]
	if !ok {
		return
	}

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// Channel full, skip
		}
	}
}

// closeSubscribers closes all subscriber channels for a job
func (q *Queue) closeSubscribers(jobID string) {
	subs, ok := q.subscribers[jobID]
	if !ok {
		return
	}

	for _, ch := range subs {
		close(ch)
	}
	delete(q.subscribers, jobID)
}

// backupJobToRedis saves a job to Redis
func (q *Queue) backupJobToRedis(job *Job) {
	if q.redis == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	data, err := json.Marshal(job)
	if err != nil {
		q.logger.Warn("failed to marshal job for Redis", "error", err)
		return
	}

	key := redisJobKeyPrefix + job.ID
	if err := q.redis.Set(ctx, key, data, q.config.ResultRetention).Err(); err != nil {
		q.logger.Warn("failed to backup job to Redis", "error", err)
	}
}

// backupResultToRedis saves a job result to Redis
func (q *Queue) backupResultToRedis(result *JobResult) {
	if q.redis == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	data, err := json.Marshal(result)
	if err != nil {
		q.logger.Warn("failed to marshal result for Redis", "error", err)
		return
	}

	key := redisResultKeyPrefix + result.JobID
	if err := q.redis.Set(ctx, key, data, q.config.ResultRetention).Err(); err != nil {
		q.logger.Warn("failed to backup result to Redis", "error", err)
	}
}

// restoreFromRedis loads jobs from Redis on startup
func (q *Queue) restoreFromRedis() {
	if q.redis == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	// Find all job keys
	keys, err := q.redis.Keys(ctx, redisJobKeyPrefix+"*").Result()
	if err != nil {
		q.logger.Warn("failed to get job keys from Redis", "error", err)
		return
	}

	restored := 0
	for _, key := range keys {
		data, err := q.redis.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}

		var job Job
		if err := json.Unmarshal(data, &job); err != nil {
			continue
		}

		q.mu.Lock()
		q.jobs[job.ID] = &job
		if job.Status == JobStatusPending {
			q.pending = append(q.pending, &job)
		}
		q.mu.Unlock()
		restored++
	}

	if restored > 0 {
		q.mu.Lock()
		q.sortPending()
		q.mu.Unlock()
		q.logger.Info("restored jobs from Redis", "count", restored)
	}
}
