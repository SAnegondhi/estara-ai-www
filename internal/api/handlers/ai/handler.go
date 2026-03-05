package ai

import (
	"context"
	context2 "context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/api/handlers/util"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/agents"
	"github.com/estara-ai/www/internal/services/ai/compliance"
	"github.com/estara-ai/www/internal/services/ai/prompts"
	"github.com/estara-ai/www/internal/services/investment/expenses"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/market/estimation"
	"github.com/estara-ai/www/internal/services/market/timeseries"
	"github.com/estara-ai/www/internal/services/market/trends"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/pkg/httputil"
	"github.com/estara-ai/www/pkg/sse"
)

// Handler handles AI-related HTTP requests
type Handler struct {
	store     *dbstore.Store
	redis     *redisClient.Client
	cfg       *config.Config
	validate  *validator.Validate
	chatAgent *agents.EvaluationChatAgent
	jobQueue  *queue.Queue
	logger    *slog.Logger
	// ADR-069: Economic data provider for AI prompts
	economics     *economics.Aggregator
	econContext   *prompts.EconomicContextBuilder
	// ADR-088 Phase 9: Efficient frontier optimizer for interactive workspace
	frontierOptimizer *optimization.FrontierOptimizer
	// ADR-088 Phase 12: Full pipeline (discover → score → frontier) for /run endpoint
	investmentOptimizer *optimization.Service
	propertyFinder      *finder.Orchestrator
	// Market data aggregator (L0/L1/L2 caching) — used for quality-gate benchmarks
	marketData *aggregator.Aggregator
	// MetroReader provides city snapshots including HUD FMR by bedroom count
	metroReader *timeseries.MetroReader
	// ADR-100: Market trends service for historical time series in report enrichment
	trendsService *trends.Service
	// AI fallback estimator — used when aggregator returns no price/rent data
	marketEstimator *estimation.AIEstimator
}

// NewHandler creates a new AI handler
func NewHandler(
	store *dbstore.Store,
	redis *redisClient.Client,
	cfg *config.Config,
	chatAgent *agents.EvaluationChatAgent,
	jobQueue *queue.Queue,
	econ *economics.Aggregator,
) *Handler {
	h := &Handler{
		store:     store,
		redis:     redis,
		cfg:       cfg,
		validate:  validator.New(),
		chatAgent: chatAgent,
		jobQueue:  jobQueue,
		logger:    slog.Default().With("component", "ai_handler"),
		economics: econ,
	}
	// ADR-069: Initialize economic context builder if economics aggregator is available
	if econ != nil {
		h.econContext = prompts.NewEconomicContextBuilder(econ)
	}
	return h
}

// WithFrontierOptimizer injects the frontier optimizer for ADR-088 Phase 9 endpoints.
func (h *Handler) WithFrontierOptimizer(fo *optimization.FrontierOptimizer) *Handler {
	h.frontierOptimizer = fo
	return h
}

// WithInvestmentOptimizer injects the investment optimizer for ADR-088 Phase 12 /run endpoint.
func (h *Handler) WithInvestmentOptimizer(svc *optimization.Service) *Handler {
	h.investmentOptimizer = svc
	return h
}

// WithPropertyFinder injects the property finder for ADR-088 Phase 12 /run endpoint.
func (h *Handler) WithPropertyFinder(f *finder.Orchestrator) *Handler {
	h.propertyFinder = f
	return h
}

// WithMarketData injects the market data aggregator (L0/L1/L2 caching) for quality-gate benchmarks.
func (h *Handler) WithMarketData(md *aggregator.Aggregator) *Handler {
	h.marketData = md
	return h
}

// WithMetroReader injects the metro reader for HUD FMR by bedroom count in quality-gate benchmarks.
func (h *Handler) WithMetroReader(mr *timeseries.MetroReader) *Handler {
	h.metroReader = mr
	return h
}

// WithTrendsService injects the market trends service for ADR-100 report enrichment.
func (h *Handler) WithTrendsService(svc *trends.Service) *Handler {
	h.trendsService = svc
	return h
}

// WithMarketEstimator injects the AI estimator used as fallback when structured
// market data sources return no price/rent data for a location.
func (h *Handler) WithMarketEstimator(e *estimation.AIEstimator) *Handler {
	h.marketEstimator = e
	return h
}

// ===============================
// Type Definitions
// ===============================

// EvaluationChatRequest represents a request to queue an evaluation chat
type EvaluationChatRequest struct {
	Properties         []PropertyInput  `json:"properties" validate:"required,min=1,max=10"`
	PortfolioID        *string          `json:"portfolioId,omitempty"`
	PortfolioSnapshot  json.RawMessage  `json:"portfolioSnapshot,omitempty"`
	InvestorProfile    *InvestorProfile `json:"investorProfile,omitempty"`
	Message            string           `json:"message" validate:"required,min=1,max=2000"`
	SessionID          *string          `json:"sessionId,omitempty"`
	// ADR-098: discovery session that supplied the property pool
	DiscoverySessionID string           `json:"discoverySessionId,omitempty"`
}

// PropertyInput represents a property in a chat request
type PropertyInput struct {
	ID            string  `json:"id" validate:"required"`
	// ListingID is the original provider listing ID (e.g. "5153197").
	// Populated in GET session responses so clients can identify the property
	// independently of the internal cached_properties UUID stored in ID.
	ListingID     string  `json:"listingId,omitempty"`
	Address       string  `json:"address" validate:"required"`
	City          string  `json:"city" validate:"required"`
	State         string  `json:"state" validate:"required"`
	ZipCode       string  `json:"zipCode,omitempty"`
	Price         int     `json:"price" validate:"required,gt=0"`
	Beds          int     `json:"beds,omitempty"`
	Baths         float64 `json:"baths,omitempty"`
	Sqft          int     `json:"sqft,omitempty"`
	EstimatedRent int     `json:"estimatedRent,omitempty"`
	CapRate       string  `json:"capRate,omitempty"`
	YearBuilt     int     `json:"yearBuilt,omitempty"`
	PropertyType  string  `json:"propertyType,omitempty"`
	ListingUrl    string  `json:"listingUrl,omitempty"`
	ImageUrl      string  `json:"imageUrl,omitempty"`
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
	MessageID string `json:"messageId"`
	StreamURL string `json:"streamUrl"`
}

