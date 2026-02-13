// Package context provides the Market Context Service for Stage 1 enrichment (ADR-074).
// It generates tax/regulatory/insurance/fiscal context via Sonnet + web search,
// cached 90 days per market in a 3-tier cache (memory → Redis → system_cache).
package context

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/estara-ai/www/internal/db/queries"
	redisClient "github.com/estara-ai/www/internal/db/redis"
	"github.com/estara-ai/www/internal/services/ai/anthropic"
	"github.com/estara-ai/www/internal/services/ai/prompts"
)

const (
	cacheTTL        = 90 * 24 * time.Hour // 90 days
	memTTL          = 24 * time.Hour      // 24h in-memory
	redisTTL        = 90 * 24 * time.Hour // 90 days in Redis
	maxWebSearches  = 12
	maxTokens       = 4000
)

// MarketContext holds the Stage 1 enrichment result
type MarketContext struct {
	City        string    `json:"city"`
	State       string    `json:"state"`
	Content     string    `json:"content"`     // Raw text from Sonnet
	GeneratedAt time.Time `json:"generatedAt"`
	Model       string    `json:"model"`
	PromptHash  string    `json:"promptHash"`
}

// memEntry is an in-memory cache entry with expiry
type memEntry struct {
	data      *MarketContext
	expiresAt time.Time
}

// MarketContextService provides Stage 1 context enrichment with 3-tier cache (ADR-074)
type MarketContextService struct {
	client  *anthropic.Client       // Own Sonnet client (NOT shared with Stage 2)
	queries *queries.Queries        // system_cache L2
	redis   *redisClient.Client     // L1
	mem     sync.Map                // L0 (24h in-memory)
	sfg     singleflight.Group      // Prevent thundering herd
	promptV string                  // Hash of current prompt version
	logger  *slog.Logger
}

// NewMarketContextService creates a new service with its own Anthropic client
func NewMarketContextService(
	apiKey string,
	q *queries.Queries,
	redis *redisClient.Client,
	onAPIError ...func(anthropic.APIErrorInfo),
) *MarketContextService {
	// Compute prompt version hash for cache key versioning
	promptHash := computePromptHash(prompts.MarketContextSystemPrompt)

	var errorFunc func(anthropic.APIErrorInfo)
	if len(onAPIError) > 0 {
		errorFunc = onAPIError[0]
	}

	return &MarketContextService{
		client: anthropic.NewClient(anthropic.ClientConfig{
			APIKey:     apiKey,
			MaxTokens:  maxTokens,
			OnAPIError: errorFunc,
			// Uses default Sonnet model (per ADR-074: Sonnet preferred over Haiku)
		}),
		queries: q,
		redis:   redis,
		promptV: promptHash[:8], // First 8 chars of hash
		logger:  slog.Default().With("component", "market_context_service"),
	}
}

// GetMarketContext returns market context for a city, using 3-tier cache
func (s *MarketContextService) GetMarketContext(ctx context.Context, city, state string) (*MarketContext, error) {
	key := s.cacheKey(city, state)

	// L0: In-memory cache
	if entry, ok := s.mem.Load(key); ok {
		me := entry.(*memEntry)
		if time.Now().Before(me.expiresAt) {
			s.logger.Debug("market context L0 hit", "city", city, "state", state)
			return me.data, nil
		}
		s.mem.Delete(key)
	}

	// L1: Redis cache
	if s.redis != nil {
		data, err := s.redis.GetJSON(ctx, key)
		if err == nil && data != nil {
			var mc MarketContext
			if err := json.Unmarshal(data, &mc); err == nil {
				s.logger.Debug("market context L1 hit", "city", city, "state", state)
				s.storeMemory(key, &mc)
				return &mc, nil
			}
		}
	}

	// L2: system_cache (PostgreSQL)
	if s.queries != nil {
		cached, err := s.queries.GetSystemCache(ctx, key)
		if err == nil {
			var mc MarketContext
			if err := json.Unmarshal(cached.Value, &mc); err == nil {
				s.logger.Debug("market context L2 hit", "city", city, "state", state)
				s.storeMemory(key, &mc)
				s.storeRedis(ctx, key, &mc)
				return &mc, nil
			}
		}
	}

	// Cache miss — fetch via Sonnet + web search (with singleflight dedup)
	result, err, _ := s.sfg.Do(key, func() (any, error) {
		return s.fetchFromAI(ctx, city, state)
	})
	if err != nil {
		return nil, fmt.Errorf("market context fetch failed for %s, %s: %w", city, state, err)
	}

	mc := result.(*MarketContext)

	// Store in all cache tiers
	s.storeAll(ctx, key, mc)

	return mc, nil
}

