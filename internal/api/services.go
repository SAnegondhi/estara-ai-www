package api

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/estara-ai/www/internal/config"
	dbstore "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/agents"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/ai/compliance"
	aicontext "github.com/estara-ai/www/internal/services/ai/context"
	"github.com/estara-ai/www/internal/services/ai/prompts"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/geolocation"
	"github.com/estara-ai/www/internal/services/geospatial"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/internal/services/investment/projection"
	"github.com/estara-ai/www/internal/services/investment/verdict"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/jobs/workers"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/estimation"
	"github.com/estara-ai/www/internal/services/market/unified"
	"github.com/estara-ai/www/internal/services/memo"
	"github.com/estara-ai/www/internal/services/market/bls"
	"github.com/estara-ai/www/internal/services/market/census"
	"github.com/estara-ai/www/internal/services/market/economics"
	"github.com/estara-ai/www/internal/services/market/fred"
	"github.com/estara-ai/www/internal/services/market/refresh"
	"github.com/estara-ai/www/internal/services/market/timeseries"
	"github.com/estara-ai/www/internal/services/market/trends"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/property/providers"
)

// Services holds all application service dependencies
type Services struct {
	PropertyFinder       *finder.Orchestrator
	MarketData           *aggregator.Aggregator
	FREDService          *fred.Service        // Centralized FRED economic data service (ADR-068 Phase 1)
	CensusService        *census.Service      // Census demographics service (ADR-068 Phase 2)
	BLSService           *bls.Service         // BLS labor market service (ADR-068 Phase 3)
	EconomicsAggregator  *economics.Aggregator // Unified economic data aggregator (ADR-068 Phase 4)
	TrendsService        *trends.Service        // Market trends service (ADR-073)
	RefreshService       *refresh.Service       // Event-driven refresh service (ADR-087 Phase 8-9)
	GeoLocation          *geolocation.MaxMindService // IP geolocation service (ADR-087 Phase 5)
	ChatAgent            *agents.EvaluationChatAgent
	JobQueue             *queue.Queue
	WorkerPool           *queue.WorkerPool
	HybridCache          *cache.HybridCache
	PropertyCache        *cache.PropertyCache // Size-based FIFO cache for property reads (ADR-061)
	Anthropic            *anthropic.Client
	MemoService          *memo.Service         // Decision Memo generation (ADR-079)
	FrontierOptimizer    *optimization.FrontierOptimizer // ADR-088 Phase 9: Interactive workspace
	InvestmentOptimizer  *optimization.Service           // ADR-088 Phase 12: Discovery+scoring pipeline for /run
	MetroReader          *timeseries.MetroReader         // City snapshot + HUD FMR data
	MarketEstimator      *estimation.AIEstimator         // AI fallback for missing price/rent data
	UnifiedMarket        *unified.Service               // Unified market data service (ADR-076): aggregator + Haiku fallback
}

// ServiceConfig holds configuration for creating services
type ServiceConfig struct {
	DB     *postgres.DB
	Redis  *redisClient.Client
	Config *config.Config
}