// ChatSession represents a stored chat session
type ChatSession struct {
	ID                string     `json:"id"`
	UserID            string     `json:"userId"`
	PropertyIDs       []string   `json:"propertyIds"`
	CachedPropertyIDs []string   `json:"cachedPropertyIds"`
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

// FinancialAssumptions for investment planning (nested struct matching www_v1)
type FinancialAssumptions struct {
	MortgageRate       *float64 `json:"mortgageRate,omitempty"`
	DownPaymentPercent *float64 `json:"downPaymentPercent,omitempty"`
	OperatingExpenses  *float64 `json:"operatingExpenses,omitempty"`
}

// InvestmentConstraints for planning (nested struct matching www_v1)
type InvestmentConstraints struct {
	RiskTolerance            string   `json:"riskTolerance,omitempty"`
	MaxPropertiesPerLocation *int     `json:"maxPropertiesPerLocation,omitempty"`
	MinCashFlow              *int     `json:"minCashFlow,omitempty"`
	MinCapRate               *float64 `json:"minCapRate,omitempty"`
	MinDiversification       *float64 `json:"minDiversification,omitempty"`
	MaxConcentration         *float64 `json:"maxConcentration,omitempty"`
	TargetCashFlow           *float64 `json:"targetCashFlow,omitempty"`
}

// MarketFilters for property search filtering
type MarketFilters struct {
	MinPriceGrowth5Y     *float64 `json:"minPriceGrowth5Y,omitempty"`
	MaxPriceVolatility   *float64 `json:"maxPriceVolatility,omitempty"`
	MaxUnemploymentRate  *float64 `json:"maxUnemploymentRate,omitempty"`
	MinEmploymentGrowth  *float64 `json:"minEmploymentGrowth,omitempty"`
	MinPopulationGrowth  *float64 `json:"minPopulationGrowth,omitempty"`
	MaxVacancyRate       *float64 `json:"maxVacancyRate,omitempty"`
	MaxPriceToIncome     *float64 `json:"maxPriceToIncome,omitempty"`
	MinCapRate           *float64 `json:"minCapRate,omitempty"`
}

// InvestmentPlanningRequest represents a request to queue an investment plan (www_v1 compatible)
type InvestmentPlanningRequest struct {
	CacheKey             string                  `json:"cacheKey" validate:"required,startswith=investment_plan_"`
	Locations            []string                `json:"locations" validate:"omitempty,max=5"` // Optional if SelectedProperties provided
	AvailableCapital     int                     `json:"availableCapital" validate:"required,gt=0"`
	FinancialAssumptions FinancialAssumptions    `json:"financialAssumptions" validate:"required"`
	Strategy             string                  `json:"strategy" validate:"required,oneof=cash-flow appreciation balanced risk-adjusted"`
	Constraints          *InvestmentConstraints  `json:"constraints,omitempty"`
	SearchFilters        map[string]interface{}  `json:"searchFilters,omitempty"`
	MarketFilters        *MarketFilters          `json:"marketFilters,omitempty"`
	TargetPropertyValue  *int                    `json:"targetPropertyValue,omitempty"`
	RecommendedZipCodes  []string                `json:"recommendedZipCodes,omitempty"`
	IncludeSuburbs       bool                    `json:"includeSuburbs,omitempty"`
	ExpansionRadius      string                  `json:"expansionRadius,omitempty"`
	IncludeAllTowns      bool                    `json:"includeAllTowns,omitempty"`
	ForceRefresh         bool                    `json:"forceRefresh,omitempty"`
	YearlyBudgets        []YearlyBudget          `json:"yearlyBudgets,omitempty"`
	InvestorProfile      *InvestorProfile        `json:"investorProfile,omitempty"`
	IncludePortfolio     bool                    `json:"includePortfolio,omitempty"`
	MaxProperties        int                     `json:"maxProperties,omitempty" validate:"omitempty,gte=1,lte=20"`

	// ADR-059: Selection mode - if provided, skip property search and use these
	SelectedProperties []SelectedProperty `json:"selectedProperties,omitempty" validate:"omitempty,max=20"`

	// ADR-059: Cash flow reinvestment modeling
	ReinvestSurplusCashFlows bool     `json:"reinvestSurplusCashFlows,omitempty"`
	ReinvestmentRate         *float64 `json:"reinvestmentRate,omitempty" validate:"omitempty,gte=0,lte=100"`
	ProjectionYears          *int     `json:"projectionYears,omitempty" validate:"omitempty,gte=1,lte=10"`
}

// SelectedProperty represents a property pre-selected by the user (ADR-059)
type SelectedProperty struct {
	ID            string  `json:"id" validate:"required"`
	Address       string  `json:"address" validate:"required"`
	City          string  `json:"city" validate:"required"`
	State         string  `json:"state" validate:"required"`
	ZipCode       string  `json:"zipCode,omitempty"`
	Price         int     `json:"price" validate:"required,gt=0"`
	Beds          int     `json:"beds,omitempty"`
	Baths         float64 `json:"baths,omitempty"`
	Sqft          int     `json:"sqft,omitempty"`
	EstimatedRent int     `json:"estimatedRent,omitempty"`
	CapRate       float64 `json:"capRate,omitempty"`
	YearBuilt     int     `json:"yearBuilt,omitempty"`
	PropertyType  string  `json:"propertyType,omitempty"`
	DaysOnMarket  int     `json:"daysOnMarket,omitempty"`
	ImageURL      string  `json:"imageUrl,omitempty"`
	ListingURL    string  `json:"listingUrl,omitempty"`
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
		sessionID, err = h.createChatSession(ctx, user.UserID, req.Properties, req.InvestorProfile, req.PortfolioSnapshot, req.DiscoverySessionID)
		if err != nil {
			h.logger.Error("failed to create chat session", "error", err)
			httputil.InternalError(w, fmt.Errorf("failed to create session"))
			return
		}
	}

	// Save user message to database (no token usage for user messages)
	userMessageID, err := h.saveChatMessage(ctx, sessionID, "user", req.Message, nil, nil)
	if err != nil {
		h.logger.Error("failed to save user message", "error", err)
		// Continue - don't fail the request, use a fallback ID
		userMessageID = uuid.New().String()
	}

	// Convert properties to prompt context and calculate operating expenses
	properties := make([]prompts.PropertyContext, 0, len(req.Properties))
	expenseCalc := h.getExpenseCalculator()
	for _, p := range req.Properties {
		prop := prompts.PropertyContext{
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
		}

		// Calculate operating expenses if we have price and state
		if expenseCalc != nil && p.Price > 0 && p.State != "" {
			exp, err := expenseCalc.Calculate(expenses.PropertyInput{
				Price:         p.Price,
				State:         p.State,
				City:          p.City,
				YearBuilt:     p.YearBuilt,
				EstimatedRent: p.EstimatedRent,
				PropertyType:  p.PropertyType,
			})
			if err == nil {
				propExp := &prompts.PropertyExpenses{
					PropertyTax:      exp.PropertyTax,
					Insurance:        exp.Insurance,
					Maintenance:      exp.Maintenance,
					VacancyAllowance: exp.VacancyAllowance,
					PropertyMgmt:     exp.PropertyMgmt,
					TotalAnnual:      exp.TotalAnnual,
					TotalMonthly:     exp.TotalMonthly,
					ExpenseRatio:     exp.ExpenseRatio,
					NOI:              exp.NOI,
					MonthlyNOI:       exp.NOI / 12, // Monthly pre-financing cash flow
					CapRate:          exp.CapRate,
				}
				// Compute financing estimates for stress test calculations
				// Assume 7% mortgage rate (or market rate if available)
				prompts.ComputeFinancing(propExp, p.Price, 7.0)
				prop.OperatingExpenses = propExp
			} else {
				h.logger.Warn("failed to calculate operating expenses",
					"property_id", p.ID,
					"error", err,
				)
			}
		}

		properties = append(properties, prop)
	}

	// Build market expense context for unique states
	marketContext := h.buildMarketExpenseContext(req.Properties)

	// Build payload
	payload := map[string]interface{}{
		"session_id":     sessionID,
		"message":        req.Message,
		"properties":     properties,
		"market_context": marketContext,
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

	// Enqueue (nil-safe: queue may be unavailable if Redis is down)
	if h.jobQueue == nil {
		httputil.InternalError(w, fmt.Errorf("job processing unavailable"))
		return
	}
	jobID, err := h.jobQueue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue chat job", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to queue job"))
		return
	}

	// Generate SSE token for stream authentication (EventSource doesn't support headers)
	sseToken, err := h.generateSSEToken(user.UserID, jobID, sessionID)
	if err != nil {
		h.logger.Error("failed to generate SSE token", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to generate stream token"))
		return
	}

	h.logger.Info("evaluation chat queued",
		"job_id", jobID,
		"session_id", sessionID,
		"user_id", user.UserID,
		"properties", len(req.Properties),
	)

	// Build absolute URL for SSE stream (EventSource resolves relative URLs against page origin, not API)
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	httputil.JSON(w, http.StatusOK, EvaluationChatResponse{
		Success:   true,
		JobID:     jobID,
		SessionID: sessionID,
		MessageID: userMessageID,
		StreamURL: fmt.Sprintf("%s/api/ai/evaluate/chat/stream?jobId=%s&token=%s", baseURL, jobID, sseToken),
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

	// Get job (nil-safe: queue may be unavailable after restart)
	if h.jobQueue == nil {
		sseWriter.WriteError("job not found")
		return
	}
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

	// Extract properties - job queue is in-memory, so properties are []prompts.PropertyContext
	if props, ok := job.Payload["properties"].([]prompts.PropertyContext); ok {
		chatReq.Properties = props
	}

	// Extract investor profile (uses snake_case keys as set in QueueEvaluationChat)
	if profileRaw, ok := job.Payload["investor_profile"].(map[string]interface{}); ok {
		chatReq.InvestorProfile = &prompts.InvestorProfile{
			RiskTolerance:     getString(profileRaw, "risk_tolerance"),
			InvestmentHorizon: getString(profileRaw, "investment_horizon"),
			AvailableCapital:  getInt(profileRaw, "available_capital"),
		}
	}

	// Extract market context (expense characteristics for the market)
	if marketContext, ok := job.Payload["market_context"].(string); ok {
		chatReq.MarketContext = marketContext
	}

	h.logger.Info("chat request built",
		"session_id", sessionID,
		"message", message,
		"properties_count", len(chatReq.Properties),
		"has_investor_profile", chatReq.InvestorProfile != nil,
	)

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

	// Send initial progress event immediately (www_v1 parity - ADR-057)
	// This eliminates "Connecting..." delay by giving client immediate feedback
	sseWriter.WriteEventJSON("progress", map[string]interface{}{
		"jobId":     jobID,
		"sessionId": sessionID,
		"status":    "INITIALIZING",
		"progress":  0,
		"message":   "Starting analysis...",
	})

	// Forward events to SSE
	var fullContent string
	var parsedBlocks []interface{}
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
				// Client expects "chunk" field for text events
				sseWriter.WriteEventJSON("text", map[string]interface{}{
					"type":  "text",
					"chunk": event.Content,
				})

			case "insight", "stress_test", "metrics", "comparison", "disclaimer":
				// Extract content and raw from map (www_v1 parity - ADR-057)
				var blockContent interface{}
				var blockRaw string
				if contentMap, ok := event.Content.(map[string]interface{}); ok {
					blockContent = contentMap["content"]
					if raw, ok := contentMap["raw"].(string); ok {
						blockRaw = raw
					}
				} else {
					blockContent = event.Content
				}

				block := map[string]interface{}{
					"type":    event.Type,
					"content": blockContent,
					"raw":     blockRaw,
				}
				parsedBlocks = append(parsedBlocks, block)

				sseWriter.WriteEventJSON(event.Type, block)

			case "complete":
				// Extract content and parsedBlocks from agent response (www_v1 parity)
				var agentParsedBlocks []interface{}
				if contentMap, ok := event.Content.(map[string]interface{}); ok {
					if c, ok := contentMap["content"].(string); ok {
						fullContent = c
					}
					if blocks, ok := contentMap["parsedBlocks"].([]agents.ChatBlock); ok {
						// Convert ChatBlock to map for JSON serialization
						for _, b := range blocks {
							agentParsedBlocks = append(agentParsedBlocks, map[string]interface{}{
								"type":    b.Type,
								"content": b.Content,
								"raw":     b.Raw,
							})
						}
					}
				} else if s, ok := event.Content.(string); ok {
					fullContent = s
				}

				// Use agent's parsed blocks if we have them, otherwise use collected blocks
				finalBlocks := parsedBlocks
				if len(agentParsedBlocks) > 0 {
					finalBlocks = agentParsedBlocks
				}

				// Save assistant message with parsedBlocks (ADR-057 fix)
				var messageID string
				if sessionID != "" && fullContent != "" {
					// Marshal parsedBlocks to JSON for storage
					var blocksJSON []byte
					if len(finalBlocks) > 0 {
						blocksJSON, _ = json.Marshal(finalBlocks)
					}
					messageID, _ = h.saveChatMessage(ctx, sessionID, "assistant", fullContent, blocksJSON, nil)
				}

				// Record AI usage metrics (fire and forget)
				if event.Usage != nil {
					go h.recordEvalChatUsage(job.UserID, jobID, event.Usage.InputTokens, event.Usage.OutputTokens)
				}

				// Complete the job
				h.jobQueue.Complete(jobID, &queue.JobResult{
					Data: map[string]interface{}{
						"session_id": sessionID,
						"content":    fullContent,
					},
				})

				// Include parsedBlocks, messageId, and status in complete event (www_v1 parity - ADR-057)
				sseWriter.WriteEventJSON("complete", map[string]interface{}{
					"type":         "complete",
					"sessionId":    sessionID,
					"content":      fullContent,
					"parsedBlocks": finalBlocks,
					"messageId":    messageID,
					"status":       "COMPLETED",
				})
				return

			case "error":
				sseWriter.WriteError(event.Error)
				h.jobQueue.Fail(jobID, fmt.Errorf("%s", event.Error))
				return
			}

		case progress := <-progressChan:
			// Include jobId, sessionId, and status in progress events (www_v1 parity - ADR-057)
			sseWriter.WriteEventJSON("progress", map[string]interface{}{
				"jobId":     jobID,
				"sessionId": sessionID,
				"status":    progress.Stage,
				"progress":  progress.Progress,
				"message":   progress.Message,
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

	// Parse pagination params
	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Get total count
	q := h.store.Q()
	totalCount, err := q.CountEvaluationChatSessionsByUser(ctx, user.UserID)
	if err != nil {
		h.logger.Error("failed to count sessions", "error", err)
		totalCount = 0
	}
	total := int(totalCount)

	// List sessions with extended info via sqlc
	sessionRows, err := q.ListEvaluationChatSessionsExtended(ctx, queries.ListEvaluationChatSessionsExtendedParams{
		UserID: user.UserID,
		Offset: int32(offset),
		Limit:  int32(limit),
	})
	if err != nil {
		h.logger.Error("failed to list sessions", "error", err)
		// Return empty array on error (graceful degradation)
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"sessions": []map[string]interface{}{},
			"pagination": map[string]interface{}{
				"total":   0,
				"offset":  offset,
				"limit":   limit,
				"hasMore": false,
			},
		})
		return
	}

	type sessionData struct {
		index           int
		cachedPropertyIDs []string
	}

	sessions := make([]map[string]interface{}, 0)
	sessionLookups := make([]sessionData, 0)

	for _, row := range sessionRows {
		session := map[string]interface{}{
			"id":                row.ID,
			"propertyCount":     len(row.PropertyIds),
			"propertyIds":       row.PropertyIds,
			"cachedPropertyIds": row.CachedPropertyIds,
			"createdAt":         row.CreatedAt.Time.Format(time.RFC3339),
			"updatedAt":         row.UpdatedAt.Time.Format(time.RFC3339),
			"messageCount":      row.MessageCount,
			// ADR-091: discovery session that supplied the property pool — used as
			// discoverySeedSessionId when this chat session is the source for Frontier.
			"discoverySessionId": row.DiscoverySessionID.String,
			// ADR-101: pipeline deal context (empty string when not a Pipeline session)
			"pipelineDealId": func() string {
				if row.PipelineDealID.Valid {
					return uuid.UUID(row.PipelineDealID.Bytes).String()
				}
				return ""
			}(),
		}

		// Add last message if exists
		if row.LastMessageContent != "" && row.LastMessageRole != "" && row.LastMessageAt.Valid {
			session["lastMessage"] = map[string]interface{}{
				"role":      row.LastMessageRole,
				"content":   row.LastMessageContent,
				"createdAt": row.LastMessageAt.Time.Format(time.RFC3339),
			}
		}

		// Add first user question if exists
		if row.FirstUserQuestion != "" {
			session["firstUserQuestion"] = row.FirstUserQuestion
		}

		sessions = append(sessions, session)

		// Track cached property IDs for location enrichment
		if len(row.CachedPropertyIds) > 0 {
			sessionLookups = append(sessionLookups, sessionData{
				index:           len(sessions) - 1,
				cachedPropertyIDs: row.CachedPropertyIds,
			})
		}
	}

	// Enrich sessions with location and address data from cached_properties
	if len(sessionLookups) > 0 {
		// Collect all unique cached property IDs
		idSet := make(map[string]bool)
		for _, sl := range sessionLookups {
			for _, cid := range sl.cachedPropertyIDs {
				idSet[cid] = true
			}
		}
		allIDs := make([]string, 0, len(idSet))
		for id := range idSet {
			allIDs = append(allIDs, id)
		}

		type propInfo struct {
			city    string
			state   string
			address string
		}
		propMap := make(map[string]propInfo)

		propRows, propErr := h.store.Q().GetCachedPropertiesSummaryByIDs(ctx, allIDs)
		if propErr == nil {
			for _, prop := range propRows {
				propMap[prop.ID] = propInfo{
					city:    prop.City,
					state:   prop.State,
					address: prop.Address,
				}
			}
		}

		// Derive location and addresses per session
		for _, sl := range sessionLookups {
			cities := make(map[string]bool)
			addresses := make([]string, 0, len(sl.cachedPropertyIDs))
			for _, cid := range sl.cachedPropertyIDs {
				if p, ok := propMap[cid]; ok {
					cityState := p.city + ", " + p.state
					cities[cityState] = true
					addresses = append(addresses, p.address+", "+p.city+", "+p.state)
				}
			}

			// Derive location label
			cityList := make([]string, 0, len(cities))
			for c := range cities {
				cityList = append(cityList, c)
			}
			if len(cityList) == 1 {
				sessions[sl.index]["location"] = cityList[0]
			} else if len(cityList) > 1 {
				sessions[sl.index]["location"] = fmt.Sprintf("%s + %d more", cityList[0], len(cityList)-1)
			}

			if len(addresses) > 5 {
				addresses = addresses[:5]
			}
			if len(addresses) > 0 {
				sessions[sl.index]["propertyAddresses"] = addresses
			}
		}
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
		"pagination": map[string]interface{}{
			"total":   total,
			"offset":  offset,
			"limit":   limit,
			"hasMore": offset+len(sessions) < total,
		},
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
	dbSession, err := h.store.Q().GetEvaluationChatSession(ctx, queries.GetEvaluationChatSessionParams{
		ID:     sessionID,
		UserID: user.UserID,
	})
	if err != nil {
		httputil.NotFound(w, "session not found")
		return
	}

	// Convert to handler's ChatSession struct
	var session ChatSession
	session.ID = dbSession.ID
	session.UserID = dbSession.UserID
	session.PropertyIDs = dbSession.PropertyIds
	session.CachedPropertyIDs = dbSession.CachedPropertyIds
	session.PropertyCount = len(dbSession.PropertyIds)
	session.CreatedAt = dbSession.CreatedAt.Time
	session.UpdatedAt = dbSession.UpdatedAt.Time

	// Convert JSONB []byte to *string
	if len(dbSession.InvestorProfile) > 0 {
		profileStr := string(dbSession.InvestorProfile)
		session.InvestorProfile = &profileStr
	}
	if len(dbSession.PortfolioSnapshot) > 0 {
		snapshotStr := string(dbSession.PortfolioSnapshot)
		session.PortfolioSnapshot = &snapshotStr
	}

	// Get messages
	dbMessages, err := h.store.Q().ListEvaluationChatMessages(ctx, sessionID)
	if err != nil {
		h.logger.Error("failed to get messages", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to get messages"))
		return
	}

	messages := make([]ChatMessage, 0, len(dbMessages))
	for _, dbMsg := range dbMessages {
		msg := ChatMessage{
			ID:        dbMsg.ID,
			SessionID: dbMsg.SessionID,
			Role:      dbMsg.Role,
			Content:   dbMsg.Content,
			CreatedAt: dbMsg.CreatedAt.Time,
		}

		// Convert JSONB []byte to json.RawMessage
		if len(dbMsg.ParsedBlocks) > 0 {
			msg.ParsedBlocks = json.RawMessage(dbMsg.ParsedBlocks)
		}

		// Convert JSONB []byte to *TokenUsage
		if len(dbMsg.TokenUsage) > 0 {
			var tu TokenUsage
			if json.Unmarshal(dbMsg.TokenUsage, &tu) == nil {
				msg.TokenUsage = &tu
			}
		}

		messages = append(messages, msg)
	}

	// Get cached properties — use CachedPropertyIds (UUIDs) for lookup since
	// GetCachedPropertiesDetailByIDs queries WHERE id = ANY(...) on the UUID primary key.
	// PropertyIds contains original listing IDs (e.g. "5153197") which don't match.
	properties := make([]PropertyInput, 0)
	lookupIDs := session.CachedPropertyIDs
	if len(lookupIDs) == 0 {
		lookupIDs = session.PropertyIDs // fallback for old sessions created before CachedPropertyIds was stored
	}
	if len(lookupIDs) > 0 {
		propRows, err := h.store.Q().GetCachedPropertiesDetailByIDs(ctx, lookupIDs)
		if err == nil {
			for _, dbProp := range propRows {
				p := PropertyInput{
					ID:        dbProp.ID,
					ListingID: dbProp.ListingID,
					Address:   dbProp.Address,
					City:      dbProp.City,
					State:     dbProp.State,
					Price:     int(dbProp.Price),
				}

				// Convert pgtype fields to Go types
				if dbProp.Beds.Valid {
					p.Beds = int(dbProp.Beds.Int32)
				}
				if dbProp.Baths.Valid {
					p.Baths = dbProp.Baths.Float64
				}
				if dbProp.Sqft.Valid {
					p.Sqft = int(dbProp.Sqft.Int32)
				}
				if dbProp.EstimatedRent.Valid {
					p.EstimatedRent = int(dbProp.EstimatedRent.Int32)
				}
				if dbProp.CapRate.Valid {
					p.CapRate = fmt.Sprintf("%.2f%%", dbProp.CapRate.Float64)
				}
				if dbProp.ZipCode.Valid {
					p.ZipCode = dbProp.ZipCode.String
				}
				if dbProp.ListingUrl.Valid {
					p.ListingUrl = dbProp.ListingUrl.String
				}
				if dbProp.ImageUrl.Valid {
					p.ImageUrl = dbProp.ImageUrl.String
				}

				properties = append(properties, p)
			}
		}
	}

	// Enrich properties with discovery session data (yearBuilt, propertyType, imageUrl)
	if len(session.PropertyIDs) > 0 {
		properties = h.enrichSessionProperties(ctx, properties, session.PropertyIDs)
	}

	// Look up linked discovery session context (search criteria, location name)
	var discoveryContext map[string]interface{}
	discSession, discErr := h.store.Q().GetDiscoverySessionByActivityId(ctx, sessionID)
	if discErr == nil {
		discoveryContext = map[string]interface{}{
			"discoverySessionId": discSession.ID,
			"location":           discSession.Location,
			"searchCriteria":     discSession.SearchCriteria,
			"propertyCount":      discSession.PropertyCount,
		}
		if discSession.Name.Valid {
			discoveryContext["name"] = discSession.Name.String
		}
	}

	responseBody := map[string]interface{}{
		"success":    true,
		"session":    session,
		"messages":   messages,
		"properties": properties,
	}
	if discoveryContext != nil {
		responseBody["discoveryContext"] = discoveryContext
	}
	httputil.JSON(w, http.StatusOK, responseBody)
}

// enrichSessionProperties merges yearBuilt, propertyType, imageUrl, listingUrl from
// discovery_session_properties into properties built from cached_properties.
func (h *Handler) enrichSessionProperties(ctx context.Context, properties []PropertyInput, listingIDs []string) []PropertyInput {
	if len(listingIDs) == 0 {
		return properties
	}
	discProps, err := h.store.Q().GetSessionPropertiesByListingIDs(ctx, listingIDs)
	if err != nil || len(discProps) == 0 {
		return properties
	}
	discMap := make(map[string]queries.DiscoverySessionProperty, len(discProps))
	for _, dp := range discProps {
		discMap[dp.ListingId] = dp
	}
	for i := range properties {
		dp, ok := discMap[properties[i].ListingID]
		if !ok {
			continue
		}
		if dp.YearBuilt.Valid && properties[i].YearBuilt == 0 {
			properties[i].YearBuilt = int(dp.YearBuilt.Int32)
		}
		if dp.PropertyType.Valid && properties[i].PropertyType == "" {
			properties[i].PropertyType = dp.PropertyType.String
		}
		if dp.ImageUrl.Valid && dp.ImageUrl.String != "" {
			properties[i].ImageUrl = dp.ImageUrl.String
		}
		if dp.ListingSearchUrl.Valid && dp.ListingSearchUrl.String != "" && properties[i].ListingUrl == "" {
			properties[i].ListingUrl = dp.ListingSearchUrl.String
		}
	}
	return properties
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
	rowsAffected, err := h.store.Q().DeleteEvaluationChatSession(ctx, queries.DeleteEvaluationChatSessionParams{
		ID:     sessionID,
		UserID: user.UserID,
	})
	if err != nil {
		h.logger.Error("failed to delete session", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to delete session"))
		return
	}

	if rowsAffected == 0 {
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

	// Extract downPaymentPercent from financialAssumptions (default 20%)
	downPaymentPercent := 20.0
	if req.FinancialAssumptions.DownPaymentPercent != nil {
		downPaymentPercent = *req.FinancialAssumptions.DownPaymentPercent
	}

	// Extract riskTolerance from constraints (default "moderate")
	riskTolerance := "moderate"
	if req.Constraints != nil && req.Constraints.RiskTolerance != "" {
		riskTolerance = req.Constraints.RiskTolerance
	}

	// Extract mortgageRate (default 7.0%)
	mortgageRate := 7.0
	if req.FinancialAssumptions.MortgageRate != nil {
		mortgageRate = *req.FinancialAssumptions.MortgageRate
	}

	// Extract operatingExpenses (default 35%)
	operatingExpenses := 35.0
	if req.FinancialAssumptions.OperatingExpenses != nil {
		operatingExpenses = *req.FinancialAssumptions.OperatingExpenses
	}

	// Build payload (compatible with worker expectations)
	payload := map[string]interface{}{
		"cache_key":            req.CacheKey,
		"locations":            req.Locations,
		"budget":               req.AvailableCapital, // Worker expects "budget"
		"available_capital":    req.AvailableCapital,
		"down_payment_percent": downPaymentPercent,
		"mortgage_rate":        mortgageRate,
		"operating_expenses":   operatingExpenses,
		"strategy":             req.Strategy,
		"risk_tolerance":       riskTolerance,
		"max_properties":       req.MaxProperties,
		"include_portfolio":    req.IncludePortfolio,
		"include_suburbs":      req.IncludeSuburbs,
		"force_refresh":        req.ForceRefresh,
	}

	// Add optional fields
	if len(req.YearlyBudgets) > 0 {
		payload["yearly_budgets"] = req.YearlyBudgets
	}
	if req.TargetPropertyValue != nil {
		payload["target_property_value"] = *req.TargetPropertyValue
	}
	if len(req.RecommendedZipCodes) > 0 {
		payload["recommended_zip_codes"] = req.RecommendedZipCodes
	}
	if req.ExpansionRadius != "" {
		payload["expansion_radius"] = req.ExpansionRadius
	}
	if req.IncludeAllTowns {
		payload["include_all_towns"] = req.IncludeAllTowns
	}
	if req.SearchFilters != nil {
		payload["search_filters"] = req.SearchFilters
	}
	if req.MarketFilters != nil {
		payload["market_filters"] = req.MarketFilters
	}
	if req.Constraints != nil {
		payload["constraints"] = req.Constraints
	}
	if req.InvestorProfile != nil {
		payload["investor_profile"] = req.InvestorProfile
	}

	// ADR-059: Selection mode - add pre-selected properties
	// Convert to []map[string]interface{} for worker compatibility
	if len(req.SelectedProperties) > 0 {
		propsAsMap := make([]map[string]interface{}, len(req.SelectedProperties))
		for i, sp := range req.SelectedProperties {
			propsAsMap[i] = map[string]interface{}{
				"id":            sp.ID,
				"address":       sp.Address,
				"city":          sp.City,
				"state":         sp.State,
				"zipCode":       sp.ZipCode,
				"price":         sp.Price,
				"beds":          sp.Beds,
				"baths":         sp.Baths,
				"sqft":          sp.Sqft,
				"estimatedRent": sp.EstimatedRent,
				"capRate":       sp.CapRate,
				"yearBuilt":     sp.YearBuilt,
				"propertyType":  sp.PropertyType,
				"daysOnMarket":  sp.DaysOnMarket,
				"imageUrl":      sp.ImageURL,
				"listingUrl":    sp.ListingURL,
			}
		}
		payload["selectedProperties"] = propsAsMap
	}

	// ADR-059: Reinvestment modeling parameters
	if req.ReinvestSurplusCashFlows {
		payload["reinvestSurplusCashFlows"] = req.ReinvestSurplusCashFlows
	}
	if req.ReinvestmentRate != nil {
		payload["reinvestmentRate"] = *req.ReinvestmentRate
	}
	if req.ProjectionYears != nil {
		payload["projectionYears"] = *req.ProjectionYears
	}

	// Store financial assumptions for cache/history
	payload["financial_assumptions"] = map[string]interface{}{
		"mortgageRate":       mortgageRate,
		"downPaymentPercent": downPaymentPercent,
		"operatingExpenses":  operatingExpenses,
	}

	// Check cache first (unless forceRefresh)
	// Investment plans don't expire like ephemeral cached data
	if !req.ForceRefresh {
		cacheRecord, err := h.store.Q().GetCacheByUserAndKeyNoExpiry(ctx, queries.GetCacheByUserAndKeyNoExpiryParams{
			UserId: user.UserID,
			Key:    req.CacheKey,
		})
		if err == nil && cacheRecord.Content != "" {
			h.logger.Info("investment planning cache hit",
				"cache_key", req.CacheKey,
				"user_id", user.UserID,
			)

			var cachedData map[string]interface{}
			if err := json.Unmarshal([]byte(cacheRecord.Content), &cachedData); err == nil {
				httputil.JSON(w, http.StatusOK, map[string]interface{}{
					"success":     true,
					"jobId":       cacheRecord.ID,
					"responseKey": req.CacheKey,
					"status":      "COMPLETED",
					"cached":      true,
					"plan":        normalizeInvestmentPlanResult(cachedData),
				})
				return
			}
		}
	}

	// Create job
	job := queue.NewJob(queue.JobTypeInvestmentPlanning, user.UserID, payload)

	// Enqueue (nil-safe: queue may be unavailable if Redis is down)
	if h.jobQueue == nil {
		httputil.InternalError(w, fmt.Errorf("job processing unavailable"))
		return
	}
	jobID, err := h.jobQueue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue investment planning job", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to queue job"))
		return
	}

	// Generate SSE token for stream authentication (EventSource doesn't support headers)
	sseToken, err := h.generateSSEToken(user.UserID, jobID, "")
	if err != nil {
		h.logger.Error("failed to generate SSE token", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to generate stream token"))
		return
	}

	h.logger.Info("investment planning queued",
		"job_id", jobID,
		"user_id", user.UserID,
		"cache_key", req.CacheKey,
		"locations", req.Locations,
	)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"jobId":       jobID,
		"responseKey": req.CacheKey,
		"status":      "QUEUED",
		"streamUrl":   fmt.Sprintf("/api/ai/investment-planning/stream?jobId=%s", jobID),
		"sseToken":    sseToken,
	})
}

// StreamInvestmentPlan streams investment planning results via SSE
func (h *Handler) StreamInvestmentPlan(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId required")
		return
	}

	h.streamInvestmentPlanByID(w, r, jobID)
}

