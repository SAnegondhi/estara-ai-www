package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/estara-ai/www/internal/api/middleware"
	"github.com/estara-ai/www/internal/db/queries"
	"github.com/estara-ai/www/pkg/httputil"
)

// BeginPasskeyRegistration starts the passkey registration ceremony (authenticated).
func (h *Handler) BeginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	user, err := h.getUserByID(ctx, claims.UserID)
	if err != nil {
		httputil.NotFound(w, "User not found")
		return
	}

	// Load existing credentials for exclusion
	q := h.store.Q()
	creds, err := q.GetWebAuthnCredentialsByUserID(ctx, user.ID)
	if err != nil {
		h.logger.Error("failed to load credentials", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to load credentials")
		return
	}

	wUser := toWebAuthnUser(user, creds)

	// Build exclusion list so authenticator doesn't re-register existing credentials
	excludeList := make([]protocol.CredentialDescriptor, 0, len(creds))
	for _, c := range wUser.WebAuthnCredentials() {
		excludeList = append(excludeList, c.Descriptor())
	}

	creation, session, err := h.webauthn.BeginRegistration(wUser,
		webauthn.WithExclusions(excludeList),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		h.logger.Error("begin registration failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to begin registration")
		return
	}

	// Store session in Redis
	sessionKey := fmt.Sprintf("reg:%s", user.ID)
	if err := storeWebAuthnSession(ctx, h.redis, sessionKey, session); err != nil {
		h.logger.Error("failed to store session", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to store session")
		return
	}

	httputil.JSON(w, http.StatusOK, creation)
}

// FinishPasskeyRegistration completes the passkey registration ceremony (authenticated).
func (h *Handler) FinishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	user, err := h.getUserByID(ctx, claims.UserID)
	if err != nil {
		httputil.NotFound(w, "User not found")
		return
	}

	// Load existing credentials
	q := h.store.Q()
	creds, err := q.GetWebAuthnCredentialsByUserID(ctx, user.ID)
	if err != nil {
		h.logger.Error("failed to load credentials", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to load credentials")
		return
	}

	wUser := toWebAuthnUser(user, creds)

	// Load session from Redis
	sessionKey := fmt.Sprintf("reg:%s", user.ID)
	session, err := loadWebAuthnSession(ctx, h.redis, sessionKey)
	if err != nil {
		h.logger.Warn("session not found or expired", "error", err)
		httputil.BadRequest(w, "Registration session expired. Please try again.")
		return
	}

	credential, err := h.webauthn.FinishRegistration(wUser, *session, r)
	if err != nil {
		h.logger.Warn("finish registration failed", "error", err)
		httputil.BadRequest(w, "Registration failed: "+err.Error())
		return
	}

	// Extract friendly name from request (query param or default)
	friendlyName := r.URL.Query().Get("name")
	if friendlyName == "" {
		friendlyName = "Passkey"
	}

	// Convert transports to string slice
	transports := make([]string, 0, len(credential.Transport))
	for _, t := range credential.Transport {
		transports = append(transports, string(t))
	}

	// Generate ID for the record
	idBytes := make([]byte, 16)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	// Store credential in database
	_, err = q.CreateWebAuthnCredential(ctx, queries.CreateWebAuthnCredentialParams{
		ID:              id,
		UserId:          user.ID,
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		Transport:       transports,
		FlagsValue:      int32(credential.Flags.ProtocolValue()),
		SignCount:       int64(credential.Authenticator.SignCount),
		Aaguid:          credential.Authenticator.AAGUID,
		FriendlyName:    friendlyName,
	})
	if err != nil {
		h.logger.Error("failed to store credential", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to store credential")
		return
	}

	h.logAuthAudit(ctx, r, user.ID, "PASSKEY_REGISTERED", "Passkey registered: "+friendlyName, true, "")
	h.logger.Info("passkey registered", "user_id", user.ID, "credential_id", base64.URLEncoding.EncodeToString(credential.ID))

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"id":           id,
		"friendlyName": friendlyName,
		"createdAt":    time.Now().UTC().Format(time.RFC3339),
	})
}

