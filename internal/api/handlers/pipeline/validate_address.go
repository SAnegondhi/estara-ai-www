package pipeline

// ValidateAddress handles POST /api/pipeline/validate-address
// ADR-112: 3-tier address validation for off-market properties.
// Tier 2: calls Geocodio to normalize and geocode the address.
// Returns normalized address + lat/lng on success, or advisory message on miss.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/estara-ai/www/pkg/httputil"
)

type validateAddressRequest struct {
	Address string `json:"address"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
}

type validateAddressResponse struct {
	// verified is true when Geocodio returned a high-confidence match.
	Verified bool `json:"verified"`
	// Message is shown to the user: "Address verified" or advisory text.
	Message string `json:"message"`
	// NormalizedAddress is the formatted address from Geocodio (if found).
	NormalizedAddress string `json:"normalizedAddress,omitempty"`
	// City, State, Zip — auto-fill candidates populated from geocoding result.
	City  string `json:"city,omitempty"`
	State string `json:"state,omitempty"`
	Zip   string `json:"zip,omitempty"`
	// Lat/Lng — stored on property for proximity queries.
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

// geocodioResponse is a minimal parsing of the Geocodio API response.
type geocodioResponse struct {
	Results []struct {
		FormattedAddress string `json:"formatted_address"`
		AddressComponents struct {
			Number     string `json:"number"`
			Street     string `json:"formatted_street"`
			City       string `json:"city"`
			State      string `json:"state"`
			Zip        string `json:"zip"`
		} `json:"address_components"`
		Location struct {
			Lat float64 `json:"lat"`
			Lng float64 `json:"lng"`
		} `json:"location"`
		Accuracy      float64 `json:"accuracy"`
		AccuracyType  string  `json:"accuracy_type"`
	} `json:"results"`
}

// ValidateAddress calls Geocodio to verify and geocode a property address.
func (h *Handler) ValidateAddress(w http.ResponseWriter, r *http.Request) {
	_, ok := getUserID(r)
	if !ok {
		httputil.Unauthorized(w, "not authenticated")
		return
	}

	var req validateAddressRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Address == "" {
		httputil.BadRequest(w, "address is required")
		return
	}

	apiKey := h.cfg.Market.GeocodioAPIKey
	if apiKey == "" {
		// No API key configured — return unverified advisory.
		httputil.Success(w, validateAddressResponse{
			Verified: false,
			Message:  "Address validation unavailable — geocoding not configured.",
		})
		return
	}

	// Build the query string: combine address parts.
	query := req.Address
	if req.City != "" {
		query += ", " + req.City
	}
	if req.State != "" {
		query += ", " + req.State
	}
	if req.Zip != "" {
		query += " " + req.Zip
	}

	geocodioURL := fmt.Sprintf(
		"https://api.geocod.io/v1.7/geocode?q=%s&api_key=%s&limit=1",
		url.QueryEscape(query),
		url.QueryEscape(apiKey),
	)

	resp, err := http.Get(geocodioURL) //nolint:noctx // acceptable for a simple geocoding call
	if err != nil {
		h.logger.Warn("Geocodio request failed", "error", err)
		httputil.Success(w, validateAddressResponse{
			Verified: false,
			Message:  "Address lookup unavailable — network error.",
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		h.logger.Warn("Geocodio response error", "status", resp.StatusCode)
		httputil.Success(w, validateAddressResponse{
			Verified: false,
			Message:  "Address not found in public records — expected for off-market properties.",
		})
		return
	}

	var geo geocodioResponse
	if err := json.Unmarshal(body, &geo); err != nil || len(geo.Results) == 0 {
		httputil.Success(w, validateAddressResponse{
			Verified: false,
			Message:  "Address not found in public records — expected for off-market properties.",
		})
		return
	}

	result := geo.Results[0]
	// Accept accuracy >= 0.8 (rooftop, range_interpolation, etc.)
	if result.Accuracy < 0.8 {
		httputil.Success(w, validateAddressResponse{
			Verified: false,
			Message:  "Address not found in public records — expected for off-market properties.",
		})
		return
	}

	lat := result.Location.Lat
	lng := result.Location.Lng
	httputil.Success(w, validateAddressResponse{
		Verified:          true,
		Message:           "Address verified",
		NormalizedAddress: result.FormattedAddress,
		City:              result.AddressComponents.City,
		State:             result.AddressComponents.State,
		Zip:               result.AddressComponents.Zip,
		Latitude:          &lat,
		Longitude:         &lng,
	})
}