// StreamInvestmentPlanStatus streams investment planning results via SSE for the v1 contract.
// GET /api/ai/investment-planning/status/{jobId}
func (h *Handler) StreamInvestmentPlanStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		httputil.BadRequest(w, "jobId required")
		return
	}

	h.streamInvestmentPlanByID(w, r, jobID)
}

// streamInvestmentPlanByID handles the shared SSE logic for investment planning streams.
func (h *Handler) streamInvestmentPlanByID(w http.ResponseWriter, r *http.Request, jobID string) {
	ctx := r.Context()

	// Create SSE writer
	sseWriter, err := sse.NewWriter(w)
	if err != nil {
		httputil.InternalError(w, fmt.Errorf("streaming not supported"))
		return
	}

	// Start heartbeat
	stopHeartbeat := sseWriter.StartHeartbeat(sse.HeartbeatInterval)
	defer close(stopHeartbeat)

	// Nil-safe: queue may be unavailable after restart
	if h.jobQueue == nil {
		sseWriter.WriteError("job not found")
		return
	}

	// Calculate dynamic timeout: base 8min + 2min/location + 5min if suburbs, cap 15min
	timeout := 8 * time.Minute
	if job, err := h.jobQueue.GetJob(jobID); err == nil {
		var locationCount int
		if locations, ok := job.Payload["locations"].([]interface{}); ok {
			locationCount = len(locations)
		} else if locations, ok := job.Payload["locations"].([]string); ok {
			locationCount = len(locations)
		}
		timeout += 2 * time.Minute * time.Duration(locationCount)

		// Suburbs expand 1 location into 10-15 sub-locations, needs more time
		if includeSuburbs, ok := job.Payload["include_suburbs"].(bool); ok && includeSuburbs {
			timeout += 5 * time.Minute
		} else if includeSuburbs, ok := job.Payload["includeSuburbs"].(bool); ok && includeSuburbs {
			timeout += 5 * time.Minute
		}

		// Cap at 15 minutes (matches client hard timeout)
		if timeout > 15*time.Minute {
			timeout = 15 * time.Minute
		}
	}

	// Create context with dynamic timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Subscribe to progress
	progressChan := h.jobQueue.Subscribe(jobID)

	// Forward events to SSE
	// NOTE: Client uses eventSource.onmessage which only receives unnamed events.
	// We must use WriteDataJSON (no event type) with status field in data to match www_v1 format.
	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				h.logger.Warn("investment planning stream timeout", "job_id", jobID, "timeout", timeout)
				// Send TIMED_OUT (not FAILED) — the job is still running, only SSE died
				sseWriter.WriteDataJSON(map[string]interface{}{
					"jobId":   jobID,
					"status":  "TIMED_OUT",
					"message": "SSE connection timed out, job still processing",
				})
			} else {
				h.logger.Info("client disconnected", "job_id", jobID)
			}
			return

		case event, ok := <-progressChan:
			if !ok {
				// Channel closed - job completed, get final result
				result, err := h.jobQueue.GetResult(jobID)
				if err == nil && result.Status == queue.JobStatusCompleted {
					// Send completion in www_v1 format (unnamed event with status + plan)
					sseWriter.WriteDataJSON(map[string]interface{}{
						"jobId":    jobID,
						"status":   "COMPLETED",
						"progress": 100,
						"message":  "Investment plan completed",
						"plan":     normalizeInvestmentPlanResult(result.Data),
					})
				}
				return
			}

			switch event.Stage {
			case "completed":
				// Send completion in www_v1 format (unnamed event with status + plan)
				sseWriter.WriteDataJSON(map[string]interface{}{
					"jobId":    jobID,
					"status":   "COMPLETED",
					"progress": 100,
					"message":  event.Message,
					"plan":     normalizeInvestmentPlanResult(event.Data),
				})
				return
			case "failed":
				// Send failure in www_v1 format (unnamed event with status + error)
				sseWriter.WriteDataJSON(map[string]interface{}{
					"jobId":   jobID,
					"status":  "FAILED",
					"error":   event.Message,
					"message": event.Message,
				})
				return
			default:
				// Send progress in www_v1 format (unnamed event with PROCESSING status)
				// Map internal stages to client status constants
				clientStatus := "AGENT1_RUNNING"
				switch event.Stage {
				case "searching", "expanding_locations":
					clientStatus = "AGENT1_RUNNING"
				case "scoring", "analyzing":
					clientStatus = "AGENT1_COMPLETE"
				case "optimizing", "portfolio_optimization":
					clientStatus = "AGENT2_RUNNING"
				case "finalizing", "caching":
					clientStatus = "STORING"
				}
				sseWriter.WriteDataJSON(map[string]interface{}{
					"jobId":    jobID,
					"status":   clientStatus,
					"progress": event.Progress,
					"message":  event.Message,
				})
			}
		}
	}
}

