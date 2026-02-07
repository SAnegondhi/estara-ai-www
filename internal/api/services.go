package api

import (
	"context"
	"log/slog"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/agents"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/ai/compliance"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/investment/optimization"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/jobs/workers"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/estimation"
	"github.com/estara-ai/www/internal/services/market/fred"
	"github.com/estara-ai/www/internal/services/market/timeseries"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/property/providers"
)

// Services holds all application service dependencies
type Services struct {
	PropertyFinder *finder.Orchestrator
	MarketData     *aggregator.Aggregator
	FREDService    *fred.Service // Centralized FRED economic data service
	ChatAgent      *agents.EvaluationChatAgent
	JobQueue       *queue.Queue
	WorkerPool     *queue.WorkerPool
	HybridCache    *cache.HybridCache
	PropertyCache  *cache.PropertyCache // Size-based FIFO cache for property reads (ADR-061)
	Anthropic      *anthropic.Client
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

	// Create Anthropic client
	if cfg.Config.AI.AnthropicAPIKey != "" {
		services.Anthropic = anthropic.NewClient(anthropic.ClientConfig{
			APIKey: cfg.Config.AI.AnthropicAPIKey,
		})
		logger.Info("anthropic client initialized")
	}

	// Create job queue
	if cfg.Redis != nil {
		services.JobQueue = queue.NewQueue(cfg.Redis.Client, queue.QueueConfig{
			Capacity:        1000,
			ResultRetention: 24 * 60, // 24 hours in minutes
		})
		logger.Info("job queue initialized")
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
	var fredClient *timeseries.FREDClient
	var aiEstimator *estimation.AIEstimator

	// Initialize MetroReader if market DB is configured
	if cfg.DB != nil && cfg.DB.Market != nil {
		metroReader = timeseries.NewMetroReader(cfg.DB.Market)
		logger.Info("metro reader initialized")
	}

	// Initialize FRED service if API key is configured
	// Centralized service with three-tier caching: L0 (memory) -> L1 (Redis) -> L2 (PostgreSQL)
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

		// Also create legacy client for aggregator compatibility
		fredClient = timeseries.NewFREDClient(cfg.Config.Market.FREDAPIKey, cfg.Redis)
	}

	// Initialize AI estimator if Anthropic client is available
	if services.Anthropic != nil {
		aiEstimator = estimation.NewAIEstimator(services.Anthropic)
		logger.Info("AI estimator initialized")
	}

	// Create aggregator if we have at least one data source
	if metroReader != nil || fredClient != nil || aiEstimator != nil {
		services.MarketData = aggregator.NewAggregator(
			metroReader,
			fredClient,
			aiEstimator,
			services.HybridCache,
		)
		logger.Info("market data aggregator initialized")
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
		var optimizer *optimization.Service
		if services.Anthropic != nil {
			if cfg.DB != nil && cfg.DB.Main != nil {
				// With database access for AI scoring cache (ADR-064)
				optimizer = optimization.NewServiceWithDB(
					services.Anthropic,
					services.MarketData,
					services.HybridCache,
					cfg.DB.Main,
					cfg.Redis,
				)
				logger.Info("optimization service initialized with AI scoring cache")
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

		// Create investment planning worker and register with queue
		investmentWorker := workers.NewInvestmentPlanningWorker(workers.InvestmentPlanningWorkerConfig{
			Optimizer: optimizer,
			Finder:    services.PropertyFinder,
			Market:    services.MarketData,
			Cache:     services.HybridCache,
			Client:    services.Anthropic,
			Redis:     cfg.Redis,
		})
		services.JobQueue.RegisterHandler(queue.JobTypeInvestmentPlanning, investmentWorker.GetHandler())
		logger.Info("investment planning worker registered")

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
}
