package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/agents"
	"github.com/estara-ai/www/internal/services/ai/prompts"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/estara-ai/www/pkg/sse"
)

// Handler handles AI-related HTTP requests
type Handler struct {
	db        *postgres.DB
	redis     *redisClient.Client
	cfg       *config.Config
	validate  *validator.Validate
	chatAgent *agents.EvaluationChatAgent
	jobQueue  *queue.Queue
	logger    *slog.Logger
}

// NewHandler creates a new AI handler
func NewHandler(
	db *postgres.DB,
	redis *redisClient.Client,
	cfg *config.Config,
	chatAgent *agents.EvaluationChatAgent,
	jobQueue *queue.Queue,
) *Handler {
	return &Handler{
		db:        db,
		redis:     redis,
		cfg:       cfg,
		validate:  validator.New(),
		chatAgent: chatAgent,
		jobQueue:  jobQueue,
		logger:    slog.Default().With("component", "ai_handler"),
	}
}

// ===============================
// Type Definitions
// ===============================

// EvaluationChatRequest represents a request to queue an evaluation chat
type EvaluationChatRequest struct {
	Properties      []PropertyInput  `json:"properties" validate:"required,min=1,max=10"`
	PortfolioID     *string          `json:"portfolioId,omitempty"`
	InvestorProfile *InvestorProfile `json:"investorProfile,omitempty"`
	Message         string           `json:"message" validate:"required,min=1,max=2000"`
	SessionID       *string          `json:"sessionId,omitempty"`
}

// PropertyInput represents a property in a chat request
type PropertyInput struct {
	ID            string  `json:"id" validate:"required"`
	Address       string  `json:"address" validate:"required"`
	City          string  `json:"city" validate:"required"`
	State         string  `json:"state" validate:"required"`
	Price         int     `json:"price" validate:"required,gt=0"`
	Beds          int     `json:"beds,omitempty"`
	Baths         float64 `json:"baths,omitempty"`
	Sqft          int     `json:"sqft,omitempty"`
	EstimatedRent int     `json:"estimatedRent,omitempty"`
	CapRate       string  `json:"capRate,omitempty"`
	YearBuilt     int     `json:"yearBuilt,omitempty"`
	PropertyType  string  `json:"propertyType,omitempty"`
}

// InvestorProfile represents the user's investment profile
type InvestorProfile struct {
	RiskTolerance     string `json:"riskTolerance" validate:"omitempty,oneof=conservative moderate aggressive"`
	InvestmentHorizon string `json:"investmentHorizon,omitempty"`
	AvailableCapital  int    `json:"availableCapital,omitempty"`
}

// EvaluationChatResponse is returned when queueing a chat job
type EvaluationChatResponse struct {
	Success   bool   `json:"success"`
	JobID     string `json:"jobId"`
	SessionID string `json:"sessionId"`
	StreamURL string `json:"streamUrl"`
}