// CancelInvestmentPlan cancels a queued or running investment planning job.
// POST /api/ai/investment-planning/{jobId}/cancel
func (h *Handler) CancelInvestmentPlan(w http.ResponseWriter, r *http.Request) {
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

	if h.jobQueue == nil {
		httputil.NotFound(w, "job not found")
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

	if job.Type != queue.JobTypeInvestmentPlanning {
		httputil.BadRequest(w, "invalid job type for cancellation")
		return
	}

	if err := h.jobQueue.Cancel(jobID); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	h.logger.Info("investment planning job cancelled", "job_id", jobID, "user_id", user.UserID)

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"jobId":   jobID,
		"status":  normalizeJobStatus(queue.JobStatusCancelled),
		"message": "job cancelled",
	})
}

// DeleteInvestmentPlan deletes an investment plan from cache and queue.
// DELETE /api/ai/investment-planning/{jobId}
func (h *Handler) DeleteInvestmentPlan(w http.ResponseWriter, r *http.Request) {
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

	// Remove job from queue if it exists (nil-safe: queue may be unavailable after restart).
	if h.jobQueue != nil {
		if job, err := h.jobQueue.GetJob(jobID); err == nil {
			if job.UserID != user.UserID {
				httputil.NotFound(w, "job not found")
				return
			}
			if job.Type != queue.JobTypeInvestmentPlanning {
				httputil.BadRequest(w, "invalid job type for deletion")
				return
			}

			_ = h.jobQueue.Delete(jobID)
		}
	}

	deletedID, deletedKey, err := h.deleteInvestmentPlanCache(ctx, user.UserID, jobID)
	if err != nil {
		h.logger.Error("failed to delete investment plan cache", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to delete investment plan"))
		return
	}

	if deletedID == "" && deletedKey == "" {
		httputil.NotFound(w, "investment plan not found")
		return
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"message":   "investment plan deleted",
		"deletedId": deletedID,
	})
}

type investmentPlanCacheRecord struct {
	ID  string
	Key string
}

func (h *Handler) deleteInvestmentPlanCache(ctx context.Context, userID, jobID string) (string, string, error) {
	record, err := h.findInvestmentPlanCacheRecord(ctx, userID, jobID)
	if err != nil {
		return "", "", err
	}
	if record == nil {
		return "", "", nil
	}

	if h.redis != nil {
		// Use plain cache key (matches www_v1 responseCacheManager format).
		// Also try the legacy "cache:{userID}:{key}" format for backwards compatibility.
		if err := h.redis.Delete(ctx, record.Key); err != nil {
			h.logger.Warn("failed to delete investment plan from Redis (plain key)", "error", err)
		}
		legacyCacheKey := fmt.Sprintf("cache:%s:%s", userID, record.Key)
		if err := h.redis.Delete(ctx, legacyCacheKey); err != nil {
			h.logger.Warn("failed to delete investment plan from Redis (legacy key)", "error", err)
		}
	}

	err = h.store.Q().DeleteCacheByIDAndUserID(ctx, queries.DeleteCacheByIDAndUserIDParams{
		ID:     record.ID,
		UserId: userID,
	})
	if err != nil {
		return "", "", err
	}

	return record.ID, record.Key, nil
}