// NewServices creates all application services
func NewServices(ctx context.Context, cfg ServiceConfig) (*Services, error) {
	logger := slog.Default().With("component", "services")
	services := &Services{}

	// Create hybrid cache (Redis L1 + PostgreSQL L2)
	if cfg.Redis != nil && cfg.DB != nil && cfg.DB.Main != nil {
		services.HybridCache = cache.NewHybridCache(cfg.Redis, cfg.DB.Main)
		logger.Info("hybrid cache initialized")

		// Create property cache (size-based FIFO for property reads, ADR-061)
		services.PropertyCache = cache.NewPropertyCache(
			cfg.Redis,
			queries.New(cfg.DB.Main),
			logger,
			cache.PropertyCacheConfig{
				L1MaxSize:  50000,  // Redis max entries
				L2MaxSize:  500000, // Postgres max entries
				EvictBatch: 1000,   // Entries to evict when full
			},
		)
		logger.Info("property cache initialized (size-based FIFO)")
	}

	// Build API error alerter for all Anthropic clients
	var apiErrorAlert func(anthropic.APIErrorInfo)
	if cfg.DB != nil && cfg.DB.Main != nil {
		alertQ := queries.New(cfg.DB.Main)
		apiErrorAlert = func(info anthropic.APIErrorInfo) {
			var severity, title, alertKey string
			switch {
			case info.IsBillingError():
				severity, title, alertKey = "critical", "Anthropic API: Credit Balance Exhausted", "anthropic_billing_error"
			case info.IsAuthError():
				severity, title, alertKey = "critical", "Anthropic API: Authentication Failed", "anthropic_auth_error"
			case info.IsRateLimitError():
				severity, title, alertKey = "warning", "Anthropic API: Rate Limited", "anthropic_rate_limit"
			default:
				return
			}
			_, err := alertQ.UpsertSystemAlert(context.Background(), queries.UpsertSystemAlertParams{
				ID:             alertKey,
				Type:           "api_error",
				Severity:       severity,
				Title:          title,
				Description:    info.Message,
				AlertKey:       alertKey,
				Metadata:       "{}",
				ActionRequired: info.IsBillingError() || info.IsAuthError(),
			})
			if err != nil {
				logger.Error("failed to create system alert", "error", err, "alertKey", alertKey)
			} else {
				logger.Warn("system alert created", "alertKey", alertKey, "severity", severity)
			}
		}
	}

	// Create Anthropic client with system alert on critical API errors
	if cfg.Config.AI.AnthropicAPIKey != "" {
		services.Anthropic = anthropic.NewClient(anthropic.ClientConfig{
			APIKey:     cfg.Config.AI.AnthropicAPIKey,
			OnAPIError: apiErrorAlert,
		})
		logger.Info("anthropic client initialized")
	}

	// Initialize MaxMind GeoIP service for IP-based location detection
	// Looks for GeoLite2-City.mmdb in data/geolite2/ directory
	geoDBPath := filepath.Join("data", "geolite2", "GeoLite2-City.mmdb")
	if _, err := os.Stat(geoDBPath); err == nil {
		geoService, err := geolocation.NewMaxMindService(geoDBPath)
		if err != nil {
			logger.Warn("failed to initialize geolocation service", "error", err, "path", geoDBPath)
		} else {
			services.GeoLocation = geoService
			logger.Info("geolocation service initialized", "db_path", geoDBPath)
		}
	} else {
		logger.Info("geolocation database not found, IP detection disabled",
			"path", geoDBPath,
			"hint", "download GeoLite2-City.mmdb from MaxMind to enable IP geolocation",
		)
	}

	// Create job queue (in-memory; Redis is optional backup only)
	if cfg.Redis != nil {
		services.JobQueue = queue.NewQueue(cfg.Redis.Client, queue.QueueConfig{
			Capacity:        1000,
			ResultRetention: 24 * 60, // 24 hours in minutes
			UseRedisBackup:  true,
		})
		logger.Info("job queue initialized", "redis_backup", true)
	} else {
		services.JobQueue = queue.NewQueue(nil, queue.QueueConfig{
			Capacity:        1000,
			ResultRetention: 24 * 60, // 24 hours in minutes
		})
		logger.Info("job queue initialized", "redis_backup", false)
	}

	// Create property finder orchestrator
	// Provider priority order per www_v1 .env.local:
	// PROPERTY_FINDER_PRIORITY=hasdata,brightdata,claude,public
	var providerList []providers.Provider

	// Create HasData provider if enabled (Priority 1 - PRIMARY per www_v1)
	if cfg.Config.Property.HasDataEnabled && cfg.Config.Property.HasDataAPIKey != "" {
		hasdata := providers.NewHasDataProvider(providers.HasDataConfig{
			APIKey:   cfg.Config.Property.HasDataAPIKey,
			Enabled:  true,
			Priority: 1, // PRIMARY - per www_v1 config
		})
		providerList = append(providerList, hasdata)
		logger.Info("hasdata provider enabled", "priority", 1)
	}

	// Create BrightData provider if enabled (Priority 2)
	if cfg.Config.Property.BrightDataEnabled && cfg.Config.Property.BrightDataAPIKey != "" {
		brightdata := providers.NewBrightDataProvider(providers.BrightDataConfig{
			APIKey:   cfg.Config.Property.BrightDataAPIKey,
			Enabled:  true,
			Priority: 2,
		})
		providerList = append(providerList, brightdata)
		logger.Info("brightdata provider enabled", "priority", 2)
	}

	// Create Claude Web Search provider if enabled (Priority 3)
	if cfg.Config.Property.ClaudeEnabled && services.Anthropic != nil {
		claude := providers.NewClaudeWebSearchProvider(providers.ClaudeWebSearchConfig{
			Client:   services.Anthropic,
			Enabled:  true,
			Priority: 3,
		})
		providerList = append(providerList, claude)
		logger.Info("claude web search provider enabled", "priority", 3)
	}

	if len(providerList) > 0 {
		services.PropertyFinder = finder.NewOrchestrator(finder.OrchestratorConfig{
			Providers:             providerList,
			Cache:                 services.HybridCache,
			PropertyCache:         services.PropertyCache, // Size-based FIFO cache (ADR-061)
			PriorityOrder:         cfg.Config.Property.Priority,
			EnrichmentConcurrency: cfg.Config.Property.EnrichmentConcurrency,
		})
		logger.Info("property finder initialized",
			"providers", len(providerList),
			"enrichmentConcurrency", cfg.Config.Property.EnrichmentConcurrency,
		)
	}

	// Create market data aggregator
	var metroReader *timeseries.MetroReader

	// Initialize MetroReader if market DB is configured
	if cfg.DB != nil && cfg.DB.Market != nil {
		metroReader = timeseries.NewMetroReader(cfg.DB.Market)
		services.MetroReader = metroReader
		logger.Info("metro reader initialized")
	}

	// Initialize FRED service if API key is configured
	// Centralized service with three-tier caching: L0 (memory) -> L1 (Redis) -> L2 (PostgreSQL)
	// ADR-076: Single FRED path — all consumers use fred.Service (no legacy FREDClient)
	if cfg.Config.Market.FREDAPIKey != "" {
		// Get queries for L2 PostgreSQL cache
		var q *queries.Queries
		if cfg.DB != nil && cfg.DB.Main != nil {
			q = queries.New(cfg.DB.Main)
		}

		services.FREDService = fred.NewService(cfg.Config.Market.FREDAPIKey, cfg.Redis, q)
		// Start background refresh for proactive data updates
		services.FREDService.StartBackgroundRefresh(ctx)
		logger.Info("FRED service initialized with background refresh",
			"l1Cache", cfg.Redis != nil,
			"l2Cache", q != nil,
		)
	}

	// Initialize Census service if API key is configured (ADR-068 Phase 2)
	if cfg.Config.Market.CensusAPIKey != "" {
		var q *queries.Queries
		if cfg.DB != nil && cfg.DB.Main != nil {
			q = queries.New(cfg.DB.Main)
		}

		services.CensusService = census.NewService(cfg.Config.Market.CensusAPIKey, cfg.Redis, q)
		logger.Info("Census service initialized",
			"l1Cache", cfg.Redis != nil,
			"l2Cache", q != nil,
		)
	}

	// Initialize BLS service if API key is configured (ADR-068 Phase 3)
	if cfg.Config.Market.BLSAPIKey != "" {
		var q *queries.Queries
		if cfg.DB != nil && cfg.DB.Main != nil {
			q = queries.New(cfg.DB.Main)
		}

		services.BLSService = bls.NewService(cfg.Config.Market.BLSAPIKey, cfg.Redis, q)
		logger.Info("BLS service initialized",
			"l1Cache", cfg.Redis != nil,
			"l2Cache", q != nil,
		)
	}

	// Initialize Economics Aggregator (ADR-068 Phase 4)
	// Combines FRED, Census, and BLS data into unified MarketEconomics structure
	services.EconomicsAggregator = economics.NewAggregator(
		services.FREDService,
		services.CensusService,
		services.BLSService,
	)
	if services.EconomicsAggregator.IsConfigured() {
		logger.Info("economics aggregator initialized",
			"sources", services.EconomicsAggregator.AvailableSources(),
		)
	}

	// Create aggregator if we have at least one data source
	// ADR-076: Uses fred.Service directly (no legacy FREDClient, no AI estimator)
	if metroReader != nil || services.FREDService != nil {
		services.MarketData = aggregator.NewAggregator(
			metroReader,
			services.FREDService,
			services.HybridCache,
		)
		logger.Info("market data aggregator initialized")
	}

	// Create market trends service (ADR-073)
	// ADR-076: Uses fred.Service directly (no legacy FREDClient)
	if metroReader != nil || services.FREDService != nil {
		services.TrendsService = trends.NewService(trends.ServiceConfig{
			Metro: metroReader,
			FRED:  services.FREDService,
			AI:    services.Anthropic,
			Cache: services.HybridCache,
		})
		logger.Info("market trends service initialized")
	}

	// Create AI market estimator — used as fallback when structured sources have no price/rent data
	if services.Anthropic != nil {
		services.MarketEstimator = estimation.NewAIEstimator(services.Anthropic)
		logger.Info("AI market estimator initialized")
	}

	// Create unified market data service (ADR-076 extension).
	// Aggregator first (Zillow/FRED/DB, 3-tier cached 7d TTL), then dedicated Haiku fallback
	// for cities not covered by structured sources. All features use this as the single entry
	// point for location market data — returns a ForPrompt() block ready for any AI prompt.
	services.UnifiedMarket = unified.New(services.MarketData, cfg.Config.AI.AnthropicAPIKey)
	if services.UnifiedMarket.IsConfigured() {
		logger.Info("unified market service initialized")
	} else {
		logger.Warn("unified market service: no data sources configured")
	}

	// Create refresh service for event-driven refresh (ADR-087 Phase 8-9)
	if cfg.DB != nil && cfg.DB.Main != nil && services.MarketData != nil && services.EconomicsAggregator != nil && metroReader != nil {
		mainQueries := queries.New(cfg.DB.Main)
		services.RefreshService = refresh.NewService(refresh.Config{
			Queries:         mainQueries,
			Market:          services.MarketData,
			Economics:       services.EconomicsAggregator,
			Metro:           metroReader,
			ProactiveTopN:   100, // Refresh top 100 reports after data update
			RateLimit:       10,  // Max 10 concurrent refreshes
			EnableProactive: true,
			EnableReactive:  true,
		})
		logger.Info("refresh service initialized", "proactive_top_n", 100)
	}

	// Create evaluation chat agent
	if services.Anthropic != nil {
		complianceFilter := compliance.NewFilter(compliance.FilterConfig{StrictMode: false})
		services.ChatAgent = agents.NewEvaluationChatAgent(
			services.Anthropic,
			services.HybridCache,
			complianceFilter,
		)
		logger.Info("evaluation chat agent initialized")
	}

	// Create and start worker pool for job processing
	if services.JobQueue != nil {
		// Create optimization service for investment planning
		// ADR-064: Use NewServiceWithDB to enable AI scoring cache
		// ADR-069: Use NewServiceWithEconomics for live economic data
		var optimizer *optimization.Service
		if services.Anthropic != nil {
			if cfg.DB != nil && cfg.DB.Main != nil {
				// With database access for AI scoring cache (ADR-064) and economics (ADR-069)
				optimizer = optimization.NewServiceWithEconomics(
					services.Anthropic,
					services.MarketData,
					services.HybridCache,
					cfg.DB.Main,
					cfg.Redis,
					services.EconomicsAggregator,
					metroReader,
				)
				logger.Info("optimization service initialized with AI scoring cache and economics")
			} else {
				// Fallback: without database (no caching)
				optimizer = optimization.NewService(
					services.Anthropic,
					services.MarketData,
					services.HybridCache,
				)
				logger.Warn("optimization service initialized without AI scoring cache (no database)")
			}
		}

		// Store optimizer on Services for ADR-088 Phase 12 /run endpoint
		services.InvestmentOptimizer = optimizer

		// Create market analysis worker and register with queue (ADR-073)
		// ADR-074: Wire V2 data-driven analysis pipeline
		if services.Anthropic != nil {
			orchCfg := agents.OrchestratorConfig{}

			// Build V2 dependencies if all required services are available
			if metroReader != nil && services.EconomicsAggregator != nil {
				// DataPayloadBuilder assembles DATA_PAYLOAD from existing services
				// ADR-111 Phase 2: BLS service passed for MSA-level unemployment lookup
				orchCfg.PayloadBuilder = prompts.NewDataPayloadBuilder(
					services.MarketData,
					services.EconomicsAggregator,
					metroReader,
					services.BLSService,
				)

				// MarketContextService provides Stage 1 context enrichment (Sonnet + web search)
				var q *queries.Queries
				if cfg.DB != nil && cfg.DB.Main != nil {
					q = queries.New(cfg.DB.Main)
				}
				orchCfg.ContextService = aicontext.NewMarketContextService(
					cfg.Config.AI.AnthropicAPIKey,
					q,
					cfg.Redis,
					apiErrorAlert,
				)
				logger.Info("V2 analysis pipeline configured (ADR-074)",
					"payload_builder", true,
					"context_service", orchCfg.ContextService.IsConfigured(),
				)
			}

			orchestrator := agents.NewDualAgentOrchestrator(
				services.Anthropic,
				services.HybridCache,
				services.MarketData,
				orchCfg,
			)
			// Determine main DB pool for background trend storage
			var mainDBPool *postgres.Pool
			if cfg.DB != nil && cfg.DB.Main != nil {
				mainDBPool = cfg.DB.Main
			}

			// ADR-087: Create Store for report tracking
			var workerStore *dbstore.Store
			if cfg.DB != nil {
				workerStore = dbstore.NewStore(cfg.DB)
			}

			analysisWorker := workers.NewMarketAnalysisWorker(workers.MarketAnalysisWorkerConfig{
				Orchestrator: orchestrator,
				Market:       services.MarketData,
				Cache:        services.HybridCache,
				Trends:       services.TrendsService,
				MainDB:       mainDBPool,
				Store:        workerStore, // ADR-087: For report tracking
			})
			services.JobQueue.RegisterHandler(queue.JobTypeMarketAnalysis, analysisWorker.GetHandler())
			logger.Info("market analysis worker registered",
				"v2_enabled", orchestrator.HasV2(),
			)
		}

		// Create FrontierOptimizer for ADR-088 Phase 9 interactive workspace endpoints
		foLogger := slog.Default().With("component", "frontier_optimizer")
		var foCorrelationAnalyzer *optimization.CorrelationAnalyzer
		if metroReader != nil {
			foCorrelationAnalyzer = optimization.NewCorrelationAnalyzer(metroReader)
		}
		services.FrontierOptimizer = optimization.NewFrontierOptimizer(
			foLogger,
			optimization.NewMarkowitzCalculator(),
			projection.NewReinvestmentModeler(foLogger, metroReader),
			projection.NewMonteCarloSimulator(foLogger),
			projection.NewScenarioGenerator(foLogger),
			verdict.NewGenerator(foLogger),
			foCorrelationAnalyzer,
			services.FREDService,
		)
		logger.Info("frontier optimizer initialized (ADR-088 Phase 9)")

		// Create investment planning worker and register with queue
		// Create Store for saving completed scenarios
		var investmentStore *dbstore.Store
		if cfg.DB != nil {
			investmentStore = dbstore.NewStore(cfg.DB)
		}

		investmentWorker := workers.NewInvestmentPlanningWorker(workers.InvestmentPlanningWorkerConfig{
			Optimizer: optimizer,
			Finder:    services.PropertyFinder,
			Market:    services.MarketData,
			Cache:     services.HybridCache,
			Store:     investmentStore, // For saving completed scenarios to database
			Client:    services.Anthropic,
			Redis:     cfg.Redis,
		})
		services.JobQueue.RegisterHandler(queue.JobTypeInvestmentPlanning, investmentWorker.GetHandler())
		logger.Info("investment planning worker registered")

		// Create decision memo service and worker (ADR-079)
		if services.Anthropic != nil {
			var memoQueries *queries.Queries
			if cfg.DB != nil && cfg.DB.Main != nil {
				memoQueries = queries.New(cfg.DB.Main)
			}
			services.MemoService = memo.NewService(memo.ServiceConfig{
				AIClient:    services.Anthropic,
				Aggregator:  services.MarketData,
				MetroReader: metroReader,
				FREDService: services.FREDService,
				GeoSpatial:  geospatial.NewStub(),
				DB:          memoQueries,
			})

			memoWorker := workers.NewDecisionMemoWorker(workers.DecisionMemoWorkerConfig{
				MemoService: services.MemoService,
				DB:          memoQueries,
			})
			services.JobQueue.RegisterHandler(queue.JobTypeDecisionMemo, memoWorker.GetHandler())
			logger.Info("decision memo worker registered")
		}
		services.JobQueue.RegisterHandler(queue.JobTypeInvestmentPlanning, investmentWorker.GetHandler())
		logger.Info("investment planning worker registered")

		// Note: evaluation_chat jobs are NOT registered with the worker pool
		// They are processed via HTTP SSE streaming in StreamEvaluationChat handler
		// Worker pool will skip jobs without handlers (keeps them in processing state)

		// Create and start worker pool
		services.WorkerPool = queue.NewWorkerPool(services.JobQueue, queue.WorkerPoolConfig{
			Workers:      4, // 4 concurrent workers
			PollInterval: 0, // Use default (100ms)
			JobTimeout:   0, // Use default (10 minutes)
		})
		services.WorkerPool.Start()
		logger.Info("worker pool started", "workers", 4)
	}

	return services, nil
}

// Close cleans up service resources
func (s *Services) Close() {
	// Stop worker pool first (gracefully waits for in-progress jobs)
	if s.WorkerPool != nil {
		s.WorkerPool.Stop()
	}
	if s.JobQueue != nil {
		s.JobQueue.Close()
	}
	// Close geolocation service
	if s.GeoLocation != nil {
		if err := s.GeoLocation.Close(); err != nil {
			slog.Warn("failed to close geolocation service", "error", err)
		}
	}
}
