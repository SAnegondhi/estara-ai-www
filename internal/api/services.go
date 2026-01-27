package api

import (
	"context"
	"log/slog"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/agents"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/cache"
	"github.com/estara-ai/www/internal/services/jobs/queue"
	"github.com/estara-ai/www/internal/services/market/aggregator"
	"github.com/estara-ai/www/internal/services/market/estimation"
	"github.com/estara-ai/www/internal/services/market/timeseries"
	"github.com/estara-ai/www/internal/services/property/finder"
	"github.com/estara-ai/www/internal/services/property/providers"
)

// Services holds all application service dependencies
type Services struct {
	PropertyFinder *finder.Orchestrator
	MarketData     *aggregator.Aggregator
	ChatAgent      *agents.EvaluationChatAgent
	JobQueue       *queue.Queue
	HybridCache    *cache.HybridCache
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
	var providerList []providers.Provider

	// Create BrightData provider if enabled
	if cfg.Config.Property.BrightDataEnabled && cfg.Config.Property.BrightDataAPIKey != "" {
		brightdata := providers.NewBrightDataProvider(providers.BrightDataConfig{
			APIKey:  cfg.Config.Property.BrightDataAPIKey,
			Enabled: true,
		})
		providerList = append(providerList, brightdata)
		logger.Info("brightdata provider enabled")
	}

	// Create HasData provider if enabled
	if cfg.Config.Property.HasDataEnabled && cfg.Config.Property.HasDataAPIKey != "" {
		hasdata := providers.NewHasDataProvider(providers.HasDataConfig{
			APIKey:  cfg.Config.Property.HasDataAPIKey,
			Enabled: true,
		})
		providerList = append(providerList, hasdata)
		logger.Info("hasdata provider enabled")
	}

	if len(providerList) > 0 {
		services.PropertyFinder = finder.NewOrchestrator(finder.OrchestratorConfig{
			Providers:     providerList,
			Cache:         services.HybridCache,
			PriorityOrder: cfg.Config.Property.Priority,
		})
		logger.Info("property finder initialized", "providers", len(providerList))
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

	// Initialize FRED client if API key is configured
	if cfg.Config.Market.FREDAPIKey != "" {
		fredClient = timeseries.NewFREDClient(cfg.Config.Market.FREDAPIKey)
		logger.Info("FRED client initialized")
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
		services.ChatAgent = agents.NewEvaluationChatAgent(
			services.Anthropic,
			services.HybridCache,
			nil, // compliance filter - will add later
		)
		logger.Info("evaluation chat agent initialized")
	}

	return services, nil
}

// Close cleans up service resources
func (s *Services) Close() {
	if s.JobQueue != nil {
		s.JobQueue.Close()
	}
}