// ChatSession represents a stored chat session
type ChatSession struct {
	ID                string     `json:"id"`
	UserID            string     `json:"userId"`
	PropertyIDs       []string   `json:"propertyIds"`
	PropertyCount     int        `json:"propertyCount"`
	InvestorProfile   *string    `json:"investorProfile,omitempty"`
	PortfolioSnapshot *string    `json:"portfolioSnapshot,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	LastMessageAt     *time.Time `json:"lastMessageAt,omitempty"`
}

// ChatMessage represents a message in a chat session
type ChatMessage struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"sessionId"`
	Role         string          `json:"role"`
	Content      string          `json:"content"`
	ParsedBlocks json.RawMessage `json:"parsedBlocks,omitempty"`
	TokenUsage   *TokenUsage     `json:"tokenUsage,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// TokenUsage represents token usage for a message
type TokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// InvestmentPlanningRequest represents a request to queue an investment plan
type InvestmentPlanningRequest struct {
	Locations            []string         `json:"locations" validate:"required,min=1,max=5"`
	Budget               int              `json:"budget" validate:"required,gt=0"`
	DownPaymentPercent   float64          `json:"downPaymentPercent" validate:"required,gt=0,lte=100"`
	Strategy             string           `json:"strategy" validate:"required,oneof=cash-flow appreciation balanced"`
	RiskTolerance        string           `json:"riskTolerance" validate:"required,oneof=conservative moderate aggressive"`
	MaxProperties        int              `json:"maxProperties" validate:"omitempty,gte=1,lte=20"`
	IncludePortfolio     bool             `json:"includePortfolio"`
	YearlyBudgets        []YearlyBudget   `json:"yearlyBudgets,omitempty"`
	InvestorProfile      *InvestorProfile `json:"investorProfile,omitempty"`
}

// YearlyBudget represents a budget for a specific year
type YearlyBudget struct {
	Year      int      `json:"year" validate:"required,gte=1,lte=5"`
	Budget    int      `json:"budget" validate:"required,gt=0"`
	Locations []string `json:"locations,omitempty"`
}

// MarketAnalysisRequest represents a request to queue a market analysis
type MarketAnalysisRequest struct {
	Location     string `json:"location" validate:"required"`
	CacheKey     string `json:"cacheKey" validate:"required"`
	ForceRefresh bool   `json:"forceRefresh"`
}

// ===============================
// Evaluation Chat Endpoints
// ===============================

// QueueEvaluationChat queues an evaluation chat job
func (h *Handler) QueueEvaluationChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req EvaluationChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Create or get session
	var sessionID string
	if req.SessionID != nil && *req.SessionID != "" {
		// Validate existing session belongs to user
		sessionID = *req.SessionID
		exists, err := h.validateSessionOwnership(ctx, sessionID, user.UserID)
		if err != nil || !exists {
			httputil.BadRequest(w, "invalid session")
			return
		}
	} else {
		// Create new session
		var err error
		sessionID, err = h.createChatSession(ctx, user.UserID, req.Properties, req.InvestorProfile)
		if err != nil {
			h.logger.Error("failed to create chat session", "error", err)
			httputil.InternalError(w, fmt.Errorf("failed to create session"))
			return
		}
	}

	// Save user message to database
	if err := h.saveChatMessage(ctx, sessionID, "user", req.Message, nil); err != nil {
		h.logger.Error("failed to save user message", "error", err)
		// Continue - don't fail the request
	}

	// Convert properties to prompt context
	properties := make([]prompts.PropertyContext, 0, len(req.Properties))
	for _, p := range req.Properties {
		properties = append(properties, prompts.PropertyContext{
			ID:            p.ID,
			Address:       p.Address,
			City:          p.City,
			State:         p.State,
			Price:         p.Price,
			Beds:          p.Beds,
			Baths:         p.Baths,
			Sqft:          p.Sqft,
			EstimatedRent: p.EstimatedRent,
			CapRate:       p.CapRate,
			YearBuilt:     p.YearBuilt,
			PropertyType:  p.PropertyType,
		})
	}

	// Build payload
	payload := map[string]interface{}{
		"session_id": sessionID,
		"message":    req.Message,
		"properties": properties,
	}

	if req.InvestorProfile != nil {
		payload["investor_profile"] = map[string]interface{}{
			"risk_tolerance":     req.InvestorProfile.RiskTolerance,
			"investment_horizon": req.InvestorProfile.InvestmentHorizon,
			"available_capital":  req.InvestorProfile.AvailableCapital,
		}
	}

	// Create job
	job := queue.NewJob(queue.JobTypeEvaluationChat, user.UserID, payload)

	// Enqueue
	jobID, err := h.jobQueue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue chat job", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to queue job"))
		return
	}

	h.logger.Info("evaluation chat queued",
		"job_id", jobID,
		"session_id", sessionID,
		"user_id", user.UserID,
		"properties", len(req.Properties),
	)

	httputil.JSON(w, http.StatusOK, EvaluationChatResponse{
		Success:   true,
		JobID:     jobID,
		SessionID: sessionID,
		StreamURL: fmt.Sprintf("/api/ai/evaluate/chat/stream?jobId=%s", jobID),
	})
}

// StreamEvaluationChat streams evaluation chat responses via SSE
func (h *Handler) StreamEvaluationChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get token from query param (EventSource limitation)
	token := r.URL.Query().Get("token")
	if token == "" {
		httputil.Error(w, http.StatusUnauthorized, "token required")
		return
	}

	// Get job ID
	jobID := r.URL.Query().Get("jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId required")
		return
	}

	// Create SSE writer
	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("streaming not supported"))
		return
	}

	// Start heartbeat
	stopHeartbeat := sseWriter.StartHeartbeat(sse.HeartbeatInterval)
	defer close(stopHeartbeat)

	// Get job
	job, err := h.jobQueue.GetJob(jobID)
	if err != nil {
		sseWriter.WriteError("job not found")
		return
	}

	// Subscribe to progress
	progressChan := h.jobQueue.Subscribe(jobID)

	// Extract payload
	sessionID, _ := job.Payload["session_id"].(string)
	message, _ := job.Payload["message"].(string)

	// Build chat request
	chatReq := agents.ChatRequest{
		SessionID: sessionID,
		UserID:    job.UserID,
		Message:   message,
	}

	// Extract properties
	if propsRaw, ok := job.Payload["properties"].([]interface{}); ok {
		for _, pRaw := range propsRaw {
			if pMap, ok := pRaw.(map[string]interface{}); ok {
				chatReq.Properties = append(chatReq.Properties, prompts.PropertyContext{
					ID:            getString(pMap, "id"),
					Address:       getString(pMap, "address"),
					City:          getString(pMap, "city"),
					State:         getString(pMap, "state"),
					Price:         getInt(pMap, "price"),
					Beds:          getInt(pMap, "beds"),
					Baths:         getFloat(pMap, "baths"),
					Sqft:          getInt(pMap, "sqft"),
					EstimatedRent: getInt(pMap, "estimated_rent"),
					CapRate:       getString(pMap, "cap_rate"),
					YearBuilt:     getInt(pMap, "year_built"),
					PropertyType:  getString(pMap, "property_type"),
				})
			}
		}
	}

	// Extract investor profile
	if profileRaw, ok := job.Payload["investor_profile"].(map[string]interface{}); ok {
		chatReq.InvestorProfile = &prompts.InvestorProfile{
			RiskTolerance:     getString(profileRaw, "risk_tolerance"),
			InvestmentHorizon: getString(profileRaw, "investment_horizon"),
			AvailableCapital:  getInt(profileRaw, "available_capital"),
		}
	}

	// Load conversation history
	history, err := h.getSessionHistory(ctx, sessionID)
	if err == nil && len(history) > 0 {
		for _, msg := range history {
			chatReq.History = append(chatReq.History, agents.ChatMessage{
				Role:      msg.Role,
				Content:   msg.Content,
				Timestamp: msg.CreatedAt,
			})
		}
	}

	// Create event channel
	events := make(chan agents.ChatEvent, 100)

	// Start streaming in goroutine
	go func() {
		if err := h.chatAgent.Stream(ctx, chatReq, events); err != nil {
			h.logger.Error("chat stream failed", "error", err)
			h.jobQueue.Fail(jobID, err)
		}
	}()

	// Forward events to SSE
	var fullContent string
	for {
		select {
		case <-ctx.Done():
			h.logger.Info("client disconnected", "job_id", jobID)
			return

		case event, ok := <-events:
			if !ok {
				// Channel closed - stream complete
				return
			}

			switch event.Type {
			case "text":
				sseWriter.WriteEventJSON("text", map[string]interface{}{
					"type":    "text",
					"content": event.Content,
				})

			case "insight", "stress_test", "metrics", "comparison", "disclaimer":
				sseWriter.WriteEventJSON(event.Type, map[string]interface{}{
					"type":    event.Type,
					"content": event.Content,
				})

			case "complete":
				fullContent, _ = event.Content.(string)

				// Save assistant message
				if sessionID != "" && fullContent != "" {
					h.saveChatMessage(ctx, sessionID, "assistant", fullContent, nil)
				}

				// Complete the job
				h.jobQueue.Complete(jobID, &queue.JobResult{
					Data: map[string]interface{}{
						"session_id": sessionID,
						"content":    fullContent,
					},
				})

				sseWriter.WriteEventJSON("complete", map[string]interface{}{
					"type":      "complete",
					"sessionId": sessionID,
					"content":   fullContent,
				})
				return

			case "error":
				sseWriter.WriteError(event.Error)
				h.jobQueue.Fail(jobID, fmt.Errorf("%s", event.Error))
				return
			}

		case progress := <-progressChan:
			sseWriter.WriteEventJSON("progress", map[string]interface{}{
				"progress": progress.Progress,
				"stage":    progress.Stage,
				"message":  progress.Message,
			})
		}
	}
}

// ListChatSessions lists chat sessions for the current user
func (h *Handler) ListChatSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Table names use plural form (Prisma @@map): evaluation_chat_sessions, evaluation_chat_messages
	query := `
		SELECT
			id, user_id, cached_property_ids, investor_profile, portfolio_snapshot,
			created_at, updated_at,
			(SELECT MAX(created_at) FROM evaluation_chat_messages WHERE session_id = s.id) as last_message_at
		FROM evaluation_chat_sessions s
		WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT 50
	`

	rows, err := h.db.Main.Query(ctx, query, user.UserID)
	if err != nil {
		h.logger.Error("failed to list sessions", "error", err)
		// Return empty array on error (graceful degradation)
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"sessions": []ChatSession{},
		})
		return
	}
	defer rows.Close()

	sessions := make([]ChatSession, 0)
	for rows.Next() {
		var s ChatSession
		var propertyIDs []string
		var investorProfile, portfolioSnapshot *string
		var lastMessageAt *time.Time

		err := rows.Scan(
			&s.ID, &s.UserID, &propertyIDs, &investorProfile, &portfolioSnapshot,
			&s.CreatedAt, &s.UpdatedAt, &lastMessageAt,
		)
		if err != nil {
			h.logger.Error("failed to scan session", "error", err)
			continue
		}

		s.PropertyIDs = propertyIDs
		s.PropertyCount = len(propertyIDs)
		s.InvestorProfile = investorProfile
		s.PortfolioSnapshot = portfolioSnapshot
		s.LastMessageAt = lastMessageAt
		sessions = append(sessions, s)
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"sessions": sessions,
	})
}

// GetChatSession gets a specific chat session
func (h *Handler) GetChatSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sessionID := chi.URLParam(r, "sessionId")
	if sessionID == "" {
		httputil.BadRequest(w, "sessionId is required")
		return
	}

	// Get session (table: evaluation_chat_sessions per Prisma @@map)
	query := `
		SELECT id, user_id, cached_property_ids, investor_profile, portfolio_snapshot, created_at, updated_at
		FROM evaluation_chat_sessions
		WHERE id = $1 AND user_id = $2
	`

	var session ChatSession
	var propertyIDs []string
	var investorProfile, portfolioSnapshot *string

	err := h.db.Main.QueryRow(ctx, query, sessionID, user.UserID).Scan(
		&session.ID, &session.UserID, &propertyIDs, &investorProfile, &portfolioSnapshot,
		&session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	session.PropertyIDs = propertyIDs
	session.PropertyCount = len(propertyIDs)
	session.InvestorProfile = investorProfile
	session.PortfolioSnapshot = portfolioSnapshot

	// Get messages
	messagesQuery := `
		SELECT id, session_id, role, content, parsed_blocks, token_usage, created_at
		FROM evaluation_chat_messages
		WHERE session_id = $1
		ORDER BY created_at ASC
	`

	rows, err := h.db.Main.Query(ctx, messagesQuery, sessionID)
	if err != nil {
		h.logger.Error("failed to get messages", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to get messages"))
		return
	}
	defer rows.Close()

	messages := make([]ChatMessage, 0)
	for rows.Next() {
		var msg ChatMessage
		var parsedBlocks, tokenUsage *string

		err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &parsedBlocks, &tokenUsage, &msg.CreatedAt,
		)
		if err != nil {
			continue
		}

		if parsedBlocks != nil {
			msg.ParsedBlocks = json.RawMessage(*parsedBlocks)
		}
		if tokenUsage != nil {
			var tu TokenUsage
			if json.Unmarshal([]byte(*tokenUsage), &tu) == nil {
				msg.TokenUsage = &tu
			}
		}

		messages = append(messages, msg)
	}

	// Get cached properties
	properties := make([]PropertyInput, 0)
	if len(propertyIDs) > 0 {
		propQuery := `
			SELECT id, listing_id, address, city, state, zip_code, price, beds, baths, sqft,
			       estimated_rent, cap_rate, listing_url, image_url
			FROM cached_properties
			WHERE id = ANY($1)
		`

		propRows, err := h.db.Main.Query(ctx, propQuery, propertyIDs)
		if err == nil {
			defer propRows.Close()
			for propRows.Next() {
				var p PropertyInput
				var listingID, zipCode, listingURL, imageURL *string
				var capRate *float64

				propRows.Scan(
					&p.ID, &listingID, &p.Address, &p.City, &p.State, &zipCode,
					&p.Price, &p.Beds, &p.Baths, &p.Sqft, &p.EstimatedRent, &capRate,
					&listingURL, &imageURL,
				)

				if capRate != nil {
					p.CapRate = fmt.Sprintf("%.2f%%", *capRate)
				}
				properties = append(properties, p)
			}
		}
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"session":    session,
		"messages":   messages,
		"properties": properties,
	})
}

// DeleteChatSession deletes a chat session
func (h *Handler) DeleteChatSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sessionID := chi.URLParam(r, "sessionId")
	if sessionID == "" {
		httputil.BadRequest(w, "sessionId is required")
		return
	}

	// Delete session and messages (cascade)
	query := `DELETE FROM evaluation_chat_sessions WHERE id = $1 AND user_id = $2`

	result, err := h.db.Main.Exec(ctx, query, sessionID, user.UserID)
	if err != nil {
		h.logger.Error("failed to delete session", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to delete session"))
		return
	}

	if result.RowsAffected() == 0 {
		httputil.NotFound(w, "session not found")
		return
	}

	h.logger.Info("chat session deleted",
		"session_id", sessionID,
		"user_id", user.UserID,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "session deleted",
	})
}

// ===============================
// Investment Planning Endpoints
// ===============================

// QueueInvestmentPlan queues an investment planning job
func (h *Handler) QueueInvestmentPlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req InvestmentPlanningRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Set defaults
	if req.MaxProperties == 0 {
		req.MaxProperties = 5
	}

	// Build payload
	payload := map[string]interface{}{
		"locations":              req.Locations,
		"budget":                 req.Budget,
		"down_payment_percent":   req.DownPaymentPercent,
		"strategy":               req.Strategy,
		"risk_tolerance":         req.RiskTolerance,
		"max_properties":         req.MaxProperties,
		"include_portfolio":      req.IncludePortfolio,
	}

	if len(req.YearlyBudgets) > 0 {
		payload["yearly_budgets"] = req.YearlyBudgets
	}

	// Create job
	job := queue.NewJob(queue.JobTypeInvestmentPlanning, user.UserID, payload)

	// Enqueue
	jobID, err := h.jobQueue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue investment planning job", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to queue job"))
		return
	}

	h.logger.Info("investment planning queued",
		"job_id", jobID,
		"user_id", user.UserID,
		"locations", req.Locations,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"jobId":     jobID,
		"streamUrl": fmt.Sprintf("/api/ai/investment-planning/stream?jobId=%s", jobID),
	})
}

// StreamInvestmentPlan streams investment planning results via SSE
func (h *Handler) StreamInvestmentPlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	jobID := r.URL.Query().Get("jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId required")
		return
	}

	// Create SSE writer
	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("streaming not supported"))
		return
	}

	// Start heartbeat
	stopHeartbeat := sseWriter.StartHeartbeat(sse.HeartbeatInterval)
	defer close(stopHeartbeat)

	// Subscribe to progress
	progressChan := h.jobQueue.Subscribe(jobID)

	// Forward events to SSE
	for {
		select {
		case <-ctx.Done():
			h.logger.Info("client disconnected", "job_id", jobID)
			return

		case event, ok := <-progressChan:
			if !ok {
				// Job completed
				result, err := h.jobQueue.GetResult(jobID)
				if err == nil && result.Status == queue.JobStatusCompleted {
					sseWriter.WriteComplete(result.Data)
				}
				return
			}

			switch event.Stage {
			case "completed":
				sseWriter.WriteComplete(event.Data)
				return
			case "failed":
				sseWriter.WriteError(event.Message)
				return
			default:
				sseWriter.WriteEventJSON("progress", map[string]interface{}{
					"progress": event.Progress,
					"stage":    event.Stage,
					"message":  event.Message,
				})
			}
		}
	}
}

// GetInvestmentPlan gets an investment plan by job ID
func (h *Handler) GetInvestmentPlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId is required")
		return
	}

	// Get job
	job, err := h.jobQueue.GetJob(jobID)
	if err != nil {
		httputil.NotFound(w, "job not found")
		return
	}

	// Verify ownership
	if job.UserID != user.UserID {
		httputil.NotFound(w, "job not found")
		return
	}

	// Get result if completed
	var result *queue.JobResult
	if job.Status == queue.JobStatusCompleted {
		result, _ = h.jobQueue.GetResult(jobID)
	}

	response := map[string]interface{}{
		"success": true,
		"job": map[string]interface{}{
			"id":        job.ID,
			"status":    string(job.Status),
			"createdAt": job.CreatedAt.Format(time.RFC3339),
			"updatedAt": job.UpdatedAt.Format(time.RFC3339),
			"request":   job.Payload,
		},
	}

	if result != nil {
		response["job"].(map[string]interface{})["result"] = result.Data
		response["job"].(map[string]interface{})["completedAt"] = result.CompletedAt.Format(time.RFC3339)
	}

	if job.Error != "" {
		response["job"].(map[string]interface{})["error"] = job.Error
	}

	httputil.JSON(w, http.StatusOK, response)
}

// InvestmentPlanHistoryItem represents a single plan in the history
type InvestmentPlanHistoryItem struct {
	ID                   string                 `json:"id"`
	ResponseKey          string                 `json:"responseKey"`
	UserID               string                 `json:"userId"`
	Locations            []string               `json:"locations"`
	Strategy             string                 `json:"strategy"`
	AvailableCapital     int                    `json:"availableCapital"`
	Status               string                 `json:"status"`
	CreatedAt            string                 `json:"createdAt"`
	Metrics              map[string]interface{} `json:"metrics,omitempty"`
	FinancialAssumptions map[string]interface{} `json:"financialAssumptions,omitempty"`
	RecommendedZipCodes  []string               `json:"recommendedZipCodes,omitempty"`
}

// InvestmentPlanHistoryResponse matches www_v1 /api/ai/investment-planning/history response
type InvestmentPlanHistoryResponse struct {
	Success    bool                        `json:"success"`
	Plans      []InvestmentPlanHistoryItem `json:"plans"`
	Pagination struct {
		Page       int `json:"page"`
		Limit      int `json:"limit"`
		Total      int `json:"total"`
		TotalPages int `json:"totalPages"`
	} `json:"pagination"`
}

// GetInvestmentPlanHistory returns the user's investment planning history
func (h *Handler) GetInvestmentPlanHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Parse pagination params
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if val, err := fmt.Sscanf(p, "%d", &page); err == nil && val > 0 {
			page = val
		}
	}
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := fmt.Sscanf(l, "%d", &limit); err == nil && val > 0 && val <= 50 {
			limit = val
		}
	}
	offset := (page - 1) * limit

	// Query investment planning records from analysis_cache table (Prisma @@map)
	// Prisma uses camelCase column names (no @map directives in schema)
	// Filter by feature = 'investment_planning'
	query := `
		SELECT
			id,
			key,
			"userId",
			metadata,
			"metricsData",
			'COMPLETED' as status,
			"createdAt"
		FROM analysis_cache
		WHERE "userId" = $1 AND feature = 'investment_planning'
		ORDER BY "createdAt" DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := h.db.Main.Query(ctx, query, user.UserID, limit, offset)
	if err != nil {
		h.logger.Error("failed to get investment plan history", "error", err)
		// Return empty on error
		httputil.JSON(w, http.StatusOK, InvestmentPlanHistoryResponse{
			Success: true,
			Plans:   []InvestmentPlanHistoryItem{},
			Pagination: struct {
				Page       int `json:"page"`
				Limit      int `json:"limit"`
				Total      int `json:"total"`
				TotalPages int `json:"totalPages"`
			}{
				Page:       page,
				Limit:      limit,
				Total:      0,
				TotalPages: 0,
			},
		})
		return
	}
	defer rows.Close()

	plans := make([]InvestmentPlanHistoryItem, 0)
	for rows.Next() {
		var item InvestmentPlanHistoryItem
		var criteriaData, metricsData *string
		var createdAt time.Time

		err := rows.Scan(
			&item.ID,
			&item.ResponseKey,
			&item.UserID,
			&criteriaData,
			&metricsData,
			&item.Status,
			&createdAt,
		)
		if err != nil {
			h.logger.Warn("failed to scan plan history item", "error", err)
			continue
		}

		item.CreatedAt = createdAt.Format(time.RFC3339)

		// Parse criteria data
		if criteriaData != nil && *criteriaData != "" {
			var criteria map[string]interface{}
			if json.Unmarshal([]byte(*criteriaData), &criteria) == nil {
				if locations, ok := criteria["locations"].([]interface{}); ok {
					item.Locations = make([]string, 0, len(locations))
					for _, l := range locations {
						if s, ok := l.(string); ok {
							item.Locations = append(item.Locations, s)
						}
					}
				}
				if strategy, ok := criteria["strategy"].(string); ok {
					item.Strategy = strategy
				}
				if capital, ok := criteria["availableCapital"].(float64); ok {
					item.AvailableCapital = int(capital)
				}
				if budget, ok := criteria["budget"].(float64); ok {
					item.AvailableCapital = int(budget)
				}
				if assumptions, ok := criteria["financialAssumptions"].(map[string]interface{}); ok {
					item.FinancialAssumptions = assumptions
				}
			}
		}

		// Parse metrics data
		if metricsData != nil && *metricsData != "" {
			var metrics map[string]interface{}
			if json.Unmarshal([]byte(*metricsData), &metrics) == nil {
				item.Metrics = metrics
				if zips, ok := metrics["recommendedZipCodes"].([]interface{}); ok {
					item.RecommendedZipCodes = make([]string, 0, len(zips))
					for _, z := range zips {
						if s, ok := z.(string); ok {
							item.RecommendedZipCodes = append(item.RecommendedZipCodes, s)
						}
					}
				}
			}
		}

		plans = append(plans, item)
	}

	// Get total count
	var total int
	h.db.Main.QueryRow(ctx, `SELECT COUNT(*) FROM analysis_cache WHERE "userId" = $1 AND feature = 'investment_planning'`, user.UserID).Scan(&total)

	totalPages := total / limit
	if total%limit > 0 {
		totalPages++
	}

	response := InvestmentPlanHistoryResponse{
		Success: true,
		Plans:   plans,
		Pagination: struct {
			Page       int `json:"page"`
			Limit      int `json:"limit"`
			Total      int `json:"total"`
			TotalPages int `json:"totalPages"`
		}{
			Page:       page,
			Limit:      limit,
			Total:      total,
			TotalPages: totalPages,
		},
	}

	httputil.JSON(w, http.StatusOK, response)
}

