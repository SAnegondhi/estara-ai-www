package public

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContactSubmissionRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     ContactSubmissionRequest
		expectValid bool
	}{
		{
			name: "valid request",
			request: ContactSubmissionRequest{
				Name:    "John Doe",
				Email:   "john@example.com",
				Message: "Hello, I have a question.",
			},
			expectValid: true,
		},
		{
			name: "missing name",
			request: ContactSubmissionRequest{
				Email:   "john@example.com",
				Message: "Hello",
			},
			expectValid: false,
		},
		{
			name: "missing email",
			request: ContactSubmissionRequest{
				Name:    "John Doe",
				Message: "Hello",
			},
			expectValid: false,
		},
		{
			name: "missing message",
			request: ContactSubmissionRequest{
				Name:  "John Doe",
				Email: "john@example.com",
			},
			expectValid: false,
		},
		{
			name: "all optional fields provided",
			request: ContactSubmissionRequest{
				Name:    "John Doe",
				Email:   "john@example.com",
				Phone:   "555-1234",
				Subject: "Question",
				Message: "Hello, I have a question.",
				Source:  "website",
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate required fields
			isValid := tt.request.Name != "" && tt.request.Email != "" && tt.request.Message != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v", tt.expectValid, isValid)
			}
		})
	}
}

func TestEarlyAccessRequest_Validation(t *testing.T) {
	tests := []struct {
		name        string
		request     EarlyAccessRequest
		expectValid bool
	}{
		{
			name: "valid request",
			request: EarlyAccessRequest{
				Email: "john@example.com",
			},
			expectValid: true,
		},
		{
			name: "missing email",
			request: EarlyAccessRequest{
				Name: "John Doe",
			},
			expectValid: false,
		},
		{
			name: "with optional fields",
			request: EarlyAccessRequest{
				Email:  "john@example.com",
				Name:   "John Doe",
				Source: "referral",
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.request.Email != ""
			if isValid != tt.expectValid {
				t.Errorf("expected valid=%v, got valid=%v", tt.expectValid, isValid)
			}
		})
	}
}

func TestContactSubmissionResponse_JSON(t *testing.T) {
	resp := ContactSubmissionResponse{
		Success: true,
		Message: "Thank you for contacting us.",
		ID:      "test-id-123",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var decoded ContactSubmissionResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if decoded.Success != resp.Success {
		t.Errorf("expected success=%v, got success=%v", resp.Success, decoded.Success)
	}
	if decoded.Message != resp.Message {
		t.Errorf("expected message=%s, got message=%s", resp.Message, decoded.Message)
	}
	if decoded.ID != resp.ID {
		t.Errorf("expected id=%s, got id=%s", resp.ID, decoded.ID)
	}
}

func TestEarlyAccessResponse_JSON(t *testing.T) {
	resp := EarlyAccessResponse{
		Success:  true,
		Message:  "You have been added to the waitlist!",
		Position: 42,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var decoded EarlyAccessResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if decoded.Success != resp.Success {
		t.Errorf("expected success=%v, got success=%v", resp.Success, decoded.Success)
	}
	if decoded.Position != resp.Position {
		t.Errorf("expected position=%d, got position=%d", resp.Position, decoded.Position)
	}
}

// TestHTTPRequestParsing tests that HTTP request bodies can be properly parsed
func TestHTTPRequestParsing(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		expectParseErr bool
	}{
		{
			name:           "valid JSON",
			body:           `{"name":"John","email":"john@example.com","message":"Hello"}`,
			expectParseErr: false,
		},
		{
			name:           "invalid JSON",
			body:           `{"name":"John",}`,
			expectParseErr: true,
		},
		{
			name:           "empty body",
			body:           "",
			expectParseErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/public/contact", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			var parsed ContactSubmissionRequest
			err := json.NewDecoder(req.Body).Decode(&parsed)

			if tt.expectParseErr && err == nil {
				t.Error("expected parse error but got none")
			}
			if !tt.expectParseErr && err != nil {
				t.Errorf("unexpected parse error: %v", err)
			}
		})
	}
}
