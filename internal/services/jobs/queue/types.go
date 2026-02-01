package queue

import (
	"context"
	"time"
)

// JobType represents the type of job
type JobType string

const (
	// JobTypeMarketAnalysis is for market analysis jobs
	JobTypeMarketAnalysis JobType = "market_analysis"
	// JobTypeInvestmentPlanning is for investment planning jobs
	JobTypeInvestmentPlanning JobType = "investment_planning"
	// JobTypeEvaluationChat is for evaluation chat jobs
	JobTypeEvaluationChat JobType = "evaluation_chat"
	// JobTypePropertySearch is for property search jobs
	JobTypePropertySearch JobType = "property_search"
)

// JobStatus represents the current status of a job
type JobStatus string

const (
	// JobStatusPending means the job is waiting to be processed
	JobStatusPending JobStatus = "pending"
	// JobStatusProcessing means the job is currently being processed
	JobStatusProcessing JobStatus = "processing"
	// JobStatusCompleted means the job has finished successfully
	JobStatusCompleted JobStatus = "completed"
	// JobStatusFailed means the job has failed
	JobStatusFailed JobStatus = "failed"
	// JobStatusCancelled means the job was cancelled
	JobStatusCancelled JobStatus = "cancelled"
)

// Job represents a unit of work to be processed
type Job struct {
	ID              string                 `json:"id"`
	Type            JobType                `json:"type"`
	UserID          string                 `json:"user_id"`
	Payload         map[string]interface{} `json:"payload"`
	Status          JobStatus              `json:"status"`
	Priority        int                    `json:"priority"` // Higher = more urgent
	Progress        float64                `json:"progress"` // 0-100
	ProgressMessage string                 `json:"progress_message,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at"`
	Attempts        int                    `json:"attempts"`
	MaxRetries      int                    `json:"max_retries"`
	Error           string                 `json:"error,omitempty"`
}

// JobResult represents the result of a completed job
type JobResult struct {
	JobID       string                 `json:"job_id"`
	Status      JobStatus              `json:"status"`
	Data        map[string]interface{} `json:"data,omitempty"`
	Error       string                 `json:"error,omitempty"`
	CompletedAt time.Time              `json:"completed_at"`
	Duration    time.Duration          `json:"duration"`
}

// ProgressEvent represents a progress update for a job
type ProgressEvent struct {
	JobID    string  `json:"job_id"`
	Progress float64 `json:"progress"` // 0-100
	Stage    string  `json:"stage"`
	Message  string  `json:"message,omitempty"`
	Data     interface{} `json:"data,omitempty"`
}

// JobHandler is a function that processes a job
type JobHandler func(ctx context.Context, job *Job, progress chan<- ProgressEvent) (*JobResult, error)

// JobConfig holds configuration for job processing
type JobConfig struct {
	MaxRetries      int           `json:"max_retries"`
	RetryDelay      time.Duration `json:"retry_delay"`
	Timeout         time.Duration `json:"timeout"`
	ProgressBuffer  int           `json:"progress_buffer"`
}

// DefaultJobConfig returns sensible defaults for job configuration
func DefaultJobConfig() JobConfig {
	return JobConfig{
		MaxRetries:     3,
		RetryDelay:     time.Second * 5,
		Timeout:        time.Minute * 10,
		ProgressBuffer: 100,
	}
}

// NewJob creates a new job with the given parameters
func NewJob(jobType JobType, userID string, payload map[string]interface{}) *Job {
	now := time.Now()
	return &Job{
		ID:        generateJobID(),
		Type:      jobType,
		UserID:    userID,
		Payload:   payload,
		Status:    JobStatusPending,
		Priority:  0,
		CreatedAt: now,
		UpdatedAt: now,
		Attempts:  0,
		MaxRetries: 3,
	}
}

// NewJobWithPriority creates a new job with a specific priority
func NewJobWithPriority(jobType JobType, userID string, payload map[string]interface{}, priority int) *Job {
	job := NewJob(jobType, userID, payload)
	job.Priority = priority
	return job
}

// generateJobID creates a unique job ID
func generateJobID() string {
	return "job_" + time.Now().Format("20060102150405") + "_" + randomString(8)
}

// randomString generates a random alphanumeric string
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}