// ===============================
// Market Analysis Endpoints
// ===============================

// QueueAnalysis queues a market analysis job
func (h *Handler) QueueAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req MarketAnalysisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// Check cache first if not forcing refresh
	if !req.ForceRefresh {
		cacheKey := fmt.Sprintf("analysis:%s", req.CacheKey)
		cached, err := h.redis.Client.Get(ctx, cacheKey).Bytes()
		if err == nil && len(cached) > 0 {
			h.logger.Info("returning cached analysis", "cache_key", req.CacheKey)
			httputil.JSON(w, http.StatusOK, map[string]interface{}{
				"success":  true,
				"cached":   true,
				"cacheKey": req.CacheKey,
				"data":     json.RawMessage(cached),
			})
			return
		}
	}

	// Build payload
	payload := map[string]interface{}{
		"location":      req.Location,
		"cache_key":     req.CacheKey,
		"force_refresh": req.ForceRefresh,
	}

	// Create job
	job := queue.NewJob(queue.JobTypeMarketAnalysis, user.UserID, payload)

	// Enqueue
	jobID, err := h.jobQueue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue analysis job", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to queue job"))
		return
	}

	h.logger.Info("market analysis queued",
		"job_id", jobID,
		"user_id", user.UserID,
		"location", req.Location,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"jobId":     jobID,
		"streamUrl": fmt.Sprintf("/api/ai/analysis/stream?jobId=%s", jobID),
	})
}

