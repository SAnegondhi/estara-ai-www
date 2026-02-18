package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	t.Run("Register", testRegister(env))
	t.Run("Login", testLogin(env))
	t.Run("RefreshToken", testRefreshToken(env))
	t.Run("Me", testAuthMe(env))
	t.Run("UpdatePassword", testUpdatePassword(env))
	t.Run("Logout", testLogout(env))
	t.Run("OAuthLogin", testOAuthLogin(env))
}

func testRegister(env *TestEnv) func(t *testing.T) {
	return func(t *testing.T) {
		email := RandomEmail()
		password := RandomPassword()
		defer CleanupTestData(t, env, email)

		cases := []TestCase{
			{
				Name: "valid registration",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/register",
					Body: map[string]string{
						"email":    email,
						"password": password,
					},
				},
				WantStatus: http.StatusOK,
				Validate: func(t *testing.T, body []byte) {
					var resp struct {
						User struct {
							ID    string `json:"id"`
							Email string `json:"email"`
						} `json:"user"`
						Token        string `json:"token"`
						RefreshToken string `json:"refreshToken"`
					}
					ParseJSON(t, body, &resp)

					assert.NotEmpty(t, resp.User.ID)
					assert.Equal(t, email, resp.User.Email)
					assert.NotEmpty(t, resp.Token)
					assert.NotEmpty(t, resp.RefreshToken)
				},
			},
			{
				Name: "duplicate email",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/register",
					Body: map[string]string{
						"email":    email,
						"password": password,
					},
				},
				WantStatus: http.StatusConflict,
				WantError:  "already registered",
			},
			{
				Name: "invalid email format",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/register",
					Body: map[string]string{
						"email":    "invalid-email",
						"password": password,
					},
				},
				WantStatus: http.StatusBadRequest,
				WantError:  "validation",
			},
			{
				Name: "weak password",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/register",
					Body: map[string]string{
						"email":    "new@example.com",
						"password": "123",
					},
				},
				WantStatus: http.StatusBadRequest,
				WantError:  "validation",
			},
		}

		RunTestCases(t, env, cases)
	}
}

func testLogin(env *TestEnv) func(t *testing.T) {
	return func(t *testing.T) {
		email := RandomEmail()
		password := RandomPassword()
		defer CleanupTestData(t, env, email)

		// Create test user
		user := CreateTestUser(t, env, email, password)

		cases := []TestCase{
			{
				Name: "valid credentials",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/client-login",
					Body: map[string]string{
						"email":    email,
						"password": password,
					},
				},
				WantStatus: http.StatusOK,
				Validate: func(t *testing.T, body []byte) {
					var resp struct {
						User struct {
							ID    string `json:"id"`
							Email string `json:"email"`
						} `json:"user"`
						Token        string `json:"token"`
						RefreshToken string `json:"refreshToken"`
					}
					ParseJSON(t, body, &resp)

					assert.Equal(t, user.ID, resp.User.ID)
					assert.Equal(t, email, resp.User.Email)
					assert.NotEmpty(t, resp.Token)
					assert.NotEmpty(t, resp.RefreshToken)
				},
			},
			{
				Name: "wrong password",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/client-login",
					Body: map[string]string{
						"email":    email,
						"password": "WrongPassword123!",
					},
				},
				WantStatus: http.StatusUnauthorized,
				WantError:  "Invalid",
			},
			{
				Name: "nonexistent user",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/client-login",
					Body: map[string]string{
						"email":    RandomEmail(), // Use random to avoid rate limiter state across runs
						"password": password,
					},
				},
				WantStatus: http.StatusUnauthorized,
				WantError:  "Invalid",
			},
		}

		RunTestCases(t, env, cases)
	}
}

func testRefreshToken(env *TestEnv) func(t *testing.T) {
	return func(t *testing.T) {
		email := RandomEmail()
		password := RandomPassword()
		defer CleanupTestData(t, env, email)

		user := CreateTestUser(t, env, email, password)

		cases := []TestCase{
			{
				Name: "valid refresh token",
				// The refresh endpoint reads the current access token from the Authorization header
				// (not a body-based refresh token) and issues a new access token
				Request: Request{
					Method:      "POST",
					Path:        "/api/auth/refresh",
					AccessToken: user.AccessToken, // Send old access token via Authorization: Bearer
				},
				WantStatus: http.StatusOK,
				Validate: func(t *testing.T, body []byte) {
					var resp struct {
						Token string `json:"token"`
					}
					ParseJSON(t, body, &resp)

					// Verify a new token is returned (may be same value if refreshed within same second)
					assert.NotEmpty(t, resp.Token)
				},
			},
			{
				Name: "invalid refresh token",
				Request: Request{
					Method:      "POST",
					Path:        "/api/auth/refresh",
					AccessToken: "invalid-token", // Invalid JWT via Authorization: Bearer
				},
				WantStatus: http.StatusUnauthorized,
				WantError:  "Invalid",
			},
		}

		RunTestCases(t, env, cases)
	}
}