// BeginPasskeyLogin starts the passkey login ceremony (unauthenticated, discoverable).
func (h *Handler) BeginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	assertion, session, err := h.webauthn.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationPreferred),
	)
	if err != nil {
		h.logger.Error("begin discoverable login failed", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to begin login")
		return
	}

	// Store session keyed by challenge (since we don't know the user yet)
	sessionKey := fmt.Sprintf("login:%s", session.Challenge)
	if err := storeWebAuthnSession(ctx, h.redis, sessionKey, session); err != nil {
		h.logger.Error("failed to store login session", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to store session")
		return
	}

	httputil.JSON(w, http.StatusOK, assertion)
}

// FinishPasskeyLogin completes the passkey login ceremony and returns JWT + cookies.
func (h *Handler) FinishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse the assertion response to extract the challenge
	parsedResponse, err := protocol.ParseCredentialRequestResponse(r)
	if err != nil {
		h.logger.Warn("failed to parse assertion", "error", err)
		httputil.BadRequest(w, "Invalid assertion response")
		return
	}

	// Load session by challenge (already base64url-encoded string)
	challenge := parsedResponse.Response.CollectedClientData.Challenge
	sessionKey := fmt.Sprintf("login:%s", challenge)
	session, err := loadWebAuthnSession(ctx, h.redis, sessionKey)
	if err != nil {
		h.logger.Warn("login session not found or expired", "error", err)
		httputil.BadRequest(w, "Login session expired. Please try again.")
		return
	}

	// DiscoverableUserHandler: look up user by credential
	q := h.store.Q()
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		userID := string(userHandle)
		user, err := h.getUserByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("user not found: %w", err)
		}

		creds, err := q.GetWebAuthnCredentialsByUserID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to load credentials: %w", err)
		}

		return toWebAuthnUser(user, creds), nil
	}

	// Validate the assertion (this also returns the user found via userHandle)
	returnedUser, credential, err := h.webauthn.FinishPasskeyLogin(handler, *session, r)
	if err != nil {
		h.logger.Warn("finish passkey login failed", "error", err)
		httputil.Unauthorized(w, "Passkey verification failed")
		return
	}

	// Update sign count
	dbCred, err := q.GetWebAuthnCredentialByCredentialID(ctx, credential.ID)
	if err == nil {
		_ = q.UpdateWebAuthnCredentialSignCount(ctx, queries.UpdateWebAuthnCredentialSignCountParams{
			ID:        dbCred.ID,
			SignCount: int64(credential.Authenticator.SignCount),
		})
	}

	// Extract the user ID from the returned webauthn.User
	userID := string(returnedUser.WebAuthnID())
	user, err := h.getUserByID(ctx, userID)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, "Failed to load user")
		return
	}

	// Reuse the shared post-login flow (whitelist, subscription, JWT, cookies, audit)
	h.completeLogin(w, r, user)
}

