package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisputeResponse_JSON(t *testing.T) {
	t.Parallel()

	evidenceDue := "2026-02-20T00:00:00Z"
	resp := DisputeResponse{
		ID:            "dp_abc123",
		Amount:        9999,
		Currency:      "usd",
		Reason:        "fraudulent",
		Status:        "needs_response",
		Created:       "2026-02-10T12:00:00Z",
		EvidenceDueBy: &evidenceDue,
		HasEvidence:   false,
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded DisputeResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "dp_abc123", decoded.ID)
	assert.Equal(t, int64(9999), decoded.Amount)
	assert.Equal(t, "fraudulent", decoded.Reason)
	assert.Equal(t, "needs_response", decoded.Status)
	assert.NotNil(t, decoded.EvidenceDueBy)
	assert.False(t, decoded.HasEvidence)
}

func TestDisputeResponse_JSON_NilEvidenceDue(t *testing.T) {
	t.Parallel()

	resp := DisputeResponse{
		ID:       "dp_xyz789",
		Amount:   5000,
		Currency: "usd",
		Reason:   "product_not_received",
		Status:   "won",
		Created:  "2026-01-15T00:00:00Z",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	// omitempty should omit nil evidenceDueBy
	assert.NotContains(t, string(data), `"evidenceDueBy"`)
}

func TestDisputeMetricsResponse_JSON(t *testing.T) {
	t.Parallel()

	metrics := DisputeMetricsResponse{
		OpenDisputes:    5,
		TotalDisputes:   12,
		WonDisputes:     5,
		LostDisputes:    2,
		AmountAtRisk:    45000,
		WinRate:         60.0,
		ChargebackRate:  0.35,
		EvidenceDueSoon: 2,
	}

	data, err := json.Marshal(metrics)
	require.NoError(t, err)

	var decoded DisputeMetricsResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 5, decoded.OpenDisputes)
	assert.Equal(t, int64(45000), decoded.AmountAtRisk)
	assert.Equal(t, 60.0, decoded.WinRate)
	assert.Equal(t, 0.35, decoded.ChargebackRate)
	assert.Equal(t, 2, decoded.EvidenceDueSoon)
}

func TestSubmitEvidenceRequest_JSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		json        string
		expectValid bool
	}{
		{
			name:        "valid with evidence",
			json:        `{"evidence":{"customerEmail":"user@example.com","description":"Customer received service"}}`,
			expectValid: true,
		},
		{
			name:        "valid minimal",
			json:        `{"evidence":{}}`,
			expectValid: true,
		},
		{
			name:        "invalid json",
			json:        `{invalid}`,
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req SubmitEvidenceRequest
			err := json.Unmarshal([]byte(tt.json), &req)
			if tt.expectValid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