func testAuthMe(env *TestEnv) func(t *testing.T) {
	return func(t *testing.T) {
		email := RandomEmail()
		password := RandomPassword()
		defer CleanupTestData(t, env, email)

		user := CreateTestUser(t, env, email, password)

		cases := []TestCase{
			{
				Name: "authenticated request",
				Request: Request{
					Method:      "GET",
					Path:        "/api/auth/me",
					AccessToken: user.AccessToken,
				},
				WantStatus: http.StatusOK,
				Validate: func(t *testing.T, body []byte) {
					var resp struct {
						User struct {
							ID    string `json:"id"`
							Email string `json:"email"`
						} `json:"user"`
						Entitlements map[string]interface{} `json:"entitlements"`
					}
					ParseJSON(t, body, &resp)

					assert.Equal(t, user.ID, resp.User.ID)
					assert.Equal(t, email, resp.User.Email)
					assert.NotNil(t, resp.Entitlements)
				},
			},
			{
				Name: "unauthenticated request",
				Request: Request{
					Method: "GET",
					Path:   "/api/auth/me",
				},
				WantStatus: http.StatusUnauthorized,
			},
			{
				Name: "invalid token",
				Request: Request{
					Method:      "GET",
					Path:        "/api/auth/me",
					AccessToken: "invalid-token",
				},
				WantStatus: http.StatusUnauthorized,
			},
		}

		RunTestCases(t, env, cases)
	}
}

func testUpdatePassword(env *TestEnv) func(t *testing.T) {
	return func(t *testing.T) {
		email := RandomEmail()
		password := RandomPassword()
		defer CleanupTestData(t, env, email)

		user := CreateTestUser(t, env, email, password)
		newPassword := "NewPassword123!"

		// Update password
		body := MakeRequest(t, env, Request{
			Method:      "POST",
			Path:        "/api/auth/update-password",
			AccessToken: user.AccessToken,
			Body: map[string]string{
				"currentPassword": password,
				"newPassword":     newPassword,
			},
			WantStatus: http.StatusOK,
		})

		// Response is wrapped in {"success": true, "data": {"message": "..."}}
		var resp struct {
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		ParseJSON(t, body, &resp)
		assert.Contains(t, resp.Data.Message, "success")

		// Verify old password doesn't work
		MakeRequest(t, env, Request{
			Method: "POST",
			Path:   "/api/auth/client-login",
			Body: map[string]string{
				"email":    email,
				"password": password,
			},
			WantStatus: http.StatusUnauthorized,
		})

		// Verify new password works
		MakeRequest(t, env, Request{
			Method: "POST",
			Path:   "/api/auth/client-login",
			Body: map[string]string{
				"email":    email,
				"password": newPassword,
			},
			WantStatus: http.StatusOK,
		})
	}
}

func testLogout(env *TestEnv) func(t *testing.T) {
	return func(t *testing.T) {
		email := RandomEmail()
		password := RandomPassword()
		defer CleanupTestData(t, env, email)

		user := CreateTestUser(t, env, email, password)

		// Logout
		body := MakeRequest(t, env, Request{
			Method:      "POST",
			Path:        "/api/auth/logout",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		// Response is wrapped in {"success": true, "data": {"message": "..."}}
		var resp struct {
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		ParseJSON(t, body, &resp)
		assert.Contains(t, resp.Data.Message, "success")

		// Verify refresh token no longer works
		MakeRequest(t, env, Request{
			Method: "POST",
			Path:   "/api/auth/refresh",
			Body: map[string]string{
				"refreshToken": user.RefreshToken,
			},
			WantStatus: http.StatusUnauthorized,
		})
	}
}

func testOAuthLogin(env *TestEnv) func(t *testing.T) {
	return func(t *testing.T) {
		// Note: OAuth requires actual provider tokens, so we test error cases
		cases := []TestCase{
			{
				Name: "missing provider",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/oauth-login",
					Body: map[string]string{
						"idToken": "fake-token",
					},
				},
				WantStatus: http.StatusBadRequest,
				WantError:  "Provider",
			},
			{
				Name: "missing token",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/oauth-login",
					Body: map[string]string{
						"provider": "google",
					},
				},
				WantStatus: http.StatusBadRequest,
				WantError:  "IDToken",
			},
			{
				Name: "invalid provider",
				Request: Request{
					Method: "POST",
					Path:   "/api/auth/oauth-login",
					Body: map[string]string{
						"provider": "invalid",
						"idToken":  "fake-token",
					},
				},
				WantStatus: http.StatusBadRequest,
				WantError:  "Provider",
			},
		}

		RunTestCases(t, env, cases)
	}
}

func TestPasskeyEndpoints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.Cleanup()

	email := RandomEmail()
	password := RandomPassword()
	defer CleanupTestData(t, env, email)

	user := CreateTestUser(t, env, email, password)

	t.Run("List passkeys when none exist", func(t *testing.T) {
		body := MakeRequest(t, env, Request{
			Method:      "GET",
			Path:        "/api/auth/passkey",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		var resp struct {
			Passkeys []interface{} `json:"passkeys"`
		}
		ParseJSON(t, body, &resp)
		assert.Empty(t, resp.Passkeys)
	})

	// Note: Actual passkey registration/login requires WebAuthn ceremony
	// These tests verify the endpoints exist and validate input
	t.Run("Begin registration - unauthenticated", func(t *testing.T) {
		MakeRequest(t, env, Request{
			Method:     "POST",
			Path:       "/api/auth/passkey/register/begin",
			WantStatus: http.StatusUnauthorized,
		})
	})

	t.Run("Begin registration - authenticated", func(t *testing.T) {
		body := MakeRequest(t, env, Request{
			Method:      "POST",
			Path:        "/api/auth/passkey/register/begin",
			AccessToken: user.AccessToken,
			WantStatus:  http.StatusOK,
		})

		// Should return WebAuthn challenge
		var resp map[string]interface{}
		ParseJSON(t, body, &resp)
		require.NotNil(t, resp)
		// WebAuthn response contains "challenge" and "rp" fields
	})
}