func (h *Handler) findInvestmentPlanCacheRecord(ctx context.Context, userID, jobID string) (*investmentPlanCacheRecord, error) {
	record := &investmentPlanCacheRecord{}

	if strings.HasPrefix(jobID, "investment_plan") {
		result, err := h.store.Q().GetCacheIDAndKeyByUserAndKey(ctx, queries.GetCacheIDAndKeyByUserAndKeyParams{
			Key:    jobID,
			UserId: userID,
		})
		if err == nil {
			record.ID = result.ID
			record.Key = result.Key
			return record, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	result, err := h.store.Q().GetInvestmentPlanCacheByID(ctx, queries.GetInvestmentPlanCacheByIDParams{
		ID:     jobID,
		UserId: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	record.ID = result.ID
	record.Key = result.Key
	return record, nil
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

	// Get job from in-memory queue (nil-safe: queue may be unavailable after restart)
	var job *queue.Job
	if h.jobQueue != nil {
		job, _ = h.jobQueue.GetJob(jobID)
	}
	if job == nil {
		record, recordErr := h.findInvestmentPlanCacheRecord(ctx, user.UserID, jobID)
		if recordErr != nil {
			h.logger.Error("failed to find investment plan cache record", "error", recordErr)
			httputil.InternalError(w, fmt.Errorf("failed to load investment plan"))
			return
		}
		if record == nil {
			httputil.NotFound(w, "job not found")
			return
		}

		// Investment plans don't expire like ephemeral cached data
		cacheRecord, err := h.store.Q().GetCacheByUserAndKeyNoExpiry(ctx, queries.GetCacheByUserAndKeyNoExpiryParams{
			UserId: user.UserID,
			Key:    record.Key,
		})
		if err != nil {
			h.logger.Error("failed to load investment plan cache", "error", err)
			httputil.InternalError(w, fmt.Errorf("failed to load investment plan"))
			return
		}

		var cachedData map[string]interface{}
		if cacheRecord.Content != "" {
			_ = json.Unmarshal([]byte(cacheRecord.Content), &cachedData)
		}

		result := normalizeInvestmentPlanResult(cachedData)

		// Build request criteria from cache metadata
		request := map[string]interface{}{}

		// Extract locations from cache record's location field
		if cacheRecord.Location != "" {
			// Location is stored as "Peoria, IL; Chicago, IL"
			locations := strings.Split(cacheRecord.Location, "; ")
			request["locations"] = locations
		}

		// Extract request parameters from metricsData
		if len(cacheRecord.MetricsData) > 0 {
			var metricsData map[string]interface{}
			if err := json.Unmarshal(cacheRecord.MetricsData, &metricsData); err == nil {
				if strategy, ok := metricsData["strategy"]; ok {
					request["strategy"] = strategy
				}
				if capital, ok := metricsData["availableCapital"]; ok {
					request["availableCapital"] = capital
				}
				// Yearly budgets for multi-year planning display
				if yb, ok := metricsData["yearlyBudgets"]; ok && yb != nil {
					request["yearlyBudgets"] = yb
				}
				// Financial assumptions
				fa := map[string]interface{}{}
				if mr, ok := metricsData["mortgageRate"]; ok {
					fa["mortgageRate"] = mr
				}
				if dp, ok := metricsData["downPaymentPct"]; ok {
					fa["downPaymentPercent"] = dp
				}
				if oe, ok := metricsData["operatingExpensesPct"]; ok {
					fa["operatingExpenses"] = oe
				}
				if len(fa) > 0 {
					request["financialAssumptions"] = fa
				}
				// Reinvestment settings
				if v, ok := metricsData["reinvestSurplusCashFlows"]; ok {
					request["reinvestSurplusCashFlows"] = v
				}
				if v, ok := metricsData["reinvestmentRate"]; ok {
					request["reinvestmentRate"] = v
				}
				if v, ok := metricsData["projectionYears"]; ok {
					request["projectionYears"] = v
				}
				// Constraints
				if rt, ok := metricsData["riskTolerance"]; ok && rt != "" {
					request["constraints"] = map[string]interface{}{
						"riskTolerance": rt,
					}
				}
				if v, ok := metricsData["includeSuburbs"]; ok {
					request["includeSuburbs"] = v
				}
			}
		}

		createdAt := cacheRecord.CreatedAt.Time
		response := map[string]interface{}{
			"success":     true,
			"responseKey": record.Key,
			"job": map[string]interface{}{
				"id":          record.ID,
				"responseKey": record.Key,
				"status":      normalizeJobStatus(queue.JobStatusCompleted),
				"progress":    float64(100),
				"createdAt":   createdAt.Format(time.RFC3339),
				"updatedAt":   createdAt.Format(time.RFC3339),
				"result":      result,
				"request":     request,
			},
		}

		httputil.JSON(w, http.StatusOK, response)
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
			"id":              job.ID,
			"status":          normalizeJobStatus(job.Status),
			"progress":        job.Progress,
			"progressMessage": job.ProgressMessage,
			"createdAt":       job.CreatedAt.Format(time.RFC3339),
			"updatedAt":       job.UpdatedAt.Format(time.RFC3339),
			"request":         job.Payload,
		},
	}

	if result != nil {
		response["job"].(map[string]interface{})["result"] = normalizeInvestmentPlanResult(result.Data)
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

func parseBoundedIntQuery(r *http.Request, key string, defaultVal, minVal, maxVal int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultVal
	}
	if n < minVal {
		return minVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

func parseKeyForLocation(key string) string {
	// Cache keys are client-provided: investment_plan_{city}_{st}_dp.._ir.._oe.._{strategy}
	// or shorter format: investment_plan_{hash}
	withoutPrefix := strings.TrimPrefix(key, "qry_investment_plan_")
	withoutPrefix = strings.TrimPrefix(withoutPrefix, "investment_plan_")

	parts := strings.Split(withoutPrefix, "_")
	locationParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "dp") || strings.HasPrefix(part, "ir") || strings.HasPrefix(part, "oe") || len(part) == 16 {
			break
		}
		locationParts = append(locationParts, part)
	}

	if len(locationParts) >= 2 {
		cityParts := locationParts[:len(locationParts)-1]
		state := strings.ToUpper(locationParts[len(locationParts)-1])
		for i := range cityParts {
			if cityParts[i] == "" {
				continue
			}
			cityParts[i] = strings.ToUpper(cityParts[i][:1]) + cityParts[i][1:]
		}
		return strings.Join(cityParts, " ") + ", " + state
	}

	if len(locationParts) == 1 && strings.TrimSpace(locationParts[0]) != "" {
		return locationParts[0]
	}
	return "Unknown Location"
}

func extractIntAfterPrefix(s, prefix string) (int, bool) {
	idx := strings.Index(s, prefix)
	if idx == -1 {
		return 0, false
	}
	rest := s[idx+len(prefix):]
	digits := make([]byte, 0, 8)
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			break
		}
		digits = append(digits, rest[i])
	}
	if len(digits) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(string(digits))
	if err != nil {
		return 0, false
	}
	return n, true
}

func parseFinancialAssumptionsFromKey(key string) map[string]interface{} {
	dp, dpOK := extractIntAfterPrefix(key, "dp")
	ir, irOK := extractIntAfterPrefix(key, "ir")
	oe, oeOK := extractIntAfterPrefix(key, "oe")
	if !dpOK || !irOK || !oeOK {
		return nil
	}
	return map[string]interface{}{
		"downPaymentPercent": dp,
		"mortgageRate":       float64(ir) / 100.0,
		"operatingExpenses":  oe,
	}
}

func getNumberField(m map[string]interface{}, key string) (float64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func getStringField(m map[string]interface{}, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

func getBoolField(m map[string]interface{}, key string) (bool, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	if !ok {
		return false, false
	}
	return b, true
}

func mapMetricsForHistory(metrics map[string]interface{}) map[string]interface{} {
	if metrics == nil {
		return nil
	}

	out := map[string]interface{}{
		"propertyCount":        0,
		"totalInvestment":      0,
		"projectedReturn":      0.0,
		"cashOnCashReturn":     0.0,
		"annualCashFlow":       0,
		"totalValue":           0,
		"totalDebt":            0,
		"sharpeRatio":          0.0,
		"debtServiceCoverage":  0.0,
		"diversificationScore": 0.0,
		"totalROI":             0.0,
		"locationCount":        0,
	}

	if n, ok := getNumberField(metrics, "propertyCount"); ok {
		out["propertyCount"] = int(math.Round(n))
	} else if n, ok := getNumberField(metrics, "property_count"); ok {
		out["propertyCount"] = int(math.Round(n))
	}

	// Prefer v1 totalInvestment; fall back to Go-worker totalDownPayment.
	if n, ok := getNumberField(metrics, "totalInvestment"); ok {
		out["totalInvestment"] = int(math.Round(n))
	} else if n, ok := getNumberField(metrics, "totalDownPayment"); ok {
		out["totalInvestment"] = int(math.Round(n))
	}

	// v1 uses expectedAnnualReturn.
	if n, ok := getNumberField(metrics, "expectedAnnualReturn"); ok {
		out["projectedReturn"] = n
	} else if n, ok := getNumberField(metrics, "projectedReturn"); ok {
		out["projectedReturn"] = n
	}

	if n, ok := getNumberField(metrics, "cashOnCashReturn"); ok {
		out["cashOnCashReturn"] = n
	} else if n, ok := getNumberField(metrics, "avgCashOnCash"); ok {
		out["cashOnCashReturn"] = n
	} else if n, ok := getNumberField(metrics, "averageCashOnCash"); ok {
		// Fallback for legacy cached data
		out["cashOnCashReturn"] = n
	}

	if n, ok := getNumberField(metrics, "annualCashFlow"); ok {
		out["annualCashFlow"] = int(math.Round(n))
	} else if n, ok := getNumberField(metrics, "annualNetCashFlow"); ok {
		out["annualCashFlow"] = int(math.Round(n))
	}

	if n, ok := getNumberField(metrics, "totalValue"); ok {
		out["totalValue"] = int(math.Round(n))
	} else if n, ok := getNumberField(metrics, "projectedValue"); ok {
		out["totalValue"] = int(math.Round(n))
	}

	if n, ok := getNumberField(metrics, "totalDebt"); ok {
		out["totalDebt"] = int(math.Round(n))
	} else if n, ok := getNumberField(metrics, "totalLoanAmount"); ok {
		out["totalDebt"] = int(math.Round(n))
	}

	// Additional metrics for portfolio view
	if n, ok := getNumberField(metrics, "sharpeRatio"); ok {
		out["sharpeRatio"] = n
	}
	if n, ok := getNumberField(metrics, "debtServiceCoverage"); ok {
		out["debtServiceCoverage"] = n
	} else if n, ok := getNumberField(metrics, "portfolioDscr"); ok {
		out["debtServiceCoverage"] = n
	}
	if n, ok := getNumberField(metrics, "diversificationScore"); ok {
		out["diversificationScore"] = n
	}
	if n, ok := getNumberField(metrics, "totalROI"); ok {
		out["totalROI"] = n
	}
	if n, ok := getNumberField(metrics, "locationCount"); ok {
		out["locationCount"] = int(math.Round(n))
	}

	// User-set parameters for refresh (stored by worker)
	if s, ok := getStringField(metrics, "riskTolerance"); ok && s != "" {
		out["riskTolerance"] = s
	}
	if n, ok := getNumberField(metrics, "downPaymentPct"); ok {
		out["downPaymentPct"] = n
	}
	if n, ok := getNumberField(metrics, "mortgageRate"); ok {
		out["mortgageRate"] = n
	}
	if n, ok := getNumberField(metrics, "operatingExpensesPct"); ok {
		out["operatingExpensesPct"] = n
	}
	if b, ok := getBoolField(metrics, "reinvestSurplusCashFlows"); ok {
		out["reinvestSurplusCashFlows"] = b
	}
	if n, ok := getNumberField(metrics, "reinvestmentRate"); ok {
		out["reinvestmentRate"] = n
	}
	if n, ok := getNumberField(metrics, "projectionYears"); ok {
		out["projectionYears"] = int(math.Round(n))
	}
	if n, ok := getNumberField(metrics, "avgMarketHeat"); ok {
		out["avgMarketHeat"] = int(math.Round(n))
	}

	// Multi-year and additional params
	if yearlyBudgets, ok := metrics["yearlyBudgets"]; ok && yearlyBudgets != nil {
		out["yearlyBudgets"] = yearlyBudgets
	}
	if b, ok := getBoolField(metrics, "includeSuburbs"); ok {
		out["includeSuburbs"] = b
	}
	if n, ok := getNumberField(metrics, "maxProperties"); ok {
		out["maxProperties"] = int(math.Round(n))
	}

	return out
}

// GetInvestmentPlanHistory returns the user's investment planning history
func (h *Handler) GetInvestmentPlanHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Parse pagination params (fix: fmt.Sscanf returned the scanned-item count, not the parsed value).
	page := parseBoundedIntQuery(r, "page", 1, 1, 10_000)
	limit := parseBoundedIntQuery(r, "limit", 50, 1, 100)
	offset := (page - 1) * limit

	search := strings.TrimSpace(r.URL.Query().Get("search"))

	// Match www_v1 behavior:
	// - feature = 'investment_planning'
	// - supersededBy = null (only current versions)
	// - order by lastAccessedAt desc
	dbPlans, err := h.store.Q().ListInvestmentPlanHistory(ctx, queries.ListInvestmentPlanHistoryParams{
		UserId: user.UserID,
		Search: pgtype.Text{String: search, Valid: search != ""},
		Off:    int32(offset),
		Lim:    int32(limit),
	})
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

	plans := make([]InvestmentPlanHistoryItem, 0, len(dbPlans))
	for _, dbPlan := range dbPlans {
		var item InvestmentPlanHistoryItem
		item.ID = dbPlan.ID
		item.ResponseKey = dbPlan.Key
		item.UserID = dbPlan.UserId
		location := dbPlan.Location

		// Convert JSONB []byte to *string
		var metadataData, metricsData, investorProfileData *string
		if len(dbPlan.Metadata) > 0 {
			str := string(dbPlan.Metadata)
			metadataData = &str
		}
		if len(dbPlan.MetricsData) > 0 {
			str := string(dbPlan.MetricsData)
			metricsData = &str
		}
		if len(dbPlan.InvestorProfile) > 0 {
			str := string(dbPlan.InvestorProfile)
			investorProfileData = &str
		}

		lastAccessedAt := dbPlan.LastAccessedAt.Time

		item.Status = "COMPLETED"
		item.CreatedAt = lastAccessedAt.Format(time.RFC3339)

		// Locations: prefer analysis_cache.location; fall back to key parsing.
		if strings.TrimSpace(location) != "" {
			item.Locations = strings.Split(location, "; ")
		} else {
			item.Locations = []string{parseKeyForLocation(item.ResponseKey)}
		}

		// Parse metadata (fallback for older rows).
		var metadata map[string]interface{}
		if metadataData != nil && *metadataData != "" {
			_ = json.Unmarshal([]byte(*metadataData), &metadata)
		}

		// Parse metrics data
		if metricsData != nil && *metricsData != "" {
			var metrics map[string]interface{}
			if json.Unmarshal([]byte(*metricsData), &metrics) == nil {
				// strategy/availableCapital are stored in metricsData by www_v1 for card display.
				if s, ok := getStringField(metrics, "strategy"); ok && s != "" {
					item.Strategy = s
				}
				if n, ok := getNumberField(metrics, "availableCapital"); ok {
					item.AvailableCapital = int(math.Round(n))
				}

				item.Metrics = mapMetricsForHistory(metrics)

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

		// Fallbacks for strategy/capital from metadata.
		if item.Strategy == "" && metadata != nil {
			if s, ok := getStringField(metadata, "strategy"); ok && s != "" {
				item.Strategy = s
			}
		}
		if item.AvailableCapital == 0 && metadata != nil {
			if n, ok := getNumberField(metadata, "availableCapital"); ok {
				item.AvailableCapital = int(math.Round(n))
			} else if n, ok := getNumberField(metadata, "budget"); ok {
				item.AvailableCapital = int(math.Round(n))
			}
		}
		if item.Strategy == "" {
			item.Strategy = "balanced"
		}

		// Financial assumptions: prefer metadata, then investorProfile, then parse from key.
		if metadata != nil {
			if assumptions, ok := metadata["financialAssumptions"].(map[string]interface{}); ok {
				item.FinancialAssumptions = assumptions
			}
		}
		if item.FinancialAssumptions == nil && investorProfileData != nil && *investorProfileData != "" {
			var profile map[string]interface{}
			if json.Unmarshal([]byte(*investorProfileData), &profile) == nil {
				item.FinancialAssumptions = map[string]interface{}{
					"mortgageRate":       profile["mortgageRate"],
					"downPaymentPercent": profile["downPaymentPercent"],
					"operatingExpenses":  profile["operatingExpenses"],
				}
			}
		}
		if item.FinancialAssumptions == nil {
			item.FinancialAssumptions = parseFinancialAssumptionsFromKey(item.ResponseKey)
		}

		plans = append(plans, item)
	}

	// Get total count
	totalCount, _ := h.store.Q().CountInvestmentPlanHistory(ctx, queries.CountInvestmentPlanHistoryParams{
		UserId: user.UserID,
		Search: pgtype.Text{String: search, Valid: search != ""},
	})
	total := int(totalCount)

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

// normalizeJobStatus converts lowercase job status to uppercase for client compatibility
// Go backend uses lowercase (completed, failed, processing) but client expects uppercase (COMPLETED, FAILED, PROCESSING)
func normalizeJobStatus(status queue.JobStatus) string {
	return strings.ToUpper(string(status))
}

func normalizeInvestmentPlanResult(data interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}

	switch v := data.(type) {
	case map[string]interface{}:
		return normalizeInvestmentPlanResultMap(v)
	default:
		blob, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var m map[string]interface{}
		if err := json.Unmarshal(blob, &m); err != nil {
			return nil
		}
		return normalizeInvestmentPlanResultMap(m)
	}
}

// complianceFilter is a shared instance for filtering content
var complianceFilter = compliance.NewFilter(compliance.FilterConfig{StrictMode: false})

func normalizeInvestmentPlanResultMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}

	out := map[string]interface{}{}

	if raw, ok := input["selectedProperties"]; ok {
		out["selectedProperties"] = normalizeInvestmentPlanProperties(raw)
	} else if raw, ok := input["selected_properties"]; ok {
		out["selectedProperties"] = normalizeInvestmentPlanProperties(raw)
	}

	if raw, ok := input["metrics"]; ok {
		out["metrics"] = normalizeInvestmentPlanMetrics(raw)
	}

	if raw, ok := input["growthProjection"]; ok {
		out["growthProjection"] = raw
	} else if raw, ok := input["growth_projection"]; ok {
		out["growthProjection"] = raw
	}

	if raw, ok := input["existingPortfolio"]; ok {
		out["existingPortfolio"] = raw
	} else if raw, ok := input["existing_portfolio"]; ok {
		out["existingPortfolio"] = raw
	}

	if raw, ok := input["combinedMetrics"]; ok {
		out["combinedMetrics"] = raw
	} else if raw, ok := input["combined_metrics"]; ok {
		out["combinedMetrics"] = raw
	}

	if raw, ok := input["multiYearPlan"]; ok {
		out["multiYearPlan"] = raw
	} else if raw, ok := input["multi_year_plan"]; ok {
		out["multiYearPlan"] = raw
	}

	if raw, ok := input["marketFilters"]; ok {
		out["marketFiltering"] = raw
	} else if raw, ok := input["market_filters"]; ok {
		out["marketFiltering"] = raw
	}

	if raw, ok := input["marketQuality"]; ok {
		out["marketAnalysis"] = raw
	} else if raw, ok := input["market_quality"]; ok {
		out["marketAnalysis"] = raw
	}

	if raw, ok := input["concentration"]; ok {
		out["concentration"] = raw
	}

	// ADR-059: Reinvestment analysis
	if raw, ok := input["reinvestmentAnalysis"]; ok {
		out["reinvestmentAnalysis"] = raw
	} else if raw, ok := input["reinvestment_analysis"]; ok {
		out["reinvestmentAnalysis"] = raw
	}

	// ADR-059: Property recommendations
	if raw, ok := input["propertyRecommendations"]; ok {
		out["propertyRecommendations"] = raw
	} else if raw, ok := input["property_recommendations"]; ok {
		out["propertyRecommendations"] = raw
	}

	// Apply compliance filtering to recommendations (ADR-051)
	if raw, ok := input["recommendations"]; ok {
		out["recommendations"] = filterRecommendations(raw)
	}

	// Apply compliance filtering to riskAnalysis.warnings
	if raw, ok := input["riskAnalysis"]; ok {
		out["riskAnalysis"] = filterRiskAnalysisWarnings(raw)
	} else if raw, ok := input["risk_analysis"]; ok {
		out["riskAnalysis"] = filterRiskAnalysisWarnings(raw)
	}

	// Preserve any other fields we don't explicitly map
	for key, value := range input {
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = value
	}

	return out
}

// filterRecommendations applies compliance filtering to recommendations array
func filterRecommendations(raw interface{}) []string {
	recs, ok := raw.([]interface{})
	if !ok {
		// Try string slice
		if strRecs, ok := raw.([]string); ok {
			filtered := make([]string, 0, len(strRecs))
			for _, rec := range strRecs {
				filteredRec, _ := complianceFilter.FilterContent(rec)
				filtered = append(filtered, filteredRec)
			}
			return filtered
		}
		return nil
	}

	filtered := make([]string, 0, len(recs))
	for _, rec := range recs {
		if str, ok := rec.(string); ok {
			filteredRec, _ := complianceFilter.FilterContent(str)
			filtered = append(filtered, filteredRec)
		}
	}
	return filtered
}

// filterRiskAnalysisWarnings applies compliance filtering to riskAnalysis.warnings
func filterRiskAnalysisWarnings(raw interface{}) map[string]interface{} {
	riskAnalysis, ok := raw.(map[string]interface{})
	if !ok {
		// Try to convert via JSON
		blob, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(blob, &riskAnalysis); err != nil {
			return nil
		}
	}

	// Filter warnings array
	if warnings, ok := riskAnalysis["warnings"]; ok {
		if warningsSlice, ok := warnings.([]interface{}); ok {
			filtered := make([]string, 0, len(warningsSlice))
			for _, w := range warningsSlice {
				if str, ok := w.(string); ok {
					filteredW, _ := complianceFilter.FilterContent(str)
					filtered = append(filtered, filteredW)
				}
			}
			riskAnalysis["warnings"] = filtered
		}
	}

	return riskAnalysis
}

func normalizeInvestmentPlanMetrics(raw interface{}) map[string]interface{} {
	metrics := map[string]interface{}{}

	blob, err := json.Marshal(raw)
	if err != nil {
		return metrics
	}
	if err := json.Unmarshal(blob, &metrics); err != nil {
		return metrics
	}

	if _, ok := metrics["totalValue"]; !ok {
		if v, ok := metrics["projectedValue"]; ok {
			metrics["totalValue"] = v
		}
	}
	if _, ok := metrics["cashOnCashReturn"]; !ok {
		if v, ok := metrics["avgCashOnCash"]; ok {
			metrics["cashOnCashReturn"] = v
		}
	}
	// expectedAnnualReturn should come from the calculator (NOI / Cash Invested)
	// Only fallback to avgCashOnCash if not present (legacy data)
	if _, ok := metrics["expectedAnnualReturn"]; !ok {
		// Fallback for legacy cached data without expectedAnnualReturn
		if v, ok := metrics["avgCashOnCash"]; ok {
			metrics["expectedAnnualReturn"] = v
		}
	}
	if _, ok := metrics["averageCapRate"]; !ok {
		if v, ok := metrics["avgCapRate"]; ok {
			metrics["averageCapRate"] = v
		}
	}
	if _, ok := metrics["propertyCount"]; !ok {
		if v, ok := metrics["property_count"]; ok {
			metrics["propertyCount"] = v
		}
	}

	// Map monthlyCashFlow → netMonthlyCashFlow (client expects this name)
	if _, ok := metrics["netMonthlyCashFlow"]; !ok {
		if v, ok := metrics["monthlyCashFlow"]; ok {
			metrics["netMonthlyCashFlow"] = v
		}
	}

	return metrics
}

func normalizeInvestmentPlanProperties(raw interface{}) []interface{} {
	var items []map[string]interface{}

	blob, err := json.Marshal(raw)
	if err != nil {
		return []interface{}{}
	}
	if err := json.Unmarshal(blob, &items); err != nil {
		return []interface{}{}
	}

	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		if prop, ok := item["property"].(map[string]interface{}); ok {
			normalized := map[string]interface{}{}
			if id, ok := prop["id"]; ok {
				normalized["zpid"] = id
			} else if id, ok := prop["zpid"]; ok {
				normalized["zpid"] = id
			}
			copyIfPresent(normalized, prop, "address", "address")
			copyIfPresent(normalized, prop, "city", "city")
			copyIfPresent(normalized, prop, "state", "state")
			copyIfPresent(normalized, prop, "zipCode", "zipCode")
			copyIfPresent(normalized, prop, "price", "price")
			if beds, ok := prop["beds"]; ok {
				normalized["bedrooms"] = beds
			} else {
				copyIfPresent(normalized, prop, "bedrooms", "bedrooms")
			}
			if baths, ok := prop["baths"]; ok {
				normalized["bathrooms"] = baths
			} else {
				copyIfPresent(normalized, prop, "bathrooms", "bathrooms")
			}
			copyIfPresent(normalized, prop, "sqft", "sqft")
			copyIfPresent(normalized, prop, "estimatedRent", "estimatedRent")
			copyIfPresent(normalized, prop, "yearBuilt", "yearBuilt")
			copyIfPresent(normalized, prop, "propertyType", "propertyType")
			copyIfPresent(normalized, prop, "daysOnMarket", "daysOnMarket")
			copyIfPresent(normalized, prop, "listingUrl", "listingUrl")

			copyIfPresent(normalized, item, "capRate", "capRate")
			if coc, ok := item["cashOnCash"]; ok {
				normalized["cashOnCashReturn"] = coc
			} else {
				copyIfPresent(normalized, item, "cashOnCashReturn", "cashOnCashReturn")
			}
			copyIfPresent(normalized, item, "score", "score")
			copyIfPresent(normalized, item, "aiScore", "aiScore")
			copyIfPresent(normalized, item, "recommendation", "recommendation")
			copyIfPresent(normalized, item, "riskLevel", "riskLevel")
			if downPayment, ok := item["downPayment"]; ok {
				normalized["investmentAmount"] = downPayment
			} else {
				copyIfPresent(normalized, item, "investmentAmount", "investmentAmount")
			}
			copyIfPresent(normalized, item, "loanAmount", "loanAmount")
			copyIfPresent(normalized, item, "monthlyCashFlow", "monthlyCashFlow")

			out = append(out, normalized)
			continue
		}

		out = append(out, item)
	}

	return out
}

func copyIfPresent(dst map[string]interface{}, src map[string]interface{}, srcKey, dstKey string) {
	if val, ok := src[srcKey]; ok {
		dst[dstKey] = val
	}
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

	// ADR-087: Normalize location for cache key consistency
	locationNormalized := strings.ToLower(strings.ReplaceAll(req.Location, " ", "_"))
	locationNormalized = strings.ReplaceAll(locationNormalized, ",", "")
	reportCacheKey := fmt.Sprintf("market_analysis:%s", locationNormalized)

	// ADR-087: Check if report exists in database (L2 cache discovery)
	if !req.ForceRefresh {
		existingReport, err := h.store.Q().GetReportByCacheKey(ctx, reportCacheKey)
		if err == nil && existingReport.Status == "completed" {
			// Report exists and is completed - increment access count
			_ = h.store.Q().IncrementReportAccessCount(ctx, reportCacheKey)

			// Track user access (ADR-087)
			accessID := uuid.New().String()
			_ = h.store.Q().TrackUserAccess(ctx, queries.TrackUserAccessParams{
				ID:       accessID,
				UserID:   user.UserID,
				ReportID: existingReport.ID,
			})

			h.logger.Info("returning existing completed report",
				"cache_key", reportCacheKey,
				"report_id", existingReport.ID,
				"user_id", user.UserID,
			)

			// Try to get from L1 (Redis) first
			redisCacheKey := fmt.Sprintf("analysis:%s", req.CacheKey)
			cached, err := h.redis.Client.Get(ctx, redisCacheKey).Bytes()
			if err == nil && len(cached) > 0 {
				httputil.JSON(w, http.StatusOK, map[string]interface{}{
					"success":  true,
					"cached":   true,
					"cacheKey": req.CacheKey,
					"data":     json.RawMessage(cached),
				})
				return
			}

			// Fallback to L2 (database analysis_cache)
			dbCache, err := h.store.Q().GetCacheByKey(ctx, req.CacheKey)
			if err == nil {
				httputil.JSON(w, http.StatusOK, map[string]interface{}{
					"success":  true,
					"cached":   true,
					"cacheKey": req.CacheKey,
					"data":     dbCache.Content,
				})
				return
			}
		}
	}

	// Build payload — use camelCase keys to match client (single source of truth for cache keys)
	payload := map[string]interface{}{
		"location":     req.Location,
		"cacheKey":     req.CacheKey,
		"forceRefresh": req.ForceRefresh,
	}

	// Create job
	job := queue.NewJob(queue.JobTypeMarketAnalysis, user.UserID, payload)

	// Enqueue (nil-safe: queue may be unavailable if Redis is down)
	if h.jobQueue == nil {
		httputil.InternalError(w, fmt.Errorf("job processing unavailable"))
		return
	}
	jobID, err := h.jobQueue.Enqueue(job)
	if err != nil {
		h.logger.Error("failed to enqueue analysis job", "error", err)
		httputil.InternalError(w, fmt.Errorf("failed to queue job"))
		return
	}

	// ADR-087: Create pending report record
	reportID := uuid.New().String()
	_, err = h.store.Q().CreateMarketAnalysisReport(ctx, queries.CreateMarketAnalysisReportParams{
		ID:                 reportID,
		Location:           req.Location,
		LocationNormalized: locationNormalized,
		CacheKey:           reportCacheKey,
		Status:             "pending",
	})
	if err != nil {
		// Log but don't fail - report tracking is supplementary
		h.logger.Warn("failed to create report record",
			"error", err,
			"cache_key", reportCacheKey,
		)
	} else {
		h.logger.Info("created pending report record",
			"report_id", reportID,
			"cache_key", reportCacheKey,
		)
	}

	h.logger.Info("market analysis queued",
		"job_id", jobID,
		"user_id", user.UserID,
		"location", req.Location,
	)

	// Log feature usage for customer support and chargeback defense
	_ = util.LogFeatureUsage(ctx, h.store, r, user.UserID, "FEATURE_MARKET_ANALYSIS",
		fmt.Sprintf("Market analysis: %s", req.Location),
		map[string]any{
			"jobId":        jobID,
			"location":     req.Location,
			"cacheKey":     req.CacheKey,
			"forceRefresh": req.ForceRefresh,
		},
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

	// Nil-safe: queue may be unavailable after restart
	if h.jobQueue == nil {
		sseWriter.WriteError("job not found")
		return
	}

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

	var jobs []*queue.Job
	if h.jobQueue != nil {
		jobs = h.jobQueue.GetUserJobs(user.UserID, status)
	}

	// Filter to analysis jobs only
	analysisJobs := make([]map[string]interface{}, 0)
	for _, job := range jobs {
		if job.Type == queue.JobTypeMarketAnalysis {
			analysisJobs = append(analysisJobs, map[string]interface{}{
				"id":        job.ID,
				"status":    normalizeJobStatus(job.Status),
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

	// Get original job (nil-safe: queue may be unavailable after restart)
	if h.jobQueue == nil {
		httputil.NotFound(w, "job not found")
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

	if h.jobQueue == nil {
		httputil.NotFound(w, "job not found")
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
// Analysis History & Dismiss
// ===============================

// AnalysisHistoryItem represents a single analysis in the history
type AnalysisHistoryItem struct {
	ID        string                 `json:"id"`
	CacheKey  string                 `json:"cacheKey"`
	Location  string                 `json:"location"`
	Status    string                 `json:"status"`
	CreatedAt string                 `json:"createdAt"`
	Preview   string                 `json:"preview"`
	HasReport bool                   `json:"hasReport"`
	Metrics   map[string]interface{} `json:"metrics,omitempty"`
	Synthesis map[string]interface{} `json:"synthesis,omitempty"`
}

// GetAnalysisHistory returns the user's market analysis history (ADR-087)
func (h *Handler) GetAnalysisHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	page := parseBoundedIntQuery(r, "page", 1, 1, 10_000)
	limit := parseBoundedIntQuery(r, "limit", 50, 1, 100)
	offset := (page - 1) * limit
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	// ADR-087: Query all reports from market_analysis_reports (shared, not user-filtered)
	var dbReports []queries.MarketAnalysisReport
	var err error

	if search != "" {
		dbReports, err = h.store.Q().ListReportsWithSearch(ctx, queries.ListReportsWithSearchParams{
			Search: pgtype.Text{String: search, Valid: true},
			Limit:  int32(limit),
			Offset: int32(offset),
		})
	} else {
		dbReports, err = h.store.Q().ListAllReports(ctx, queries.ListAllReportsParams{
			Limit:  int32(limit),
			Offset: int32(offset),
		})
	}
	if err != nil {
		h.logger.Error("failed to get analysis history", "error", err)
		httputil.JSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"analyses": []AnalysisHistoryItem{},
			"pagination": map[string]int{
				"page": page, "limit": limit, "total": 0, "totalPages": 0,
			},
		})
		return
	}

	// Batch fetch all cache entries to avoid N+1 queries
	cacheKeys := make([]string, len(dbReports))
	for i, report := range dbReports {
		cacheKeys[i] = report.CacheKey
	}

	cacheMap := make(map[string]queries.BatchGetAnalysisCacheByKeysRow)
	if len(cacheKeys) > 0 {
		cachedItems, err := h.store.Q().BatchGetAnalysisCacheByKeys(ctx, cacheKeys)
		if err == nil {
			for _, cached := range cachedItems {
				cacheMap[cached.Key] = cached
			}
		}
	}

	analyses := make([]AnalysisHistoryItem, 0, len(dbReports))
	for _, report := range dbReports {
		item := AnalysisHistoryItem{
			ID:       report.ID,
			CacheKey: report.CacheKey,
			Location: report.Location,
			Status:   report.Status,
		}

		// Use generated_at if available, otherwise created_at
		if report.GeneratedAt.Valid {
			item.CreatedAt = report.GeneratedAt.Time.Format(time.RFC3339)
		} else {
			item.CreatedAt = report.CreatedAt.Format(time.RFC3339)
		}

		// ADR-087: Report content is still in analysis_cache
		// Use batch-fetched cache data
		if cachedData, found := cacheMap[report.CacheKey]; found {
			content := string(cachedData.Content)
			fullReport := ""
			if cachedData.FullReport.Valid {
				fullReport = cachedData.FullReport.String
			}

			reportContent := fullReport
			if reportContent == "" {
				reportContent = content
			}
			item.HasReport = reportContent != ""
			item.Preview = extractExecutiveSummary(reportContent)
			// Note: metrics and synthesis not populated in batch mode for performance
		}

		analyses = append(analyses, item)
	}

	// Get total count (all reports, not filtered by user)
	var totalCount int64
	if search != "" {
		totalCount, _ = h.store.Q().CountReportsWithSearch(ctx, pgtype.Text{String: search, Valid: true})
	} else {
		totalCount, _ = h.store.Q().CountReports(ctx)
	}
	total := int(totalCount)

	totalPages := total / limit
	if total%limit > 0 {
		totalPages++
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"analyses": analyses,
		"pagination": map[string]int{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": totalPages,
		},
	})
}

// DismissAnalysisJob deletes an analysis from all cache layers (L0 job queue, L1 Redis, L2 PostgreSQL).
// This is a hard delete — the analysis is removed from history and cannot be recovered.
func (h *Handler) DismissAnalysisJob(w http.ResponseWriter, r *http.Request) {
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

	// L0: Remove from in-memory job queue if present (nil-safe: queue may be unavailable after restart)
	if h.jobQueue != nil {
		if job, err := h.jobQueue.GetJob(jobID); err == nil {
			if job.UserID != user.UserID {
				httputil.NotFound(w, "analysis not found")
				return
			}
			_ = h.jobQueue.Delete(jobID)
		}
	}

	// L2 + L1: Look up analysis_cache row by ID to get the cache key, then purge
	q := h.store.Q()
	cacheRow, err := q.GetCacheByID(ctx, jobID)
	if err == nil {
		// Verify ownership
		if cacheRow.UserId != user.UserID {
			httputil.NotFound(w, "analysis not found")
			return
		}

		// L1: Delete from Redis
		if h.redis != nil {
			redisKey := "cache:" + cacheRow.UserId + ":" + cacheRow.Key
			if delErr := h.redis.Delete(ctx, redisKey); delErr != nil {
				h.logger.Warn("failed to delete analysis from L1 Redis", "key", cacheRow.Key, "error", delErr)
			}
		}

		// L2: Delete from PostgreSQL
		if delErr := q.DeleteCacheByUserAndKey(ctx, queries.DeleteCacheByUserAndKeyParams{
			UserId: cacheRow.UserId,
			Key:    cacheRow.Key,
		}); delErr != nil {
			h.logger.Warn("failed to delete analysis from L2 PostgreSQL", "key", cacheRow.Key, "error", delErr)
		}

		h.logger.Info("analysis deleted from cache", "id", jobID, "key", cacheRow.Key, "user_id", user.UserID)
	} else {
		h.logger.Info("analysis not found in cache (job queue only)", "id", jobID, "user_id", user.UserID)
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "analysis deleted",
	})
}

// GetAnalysisContext returns market context for a location (used to pre-populate analysis)
func (h *Handler) GetAnalysisContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	location := r.URL.Query().Get("location")
	if location == "" {
		httputil.BadRequest(w, "location query parameter is required")
		return
	}

	// Check if we have a cached analysis for this location
	dbAnalysis, err := h.store.Q().GetLatestMarketAnalysisByLocation(ctx, queries.GetLatestMarketAnalysisByLocationParams{
		UserId:   user.UserID,
		Location: location,
	})

	contextData := map[string]interface{}{
		"location":    location,
		"hasAnalysis": false,
	}

	if err == nil {
		contextData["hasAnalysis"] = true
		contextData["lastAnalyzedAt"] = dbAnalysis.LastAccessedAt.Time.Format(time.RFC3339)

		// Convert JSONB metricsData
		if len(dbAnalysis.MetricsData) > 0 {
			var metrics map[string]interface{}
			if json.Unmarshal(dbAnalysis.MetricsData, &metrics) == nil {
				contextData["metrics"] = metrics
			}
		}

		// Convert JSONB narrativeData
		if len(dbAnalysis.NarrativeData) > 0 {
			var narrative map[string]interface{}
			if json.Unmarshal(dbAnalysis.NarrativeData, &narrative) == nil {
				contextData["synthesis"] = narrative
			}
		}
	}

	// Build economic context if available
	if h.econContext != nil {
		parts := strings.SplitN(location, ",", 2)
		city := strings.TrimSpace(parts[0])
		var state string
		if len(parts) > 1 {
			state = strings.TrimSpace(parts[1])
		}
		econStr := h.econContext.BuildEconomicContext(ctx, city, state, 0)
		if econStr != "" {
			contextData["economicSummary"] = econStr
		}
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"context": contextData,
	})
}

// extractExecutiveSummary extracts the first paragraph under "## Executive Summary"
// from markdown content. Falls back to the first 300 chars if not found.
func extractExecutiveSummary(content string) string {
	if content == "" {
		return ""
	}

	// Look for Executive Summary heading (case-insensitive)
	lower := strings.ToLower(content)
	idx := strings.Index(lower, "## executive summary")
	if idx == -1 {
		// Fallback: first 300 chars of content
		if len(content) > 300 {
			return strings.TrimSpace(content[:300]) + "..."
		}
		return strings.TrimSpace(content)
	}

	// Skip the heading line itself
	rest := content[idx:]
	nlIdx := strings.Index(rest, "\n")
	if nlIdx == -1 {
		return ""
	}
	rest = rest[nlIdx+1:]

	// Skip blank lines after the heading
	lines := strings.Split(rest, "\n")
	var paragraphLines []string
	started := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !started {
			if trimmed == "" {
				continue
			}
			// Stop if we hit another heading
			if strings.HasPrefix(trimmed, "#") {
				break
			}
			started = true
		}
		if started {
			// Stop at blank line or next heading (end of first paragraph)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				break
			}
			paragraphLines = append(paragraphLines, trimmed)
		}
	}

	result := strings.Join(paragraphLines, " ")
	if len(result) > 500 {
		result = result[:500] + "..."
	}
	return result
}

// GetAnalysisReport returns the full analysis report for a given cache key
func (h *Handler) GetAnalysisReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	cacheKey := r.URL.Query().Get("key")
	if cacheKey == "" {
		httputil.BadRequest(w, "key query parameter is required")
		return
	}

	q := h.store.Q()

	// ADR-087: Market analysis reports are shared across users and should remain viewable after expiry
	// Use GetCacheByKeyNoExpiry to access historical reports
	cache, err := q.GetCacheByKeyNoExpiry(ctx, cacheKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.logger.Warn("analysis report not found",
				"key", cacheKey,
				"user_id", user.UserID,
			)
			httputil.NotFound(w, "report not found")
			return
		}
		h.logger.Error("failed to get analysis report", "error", err, "key", cacheKey)
		httputil.Error(w, http.StatusInternalServerError, "failed to load report")
		return
	}

	h.logger.Info("analysis report retrieved",
		"key", cacheKey,
		"user_id", user.UserID,
		"creator_user_id", cache.UserId,
		"location", cache.Location,
	)

	// Build response
	resp := map[string]interface{}{
		"success":  true,
		"location": cache.Location,
		"content":  cache.Content,
	}

	if cache.FullReport.Valid && cache.FullReport.String != "" {
		resp["content"] = cache.FullReport.String
	}

	// Add generated timestamp if available
	if cache.CreatedAt.Valid {
		resp["generatedAt"] = cache.CreatedAt.Time.Format(time.RFC3339)
	}

	if cache.MetricsData != nil {
		var metrics map[string]interface{}
		if json.Unmarshal(cache.MetricsData, &metrics) == nil {
			resp["metricsData"] = metrics
		}
	}

	if cache.NarrativeData != nil {
		var narrative map[string]interface{}
		if json.Unmarshal(cache.NarrativeData, &narrative) == nil {
			resp["narrativeData"] = narrative
		}
	}

	// ADR-100: Enrich response with structured market metrics + historical time series.
	// Both calls are best-effort — failures are logged and skipped (never block the response).
	enrichCtx, enrichCancel := context2.WithTimeout(ctx, 6*time.Second)
	defer enrichCancel()
	enrichment := h.buildReportEnrichment(enrichCtx, cache.Location)
	if enrichment != nil {
		resp["reportData"] = enrichment
	}

	httputil.JSON(w, http.StatusOK, resp)
}

