package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/estara-ai/www/internal/api/handlers/admin"
	"github.com/estara-ai/www/internal/api/handlers/ai"
	"github.com/estara-ai/www/internal/api/handlers/app"
	"github.com/estara-ai/www/internal/api/handlers/auth"
	"github.com/estara-ai/www/internal/api/handlers/billing"
	"github.com/estara-ai/www/internal/api/handlers/cron"
	"github.com/estara-ai/www/internal/api/handlers/discover"
	"github.com/estara-ai/www/internal/api/handlers/iap"
	"github.com/estara-ai/www/internal/api/handlers/location"
	"github.com/estara-ai/www/internal/api/handlers/market"
	"github.com/estara-ai/www/internal/api/handlers/portfolio"
	"github.com/estara-ai/www/internal/api/handlers/public"
	"github.com/estara-ai/www/internal/api/handlers/report"
	"github.com/estara-ai/www/internal/api/handlers/webhooks"
	"github.com/estara-ai/www/internal/api/handlers/website"
	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
)

// RouterConfig holds all dependencies needed for the router
type RouterConfig struct {
	Config   *config.Config
	DB       *postgres.DB
	Redis    *redisClient.Client
	Services *Services
}

// Handlers holds all handler instances
type Handlers struct {
	Auth          *auth.Handler
	App           *app.Handler
	Discover      *discover.Handler
	AI            *ai.Handler
	Portfolio     *portfolio.Handler
	Admin         *admin.Handler
	Cron          *cron.Handler
	Location      *location.Handler
	Market        *market.Handler
	Report        *report.Handler
	Billing       *billing.Handler
	IAP           *iap.Handler
	Website       *website.Handler
	Public        *public.Handler
	StripeWebhook *webhooks.StripeHandler
	AppleWebhook  *webhooks.AppleHandler
}

// Middleware holds all middleware instances
type Middleware struct {
	Auth        *middleware.AuthMiddleware
	RateLimiter middleware.Limiter
}