// StreamAnalysis streams market analysis results via SSE
func (h *Handler) StreamAnalysis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	jobID := r.URL.Query().Get("jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId required")
		return
	}

	// Create SSE writer
	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("streaming not supported"))
		return
	}

	// Start heartbeat
	stopHeartbeat := sseWriter.StartHeartbeat(sse.HeartbeatInterval)
	defer close(stopHeartbeat)

	// Subscribe to progress
	progressChan := h.jobQueue.Subscribe(jobID)

	// Forward events to SSE
	for {
		select {
		case <-ctx.Done():
			h.logger.Info("client disconnected", "job_id", jobID)
			return

		case event, ok := <-progressChan:
			if !ok {
				result, err := h.jobQueue.GetResult(jobID)
				if err == nil && result.Status == queue.JobStatusCompleted {
					sseWriter.WriteComplete(result.Data)
				}
				return
			}

			switch event.Stage {
			case "completed":
				sseWriter.WriteComplete(event.Data)
				return
			case "failed":
				sseWriter.WriteError(event.Message)
				return
			default:
				sseWriter.WriteEventJSON("progress", map[string]interface{}{
					"progress": event.Progress,
					"stage":    event.Stage,
					"message":  event.Message,
				})
			}
		}
	}
}

// ListAnalysisJobs lists analysis jobs for the current user
func (h *Handler) ListAnalysisJobs(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUserFromContext(r.Context())
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	status := (*queue.JobStatus)(nil)
	if s := r.URL.Query().Get("status"); s != "" {
		js := queue.JobStatus(s)
		status = &js
	}

	jobs := h.jobQueue.GetUserJobs(user.UserID, status)

	// Filter to analysis jobs only
	analysisJobs := make([]map[string]interface{}, 0)
	for _, job := range jobs {
		if job.Type == queue.JobTypeMarketAnalysis {
			analysisJobs = append(analysisJobs, map[string]interface{}{
				"id":        job.ID,
				"status":    string(job.Status),
				"location":  job.Payload["location"],
				"cacheKey":  job.Payload["cache_key"],
				"createdAt": job.CreatedAt.Format(time.RFC3339),
				"updatedAt": job.UpdatedAt.Format(time.RFC3339),
			})
		}
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"jobs":    analysisJobs,
	})
}

