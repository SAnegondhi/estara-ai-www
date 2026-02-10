package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Writer provides methods to write Server-Sent Events
type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewWriter creates a new SSE writer
// Returns an error if the response writer doesn't support flushing
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Disable write deadline for SSE connections (Go 1.20+)
	// The server's WriteTimeout is a hard limit from when headers are sent.
	// SSE connections are long-lived, so we need to disable the deadline.
	// Heartbeats + context cancellation handle cleanup instead.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // zero = no deadline

	return &Writer{
		w:       w,
		flusher: flusher,
	}, nil
}

// WriteEvent sends an SSE event with the given event type and data
func (w *Writer) WriteEvent(event, data string) error {
	if event != "" {
		if _, err := fmt.Fprintf(w.w, "event: %s\n", event); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(w.w, "data: %s\n\n", data); err != nil {
		return err
	}

	w.flusher.Flush()
	return nil
}

// WriteEventJSON sends an SSE event with JSON-encoded data
func (w *Writer) WriteEventJSON(event string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	return w.WriteEvent(event, string(jsonData))
}

// WriteData sends data without an event type
func (w *Writer) WriteData(data string) error {
	return w.WriteEvent("", data)
}

// WriteDataJSON sends JSON-encoded data without an event type
func (w *Writer) WriteDataJSON(data interface{}) error {
	return w.WriteEventJSON("", data)
}

// WriteHeartbeat sends a keep-alive comment
func (w *Writer) WriteHeartbeat() error {
	if _, err := fmt.Fprint(w.w, ": heartbeat\n\n"); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

// WriteError sends an error event
func (w *Writer) WriteError(message string) error {
	return w.WriteEventJSON("error", map[string]string{
		"error": message,
	})
}

// WriteProgress sends a progress event
func (w *Writer) WriteProgress(current, total int, message string) error {
	return w.WriteEventJSON("progress", map[string]interface{}{
		"current": current,
		"total":   total,
		"message": message,
	})
}

// WriteComplete sends a completion event
func (w *Writer) WriteComplete(data interface{}) error {
	return w.WriteEventJSON("complete", data)
}

// EventType constants for common event types
const (
	EventTypeProgress = "progress"
	EventTypeData     = "data"
	EventTypeError    = "error"
	EventTypeComplete = "complete"
	EventTypeInsight  = "insight"
	EventTypeMetrics  = "metrics"
	EventTypeText     = "text"
)

// HeartbeatInterval is the recommended interval between heartbeats
const HeartbeatInterval = 15 * time.Second

// StartHeartbeat starts a goroutine that sends periodic heartbeats
// Returns a channel that should be closed to stop the heartbeat
func (w *Writer) StartHeartbeat(interval time.Duration) chan struct{} {
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := w.WriteHeartbeat(); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	return done
}

// ProgressEvent represents a progress update
type ProgressEvent struct {
	Stage    string  `json:"stage"`
	Progress float64 `json:"progress"` // 0-100
	Message  string  `json:"message,omitempty"`
}

// TextEvent represents a text chunk
type TextEvent struct {
	Text string `json:"text"`
}

// InsightEvent represents an insight block
type InsightEvent struct {
	Title      string `json:"title"`
	Type       string `json:"type"` // opportunity, risk, neutral
	Confidence string `json:"confidence"`
	Summary    string `json:"summary"`
	Details    string `json:"details,omitempty"`
}

// ErrorEvent represents an error
type ErrorEvent struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// CompleteEvent represents a completion
type CompleteEvent struct {
	SessionID   string      `json:"sessionId,omitempty"`
	FullContent string      `json:"fullContent,omitempty"`
	Usage       *UsageStats `json:"usage,omitempty"`
}

// UsageStats represents token usage
type UsageStats struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}
