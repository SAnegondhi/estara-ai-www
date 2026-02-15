package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	MarketDB DatabaseConfig
	Redis    RedisConfig
	JWT      JWTConfig
	AI       AIConfig
	Property PropertyConfig
	Market   MarketConfig
	Cron     CronConfig
	Admin    AdminConfig
	CORS     CORSConfig
	Email    EmailConfig
	Stripe   StripeConfig
	Security SecurityConfig
	IAP      IAPConfig
	WebAuthn WebAuthnConfig
}

// WebAuthnConfig holds WebAuthn/Passkey configuration (ADR-081)
type WebAuthnConfig struct {
	RPDisplayName string   `mapstructure:"WEBAUTHN_RP_DISPLAY_NAME"`
	RPID          string   `mapstructure:"WEBAUTHN_RP_ID"`
	RPOrigins     []string `mapstructure:"WEBAUTHN_RP_ORIGINS"`
}

// IAPConfig holds In-App Purchase configuration for Apple and Google
type IAPConfig struct {
	AppleSharedSecret         string `mapstructure:"APPLE_IAP_SHARED_SECRET"`
	GoogleServiceAccountJSON  string `mapstructure:"GOOGLE_SERVICE_ACCOUNT_JSON"`
	GooglePlayPackageName     string `mapstructure:"GOOGLE_PLAY_PACKAGE_NAME"`
}

// SecurityConfig holds security-related configuration
type SecurityConfig struct {
	CheckoutEncryptionKey string `mapstructure:"CHECKOUT_ENCRYPTION_KEY"`
	// ADR-066: Cookie-based authentication
	CookieDomain    string `mapstructure:"COOKIE_DOMAIN"`
	CookieSecure    bool   `mapstructure:"COOKIE_SECURE"`
	CSRFTokenLength int    `mapstructure:"CSRF_TOKEN_LENGTH"`
}

// StripeConfig holds Stripe payment configuration
type StripeConfig struct {
	SecretKey        string `mapstructure:"STRIPE_SECRET_KEY"`
	WebhookSecret    string `mapstructure:"STRIPE_WEBHOOK_SECRET"`
	PublishableKey   string `mapstructure:"STRIPE_PUBLISHABLE_KEY"`
	PriceSingleReport   string `mapstructure:"STRIPE_PRICE_SINGLE_REPORT"`
	PriceReportPack     string `mapstructure:"STRIPE_PRICE_REPORT_PACK"`
	PriceAnnualAccess      string `mapstructure:"STRIPE_PRICE_ANNUAL_ACCESS"`
	PriceProfessionalAllocator string `mapstructure:"STRIPE_PRICE_PROFESSIONAL_ALLOCATOR"`
	PriceAPIInvestor   string `mapstructure:"STRIPE_PRICE_AAPI_INVESTOR"`
	PriceAPIAllocator  string `mapstructure:"STRIPE_PRICE_AAPI_ALLOCATOR"`
	PriceOverageReport string `mapstructure:"STRIPE_PRICE_OVERAGE_REPORT"`
}

