// Package cronjoborgclient provides a REST API client for cron-job.org.
// Authentication uses CRON_API_KEY as a Bearer token (the cron-job.org account
// API key — distinct from CRON_SECRET which authenticates inbound cron requests).
package cronjoborgclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://api.cron-job.org"

// CronOrgJob represents a job entry returned by the cron-job.org API.
type CronOrgJob struct {
	JobID   int64  `json:"jobId"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Title   string `json:"title"`
}

// Client is the cron-job.org API client.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// New creates a new cron-job.org client. apiKey is the Bearer token
// (maps to CRON_API_KEY in .env.local).
func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// ListJobs returns all jobs registered on cron-job.org for this account.
func (c *Client) ListJobs() ([]CronOrgJob, error) {
	resp, err := c.do(http.MethodGet, "/jobs", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError(resp)
	}

	var result struct {
		Jobs []CronOrgJob `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode list jobs response: %w", err)
	}
	return result.Jobs, nil
}

// createJobPayload is the request body for PUT /jobs.
type createJobPayload struct {
	Job createJobBody `json:"job"`
}

type createJobBody struct {
	URL           string      `json:"url"`
	Enabled       bool        `json:"enabled"`
	SaveResponses bool        `json:"saveResponses"`
	Schedule      JobSchedule `json:"schedule"`
	RequestMethod int         `json:"requestMethod"`
	Title         string      `json:"title"`
	ExtendedData  extData     `json:"extendedData"`
}

type extData struct {
	Headers map[string]string `json:"headers"`
}

// createJobResponse is the response body from PUT /jobs.
type createJobResponse struct {
	JobID int64 `json:"jobId"`
}

// CreateJob creates a new job on cron-job.org. It is always created disabled.
// Returns the external jobId assigned by cron-job.org.
func (c *Client) CreateJob(url string, schedule JobSchedule, title, secret string) (int64, error) {
	payload := createJobPayload{
		Job: createJobBody{
			URL:           url,
			Enabled:       false, // always disabled on creation — admin must explicitly enable
			SaveResponses: false,
			Schedule:      schedule,
			RequestMethod: 1, // POST
			Title:         title,
			ExtendedData: extData{
				Headers: map[string]string{"X-Cron-Secret": secret},
			},
		},
	}

	resp, err := c.do(http.MethodPut, "/jobs", payload)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return 0, c.apiError(resp)
	}

	var result createJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode create job response: %w", err)
	}
	if result.JobID == 0 {
		return 0, fmt.Errorf("cron-job.org returned zero jobId")
	}
	return result.JobID, nil
}

// UpdateJob enables or disables an existing job on cron-job.org.
func (c *Client) UpdateJob(jobID int64, enabled bool) error {
	payload := map[string]interface{}{
		"job": map[string]interface{}{
			"enabled": enabled,
		},
	}

	resp, err := c.do(http.MethodPatch, fmt.Sprintf("/jobs/%d", jobID), payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError(resp)
	}
	return nil
}

// DeleteJob removes a job from cron-job.org.
func (c *Client) DeleteJob(jobID int64) error {
	resp, err := c.do(http.MethodDelete, fmt.Sprintf("/jobs/%d", jobID), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError(resp)
	}
	return nil
}

// do executes an authenticated HTTP request against the cron-job.org API.
func (c *Client) do(method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// apiError reads the error body and returns a descriptive error.
func (c *Client) apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("cron-job.org API error %d: %s", resp.StatusCode, string(body))
}