// NewRouter creates a new Chi router with all routes configured
func NewRouter(ctx context.Context, routerCfg RouterConfig) chi.Router {
	r := chi.NewRouter()

	cfg := routerCfg.Config
	db := routerCfg.DB
	redis := routerCfg.Redis
	svc := routerCfg.Services

	// Create middleware
	authMiddleware := middleware.NewAuthMiddleware(cfg)
	rateLimiter := middleware.NewLimiter(ctx, redis, 100, time.Minute) // 100 req/min default

	// Apply global middleware
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.NewCORSMiddleware(cfg))
	r.Use(middleware.SecurityHeaders(cfg))

	// Create handlers
	handlers := &Handlers{
		Auth:          auth.NewHandler(authMiddleware, db, redis, cfg),
		App:           app.NewHandler(db, cfg),
		Discover:      discover.NewHandler(db, redis, cfg, svc.PropertyFinder, svc.MarketData),
		AI:            ai.NewHandler(db, redis, cfg, svc.ChatAgent, svc.JobQueue),
		Portfolio:     portfolio.NewHandler(db, cfg),
		Admin:         admin.NewHandler(db, redis, cfg),
		Cron:          cron.NewHandler(db, redis, cfg),
		Location:      location.NewHandler(db, redis, cfg),
		Market:        market.NewHandler(cfg),
		Report:        report.NewHandler(db, cfg),
		Billing:       billing.NewHandler(db, cfg),
		IAP:           iap.NewHandler(ctx, db, cfg),
		Website:       website.NewHandler(db, cfg),
		Public:        public.NewHandler(db, cfg),
		StripeWebhook: webhooks.NewStripeHandler(db, cfg),
		AppleWebhook:  webhooks.NewAppleHandler(db, cfg),
	}

	// Inject services into market handler if available
	if svc.MarketData != nil {
		handlers.Market.SetAggregator(svc.MarketData)
	}
	if svc.FREDService != nil {
		handlers.Market.SetFREDService(svc.FREDService)
	}
	if svc.CensusService != nil {
		handlers.Market.SetCensusService(svc.CensusService)
	}
	if svc.BLSService != nil {
		handlers.Market.SetBLSService(svc.BLSService)
	}

	// Inject services into portfolio handler
	if svc.PropertyFinder != nil {
		handlers.Portfolio.SetPropertyFinder(svc.PropertyFinder)
	}
	handlers.Portfolio.SetQueries(queries.New(db.Main))

	// Health check (no auth required)
	r.Get("/health", handlers.Auth.Health)
	r.Get("/api/health", handlers.Auth.Health) // Alias for Railway health checks

	// Anti-scraping endpoints (no auth required)
	r.Get("/robots.txt", handleRobotsTxt)
	r.Get("/llms.txt", handleLLMsTxt)

	// Auth routes (no auth required for login)
	r.Route("/api/auth", func(r chi.Router) {
		// Standard login/refresh endpoints
		r.Post("/login", handlers.Auth.Login)
		r.Post("/refresh", handlers.Auth.RefreshToken)

		// Client-specific endpoints (www_v1 API contract compatibility)
		// These are used by the Estara Insight client app
		r.Post("/client-login", handlers.Auth.ClientLogin)
		r.Post("/client-refresh", handlers.Auth.ClientRefresh)

		// Password reset (no auth required)
		r.Post("/client-forgot-password", handlers.Auth.ClientForgotPassword)
		r.Post("/reset-password", handlers.Auth.ResetPassword)

		// Email verification (no auth required)
		r.Post("/send-verification-code", handlers.Auth.SendVerificationCode)
		r.Post("/verify-code", handlers.Auth.VerifyCode)

		// Protected auth routes
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Get("/me", handlers.Auth.Me) // ADR-066: Returns user, entitlements, and CSRF token
			r.Post("/logout", handlers.Auth.Logout)
			r.Post("/update-password", handlers.Auth.UpdatePassword)
		})
	})

	// App routes (protected)
	r.Route("/api/app", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Post("/entitlements", handlers.App.GetEntitlements)

		// Scenarios CRUD
		r.Route("/scenarios", func(r chi.Router) {
			r.Get("/", handlers.App.Scenarios.ListScenarios)
			r.Post("/", handlers.App.Scenarios.CreateScenario)
			r.Get("/{id}", handlers.App.Scenarios.GetScenario)
			r.Put("/{id}", handlers.App.Scenarios.UpdateScenario)
			r.Delete("/{id}", handlers.App.Scenarios.DeleteScenario)
		})
	})

	// V2 Discovery API
	r.Route("/api/v2/discover", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		// POST search endpoint - matches www_v1 API contract for client app
		r.Post("/search", handlers.Discover.Search)
		// GET properties endpoint - alternative query param based search
		r.Get("/properties", handlers.Discover.SearchProperties)
		// Streaming search endpoint - returns fully enriched properties via SSE
		r.Get("/search/stream", handlers.Discover.StreamingSearch)
		r.Get("/market-defaults", handlers.Discover.GetMarketDefaults)
		r.Post("/batch-evaluate", handlers.Discover.BatchEvaluate)

		// Async enrichment endpoints (for SSE-based property enrichment)
		r.Get("/enrich/{jobId}", handlers.Discover.GetEnrichmentStatus)
		r.Get("/enrich/{jobId}/stream", handlers.Discover.StreamEnrichmentUpdates)

		// Cache management
		r.Post("/cache/invalidate", handlers.Discover.InvalidateSearchCache)

		// Discovery Sessions
		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", handlers.Discover.ListDiscoverySessions)
			r.Post("/", handlers.Discover.CreateDiscoverySession)
			r.Get("/{id}", handlers.Discover.GetDiscoverySession)
			r.Delete("/{id}", handlers.Discover.ArchiveDiscoverySession)
			r.Post("/{id}/restore", handlers.Discover.RestoreDiscoverySession)
			r.Post("/{id}/link", handlers.Discover.LinkActivity)
			r.Post("/{id}/evaluations", handlers.Discover.SaveEvaluations)
		})
	})

	// V2 Quota API
	r.Route("/api/v2/quota", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)
		r.Get("/", handlers.Discover.GetQuota)
	})

	// V2 Records API (discover history)
	r.Route("/api/v2/records", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)
		r.Get("/", handlers.Discover.GetRecords)
		r.Get("/{id}/download", handlers.Discover.DownloadDecisionRecord)
	})

	// V2 Evaluation API
	r.Route("/api/v2/evaluate", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)
		r.Post("/batch", handlers.Discover.BatchEvaluate)
		r.Post("/batch/export", handlers.Discover.ExportBatchEvaluations)
	})

	// Location API - autocomplete is public, validate requires auth
	r.Route("/api/location", func(r chi.Router) {
		// Autocomplete is public (no auth required) - matches www_v1
		r.Get("/autocomplete", handlers.Location.Autocomplete)

		// Validate requires auth
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Use(rateLimiter.Limit)
			r.Get("/validate", handlers.Location.Validate)
		})
	})

	// Market Data API (protected)
	r.Route("/api/market-data", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		// FRED economic data (ADR-068 Phase 1)
		r.Get("/mortgage-rate", handlers.Market.GetMortgageRate)
		r.Get("/investment-rates", handlers.Market.GetInvestmentRates)
		r.Get("/economic-rates", handlers.Market.GetEconomicRates)

		// Census demographics (ADR-068 Phase 2)
		r.Get("/demographics", handlers.Market.GetDemographics) // ?city=Austin&state=TX

		// BLS labor market (ADR-068 Phase 3)
		r.Get("/labor", handlers.Market.GetLaborData)                 // National labor data
		r.Get("/labor/state/{state}", handlers.Market.GetStateLaborData) // State labor data

		// Aggregated market data
		r.Get("/", handlers.Market.GetMarketData) // GET /api/market-data?city=Austin&state=TX
	})

	// Market Trends API (protected)
	r.Route("/api/market-trends", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Get("/search", handlers.Market.SearchMetros)
		r.Get("/historical", handlers.Market.GetHistoricalTrends)
		r.Post("/synthesize", handlers.Market.SynthesizeTrends)
		r.Post("/export", handlers.Market.ExportTrendsPDF)
	})

	// AI Evaluation Chat
	r.Route("/api/ai/evaluate/chat", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Post("/", handlers.AI.QueueEvaluationChat)
		r.Get("/stream", handlers.AI.StreamEvaluationChat)
		r.Get("/sessions", handlers.AI.ListChatSessions)
		r.Get("/sessions/{sessionId}", handlers.AI.GetChatSession)
		r.Delete("/sessions/{sessionId}", handlers.AI.DeleteChatSession)
	})

	// Investment Planning
	r.Route("/api/ai/investment-planning", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Post("/queue", handlers.AI.QueueInvestmentPlan)
		r.Get("/stream", handlers.AI.StreamInvestmentPlan)
		r.Get("/status/{jobId}", handlers.AI.StreamInvestmentPlanStatus)
		r.Get("/history", handlers.AI.GetInvestmentPlanHistory)
		r.Post("/{jobId}/cancel", handlers.AI.CancelInvestmentPlan)
		r.Delete("/{jobId}", handlers.AI.DeleteInvestmentPlan)
		r.Get("/{jobId}", handlers.AI.GetInvestmentPlan)
	})

	// Market Analysis
	r.Route("/api/ai/analysis", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Post("/queue", handlers.AI.QueueAnalysis)
		r.Get("/stream", handlers.AI.StreamAnalysis)
		r.Get("/jobs", handlers.AI.ListAnalysisJobs)
		r.Post("/retry/{jobId}", handlers.AI.RetryAnalysis)
		r.Post("/cancel/{jobId}", handlers.AI.CancelAnalysis)
	})

	// Portfolio
	r.Route("/api/v2/portfolio", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Get("/", handlers.Portfolio.List)
		r.Post("/", handlers.Portfolio.Create)
		r.Get("/metrics", handlers.Portfolio.GetMetrics)
		r.Get("/recommendations", handlers.Portfolio.GetRecommendations)
		r.Get("/projections", handlers.Portfolio.GetProjections)
		r.Post("/lookup", handlers.Portfolio.Lookup)
		r.Get("/snapshots", handlers.Portfolio.GetSnapshots)
		r.Get("/{id}", handlers.Portfolio.Get)
		r.Put("/{id}", handlers.Portfolio.Update)
		r.Delete("/{id}", handlers.Portfolio.Delete)

		// Property-level endpoints
		r.Get("/{id}/adjustments", handlers.Portfolio.GetAdjustments)
		r.Post("/{id}/adjustments", handlers.Portfolio.CreateAdjustment)
		r.Get("/{id}/baseline-changes", handlers.Portfolio.GetBaselineChanges)
		r.Post("/{id}/baseline-changes", handlers.Portfolio.CreateBaselineChange)
	})

	// Investor Reports
	r.Route("/api/investor-report", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Get("/list", handlers.Report.List)
		r.Post("/generate", handlers.Report.Generate)
		r.Get("/status/{id}", handlers.Report.GetStatus)
		r.Get("/{id}", handlers.Report.Get)
		r.Get("/download/{id}", handlers.Report.Download)
	})

	// Report exports (market analysis, investment plans, projections)
	r.Route("/api/report", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Post("/generate", handlers.Report.GenerateMarketAnalysisPDF)
		r.Post("/investment-plan", handlers.Report.GenerateInvestmentPlanPDF)
		r.Post("/portfolio-projections", handlers.Report.GeneratePortfolioProjectionsPDF)
		r.Post("/portfolio-projections/csv", handlers.Report.GeneratePortfolioProjectionsCSV)
	})

	// Cache management
	r.Route("/api/cache", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Post("/invalidate", handlers.AI.InvalidateCache)
	})

	// Admin routes
	r.Route("/api/admin", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(authMiddleware.RequireAdmin)

		// User management
		r.Route("/users", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListUsers)
			r.Get("/{id}", handlers.Admin.GetUser)
			r.Put("/{id}", handlers.Admin.UpdateUser)
			r.Post("/{id}/impersonate", handlers.Admin.ImpersonateUser)
		})

		// Cache management
		r.Route("/cache", func(r chi.Router) {
			r.Get("/stats", handlers.Admin.CacheStats)
			r.Delete("/invalidate", handlers.Admin.InvalidateCache)
		})

		// AI Metrics & Usage
		r.Get("/ai-metrics", handlers.Admin.GetAIMetrics)
		r.Get("/ai-usage", handlers.Admin.GetAIUsage)
		r.Get("/ai-cache-status", handlers.Admin.GetAICacheStatus)

		// Investor Reports Management
		r.Route("/investor-reports", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListInvestorReports)
			r.Post("/{id}/retry", handlers.Admin.RetryInvestorReport)
		})

		// Audit Log
		r.Get("/audit-log", handlers.Admin.GetAuditLog)

		// Analytics
		r.Get("/analytics", handlers.Admin.GetAnalytics)

		// System Alerts
		r.Route("/system-alerts", func(r chi.Router) {
			r.Get("/", handlers.Admin.GetSystemAlerts)
			r.Post("/{id}/resolve", handlers.Admin.ResolveSystemAlert)
		})

		// Vendor management
		r.Route("/vendors", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListVendors)
			r.Get("/{id}/health", handlers.Admin.VendorHealth)
			r.Post("/{id}/toggle", handlers.Admin.ToggleVendor)
			r.Get("/costs", handlers.Admin.GetVendorCosts)
		})

		// Monitoring
		r.Get("/metrics", handlers.Admin.Metrics)
		r.Get("/health", handlers.Admin.HealthCheck)
	})

	// Cron trigger endpoints (called by external scheduler)
	r.Route("/api/cron", func(r chi.Router) {
		r.Use(middleware.CronAuth(cfg.Cron.Secret))

		r.Post("/market-alerts", handlers.Cron.ProcessMarketAlerts)
		r.Post("/cache-cleanup", handlers.Cron.CleanupCaches)
		r.Post("/vendor-costs", handlers.Cron.AggregateVendorCosts)
		r.Post("/renewal-reminders", handlers.Cron.SendRenewalReminders)
		r.Post("/subscription-sync", handlers.Cron.SyncSubscriptions)
		r.Post("/usage-reset", handlers.Cron.ResetMonthlyUsage)
		r.Post("/stale-jobs", handlers.Cron.CleanupStaleJobs)
		r.Post("/audit-archive", handlers.Cron.ArchiveAuditLogs)
		r.Post("/market-data-refresh", handlers.Cron.RefreshMarketData)
		r.Post("/property-cache-prune", handlers.Cron.PrunePropertyCache)
		r.Post("/expire-free-trials", handlers.Cron.ExpireFreeTrials)
		r.Post("/reports", handlers.Cron.ProcessScheduledReports)
		r.Post("/ai-estimate-refresh", handlers.Cron.RefreshAIEstimates)
		r.Post("/cleanup-guest-sessions", handlers.Cron.CleanupGuestSessions)
		r.Post("/discovery-cleanup", handlers.Cron.DiscoveryCleanup)
	})

	// Billing/Payment routes (protected)
	r.Route("/api", func(r chi.Router) {
		// Checkout endpoints
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Use(rateLimiter.Limit)

			r.Post("/create-checkout-session", handlers.Billing.CreateCheckout)
			r.Get("/checkout-session/{sessionId}", handlers.Billing.GetCheckoutSession)
			r.Get("/check-payment-status", handlers.Billing.CheckPaymentStatus)
		})

		// Subscription management
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Use(rateLimiter.Limit)

			r.Get("/subscription", handlers.Billing.GetSubscription)
			r.Post("/cancel-subscription", handlers.Billing.CancelSubscription)
			r.Post("/reactivate-subscription", handlers.Billing.ReactivateSubscription)
			r.Post("/renew-subscription", handlers.Billing.RenewSubscription)
			r.Post("/create-free-subscription", handlers.Billing.CreateFreeSubscription)
			r.Post("/upgrade-subscription", handlers.Billing.UpgradeSubscription)
		})

		// Billing portal and history
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Use(rateLimiter.Limit)

			r.Post("/billing/portal", handlers.Billing.CreatePortalSession)
			r.Get("/billing/invoices", handlers.Billing.GetInvoices)
			r.Get("/billing/receipts", handlers.Billing.GetReceipts)
		})
	})

	// Stripe webhook (no auth required - uses Stripe signature verification)
	r.Post("/api/webhooks/stripe", handlers.StripeWebhook.HandleWebhook)

	// Apple webhook (no auth required - uses Apple signature verification)
	r.Post("/api/webhooks/apple", handlers.AppleWebhook.HandleWebhook)

	// Mobile IAP routes
	r.Route("/api/mobile", func(r chi.Router) {
		// Public endpoint - subscription plans
		r.Get("/subscription-plans", handlers.IAP.GetSubscriptionPlans)

		// Protected IAP endpoints
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Use(rateLimiter.Limit)

			r.Post("/iap/validate-receipt/ios", handlers.IAP.ValidateIOSReceipt)
			r.Post("/iap/validate-receipt/android", handlers.IAP.ValidateAndroidReceipt)
			r.Post("/iap/sync-entitlements", handlers.IAP.SyncEntitlements)
		})
	})

	// Website APIs (public and protected)
	r.Route("/api/website", func(r chi.Router) {
		// Public endpoints (no auth required, but rate limited)
		r.Group(func(r chi.Router) {
			r.Use(rateLimiter.Limit)
			r.Get("/pricing", handlers.Website.GetPricingConfig)
			r.Post("/free-snapshot", handlers.Website.CreateFreeSnapshot)
			r.Get("/order-status", handlers.Website.GetOrderStatus)
			r.Post("/guest-session", handlers.Website.CreateGuestSession)
			r.Post("/checkout", handlers.Website.CreateCheckout) // Public for signup flow
		})

		// Protected website endpoints (auth + rate limited)
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Use(rateLimiter.Limit)

			r.Post("/generate-report", handlers.Website.GenerateReport)
			r.Get("/insight-access/status", handlers.Website.GetInsightAccessStatus)
			r.Get("/reports/{id}", handlers.Website.GetReport)
		})
	})

	// Public APIs (no auth required)
	r.Route("/api/public", func(r chi.Router) {
		r.Use(rateLimiter.Limit)

		r.Post("/contact", handlers.Public.SubmitContact)
		r.Post("/early-access", handlers.Public.SignupEarlyAccess)
		r.Get("/early-access", handlers.Public.GetEarlyAccessStatus)
	})

	return r
}

// handleRobotsTxt blocks all crawlers from indexing the API
func handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours

	robotsTxt := `# Estara AI API - No indexing allowed
User-agent: *
Disallow: /

# Block AI/LLM crawlers explicitly
User-agent: GPTBot
Disallow: /

User-agent: ChatGPT-User
Disallow: /

User-agent: CCBot
Disallow: /

User-agent: anthropic-ai
Disallow: /

User-agent: Claude-Web
Disallow: /

User-agent: Google-Extended
Disallow: /

User-agent: FacebookBot
Disallow: /

User-agent: Bytespider
Disallow: /

User-agent: Amazonbot
Disallow: /

User-agent: Applebot-Extended
Disallow: /
`
	w.Write([]byte(robotsTxt))
}

// handleLLMsTxt explicitly denies AI training on API content
func handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours

	llmsTxt := `# Estara AI API - AI Crawler Policy
# This API does not permit AI/LLM training or scraping

User-agent: *
Disallow: /

# This content is proprietary and not available for AI training.
# Unauthorized access attempts will be logged and blocked.
# For inquiries, contact: support@estara-ai.com
`
	w.Write([]byte(llmsTxt))
}