// completeLogin is the shared post-login flow used by both password and passkey login.
// It checks whitelist, validates subscription, generates JWT, sets cookies, and logs audit.
func (h *Handler) completeLogin(w http.ResponseWriter, r *http.Request, user *User) {
	ctx := r.Context()
	email := strings.ToLower(user.Email)

	// Check whitelist (beta access control)
	if h.whitelist != nil && !h.whitelist.IsAllowed(ctx, email) {
		h.logAuthAudit(ctx, r, user.ID, "USER_SIGNIN", "Login blocked: email not whitelisted", false, "not whitelisted")
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":   "access_denied",
			"code":    "NOT_WHITELISTED",
			"message": "Your account is not approved for beta access. Please contact support.",
		})
		return
	}

	// Check V2 subscription quota
	v2Quota, err := h.getV2Quota(ctx, user.ID)
	if err != nil || v2Quota == nil || v2Quota.PeriodEndDate.Before(time.Now()) {
		h.logger.Warn("no valid V2 subscription", "user_id", user.ID)
		httputil.JSON(w, http.StatusForbidden, map[string]interface{}{
			"error":      "no_insight_access",
			"code":       "NO_V2_SUBSCRIPTION",
			"message":    "You do not have an active Estara Insight subscription.",
			"canPurchase": true,
			"websiteUrl": h.cfg.Server.MarketingURL,
		})
		return
	}

	// Calculate entitlements
	entitlements := h.buildEntitlements(user, v2Quota)

	// Generate tokens
	accessToken, err := h.auth.GenerateToken(
		user.ID,
		user.ClerkUserID,
		email,
		user.Role,
		24*time.Hour,
	)
	if err != nil {
		h.logger.Error("failed to generate access token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	refreshToken, err := h.auth.GenerateRefreshToken(user.ID, 7*24*time.Hour)
	if err != nil {
		h.logger.Error("failed to generate refresh token", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// Update last login
	_ = h.updateLastLogin(ctx, user.ID)

	// ADR-066: Set httpOnly cookies for authentication
	secure := !h.cfg.IsDevelopment()
	middleware.SetAuthCookies(w, accessToken, refreshToken, secure)

	// ADR-066: Set CSRF cookie and get token for response
	csrfToken := middleware.SetCSRFCookie(w, secure, h.cfg.Security.CSRFTokenLength)

	// Build response
	tier := strings.TrimPrefix(v2Quota.Tier, "V2_")
	response := LoginResponse{
		Success:      true,
		Token:        accessToken,
		RefreshToken: refreshToken,
		User: UserResponse{
			ID:               user.ID,
			Email:            email,
			FirstName:        user.FirstName,
			LastName:         user.LastName,
			SubscriptionTier: strings.ToLower(tier),
			Theme:            getStringOrDefault(user.Theme, "estara"),
		},
		Entitlements: entitlements,
		CSRFToken:    csrfToken,
	}

	h.logAuthAudit(ctx, r, user.ID, "PASSKEY_LOGIN", "User logged in via passkey", true, "")
	h.logger.Info("passkey login successful", "user_id", user.ID, "email", email)
	httputil.JSON(w, http.StatusOK, response)
}

// ListPasskeys returns the authenticated user's registered passkeys.
func (h *Handler) ListPasskeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	q := h.store.Q()
	creds, err := q.GetWebAuthnCredentialsByUserID(ctx, claims.UserID)
	if err != nil {
		h.logger.Error("failed to load credentials", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to load credentials")
		return
	}

	type PasskeyInfo struct {
		ID           string  `json:"id"`
		FriendlyName string  `json:"friendlyName"`
		CreatedAt    string  `json:"createdAt"`
		LastUsedAt   *string `json:"lastUsedAt"`
	}

	passkeys := make([]PasskeyInfo, 0, len(creds))
	for _, c := range creds {
		info := PasskeyInfo{
			ID:           c.ID,
			FriendlyName: c.FriendlyName,
			CreatedAt:    c.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		if c.LastUsedAt.Valid {
			t := c.LastUsedAt.Time.UTC().Format(time.RFC3339)
			info.LastUsedAt = &t
		}
		passkeys = append(passkeys, info)
	}

	httputil.JSON(w, http.StatusOK, map[string]interface{}{
		"passkeys": passkeys,
	})
}

// RenamePasskey updates the friendly name of a passkey.
func (h *Handler) RenamePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	passkeyID := chi.URLParam(r, "id")
	if passkeyID == "" {
		httputil.BadRequest(w, "passkey ID required")
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.DecodeJSON(r, &req); err != nil || req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}

	q := h.store.Q()
	err := q.UpdateWebAuthnCredentialName(ctx, queries.UpdateWebAuthnCredentialNameParams{
		ID:           passkeyID,
		FriendlyName: req.Name,
	})
	if err != nil {
		h.logger.Error("failed to rename passkey", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to rename passkey")
		return
	}

	httputil.Success(w, map[string]string{"message": "passkey renamed"})
}

// DeletePasskey removes a passkey credential.
func (h *Handler) DeletePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims := middleware.GetUserFromContext(ctx)
	if claims == nil {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	passkeyID := chi.URLParam(r, "id")
	if passkeyID == "" {
		httputil.BadRequest(w, "passkey ID required")
		return
	}

	q := h.store.Q()
	err := q.DeleteWebAuthnCredential(ctx, queries.DeleteWebAuthnCredentialParams{
		ID:     passkeyID,
		UserId: claims.UserID,
	})
	if err != nil {
		h.logger.Error("failed to delete passkey", "error", err)
		httputil.Error(w, http.StatusInternalServerError, "Failed to delete passkey")
		return
	}

	h.logAuthAudit(ctx, r, claims.UserID, "PASSKEY_DELETED", "Passkey deleted: "+passkeyID, true, "")
	httputil.Success(w, map[string]string{"message": "passkey deleted"})
}