// FormatAsXML wraps the market context content in XML tags
func (s *MarketContextService) FormatAsXML(mc *MarketContext) string {
	if mc == nil || mc.Content == "" {
		return "<MARKET_CONTEXT>No market context available</MARKET_CONTEXT>"
	}
	return mc.Content
}

// IsConfigured returns true if the service can make API calls
func (s *MarketContextService) IsConfigured() bool {
	return s.client != nil && s.client.IsConfigured()
}

// fetchFromAI calls Sonnet + web search to generate market context
func (s *MarketContextService) fetchFromAI(ctx context.Context, city, state string) (*MarketContext, error) {
	s.logger.Info("fetching market context via AI",
		"city", city, "state", state, "model", s.client.GetModel(),
	)

	userPrompt := prompts.BuildMarketContextUserPrompt(city, state)

	// Use web search tool for verification
	tools := []any{
		anthropic.WebSearchTool{
			Type:    "web_search_20250305",
			Name:    "web_search",
			MaxUses: maxWebSearches,
		},
	}

	resp, err := s.client.CompleteWithTools(ctx, prompts.MarketContextSystemPrompt, userPrompt, tools)
	if err != nil {
		return nil, err
	}

	// Extract text from mixed response blocks
	text := anthropic.ExtractText(resp)
	if text == "" {
		return nil, fmt.Errorf("empty response from AI for market context")
	}

	// Quality gate: check if categories have SOURCED entries
	s.checkQuality(city, state, text)

	mc := &MarketContext{
		City:        city,
		State:       state,
		Content:     text,
		GeneratedAt: time.Now(),
		Model:       s.client.GetModel(),
		PromptHash:  s.promptV,
	}

	s.logger.Info("market context generated",
		"city", city, "state", state,
		"length", len(text),
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
	)

	return mc, nil
}

// checkQuality logs warnings if categories are missing or lack sourced entries
func (s *MarketContextService) checkQuality(city, state, text string) {
	categories := []string{"PROPERTY_TAX_SYSTEM", "REGULATORY_ENVIRONMENT", "INSURANCE_CONTEXT", "FISCAL_CONTEXT", "ECONOMIC_INDICATORS", "SUPPLY_PIPELINE"}
	lower := strings.ToLower(text)
	missing := 0
	for _, cat := range categories {
		if !strings.Contains(text, cat) {
			s.logger.Warn("market context missing category",
				"city", city, "state", state, "category", cat)
			missing++
		}
	}
	// Check for sourced evidence: SOURCED tag, URLs, or source citations
	hasSources := strings.Contains(lower, "sourced") ||
		strings.Contains(lower, "https://") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "source:")
	if !hasSources {
		s.logger.Warn("market context has no sourced entries",
			"city", city, "state", state,
			"categories_found", len(categories)-missing,
		)
	}
}

// storeMemory stores in L0 in-memory cache
func (s *MarketContextService) storeMemory(key string, mc *MarketContext) {
	s.mem.Store(key, &memEntry{
		data:      mc,
		expiresAt: time.Now().Add(memTTL),
	})
}

// storeRedis stores in L1 Redis cache
func (s *MarketContextService) storeRedis(ctx context.Context, key string, mc *MarketContext) {
	if s.redis == nil {
		return
	}
	if err := s.redis.SetJSON(ctx, key, mc, redisTTL); err != nil {
		s.logger.Warn("failed to cache market context in Redis", "error", err)
	}
}

// storeDB stores in L2 system_cache (PostgreSQL)
func (s *MarketContextService) storeDB(ctx context.Context, key string, mc *MarketContext) {
	if s.queries == nil {
		return
	}
	data, err := json.Marshal(mc)
	if err != nil {
		return
	}
	_, err = s.queries.UpsertSystemCache(ctx, queries.UpsertSystemCacheParams{
		Key:       key,
		Value:     data,
		ExpiresAt: pgTimestamp(time.Now().Add(cacheTTL)),
	})
	if err != nil {
		s.logger.Warn("failed to cache market context in DB", "error", err)
	}
}

// storeAll stores in all cache tiers
func (s *MarketContextService) storeAll(ctx context.Context, key string, mc *MarketContext) {
	s.storeMemory(key, mc)
	s.storeRedis(ctx, key, mc)
	s.storeDB(ctx, key, mc)
}

// cacheKey generates a versioned, normalized cache key
func (s *MarketContextService) cacheKey(city, state string) string {
	normalCity := strings.ToLower(strings.TrimSpace(city))
	normalState := strings.ToLower(strings.TrimSpace(state))
	return fmt.Sprintf("market_context:v%s:%s:%s", s.promptV, normalCity, normalState)
}

// computePromptHash creates a SHA256 hash of the prompt for cache versioning
func computePromptHash(prompt string) string {
	h := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%x", h)
}

// pgTimestamp converts time.Time to pgtype-compatible timestamp
func pgTimestamp(t time.Time) time.Time {
	return t
}
