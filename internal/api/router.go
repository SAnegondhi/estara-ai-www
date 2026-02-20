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
	"github.com/estara-ai/www/internal/api/handlers/earlyaccess"
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
	billingService "github.com/estara-ai/www/internal/services/billing"
	"github.com/estara-ai/www/internal/services/market/importer"
	"github.com/estara-ai/www/internal/services/pdf"
	"github.com/estara-ai/www/internal/services/whitelist"
	dbpkg "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/postgres"
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
	EarlyAccess   *earlyaccess.Handler
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

	// Create shared Store — single auditable database interface (ADR-083)
	store := dbpkg.NewStore(db)

	// Create handlers
	handlers := &Handlers{
		Auth:          auth.NewHandler(authMiddleware, store, redis, cfg),
		App:           app.NewHandler(store, cfg),
		Discover:      discover.NewHandler(store, redis, cfg, svc.PropertyFinder, svc.MarketData),
		AI:            ai.NewHandler(store, redis, cfg, svc.ChatAgent, svc.JobQueue, svc.EconomicsAggregator),
		Portfolio:     portfolio.NewHandler(store, cfg),
		Admin:         admin.NewHandler(store, redis, cfg, authMiddleware),
		Cron:          cron.NewHandler(store, redis, cfg),
		Location:      location.NewHandler(store, redis, cfg, svc.GeoLocation),
		Market:        market.NewHandler(store, cfg),
		Report:        report.NewHandlerWithEconomics(store, cfg, svc.EconomicsAggregator),
		Billing:       billing.NewHandler(store, cfg),
		IAP:           iap.NewHandler(ctx, store, cfg),
		Website:       website.NewHandler(store, cfg),
		Public:        public.NewHandler(store, cfg),
		EarlyAccess:   earlyaccess.NewHandler(store, redis, cfg, authMiddleware),
		StripeWebhook: webhooks.NewStripeHandler(store, cfg),
		AppleWebhook:  webhooks.NewAppleHandler(store, cfg),
	}

	// Wire whitelist service for beta access control
	if store.Pool() != nil {
		wlService := whitelist.NewService(store.Pool())
		handlers.Auth.SetWhitelist(wlService)
		handlers.Admin.SetWhitelist(wlService)
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
	if svc.EconomicsAggregator != nil {
		handlers.Market.SetEconomicsAggregator(svc.EconomicsAggregator)
	}
	if svc.TrendsService != nil {
		handlers.Market.SetTrendsService(svc.TrendsService)
	}
	if svc.HybridCache != nil {
		handlers.Market.SetHybridCache(svc.HybridCache)
	}
	// ADR-075: Wire market data importer into cron and admin handlers
	// ADR-087 Phase 9: Pass main queries and refresh service for version tracking
	if mp := store.MarketPool(); mp != nil {
		imp := importer.NewService(mp, store.Q(), svc.RefreshService)
		handlers.Cron.SetImporter(imp)
		handlers.Admin.SetImporter(imp)
	}

	// Inject services into portfolio handler
	if svc.PropertyFinder != nil {
		handlers.Portfolio.SetPropertyFinder(svc.PropertyFinder)
	}
	// Portfolio queries are now provided by the store (ADR-083)

	// Wire Stripe billing client into admin handler for subscription management
	if cfg.Stripe.SecretKey != "" {
		stripeClient := billingService.NewStripeClient(&cfg.Stripe)
		handlers.Admin.SetBilling(stripeClient)
	}

	// Wire Decision Memo service into discover handler (ADR-079)
	if svc.JobQueue != nil {
		handlers.Discover.SetMemoService(svc.JobQueue)
	}
	if svc.MemoService != nil {
		handlers.Discover.SetMemoPDFBuilder(pdf.NewDecisionMemoBuilder())
	}

	// Health check (no auth required)
	r.Get("/health", handlers.Auth.Health)
	r.Get("/api/health", handlers.Auth.Health) // Alias for Railway health checks

	// Anti-scraping endpoints (no auth required)
	r.Get("/robots.txt", handleRobotsTxt)
	r.Get("/llms.txt", handleLLMsTxt)

	// ADR-085: Early Access Program — public application endpoint
	r.Route("/api/early-access", func(r chi.Router) {
		r.Post("/apply", handlers.EarlyAccess.Apply)
	})

	// Auth routes (no auth required for login)
	r.Route("/api/auth", func(r chi.Router) {
		// Standard login/refresh/register endpoints
		r.Post("/register", handlers.Auth.Register)
		r.Post("/login", handlers.Auth.Login)
		r.Post("/refresh", handlers.Auth.RefreshToken)

		// Admin login (no auth required — validates admin role internally)
		r.Post("/admin-login", handlers.Admin.AdminLogin)

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

		// ADR-082: Social login (no auth required)
		r.Post("/oauth-login", handlers.Auth.OAuthLogin)

		// ADR-081: Passkey login endpoints (no auth required)
		r.Post("/passkey/login/begin", handlers.Auth.BeginPasskeyLogin)
		r.Post("/passkey/login/finish", handlers.Auth.FinishPasskeyLogin)

		// ADR-085: Early access password setup (no auth required — token-gated)
		r.Get("/verify-setup-token", handlers.EarlyAccess.VerifySetupToken)
		r.Post("/setup-password", handlers.EarlyAccess.SetupPassword)

		// Protected auth routes
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware.Authenticate)
			r.Get("/me", handlers.Auth.Me) // ADR-066: Returns user, entitlements, and CSRF token
			r.Post("/logout", handlers.Auth.Logout)
			r.Post("/update-password", handlers.Auth.UpdatePassword)
			// ADR-085: Early access user self-termination
			r.Post("/request-termination", handlers.EarlyAccess.RequestTermination)

			// ADR-081: Passkey management (authenticated)
			r.Post("/passkey/register/begin", handlers.Auth.BeginPasskeyRegistration)
			r.Post("/passkey/register/finish", handlers.Auth.FinishPasskeyRegistration)
			r.Get("/passkey", handlers.Auth.ListPasskeys)
			r.Put("/passkey/{id}", handlers.Auth.RenamePasskey)
			r.Delete("/passkey/{id}", handlers.Auth.DeletePasskey)
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

		// Decision Memo (ADR-079)
		r.Post("/decision-memo", handlers.Discover.QueueDecisionMemos)
		r.Get("/decision-memo/{jobId}/stream", handlers.Discover.StreamDecisionMemoProgress)
		r.Post("/decision-memo/export", handlers.Discover.ExportDecisionMemoPDF)
		r.Get("/decision-memo/history", handlers.Discover.GetMemoHistory)
		r.Get("/decision-memo/cached/{key}", handlers.Discover.GetCachedMemo)
		r.Delete("/decision-memo/cached/{key}", handlers.Discover.DeleteCachedMemo)

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

	// Location API - autocomplete and detect are public, validate requires auth
	r.Route("/api/location", func(r chi.Router) {
		// Autocomplete is public (no auth required) - matches www_v1
		r.Get("/autocomplete", handlers.Location.Autocomplete)

		// Detect user location from IP (public, for intelligent preloading)
		r.Get("/detect", handlers.Location.DetectLocation)

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
		r.Get("/labor", handlers.Market.GetLaborData)                    // National labor data
		r.Get("/labor/state/{state}", handlers.Market.GetStateLaborData) // State labor data

		// Unified economics (ADR-068 Phase 4)
		r.Get("/economics", handlers.Market.GetUnifiedEconomics) // ?city=Austin&state=TX&medianHomePrice=450000

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
		r.Get("/location-autocomplete", handlers.Market.SearchLocations) // ADR-073

		// Trends history & caching
		r.Post("/generate", handlers.Market.GenerateFullTrends)
		r.Get("/history", handlers.Market.GetTrendsHistory)
		r.Delete("/history/{id}", handlers.Market.DismissTrend)
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
		r.Use(middleware.TrackLocationActivity(store)) // ADR-087 Phase 5: Track user activity

		r.Post("/queue", handlers.AI.QueueAnalysis)
		r.Get("/stream", handlers.AI.StreamAnalysis)
		r.Get("/jobs", handlers.AI.ListAnalysisJobs)
		r.Get("/history", handlers.AI.GetAnalysisHistory)      // ADR-073
		r.Get("/context", handlers.AI.GetAnalysisContext)       // ADR-073
		r.Get("/report", handlers.AI.GetAnalysisReport)        // ADR-073
		r.Post("/preload", handlers.AI.PreloadReports)         // ADR-087 Phase 5
		r.Post("/retry/{jobId}", handlers.AI.RetryAnalysis)
		r.Post("/cancel/{jobId}", handlers.AI.CancelAnalysis)
		r.Delete("/{jobId}", handlers.AI.DismissAnalysisJob)    // ADR-073
	})

	// User activity tracking (ADR-087 Phase 5)
	r.Route("/api/user/activity", func(r chi.Router) {
		r.Use(authMiddleware.Authenticate)
		r.Use(rateLimiter.Limit)

		r.Get("/locations", handlers.AI.GetUserActivity)
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
		// Use header-only auth for admin routes: prevents a regular-user httpOnly cookie
		// (access_token) from shadowing the admin Bearer token when both are present in
		// the same browser session. Admin frontend uses localStorage + Bearer, not cookies.
		r.Use(authMiddleware.AuthenticateHeaderOnly)
		r.Use(authMiddleware.RequireAdmin)

		// User management
		r.Route("/users", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListUsers)
			r.Post("/", handlers.Admin.CreateUser)
			r.Get("/{id}", handlers.Admin.GetUser)
			r.Put("/{id}", handlers.Admin.UpdateUser)
			r.Post("/{id}/impersonate", handlers.Admin.ImpersonateUser)
			r.Post("/{id}/suspend", handlers.Admin.SuspendUser)
			r.Post("/{id}/unsuspend", handlers.Admin.UnsuspendUser)
			r.Post("/{id}/promote-super-admin", handlers.Admin.PromoteToSuperAdmin)
			r.Post("/{id}/demote-from-super-admin", handlers.Admin.DemoteFromSuperAdmin)
			r.Get("/{id}/activity", handlers.Admin.GetUserActivity)
			r.Get("/{id}/financial-profile", handlers.Admin.GetUserFinancialProfile)
			r.Post("/{id}/export", handlers.Admin.ExportUserData)
			r.Delete("/{id}/data", handlers.Admin.DeleteUserData)
			// ADR-085: Early access lifecycle management
			r.Post("/{id}/suspend-early-access", handlers.Admin.SuspendEarlyAccessUser)
			r.Post("/{id}/restore-early-access", handlers.Admin.RestoreEarlyAccessUser)
			r.Post("/{id}/terminate-early-access", handlers.Admin.TerminateEarlyAccessUser)
		})

		// ADR-085: Early Access application management
		r.Route("/early-access", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListEarlyAccessRequests)
			r.Get("/{id}", handlers.Admin.GetEarlyAccessRequest)
			r.Post("/{id}/approve", handlers.Admin.ApproveEarlyAccess)
			r.Post("/{id}/reject", handlers.Admin.RejectEarlyAccess)
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
			r.Post("/", handlers.Admin.CreateVendor)
			r.Get("/costs", handlers.Admin.GetVendorCosts)
			r.Get("/alerts", handlers.Admin.GetVendorAlerts)
			r.Get("/{id}/health", handlers.Admin.VendorHealth)
			r.Post("/{id}/toggle", handlers.Admin.ToggleVendor)
			r.Put("/{id}", handlers.Admin.UpdateVendor)
			r.Delete("/{id}", handlers.Admin.DeleteVendor)
			r.Get("/{id}/contract", handlers.Admin.GetVendorContract)
			r.Post("/{id}/contract", handlers.Admin.CreateOrUpdateContract)
		})

		// Subscription management
		r.Route("/subscriptions", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListSubscriptions)
			r.Get("/{id}", handlers.Admin.GetSubscriptionDetail)
			r.Post("/{id}/change-plan", handlers.Admin.ChangePlan)
			r.Post("/{id}/apply-credit", handlers.Admin.ApplyCredit)
			r.Post("/{id}/cancel", handlers.Admin.AdminCancelSubscription)
		})

		// Stripe console
		r.Route("/stripe", func(r chi.Router) {
			r.Get("/payments", handlers.Admin.ListPayments)
			r.Post("/refunds", handlers.Admin.ProcessRefund)
			r.Get("/invoices", handlers.Admin.ListInvoices)
			r.Get("/webhook-status", handlers.Admin.WebhookStatus)

			// Dispute management
			r.Get("/disputes", handlers.Admin.ListDisputes)
			r.Get("/disputes/metrics", handlers.Admin.GetDisputeMetrics)
			r.Get("/disputes/{id}", handlers.Admin.GetDisputeDetail)
			r.Post("/disputes/{id}/submit-evidence", handlers.Admin.SubmitEvidence)
			r.Post("/disputes/{id}/auto-evidence", handlers.Admin.AutoCompileEvidence)
		})

		// Revenue analytics
		r.Route("/revenue", func(r chi.Router) {
			r.Get("/summary", handlers.Admin.GetRevenueSummary)
			r.Get("/trend", handlers.Admin.GetRevenueTrend)
			r.Get("/by-tier", handlers.Admin.GetRevenueByTier)
			r.Get("/at-risk", handlers.Admin.GetAtRiskCustomers)
			r.Get("/leakage", handlers.Admin.GetRevenueLeakage)
			r.Get("/chargeback-rate", handlers.Admin.GetChargebackRate)
			r.Get("/segments", handlers.Admin.GetRevenueSegments)
		})

		// IAP monitoring
		r.Route("/iap", func(r chi.Router) {
			r.Get("/renewal-status", handlers.Admin.GetIAPRenewalStatus)
			r.Get("/webhook-events", handlers.Admin.GetIAPWebhookEvents)
			r.Post("/subscriptions/{id}/downgrade", handlers.Admin.DowngradeIAPSubscription)
		})

		// Whitelist management
		r.Route("/whitelist", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListWhitelist)
			r.Post("/", handlers.Admin.CreateWhitelistEntry)
			r.Get("/{id}", handlers.Admin.GetWhitelistEntry)
			r.Put("/{id}", handlers.Admin.UpdateWhitelistEntry)
			r.Post("/{id}/toggle", handlers.Admin.ToggleWhitelistEntry)
			r.Delete("/{id}", handlers.Admin.DeleteWhitelistEntry)
		})

		// Contact submissions management
		r.Route("/contacts", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListContacts)
			r.Get("/{id}", handlers.Admin.GetContact)
			r.Put("/{id}", handlers.Admin.UpdateContact)
			r.Delete("/{id}", handlers.Admin.DeleteContact)
		})

		// Waitlist (early access) management
		r.Route("/waitlist", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListWaitlist)
			r.Get("/{id}", handlers.Admin.GetWaitlistEntry)
			r.Post("/{id}/invite", handlers.Admin.InviteWaitlistEntry)
			r.Post("/{id}/status", handlers.Admin.UpdateWaitlistEntryStatus)
			r.Delete("/{id}", handlers.Admin.DeleteWaitlistEntry)
		})

		// Two-Factor Authentication
		r.Route("/2fa", func(r chi.Router) {
			r.Get("/status", handlers.Admin.Get2FAStatus)
			r.Post("/setup", handlers.Admin.Setup2FA)
			r.Post("/verify", handlers.Admin.Verify2FA)
			r.Post("/disable", handlers.Admin.Disable2FA)
		})

		// Session Management
		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListSessions)
			r.Delete("/{id}", handlers.Admin.RevokeSession)
		})

		// Cron Job Management
		r.Route("/cron-jobs", func(r chi.Router) {
			r.Get("/", handlers.Admin.ListCronJobs)
			r.Get("/{id}/runs", handlers.Admin.GetCronJobRuns)
			r.Post("/{id}/toggle", handlers.Admin.ToggleCronJob)
			r.Post("/{id}/trigger", handlers.Admin.TriggerCronJob)
		})

		// Market Data Management
		r.Route("/market-data", func(r chi.Router) {
			r.Get("/status", handlers.Admin.MarketDataStatus)
			r.Post("/trigger-import", handlers.Admin.TriggerMarketDataImport)
		})

		// Compliance
		r.Route("/compliance", func(r chi.Router) {
			r.Get("/consents", handlers.Admin.GetConsents)
			r.Get("/terms-log", handlers.Admin.GetTermsLog)
			r.Get("/tax-summary", handlers.Admin.GetTaxSummary)
		})

		// Accounting
		r.Route("/accounting", func(r chi.Router) {
			r.Get("/revenue-journal", handlers.Admin.GetRevenueJournal)
			r.Get("/deferred-revenue", handlers.Admin.GetDeferredRevenue)
			r.Get("/vendor-expenses", handlers.Admin.GetVendorExpenses)
			r.Get("/profitability", handlers.Admin.GetProfitability)
			r.Post("/export", handlers.Admin.ExportAccounting)
		})

		// Monitoring
		r.Get("/metrics", handlers.Admin.Metrics)
		r.Get("/health", handlers.Admin.HealthCheck)
	})

	// Cron trigger endpoints (called by external scheduler)
	r.Route("/api/cron", func(r chi.Router) {
		r.Use(middleware.CronAuth(cfg.Cron.Secret))

		c := handlers.Cron
		r.Post("/market-alerts", c.Tracked("market-alerts", c.ProcessMarketAlerts))
		r.Post("/cache-cleanup", c.Tracked("cache-cleanup", c.CleanupCaches))
		r.Post("/vendor-costs", c.Tracked("vendor-costs", c.AggregateVendorCosts))
		r.Post("/renewal-reminders", c.Tracked("renewal-reminders", c.SendRenewalReminders))
		r.Post("/subscription-sync", c.Tracked("subscription-sync", c.SyncSubscriptions))
		r.Post("/usage-reset", c.Tracked("usage-reset", c.ResetMonthlyUsage))
		r.Post("/stale-jobs", c.Tracked("stale-jobs", c.CleanupStaleJobs))
		r.Post("/audit-archive", c.Tracked("audit-archive", c.ArchiveAuditLogs))
		r.Post("/market-data-refresh", c.Tracked("market-data-refresh", c.RefreshMarketData))
		r.Post("/property-cache-prune", c.Tracked("property-cache-prune", c.PrunePropertyCache))
		r.Post("/expire-free-trials", c.Tracked("expire-free-trials", c.ExpireFreeTrials))
		r.Post("/reports", c.Tracked("reports", c.ProcessScheduledReports))
		r.Post("/ai-estimate-refresh", c.Tracked("ai-estimate-refresh", c.RefreshAIEstimates))
		r.Post("/cleanup-guest-sessions", c.Tracked("cleanup-guest-sessions", c.CleanupGuestSessions))
		r.Post("/discovery-cleanup", c.Tracked("discovery-cleanup", c.DiscoveryCleanup))
		r.Post("/expire-iap-subscriptions", c.Tracked("expire-iap-subscriptions", c.ExpireIAPSubscriptions))
		r.Post("/admin-audit-cleanup", c.Tracked("admin-audit-cleanup", c.CleanupAdminAuditLogs)) // ADR-086

		// Market data import endpoints (ADR-075)
		r.Route("/market-data", func(r chi.Router) {
			r.Post("/zillow-zhvi", c.Tracked("zillow-zhvi", c.ImportZillowZHVI))
			r.Post("/zillow-zori", c.Tracked("zillow-zori", c.ImportZillowZORI))
			r.Post("/zillow-forecasts", c.Tracked("zillow-forecasts", c.ImportZillowForecasts))
			r.Post("/zillow-metrics", c.Tracked("zillow-metrics", c.ImportZillowMetrics))
			r.Post("/redfin", c.Tracked("redfin", c.ImportRedfinData))
			r.Post("/compute-national", c.Tracked("compute-national", c.ComputeNational))
			r.Post("/full-refresh", c.Tracked("full-refresh", c.FullRefreshMarketData))
			r.Get("/status", c.MarketDataStatus) // GET endpoint, no tracking needed
		})
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
		r.Get("/market-pulse", handlers.Public.GetMarketPulse)
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