// RetryAnalysis retries a failed analysis job
func (h *Handler) RetryAnalysis(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUserFromContext(r.Context())
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId is required")
		return
	}

	// Get original job
	job, err := h.jobQueue.GetJob(jobID)
	if err != nil {
		httputil.NotFound(w, "job not found")
		return
	}

	if job.UserID != user.UserID {
		httputil.NotFound(w, "job not found")
		return
	}

	if job.Status != queue.JobStatusFailed {
		httputil.BadRequest(w, "can only retry failed jobs")
		return
	}

	// Create new job with same payload
	newJob := queue.NewJob(job.Type, job.UserID, job.Payload)

	newJobID, err := h.jobQueue.Enqueue(newJob)
	if err != nil {
		h.logger.Error("failed to enqueue retry job", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to queue retry"))
		return
	}

	h.logger.Info("job retried",
		"original_job_id", jobID,
		"new_job_id", newJobID,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"jobId":     newJobID,
		"streamUrl": fmt.Sprintf("/api/ai/analysis/stream?jobId=%s", newJobID),
	})
}

// CancelAnalysis cancels an in-progress analysis job
func (h *Handler) CancelAnalysis(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUserFromContext(r.Context())
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId is required")
		return
	}

	job, err := h.jobQueue.GetJob(jobID)
	if err != nil {
		httputil.NotFound(w, "job not found")
		return
	}

	if job.UserID != user.UserID {
		httputil.NotFound(w, "job not found")
		return
	}

	if err := h.jobQueue.Cancel(jobID); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	h.logger.Info("job cancelled", "job_id", jobID)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "job cancelled",
	})
}

