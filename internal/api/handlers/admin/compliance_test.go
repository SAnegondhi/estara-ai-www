package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsentResponse_JSON(t *testing.T) {
	t.Parallel()

	email := "test@example.com"
	revokedAt := "2026-02-10T00:00:00Z"
	ipAddr := "192.168.1.1"

	resp := ConsentResponse{
		ID:          "consent-123",
		UserID:      "user-456",
		Email:       &email,
		ConsentType: "terms_of_service",
		Version:     "1.0",
		GrantedAt:   "2026-01-01T00:00:00Z",
		RevokedAt:   &revokedAt,
		IPAddress:   &ipAddr,
		CreatedAt:   "2026-01-01T00:00:00Z",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded ConsentResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "terms_of_service", decoded.ConsentType)
	assert.NotNil(t, decoded.RevokedAt)
	assert.NotNil(t, decoded.IPAddress)
	assert.Equal(t, "192.168.1.1", *decoded.IPAddress)
}

func TestConsentResponse_JSON_OmitsNilFields(t *testing.T) {
	t.Parallel()

	resp := ConsentResponse{
		ID:          "consent-789",
		UserID:      "user-abc",
		ConsentType: "privacy_policy",
		Version:     "2.0",
		GrantedAt:   "2026-02-01T00:00:00Z",
		CreatedAt:   "2026-02-01T00:00:00Z",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	assert.NotContains(t, string(data), `"revokedAt"`)
	assert.NotContains(t, string(data), `"email"`)
	assert.NotContains(t, string(data), `"ipAddress"`)
}

func TestConsentSummaryResponse_JSON(t *testing.T) {
	t.Parallel()

	summary := ConsentSummaryResponse{
		ConsentType:  "terms_of_service",
		ActiveCount:  450,
		RevokedCount: 12,
		TotalCount:   462,
		AdoptionRate: 97.4,
	}

	data, err := json.Marshal(summary)
	require.NoError(t, err)

	var decoded ConsentSummaryResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, int64(450), decoded.ActiveCount)
	assert.Equal(t, 97.4, decoded.AdoptionRate)
}

func TestTermsLogEntry_JSON(t *testing.T) {
	t.Parallel()

	email := "user@example.com"
	ipAddr := "10.0.0.1"

	entry := TermsLogEntry{
		ID:         "terms-123",
		UserID:     "user-456",
		Email:      &email,
		Version:    "2.1",
		AcceptedAt: "2026-02-14T12:00:00Z",
		IPAddress:  &ipAddr,
	}

	data, err := json.Marshal(entry)
	require.NoError(t, err)

	var decoded TermsLogEntry
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "2.1", decoded.Version)
	assert.NotNil(t, decoded.Email)
	assert.Equal(t, "user@example.com", *decoded.Email)
}

func TestTaxSummaryItem_JSON(t *testing.T) {
	t.Parallel()

	item := TaxSummaryItem{
		State:        "CA",
		TotalTax:     1234.56,
		InvoiceCount: 45,
		TotalRevenue: 15000.00,
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	var decoded TaxSummaryItem
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "CA", decoded.State)
	assert.Equal(t, 1234.56, decoded.TotalTax)
	assert.Equal(t, int64(45), decoded.InvoiceCount)
}

func TestConsentType_Validation(t *testing.T) {
	t.Parallel()

	validTypes := []string{
		"terms_of_service",
		"privacy_policy",
		"marketing_emails",
		"analytics_tracking",
	}

	for _, ct := range validTypes {
		resp := ConsentResponse{ConsentType: ct}
		assert.NotEmpty(t, resp.ConsentType)
	}
}
