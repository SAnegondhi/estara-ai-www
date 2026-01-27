package sse

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var (
	// ErrMaxConnectionsReached is returned when user has too many connections
	ErrMaxConnectionsReached = errors.New("maximum connections reached")
	// ErrConnectionNotFound is returned when connection doesn't exist
	ErrConnectionNotFound = errors.New("connection not found")
)

// Connection represents an active SSE connection
type Connection struct {
	ID        string
	UserID    string
	JobID     string
	CreatedAt time.Time
	cancel    context.CancelFunc
	events    chan Event
}

// Event represents a generic event to send to a connection
type Event struct {
	Type string
	Data interface{}
}

// ConnectionRegistry manages active SSE connections
type ConnectionRegistry struct {
	mu          sync.RWMutex
	connections map[string]map[string]*Connection // userID -> connID -> connection
	byJobID     map[string]*Connection            // jobID -> connection
	maxPerUser  int
	logger      *slog.Logger
}

// RegistryConfig holds configuration for the registry
type RegistryConfig struct {
	MaxConnectionsPerUser int
}

// NewConnectionRegistry creates a new connection registry
func NewConnectionRegistry(cfg RegistryConfig) *ConnectionRegistry {
	maxPerUser := cfg.MaxConnectionsPerUser
	if maxPerUser == 0 {
		maxPerUser = 10 // Default max connections per user
	}

	return &ConnectionRegistry{
		connections: make(map[string]map[string]*Connection),
		byJobID:     make(map[string]*Connection),
		maxPerUser:  maxPerUser,
		logger:      slog.Default().With("component", "sse_registry"),
	}
}

// Add registers a new connection
func (r *ConnectionRegistry) Add(userID, connID, jobID string, cancel context.CancelFunc) (*Connection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Initialize user's connection map if needed
	if r.connections[userID] == nil {
		r.connections[userID] = make(map[string]*Connection)
	}

	// Check connection limit
	if len(r.connections[userID]) >= r.maxPerUser {
		return nil, ErrMaxConnectionsReached
	}

	conn := &Connection{
		ID:        connID,
		UserID:    userID,
		JobID:     jobID,
		CreatedAt: time.Now(),
		cancel:    cancel,
		events:    make(chan Event, 100),
	}

	r.connections[userID][connID] = conn
	if jobID != "" {
		r.byJobID[jobID] = conn
	}

	r.logger.Debug("connection added",
		"user_id", userID,
		"conn_id", connID,
		"job_id", jobID,
		"total_user_conns", len(r.connections[userID]),
	)

	return conn, nil
}

// Remove unregisters a connection
func (r *ConnectionRegistry) Remove(userID, connID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if userConns, ok := r.connections[userID]; ok {
		if conn, ok := userConns[connID]; ok {
			// Cancel the connection context
			if conn.cancel != nil {
				conn.cancel()
			}

			// Close the events channel
			close(conn.events)

			// Remove from byJobID if applicable
			if conn.JobID != "" {
				delete(r.byJobID, conn.JobID)
			}

			// Remove from user connections
			delete(userConns, connID)

			// Clean up empty user maps
			if len(userConns) == 0 {
				delete(r.connections, userID)
			}

			r.logger.Debug("connection removed",
				"user_id", userID,
				"conn_id", connID,
			)
		}
	}
}

// Get retrieves a connection by user and connection ID
func (r *ConnectionRegistry) Get(userID, connID string) (*Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if userConns, ok := r.connections[userID]; ok {
		conn, ok := userConns[connID]
		return conn, ok
	}
	return nil, false
}

// GetByJobID retrieves a connection by job ID
func (r *ConnectionRegistry) GetByJobID(jobID string) (*Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conn, ok := r.byJobID[jobID]
	return conn, ok
}

// GetUserConnections returns all connections for a user
func (r *ConnectionRegistry) GetUserConnections(userID string) []*Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conns := make([]*Connection, 0)
	if userConns, ok := r.connections[userID]; ok {
		for _, conn := range userConns {
			conns = append(conns, conn)
		}
	}
	return conns
}

// CountUserConnections returns the number of connections for a user
func (r *ConnectionRegistry) CountUserConnections(userID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if userConns, ok := r.connections[userID]; ok {
		return len(userConns)
	}
	return 0
}

// CountTotal returns the total number of active connections
func (r *ConnectionRegistry) CountTotal() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	for _, userConns := range r.connections {
		total += len(userConns)
	}
	return total
}

// SendToUser sends an event to all connections for a user
func (r *ConnectionRegistry) SendToUser(userID string, event Event) int {
	r.mu.RLock()
	conns := r.GetUserConnections(userID)
	r.mu.RUnlock()

	sent := 0
	for _, conn := range conns {
		select {
		case conn.events <- event:
			sent++
		default:
			r.logger.Warn("failed to send event, channel full",
				"user_id", userID,
				"conn_id", conn.ID,
			)
		}
	}
	return sent
}

// SendToJob sends an event to the connection for a specific job
func (r *ConnectionRegistry) SendToJob(jobID string, event Event) bool {
	r.mu.RLock()
	conn, ok := r.byJobID[jobID]
	r.mu.RUnlock()

	if !ok {
		return false
	}

	select {
	case conn.events <- event:
		return true
	default:
		r.logger.Warn("failed to send event to job, channel full",
			"job_id", jobID,
		)
		return false
	}
}

// Broadcast sends an event to all connections
func (r *ConnectionRegistry) Broadcast(event Event) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sent := 0
	for _, userConns := range r.connections {
		for _, conn := range userConns {
			select {
			case conn.events <- event:
				sent++
			default:
				// Skip if channel is full
			}
		}
	}
	return sent
}

// CleanupStale removes connections older than the given duration
func (r *ConnectionRegistry) CleanupStale(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for userID, userConns := range r.connections {
		for connID, conn := range userConns {
			if conn.CreatedAt.Before(cutoff) {
				if conn.cancel != nil {
					conn.cancel()
				}
				close(conn.events)
				if conn.JobID != "" {
					delete(r.byJobID, conn.JobID)
				}
				delete(userConns, connID)
				removed++
			}
		}

		if len(userConns) == 0 {
			delete(r.connections, userID)
		}
	}

	if removed > 0 {
		r.logger.Info("cleaned up stale connections", "removed", removed)
	}

	return removed
}

// Stats returns registry statistics
func (r *ConnectionRegistry) Stats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	totalConns := 0
	userCounts := make(map[string]int)

	for userID, userConns := range r.connections {
		count := len(userConns)
		userCounts[userID] = count
		totalConns += count
	}

	return map[string]interface{}{
		"total_connections": totalConns,
		"unique_users":      len(r.connections),
		"max_per_user":      r.maxPerUser,
		"active_jobs":       len(r.byJobID),
	}
}

// Events returns the events channel for a connection
func (c *Connection) Events() <-chan Event {
	return c.events
}

// Close cancels the connection
func (c *Connection) Close() {
	if c.cancel != nil {
		c.cancel()
	}
}