// ===============================
// Cache Management
// ===============================

// InvalidateCache invalidates cache for a user
func (h *Handler) InvalidateCache(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Strategy string  `json:"strategy" validate:"required,oneof=all expired type user key"`
		Type     *string `json:"type,omitempty"`
		CacheKey *string `json:"cacheKey,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.validate.Struct(req); err != nil {
		httputil.BadRequest(w, "validation failed: "+err.Error())
		return
	}

	// For client users, require specific cache key
	if req.Strategy == "user" && (req.CacheKey == nil || *req.CacheKey == "") {
		httputil.BadRequest(w, "cacheKey required for user strategy")
		return
	}

	var redisDeleted, dbDeleted int64

	switch req.Strategy {
	case "key":
		if req.CacheKey == nil {
			httputil.BadRequest(w, "cacheKey required")
			return
		}
		// Delete specific key
		cacheKey := fmt.Sprintf("analysis:%s:%s", user.UserID, *req.CacheKey)
		result, _ := h.redis.Client.Del(ctx, cacheKey).Result()
		redisDeleted = result

		// Delete from database
		dbResult, _ := h.db.Main.Exec(ctx,
			`DELETE FROM "AiResponseCache" WHERE user_id = $1 AND cache_key = $2`,
			user.UserID, *req.CacheKey,
		)
		dbDeleted = dbResult.RowsAffected()

	case "type":
		if req.Type == nil {
			httputil.BadRequest(w, "type required")
			return
		}
		// Delete by type pattern
		pattern := fmt.Sprintf("*:%s:%s:*", user.UserID, *req.Type)
		keys, _ := h.redis.Client.Keys(ctx, pattern).Result()
		if len(keys) > 0 {
			result, _ := h.redis.Client.Del(ctx, keys...).Result()
			redisDeleted = result
		}

		// Delete from database
		dbResult, _ := h.db.Main.Exec(ctx,
			`DELETE FROM "AiResponseCache" WHERE user_id = $1 AND cache_type = $2`,
			user.UserID, *req.Type,
		)
		dbDeleted = dbResult.RowsAffected()

	default:
		httputil.BadRequest(w, "invalid strategy for client user")
		return
	}

	h.logger.Info("cache invalidated",
		"user_id", user.UserID,
		"strategy", req.Strategy,
		"redis_deleted", redisDeleted,
		"db_deleted", dbDeleted,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"strategy":     req.Strategy,
		"redisDeleted": redisDeleted,
		"dbDeleted":    dbDeleted,
	})
}

// ===============================
// Helper Methods
// ===============================

// createChatSession creates a new evaluation chat session
func (h *Handler) createChatSession(ctx context.Context, userID string, properties []PropertyInput, profile *InvestorProfile) (string, error) {
	// Cache properties first
	propertyIDs := make([]string, 0, len(properties))
	for _, p := range properties {
		// Upsert property to cache
		var cachedID string
		err := h.db.Main.QueryRow(ctx, `
			INSERT INTO cached_properties (listing_id, provider, address, city, state, price, beds, baths, sqft, estimated_rent, cap_rate)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (listing_id) DO UPDATE SET last_used_at = NOW()
			RETURNING id
		`, p.ID, "discovery", p.Address, p.City, p.State, p.Price, p.Beds, p.Baths, p.Sqft, p.EstimatedRent, parseCapRate(p.CapRate)).Scan(&cachedID)

		if err != nil {
			h.logger.Warn("failed to cache property", "error", err, "property_id", p.ID)
			continue
		}
		propertyIDs = append(propertyIDs, cachedID)
	}

	// Create session
	var investorProfileJSON *string
	if profile != nil {
		b, _ := json.Marshal(profile)
		s := string(b)
		investorProfileJSON = &s
	}

	var sessionID string
	err := h.db.Main.QueryRow(ctx, `
		INSERT INTO evaluation_chat_sessions (user_id, cached_property_ids, investor_profile)
		VALUES ($1, $2, $3)
		RETURNING id
	`, userID, propertyIDs, investorProfileJSON).Scan(&sessionID)

	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return sessionID, nil
}

// validateSessionOwnership checks if a session belongs to a user
func (h *Handler) validateSessionOwnership(ctx context.Context, sessionID, userID string) (bool, error) {
	var exists bool
	err := h.db.Main.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM evaluation_chat_sessions WHERE id = $1 AND user_id = $2)`,
		sessionID, userID,
	).Scan(&exists)
	return exists, err
}