// EmailConfig holds email service configuration
type EmailConfig struct {
	APIKey    string `mapstructure:"MAILJET_API_KEY"`
	APISecret string `mapstructure:"MAILJET_SECRET_KEY"`
	FromEmail string `mapstructure:"EMAIL_FROM_ADDRESS"`
	FromName  string `mapstructure:"EMAIL_FROM_NAME"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Port            int           `mapstructure:"PORT"`
	Env             string        `mapstructure:"ENV"`
	ReadTimeout     time.Duration `mapstructure:"READ_TIMEOUT"`
	WriteTimeout    time.Duration `mapstructure:"WRITE_TIMEOUT"`
	ShutdownTimeout time.Duration `mapstructure:"SHUTDOWN_TIMEOUT"`
	MarketingURL    string        `mapstructure:"MARKETING_URL"`
	ClientURL       string        `mapstructure:"CLIENT_URL"`
}

// DatabaseConfig holds PostgreSQL configuration
type DatabaseConfig struct {
	URL             string        `mapstructure:"URL"`
	MaxConnections  int           `mapstructure:"MAX_CONNECTIONS"`
	MinConnections  int           `mapstructure:"MIN_CONNECTIONS"`
	MaxConnLifetime time.Duration `mapstructure:"MAX_CONN_LIFETIME"`
	MaxConnIdleTime time.Duration `mapstructure:"MAX_CONN_IDLE_TIME"`
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	URL      string `mapstructure:"REDIS_URL"`
	Password string `mapstructure:"REDIS_PASSWORD"`
	DB       int    `mapstructure:"REDIS_DB"`
}

// JWTConfig holds JWT authentication configuration
type JWTConfig struct {
	Secret     string        `mapstructure:"JWT_SECRET"`
	Expiry     time.Duration `mapstructure:"JWT_EXPIRY"`
	RefreshExp time.Duration `mapstructure:"JWT_REFRESH_EXPIRY"`
}

// AIConfig holds AI provider configuration
type AIConfig struct {
	AnthropicAPIKey string `mapstructure:"ANTHROPIC_API_KEY"`
	DefaultModel    string `mapstructure:"AI_DEFAULT_MODEL"`
	MaxTokens       int    `mapstructure:"AI_MAX_TOKENS"`
}

// PropertyConfig holds property finder configuration
type PropertyConfig struct {
	Priority               []string `mapstructure:"PROPERTY_FINDER_PRIORITY"`
	BrightDataEnabled      bool     `mapstructure:"PROPERTY_FINDER_BRIGHTDATA_ENABLED"`
	BrightDataAPIKey       string   `mapstructure:"BRIGHTDATA_API_KEY"`
	HasDataEnabled         bool     `mapstructure:"PROPERTY_FINDER_HASDATA_ENABLED"`
	HasDataAPIKey          string   `mapstructure:"HASDATA_API_KEY"`
	ClaudeEnabled          bool     `mapstructure:"PROPERTY_FINDER_CLAUDE_ENABLED"`
	PublicEnabled          bool     `mapstructure:"PROPERTY_FINDER_PUBLIC_ENABLED"`
	CacheTTL               time.Duration
	EnrichmentConcurrency  int      `mapstructure:"HASDATA_CONCURRENT_CONNECTIONS"`
}

// MarketConfig holds market data configuration
type MarketConfig struct {
	FREDAPIKey   string `mapstructure:"FRED_API_KEY"`
	CensusAPIKey string `mapstructure:"CENSUS_API_KEY"`
	BLSAPIKey    string `mapstructure:"BLS_API_KEY"`
	CacheTTL     time.Duration
}

// CronConfig holds cron job configuration
type CronConfig struct {
	Secret string `mapstructure:"CRON_SECRET"`
}

// AdminConfig holds admin configuration
type AdminConfig struct {
	UserIDs []string `mapstructure:"ADMIN_USER_IDS"`
}

// CORSConfig holds CORS configuration
type CORSConfig struct {
	AllowedOrigins   []string `mapstructure:"CORS_ALLOWED_ORIGINS"`
	AllowCredentials bool     `mapstructure:"CORS_ALLOW_CREDENTIALS"`
}

// Load reads configuration from environment and .env files
func Load() (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read from .env.local file
	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")
	v.AddConfigPath("..")

	// Also try .env.local
	if err := v.ReadInConfig(); err != nil {
		// Not finding config file is OK - we'll use env vars
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			slog.Warn("error reading config file", "error", err)
		}
	}

	// Try reading .env.local as well (use SetConfigFile for exact filename)
	v.SetConfigFile(".env.local")
	if err := v.MergeInConfig(); err != nil {
		// Also try parent directory for .env.local
		v.SetConfigFile("../.env.local")
		_ = v.MergeInConfig()
	}

	// Enable reading from environment variables
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	cfg := &Config{}

	// Server config
	cfg.Server = ServerConfig{
		Port:            v.GetInt("PORT"),
		Env:             v.GetString("ENV"),
		ReadTimeout:     v.GetDuration("READ_TIMEOUT"),
		WriteTimeout:    v.GetDuration("WRITE_TIMEOUT"),
		ShutdownTimeout: v.GetDuration("SHUTDOWN_TIMEOUT"),
		MarketingURL:    v.GetString("MARKETING_URL"),
		ClientURL:       v.GetString("CLIENT_URL"),
	}

	// Main database config
	cfg.Database = DatabaseConfig{
		URL:             v.GetString("DATABASE_URL"),
		MaxConnections:  v.GetInt("DATABASE_MAX_CONNECTIONS"),
		MinConnections:  v.GetInt("DATABASE_MIN_CONNECTIONS"),
		MaxConnLifetime: v.GetDuration("DATABASE_MAX_CONN_LIFETIME"),
		MaxConnIdleTime: v.GetDuration("DATABASE_MAX_CONN_IDLE_TIME"),
	}

	// Market database config (separate PostgreSQL for time series data)
	cfg.MarketDB = DatabaseConfig{
		URL:             v.GetString("MARKET_DATABASE_URL"),
		MaxConnections:  v.GetInt("MARKET_DATABASE_MAX_CONNECTIONS"),
		MinConnections:  v.GetInt("MARKET_DATABASE_MIN_CONNECTIONS"),
		MaxConnLifetime: v.GetDuration("DATABASE_MAX_CONN_LIFETIME"),
		MaxConnIdleTime: v.GetDuration("DATABASE_MAX_CONN_IDLE_TIME"),
	}

	// Redis config
	cfg.Redis = RedisConfig{
		URL:      v.GetString("REDIS_URL"),
		Password: v.GetString("REDIS_PASSWORD"),
		DB:       v.GetInt("REDIS_DB"),
	}

	// JWT config - support both JWT_SECRET and CLIENT_JWT_SECRET for www_v1 compatibility
	jwtSecret := v.GetString("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = v.GetString("CLIENT_JWT_SECRET")
	}
	cfg.JWT = JWTConfig{
		Secret:     jwtSecret,
		Expiry:     v.GetDuration("JWT_EXPIRY"),
		RefreshExp: v.GetDuration("JWT_REFRESH_EXPIRY"),
	}

	// AI config
	cfg.AI = AIConfig{
		AnthropicAPIKey: v.GetString("ANTHROPIC_API_KEY"),
		DefaultModel:    v.GetString("AI_DEFAULT_MODEL"),
		MaxTokens:       v.GetInt("AI_MAX_TOKENS"),
	}

	// Property config
	priorityStr := v.GetString("PROPERTY_FINDER_PRIORITY")
	var priority []string
	if priorityStr != "" {
		priority = strings.Split(priorityStr, ",")
		for i := range priority {
			priority[i] = strings.TrimSpace(priority[i])
		}
	}
	cfg.Property = PropertyConfig{
		Priority:              priority,
		BrightDataEnabled:     v.GetBool("PROPERTY_FINDER_BRIGHTDATA_ENABLED"),
		BrightDataAPIKey:      v.GetString("BRIGHTDATA_API_KEY"),
		HasDataEnabled:        v.GetBool("PROPERTY_FINDER_HASDATA_ENABLED"),
		HasDataAPIKey:         v.GetString("HASDATA_API_KEY"),
		PublicEnabled:         v.GetBool("PROPERTY_FINDER_PUBLIC_ENABLED"),
		CacheTTL:              24 * time.Hour,
		EnrichmentConcurrency: v.GetInt("HASDATA_CONCURRENT_CONNECTIONS"),
	}

	// Market config
	cfg.Market = MarketConfig{
		FREDAPIKey:   v.GetString("FRED_API_KEY"),
		CensusAPIKey: v.GetString("CENSUS_API_KEY"),
		BLSAPIKey:    v.GetString("BLS_API_KEY"),
		CacheTTL:     6 * time.Hour,
	}

	// Cron config
	cfg.Cron = CronConfig{
		Secret: v.GetString("CRON_SECRET"),
	}

	// Admin config
	adminIDsStr := v.GetString("ADMIN_USER_IDS")
	var adminIDs []string
	if adminIDsStr != "" {
		adminIDs = strings.Split(adminIDsStr, ",")
		for i := range adminIDs {
			adminIDs[i] = strings.TrimSpace(adminIDs[i])
		}
	}
	cfg.Admin = AdminConfig{
		UserIDs: adminIDs,
	}

	// CORS config
	originsStr := v.GetString("CORS_ALLOWED_ORIGINS")
	var origins []string
	if originsStr != "" {
		origins = strings.Split(originsStr, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
	}
	cfg.CORS = CORSConfig{
		AllowedOrigins:   origins,
		AllowCredentials: v.GetBool("CORS_ALLOW_CREDENTIALS"),
	}

	// Email config
	cfg.Email = EmailConfig{
		APIKey:    v.GetString("MAILJET_API_KEY"),
		APISecret: v.GetString("MAILJET_SECRET_KEY"),
		FromEmail: v.GetString("EMAIL_FROM_ADDRESS"),
		FromName:  v.GetString("EMAIL_FROM_NAME"),
	}

	// Stripe config
	cfg.Stripe = StripeConfig{
		SecretKey:              v.GetString("STRIPE_SECRET_KEY"),
		WebhookSecret:          v.GetString("STRIPE_WEBHOOK_SECRET"),
		PublishableKey:         v.GetString("STRIPE_PUBLISHABLE_KEY"),
		PriceSingleReport:      v.GetString("STRIPE_PRICE_SINGLE_REPORT"),
		PriceReportPack:        v.GetString("STRIPE_PRICE_REPORT_PACK"),
		PriceAnnualAccess:         v.GetString("STRIPE_PRICE_ANNUAL_ACCESS"),
		PriceProfessionalAllocator: v.GetString("STRIPE_PRICE_PROFESSIONAL_ALLOCATOR"),
		PriceAPIInvestor:       v.GetString("STRIPE_PRICE_AAPI_INVESTOR"),
		PriceAPIAllocator:      v.GetString("STRIPE_PRICE_AAPI_ALLOCATOR"),
		PriceOverageReport:     v.GetString("STRIPE_PRICE_OVERAGE_REPORT"),
	}

	cfg.Security = SecurityConfig{
		CheckoutEncryptionKey: v.GetString("CHECKOUT_ENCRYPTION_KEY"),
	}

	// WebAuthn config (ADR-081)
	// Derive RP ID and origins from CLIENT_URL when not explicitly set via env vars.
	// This avoids requiring separate WEBAUTHN_RP_* env vars in production —
	// CLIENT_URL (e.g. https://insight.estara-ai.com) provides both hostname and origin.
	rpID := v.GetString("WEBAUTHN_RP_ID")
	rpOriginsStr := v.GetString("WEBAUTHN_RP_ORIGINS")
	var rpOrigins []string

	// If WEBAUTHN_RP_ID is still the default "localhost" and CLIENT_URL is a real domain, derive from it
	clientURL := cfg.Server.ClientURL
	if clientURL != "" && clientURL != "http://localhost:3002" {
		if parsed, err := url.Parse(clientURL); err == nil && parsed.Hostname() != "" {
			if rpID == "localhost" || rpID == "" {
				rpID = parsed.Hostname()
			}
			if rpOriginsStr == "http://localhost:3002" || rpOriginsStr == "" {
				rpOriginsStr = strings.TrimRight(clientURL, "/")
			}
		}
	}

	if rpOriginsStr != "" {
		rpOrigins = strings.Split(rpOriginsStr, ",")
		for i := range rpOrigins {
			rpOrigins[i] = strings.TrimSpace(rpOrigins[i])
		}
	}
	cfg.WebAuthn = WebAuthnConfig{
		RPDisplayName: v.GetString("WEBAUTHN_RP_DISPLAY_NAME"),
		RPID:          rpID,
		RPOrigins:     rpOrigins,
	}

	// IAP config
	cfg.IAP = IAPConfig{
		AppleSharedSecret:        v.GetString("APPLE_IAP_SHARED_SECRET"),
		GoogleServiceAccountJSON: v.GetString("GOOGLE_SERVICE_ACCOUNT_JSON"),
		GooglePlayPackageName:    v.GetString("GOOGLE_PLAY_PACKAGE_NAME"),
	}

	// Validate required configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// setDefaults sets default values for configuration
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("PORT", 3000)
	v.SetDefault("ENV", "development")
	v.SetDefault("READ_TIMEOUT", "30s")
	v.SetDefault("WRITE_TIMEOUT", "30s")
	v.SetDefault("SHUTDOWN_TIMEOUT", "10s")
	v.SetDefault("MARKETING_URL", "http://localhost:3500")
	v.SetDefault("CLIENT_URL", "http://localhost:3002")

	// Database defaults
	v.SetDefault("DATABASE_MAX_CONNECTIONS", 25)
	v.SetDefault("DATABASE_MIN_CONNECTIONS", 5)
	v.SetDefault("DATABASE_MAX_CONN_LIFETIME", "1h")
	v.SetDefault("DATABASE_MAX_CONN_IDLE_TIME", "30m")

	// Market database defaults
	v.SetDefault("MARKET_DATABASE_MAX_CONNECTIONS", 10)
	v.SetDefault("MARKET_DATABASE_MIN_CONNECTIONS", 2)

	// Redis defaults
	v.SetDefault("REDIS_URL", "redis://localhost:6379")
	v.SetDefault("REDIS_DB", 0)

	// JWT defaults
	v.SetDefault("JWT_EXPIRY", "24h")
	v.SetDefault("JWT_REFRESH_EXPIRY", "168h") // 7 days

	// AI defaults
	v.SetDefault("AI_DEFAULT_MODEL", "claude-3-5-sonnet-20241022")
	v.SetDefault("AI_MAX_TOKENS", 4096)

	// Property finder defaults
	v.SetDefault("PROPERTY_FINDER_PRIORITY", "brightdata,hasdata")
	v.SetDefault("PROPERTY_FINDER_BRIGHTDATA_ENABLED", true)
	v.SetDefault("PROPERTY_FINDER_HASDATA_ENABLED", true)
	v.SetDefault("PROPERTY_FINDER_PUBLIC_ENABLED", false)

	// CORS defaults
	v.SetDefault("CORS_ALLOW_CREDENTIALS", true)

	// Email defaults
	v.SetDefault("EMAIL_FROM_ADDRESS", "noreply@estara-ai.com")
	v.SetDefault("EMAIL_FROM_NAME", "Estara AI")

	// Security defaults (ADR-066)
	v.SetDefault("COOKIE_DOMAIN", "")       // Empty = exact host match (__Host- prefix)
	v.SetDefault("COOKIE_SECURE", true)     // Always true in production
	v.SetDefault("CSRF_TOKEN_LENGTH", 32)   // 32 bytes = 256 bits

	// WebAuthn defaults (ADR-081)
	v.SetDefault("WEBAUTHN_RP_DISPLAY_NAME", "Estara Insight")
	v.SetDefault("WEBAUTHN_RP_ID", "localhost")
	v.SetDefault("WEBAUTHN_RP_ORIGINS", "http://localhost:3002")
}

// Validate checks required configuration values
func (c *Config) Validate() error {
	var errs []string

	// Required: Database URL
	if c.Database.URL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}

	// Required: JWT Secret (must be at least 32 characters)
	if c.JWT.Secret == "" {
		errs = append(errs, "JWT_SECRET is required")
	} else if len(c.JWT.Secret) < 32 {
		errs = append(errs, "JWT_SECRET must be at least 32 characters")
	}

	// Log config status (without exposing secrets)
	slog.Info("config loaded",
		"env", c.Server.Env,
		"port", c.Server.Port,
		"read_timeout", c.Server.ReadTimeout.String(),
		"write_timeout", c.Server.WriteTimeout.String(),
		"database_configured", c.Database.URL != "",
		"market_db_configured", c.MarketDB.URL != "",
		"redis_configured", c.Redis.URL != "",
		"jwt_secret_length", len(c.JWT.Secret),
		"anthropic_configured", c.AI.AnthropicAPIKey != "",
		"property_priority", c.Property.Priority,
		"admin_users", len(c.Admin.UserIDs),
		"stripe_configured", c.Stripe.SecretKey != "",
	)

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	return nil
}

// IsDevelopment returns true if running in development mode
func (c *Config) IsDevelopment() bool {
	return c.Server.Env == "development"
}

// IsProduction returns true if running in production mode
func (c *Config) IsProduction() bool {
	return c.Server.Env == "production"
}