// buildReportEnrichment fetches structured metrics + historical time series for ADR-100 report rendering.
// Failures from either source are non-fatal — the function returns whatever data is available.
func (h *Handler) buildReportEnrichment(ctx context.Context, location string) *agents.AnalysisReportEnrichment {
	if h.marketData == nil && h.trendsService == nil {
		return nil
	}

	// Parse "City, State" location string
	parts := strings.SplitN(location, ",", 2)
	city := strings.TrimSpace(parts[0])
	state := ""
	if len(parts) > 1 {
		state = strings.TrimSpace(parts[1])
	}

	enrichment := &agents.AnalysisReportEnrichment{}

	// Fetch market metrics (aggregator) + time series (trends) concurrently
	type result struct {
		metrics *agents.AnalysisMetrics
		ts      *agents.AnalysisTimeSeries
	}
	ch := make(chan result, 1)

	go func() {
		r := result{}

		if h.marketData != nil && city != "" {
			md, err := h.marketData.GetMarketData(ctx, city, state)
			if err != nil {
				h.logger.Warn("report enrichment: aggregator fetch failed, will try AI estimation", "location", location, "error", err)
			}

			// Build metrics from structured data (may be zero-valued when err != nil)
			var m *agents.AnalysisMetrics
			if err == nil {
				m = &agents.AnalysisMetrics{
					MedianHomePrice:      md.MedianHomePrice,
					MedianRent:           md.MedianRent,
					CapRate:              md.CapRate,
					MortgageRate30:       md.MortgageRate30,
					PriceChangeYoY:       md.YearOverYearPct,
					RentChangeYoY:        md.RentYearOverYear,
					VacancyRate:          md.VacancyRate,
					UnemploymentRate:     md.UnemploymentRate,
					EmploymentGrowthRate: md.EmploymentGrowthRate,
					PopulationGrowthRate: md.PopulationGrowthRate,
					Confidence:           md.Confidence,
					DataDate:             md.DataDate.Format("2006-01-02"),
				}
				if md.MedianDaysOnMarket != nil {
					m.DaysOnMarket = *md.MedianDaysOnMarket
				}
			}

			// AI fallback: city not in DB (err != nil) or structured data returned no price/rent
			needsEstimation := h.marketEstimator != nil && (err != nil || (m != nil && m.MedianHomePrice == 0 && m.MedianRent == 0))
			if needsEstimation {
				h.logger.Info("report enrichment: trying AI estimation for missing price/rent", "location", location)
				estimated, estErr := h.marketEstimator.EstimateMarketData(ctx, city, state)
				if estErr != nil {
					h.logger.Warn("report enrichment: AI estimation failed", "location", location, "error", estErr)
				} else {
					if m == nil {
						m = &agents.AnalysisMetrics{}
					}
					m.MedianHomePrice = estimated.MedianHomePrice
					m.MedianRent = estimated.MedianRent
					m.CapRate = estimated.CapRate
					m.PriceChangeYoY = estimated.YearOverYearPct
					m.Confidence = estimated.Confidence
					m.DataSource = "ai-estimated"
					h.logger.Info("report enrichment: AI estimation succeeded",
						"location", location,
						"medianHomePrice", estimated.MedianHomePrice,
						"medianRent", estimated.MedianRent,
						"confidence", estimated.Confidence,
					)
				}
			}

			if m != nil {
				if m.MedianHomePrice > 0 && m.MedianRent > 0 {
					m.PriceToRentRatio = float64(m.MedianHomePrice) / (float64(m.MedianRent) * 12)
					m.GrossYield = (float64(m.MedianRent) * 12) / float64(m.MedianHomePrice) * 100
				}
				r.metrics = m
			}
		}

		if h.trendsService != nil {
			historical, err := h.trendsService.GetHistoricalData(ctx, location, 5)
			if err != nil {
				h.logger.Warn("report enrichment: trends fetch failed", "location", location, "error", err)
			} else {
				ts := &agents.AnalysisTimeSeries{}
				for _, p := range historical.HomeValues {
					ts.HomeValues = append(ts.HomeValues, agents.AnalysisTimeSeriesPoint{
						Date:  p.Date.Format("2006-01"),
						Value: p.Value,
					})
				}
				for _, p := range historical.RentValues {
					ts.RentValues = append(ts.RentValues, agents.AnalysisTimeSeriesPoint{
						Date:  p.Date.Format("2006-01"),
						Value: p.Value,
					})
				}
				for _, p := range historical.MortgageRates {
					ts.MortgageRates = append(ts.MortgageRates, agents.AnalysisTimeSeriesPoint{
						Date:  p.Date.Format("2006-01"),
						Value: p.Value,
					})
				}
				r.ts = ts
			}
		}

		ch <- r
	}()

	select {
	case r := <-ch:
		if r.metrics != nil {
			enrichment.Metrics = r.metrics
		}
		if r.ts != nil {
			enrichment.TimeSeries = r.ts
		}
	case <-ctx.Done():
		h.logger.Warn("report enrichment: context deadline exceeded", "location", location)
	}

	if enrichment.Metrics == nil && enrichment.TimeSeries == nil {
		return nil
	}
	return enrichment
}