// saveChatMessage saves a message to the database
func (h *Handler) saveChatMessage(ctx context.Context, sessionID, role, content string, blocks []byte) error {
	var blocksJSON *string
	if len(blocks) > 0 {
		s := string(blocks)
		blocksJSON = &s
	}

	_, err := h.db.Main.Exec(ctx, `
		INSERT INTO evaluation_chat_messages (session_id, role, content, parsed_blocks)
		VALUES ($1, $2, $3, $4)
	`, sessionID, role, content, blocksJSON)

	if err != nil {
		return fmt.Errorf("failed to save message: %w", err)
	}

	// Update session timestamp
	h.db.Main.Exec(ctx, `UPDATE evaluation_chat_sessions SET updated_at = NOW() WHERE id = $1`, sessionID)

	return nil
}

// getSessionHistory retrieves conversation history for a session
func (h *Handler) getSessionHistory(ctx context.Context, sessionID string) ([]ChatMessage, error) {
	query := `
		SELECT id, session_id, role, content, created_at
		FROM evaluation_chat_messages
		WHERE session_id = $1
		ORDER BY created_at ASC
	`

	rows, err := h.db.Main.Query(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]ChatMessage, 0)
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.CreatedAt); err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// Helper functions for extracting typed values from map
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func parseCapRate(s string) *float64 {
	if s == "" {
		return nil
	}
	// Parse "5.5%" or "5.5" format
	var v float64
	fmt.Sscanf(s, "%f", &v)
	if v > 0 {
		return &v
	}
	return nil
}
