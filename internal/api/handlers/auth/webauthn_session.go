package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	redisClient "github.com/estara-ai/www/internal/db/redis"
)

const webauthnSessionTTL = 5 * time.Minute

// storeWebAuthnSession stores WebAuthn session data in Redis with a 5-minute TTL.
func storeWebAuthnSession(ctx context.Context, redis *redisClient.Client, key string, session *webauthn.SessionData) error {
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return redis.Set(ctx, "webauthn:"+key, string(data), webauthnSessionTTL).Err()
}

// loadWebAuthnSession retrieves and deletes (one-time use) WebAuthn session data from Redis.
func loadWebAuthnSession(ctx context.Context, redis *redisClient.Client, key string) (*webauthn.SessionData, error) {
	val, err := redis.Get(ctx, "webauthn:"+key).Result()
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	// Delete after reading (one-time use)
	_ = redis.Delete(ctx, "webauthn:"+key)

	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(val), &session); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}

	return &session, nil
}