// ExportAnalysisPDF generates a PDF from a stored market analysis report (ADR-100).
// POST /api/ai/analysis/report/export  { "key": "<cacheKey>" }
func (h *Handler) ExportAnalysisPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.GetUserFromContext(ctx)
	if user == nil {
		httputil.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		httputil.BadRequest(w, "key is required")
		return
	}

	q := h.store.Q()
	cache, err := q.GetCacheByKeyNoExpiry(ctx, req.Key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.NotFound(w, "report not found")
			return
		}
		h.logger.Error("ExportAnalysisPDF: failed to load cache", "error", err, "key", req.Key)
		httputil.Error(w, http.StatusInternalServerError, "failed to load report")
		return
	}

	pdfData := pdf.MarketAnalysisPDFData{
		Location: cache.Location,
	}
	if cache.MetricsData != nil {
		_ = json.Unmarshal(cache.MetricsData, &pdfData.Metrics)
	}
	if cache.NarrativeData != nil {
		_ = json.Unmarshal(cache.NarrativeData, &pdfData.Narrative)
	}
	if cache.FullReport.Valid && cache.FullReport.String != "" {
		pdfData.FullReport = cache.FullReport.String
	} else if cache.Content != "" {
		pdfData.FullReport = cache.Content
	}
	if cache.Metadata != nil {
		_ = json.Unmarshal(cache.Metadata, &pdfData.DataPoints)
	}

	// ADR-100: Enrich with structured metrics + time series for richer PDF charts
	enrichCtx, enrichCancel := context2.WithTimeout(ctx, 8*time.Second)
	defer enrichCancel()
	enrichment := h.buildReportEnrichment(enrichCtx, cache.Location)
	if enrichment != nil && enrichment.Metrics != nil {
		m := enrichment.Metrics
		pdfData.StructuredMetrics = &pdf.MarketAnalysisMetrics{
			MedianHomePrice:      m.MedianHomePrice,
			MedianRent:           m.MedianRent,
			CapRate:              m.CapRate,
			MortgageRate30:       m.MortgageRate30,
			GrossYield:           m.GrossYield,
			PriceToRentRatio:     m.PriceToRentRatio,
			PriceChangeYoY:       m.PriceChangeYoY,
			RentChangeYoY:        m.RentChangeYoY,
			VacancyRate:          m.VacancyRate,
			UnemploymentRate:     m.UnemploymentRate,
			EmploymentGrowthRate: m.EmploymentGrowthRate,
			PopulationGrowthRate: m.PopulationGrowthRate,
			DaysOnMarket:         m.DaysOnMarket,
			DataDate:             m.DataDate,
		}
	}
	if enrichment != nil && enrichment.TimeSeries != nil {
		ts := enrichment.TimeSeries
		pdfTS := &pdf.MarketAnalysisTimeSeries{}
		for _, p := range ts.HomeValues {
			pdfTS.HomeValues = append(pdfTS.HomeValues, pdf.MarketAnalysisTimeSeriesPoint{Date: p.Date, Value: p.Value})
		}
		for _, p := range ts.RentValues {
			pdfTS.RentValues = append(pdfTS.RentValues, pdf.MarketAnalysisTimeSeriesPoint{Date: p.Date, Value: p.Value})
		}
		for _, p := range ts.MortgageRates {
			pdfTS.MortgageRates = append(pdfTS.MortgageRates, pdf.MarketAnalysisTimeSeriesPoint{Date: p.Date, Value: p.Value})
		}
		pdfData.TimeSeries = pdfTS
	}

	pdfBytes, err := pdf.BuildMarketAnalysisPDF(ctx, pdfData)
	if err != nil {
		h.logger.Error("ExportAnalysisPDF: build failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "failed to generate pdf")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="market_analysis.pdf"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
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
		err := h.store.Q().DeleteAiResponseCacheByUserAndKey(ctx, queries.DeleteAiResponseCacheByUserAndKeyParams{
			UserId: user.UserID,
			Key:    *req.CacheKey,
		})
		if err == nil {
			dbDeleted = 1
		}

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
		err := h.store.Q().DeleteAiResponseCacheByUserAndFeature(ctx, queries.DeleteAiResponseCacheByUserAndFeatureParams{
			UserId:  user.UserID,
			Feature: *req.Type,
		})
		if err == nil {
			dbDeleted = 1
		}

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
// ADR-098: discoverySessionID links this chat to the discovery pool that supplied the properties
func (h *Handler) createChatSession(ctx context.Context, userID string, properties []PropertyInput, profile *InvestorProfile, portfolioSnapshot json.RawMessage, discoverySessionID string) (string, error) {
	// Cache properties first - track both listing IDs and cached IDs
	listingIDs := make([]string, 0, len(properties))
	cachedPropertyIDs := make([]string, 0, len(properties))
	for _, p := range properties {
		listingIDs = append(listingIDs, p.ID)

		// Upsert property to cache - generate UUID for new records
		propertyUUID := uuid.New().String()

		capRate := parseCapRate(p.CapRate)
		capRatePg := pgtype.Float8{}
		if capRate != nil {
			capRatePg = pgtype.Float8{Float64: *capRate, Valid: true}
		}

		cachedID, err := h.store.Q().UpsertCachedProperty(ctx, queries.UpsertCachedPropertyParams{
			ID:            propertyUUID,
			ListingID:     p.ID,
			Provider:      "discovery",
			Address:       p.Address,
			City:          p.City,
			State:         p.State,
			ZipCode:       pgtype.Text{String: p.ZipCode, Valid: p.ZipCode != ""},
			Price:         int32(p.Price),
			Beds:          pgtype.Int4{Int32: int32(p.Beds), Valid: true},
			Baths:         pgtype.Float8{Float64: float64(p.Baths), Valid: true},
			Sqft:          pgtype.Int4{Int32: int32(p.Sqft), Valid: true},
			EstimatedRent: pgtype.Int4{Int32: int32(p.EstimatedRent), Valid: p.EstimatedRent > 0},
			CapRate:       capRatePg,
			ListingUrl:    pgtype.Text{String: p.ListingUrl, Valid: p.ListingUrl != ""},
			ImageUrl:      pgtype.Text{String: p.ImageUrl, Valid: p.ImageUrl != ""},
		})

		if err != nil {
			h.logger.Warn("failed to cache property", "error", err, "property_id", p.ID)
			continue
		}
		cachedPropertyIDs = append(cachedPropertyIDs, cachedID)
	}

	// Create session with all fields for parity with www_v1
	var investorProfileJSON []byte
	if profile != nil {
		investorProfileJSON, _ = json.Marshal(profile)
	}

	var portfolioSnapshotJSON []byte
	if len(portfolioSnapshot) > 0 {
		portfolioSnapshotJSON = portfolioSnapshot
	}

	sessionID := uuid.New().String()
	_, err := h.store.Q().CreateEvaluationChatSession(ctx, queries.CreateEvaluationChatSessionParams{
		ID:                 sessionID,
		UserID:             userID,
		PropertyIds:        listingIDs,
		CachedPropertyIds:  cachedPropertyIDs,
		InvestorProfile:    investorProfileJSON,
		PortfolioSnapshot:  portfolioSnapshotJSON,
		DiscoverySessionID: pgtype.Text{String: discoverySessionID, Valid: discoverySessionID != ""},
	})

	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return sessionID, nil
}

// nilIfEmpty returns nil if the string is empty, otherwise returns the string pointer
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// validateSessionOwnership checks if a session belongs to a user
func (h *Handler) validateSessionOwnership(ctx context.Context, sessionID, userID string) (bool, error) {
	return h.store.Q().ValidateEvaluationChatSessionOwnership(ctx, queries.ValidateEvaluationChatSessionOwnershipParams{
		ID:     sessionID,
		UserID: userID,
	})
}

// saveChatMessage saves a message to the database and returns the message ID
func (h *Handler) saveChatMessage(ctx context.Context, sessionID, role, content string, blocks []byte, tokenUsage *TokenUsage) (string, error) {
	var tokenUsageJSON []byte
	if tokenUsage != nil {
		tokenUsageJSON, _ = json.Marshal(tokenUsage)
	}

	messageID := uuid.New().String()
	_, err := h.store.Q().CreateEvaluationChatMessage(ctx, queries.CreateEvaluationChatMessageParams{
		ID:           messageID,
		SessionID:    sessionID,
		Role:         role,
		Content:      content,
		ParsedBlocks: blocks,
		TokenUsage:   tokenUsageJSON,
	})

	if err != nil {
		return "", fmt.Errorf("failed to save message: %w", err)
	}

	// Update session timestamp
	h.store.Q().UpdateEvaluationChatSessionTimestamp(ctx, sessionID)

	return messageID, nil
}

// getSessionHistory retrieves conversation history for a session
func (h *Handler) getSessionHistory(ctx context.Context, sessionID string) ([]ChatMessage, error) {
	dbMessages, err := h.store.Q().GetSessionHistory(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	messages := make([]ChatMessage, 0, len(dbMessages))
	for _, dbMsg := range dbMessages {
		msg := ChatMessage{
			ID:        dbMsg.ID,
			SessionID: dbMsg.SessionID,
			Role:      dbMsg.Role,
			Content:   dbMsg.Content,
			CreatedAt: dbMsg.CreatedAt.Time,
		}

		// Convert parsed_blocks
		if len(dbMsg.ParsedBlocks) > 0 {
			msg.ParsedBlocks = json.RawMessage(dbMsg.ParsedBlocks)
		}

		// Convert token_usage
		if len(dbMsg.TokenUsage) > 0 {
			var usage TokenUsage
			if json.Unmarshal(dbMsg.TokenUsage, &usage) == nil {
				msg.TokenUsage = &usage
			}
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

// generateSSEToken creates a JWT token for SSE stream authentication
// This is needed because EventSource doesn't support Authorization headers
func (h *Handler) generateSSEToken(userID, jobID, sessionID string) (string, error) {
	claims := jwt.MapClaims{
		"userId":    userID,
		"jobId":     jobID,
		"sessionId": sessionID,
		"exp":       time.Now().Add(30 * time.Minute).Unix(),
		"iat":       time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.cfg.JWT.Secret))
}

// getExpenseCalculator returns an expense calculator instance
// Uses a singleton pattern for efficiency
var expenseCalculator *expenses.Calculator

func (h *Handler) getExpenseCalculator() *expenses.Calculator {
	if expenseCalculator == nil {
		expenseCalculator = expenses.NewCalculator()
	}
	return expenseCalculator
}

// buildMarketExpenseContext creates market expense context for the AI prompt
// Provides state-level expense rate information for the properties being evaluated
// ADR-069: Now includes live economic data (FRED/Census/BLS)
func (h *Handler) buildMarketExpenseContext(properties []PropertyInput) string {
	if len(properties) == 0 {
		return ""
	}

	// Get unique cities/states from properties
	type location struct {
		city  string
		state string
	}
	locationsSeen := make(map[location]bool)
	statesSeen := make(map[string]bool)
	var states []string
	var locations []location
	for _, p := range properties {
		if p.State != "" && !statesSeen[p.State] {
			statesSeen[p.State] = true
			states = append(states, p.State)
		}
		loc := location{city: p.City, state: p.State}
		if p.City != "" && p.State != "" && !locationsSeen[loc] {
			locationsSeen[loc] = true
			locations = append(locations, loc)
		}
	}

	if len(states) == 0 {
		return ""
	}

	var context strings.Builder

	// ADR-069: Add economic conditions context first (if available)
	if h.econContext != nil && len(locations) > 0 {
		// Use the first location for economic context
		loc := locations[0]
		econCtx := h.econContext.BuildEconomicContext(
			context2.Background(),
			loc.city,
			loc.state,
			0, // No median home price for general context
		)
		context.WriteString(econCtx)
		context.WriteString("\n\n")
	}

	// Add expense characteristics
	calc := h.getExpenseCalculator()
	if calc != nil {
		context.WriteString("MARKET EXPENSE CHARACTERISTICS:\n")

		for _, state := range states {
			defaults := calc.GetMarketDefaults(state)
			context.WriteString(fmt.Sprintf("\n%s Market:\n", state))
			context.WriteString(fmt.Sprintf("  - Property Tax Rate: %.2f%% of home value\n", defaults.PropertyTaxRate))
			context.WriteString(fmt.Sprintf("  - Insurance Rate: %.2f%% of home value\n", defaults.InsuranceRate))
			if len(defaults.InsuranceRisk) > 0 {
				context.WriteString(fmt.Sprintf("  - Insurance Risk Factors: %s\n", strings.Join(defaults.InsuranceRisk, ", ")))
			}
			context.WriteString(fmt.Sprintf("  - Typical Maintenance: %.1f%% of home value\n", defaults.MaintenanceRate))
			context.WriteString(fmt.Sprintf("  - Typical Vacancy: %.1f%% of rent\n", defaults.VacancyRate))
			context.WriteString(fmt.Sprintf("  - Property Management: %.1f%% of rent\n", defaults.PropertyMgmtRate))
			if defaults.Notes != "" {
				context.WriteString(fmt.Sprintf("  - Note: %s\n", defaults.Notes))
			}
		}
	}

	return context.String()
}

// recordEvalChatUsage persists AI token usage for an evaluation chat request.
// Pricing: claude-sonnet-4-20250514 — input $3.00/MTok, output $15.00/MTok.
func (h *Handler) recordEvalChatUsage(userID, jobID string, inputTokens, outputTokens int) {
	if h.store == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context2.Background(), 10*time.Second)
	defer cancel()

	const inputPricePerToken = 0.000003  // $3.00 / 1M
	const outputPricePerToken = 0.000015 // $15.00 / 1M

	baseInputCost := float64(inputTokens) * inputPricePerToken
	outputCost := float64(outputTokens) * outputPricePerToken
	totalCost := baseInputCost + outputCost

	toNumeric := func(v float64) pgtype.Numeric {
		n := pgtype.Numeric{}
		_ = n.Scan(fmt.Sprintf("%.8f", v))
		return n
	}

	err := h.store.Q().CreateAIUsage(ctx, queries.CreateAIUsageParams{
		ID:             uuid.New().String(),
		UserID:         userID,
		SessionID:      jobID,
		Model:          "claude-sonnet-4-20250514",
		InputTokens:    int32(inputTokens),
		OutputTokens:   int32(outputTokens),
		TotalTokens:    int32(inputTokens + outputTokens),
		BaseInputCost:  toNumeric(baseInputCost),
		OutputCost:     toNumeric(outputCost),
		TotalCost:      toNumeric(totalCost),
		RequestType:    "stream",
		Feature:        "evaluation_chat",
		Location:       pgtype.Text{Valid: false},
		RequestID:      jobID,
		ProcessingTime: pgtype.Int4{Valid: false},
	})
	if err != nil {
		h.logger.Warn("failed to record eval chat AI usage", "error", err, "user_id", userID)
	}
}
