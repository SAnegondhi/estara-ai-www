package queue

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultWorkerCount   = 4
	defaultPollInterval  = time.Millisecond * 100
	defaultJobTimeout    = time.Minute * 10
)

// WorkerPool manages a pool of workers that process jobs
type WorkerPool struct {
	queue         *Queue
	workers       int
	pollInterval  time.Duration
	jobTimeout    time.Duration
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
	running       bool
	mu            sync.RWMutex
	logger        *slog.Logger
}

// WorkerPoolConfig holds configuration for the worker pool
type WorkerPoolConfig struct {
	Workers      int
	PollInterval time.Duration
	JobTimeout   time.Duration
}

// NewWorkerPool creates a new worker pool
func NewWorkerPool(queue *Queue, cfg WorkerPoolConfig) *WorkerPool {
	if cfg.Workers == 0 {
		cfg.Workers = defaultWorkerCount
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.JobTimeout == 0 {
		cfg.JobTimeout = defaultJobTimeout
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WorkerPool{
		queue:        queue,
		workers:      cfg.Workers,
		pollInterval: cfg.PollInterval,
		jobTimeout:   cfg.JobTimeout,
		ctx:          ctx,
		cancel:       cancel,
		logger:       slog.Default().With("component", "worker_pool"),
	}
}

// Start begins processing jobs with the worker pool
func (p *WorkerPool) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()

	p.logger.Info("starting worker pool", "workers", p.workers)

	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Stop gracefully shuts down the worker pool
func (p *WorkerPool) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	p.mu.Unlock()

	p.logger.Info("stopping worker pool")
	p.cancel()
	p.wg.Wait()
	p.logger.Info("worker pool stopped")
}

// IsRunning returns whether the pool is currently running
func (p *WorkerPool) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// worker is the main loop for each worker goroutine
func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()

	logger := p.logger.With("worker_id", id)
	logger.Debug("worker started")

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			logger.Debug("worker stopping")
			return
		case <-ticker.C:
			p.processNextJob(id)
		}
	}
}

// processNextJob attempts to dequeue and process the next job
func (p *WorkerPool) processNextJob(workerID int) {
	job, err := p.queue.Dequeue(p.ctx)
	if err != nil {
		if err != ErrQueueClosed {
			p.logger.Warn("error dequeuing job", "worker_id", workerID, "error", err)
		}
		return
	}

	if job == nil {
		return // No jobs available
	}

	logger := p.logger.With(
		"worker_id", workerID,
		"job_id", job.ID,
		"job_type", job.Type,
	)

	logger.Info("processing job")

	// Get handler for this job type
	handler, ok := p.queue.handlers[job.Type]
	if !ok {
		// No handler registered - this job type is handled elsewhere (e.g., HTTP streaming)
		// Put job back in pending state so it can be retrieved by other handlers
		logger.Debug("no worker handler for job type, skipping", "job_type", job.Type)
		p.queue.skipJob(job)
		return
	}

	// Create job context with timeout
	jobCtx, jobCancel := context.WithTimeout(p.ctx, p.jobTimeout)
	defer jobCancel()

	// Create progress channel
	progressChan := make(chan ProgressEvent, 100)
	go p.forwardProgress(job.ID, progressChan)

	// Execute the handler
	startTime := time.Now()
	result, err := handler(jobCtx, job, progressChan)
	close(progressChan)

	duration := time.Since(startTime)

	if err != nil {
		logger.Error("job failed", "error", err, "duration", duration)
		_ = p.queue.Fail(job.ID, err)
		return
	}

	if result == nil {
		result = &JobResult{
			Data: make(map[string]interface{}),
		}
	}

	logger.Info("job completed", "duration", duration)
	_ = p.queue.Complete(job.ID, result)
}

// forwardProgress forwards progress events from handler to queue subscribers
func (p *WorkerPool) forwardProgress(jobID string, progress <-chan ProgressEvent) {
	for event := range progress {
		p.queue.ReportProgress(jobID, event)
	}
}

// ErrNoHandlerRegistered is returned when no handler exists for a job type
type ErrNoHandlerRegistered JobType

func (e ErrNoHandlerRegistered) Error() string {
	return "no handler registered for job type: " + string(e)
}

// Stats returns worker pool statistics
func (p *WorkerPool) Stats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]interface{}{
		"workers":       p.workers,
		"running":       p.running,
		"poll_interval": p.pollInterval.String(),
		"job_timeout":   p.jobTimeout.String(),
	}
}

// SetWorkers dynamically adjusts the number of workers
// Note: This requires stopping and restarting the pool
func (p *WorkerPool) SetWorkers(count int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if count < 1 {
		count = 1
	}

	if p.running {
		p.logger.Warn("cannot change worker count while pool is running")
		return
	}

	p.workers = count
	p.logger.Info("worker count updated", "workers", count)
}

// Scheduler manages periodic tasks for the queue
type Scheduler struct {
	queue    *Queue
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	logger   *slog.Logger
}

// NewScheduler creates a new scheduler for periodic tasks
func NewScheduler(queue *Queue) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		queue:  queue,
		ctx:    ctx,
		cancel: cancel,
		logger: slog.Default().With("component", "job_scheduler"),
	}
}

// Start begins the scheduler
func (s *Scheduler) Start() {
	s.logger.Info("starting scheduler")

	// Cleanup old results every hour
	s.wg.Add(1)
	go s.runCleanup()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.logger.Info("stopping scheduler")
	s.cancel()
	s.wg.Wait()
	s.logger.Info("scheduler stopped")
}

// runCleanup periodically cleans up old results
func (s *Scheduler) runCleanup() {
	defer s.wg.Done()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			removed := s.queue.CleanupOldResults()
			if removed > 0 {
				s.logger.Info("cleaned up old results", "removed", removed)
			}
		}
	}
}
