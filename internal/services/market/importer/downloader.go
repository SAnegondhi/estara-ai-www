package importer

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	zillowBaseURL = "https://files.zillowstatic.com/research/public_csvs/"
	redfinBaseURL = "https://redfin-public-data.s3.us-west-2.amazonaws.com/redfin_market_tracker/"

	downloadTimeout = 10 * time.Minute
	maxCSVSize      = 500 * 1024 * 1024 // 500MB limit for in-memory CSV
)

// Zillow ZHVI URLs by level
func zillowZHVIURL(level string) (string, error) {
	switch level {
	case "metro":
		return zillowBaseURL + "zhvi/Metro_zhvi_uc_sfrcondo_tier_0.33_0.67_sm_sa_month.csv", nil
	case "city":
		return zillowBaseURL + "zhvi/City_zhvi_uc_sfrcondo_tier_0.33_0.67_sm_sa_month.csv", nil
	case "zip":
		return zillowBaseURL + "zhvi/Zip_zhvi_uc_sfrcondo_tier_0.33_0.67_sm_sa_month.csv", nil
	case "state":
		return zillowBaseURL + "zhvi/State_zhvi_uc_sfrcondo_tier_0.33_0.67_sm_sa_month.csv", nil
	default:
		return "", fmt.Errorf("unsupported ZHVI level: %s (expected metro, city, zip, state)", level)
	}
}

// Zillow ZORI URLs by level
func zillowZORIURL(level string) (string, error) {
	switch level {
	case "metro":
		return zillowBaseURL + "zori/Metro_zori_uc_sfrcondomfr_sm_month.csv", nil
	case "city":
		return zillowBaseURL + "zori/City_zori_uc_sfrcondomfr_sm_month.csv", nil
	case "zip":
		return zillowBaseURL + "zori/Zip_zori_uc_sfrcondomfr_sm_month.csv", nil
	default:
		return "", fmt.Errorf("unsupported ZORI level: %s (expected metro, city, zip)", level)
	}
}

// Redfin URLs by level
func redfinURL(level string) (string, error) {
	switch level {
	case "national":
		return redfinBaseURL + "us_national_market_tracker.tsv000.gz", nil
	case "state":
		return redfinBaseURL + "state_market_tracker.tsv000.gz", nil
	case "metro":
		return redfinBaseURL + "redfin_metro_market_tracker.tsv000.gz", nil
	case "county":
		return redfinBaseURL + "county_market_tracker.tsv000.gz", nil
	case "city":
		return redfinBaseURL + "city_market_tracker.tsv000.gz", nil
	case "zip":
		return redfinBaseURL + "zip_code_market_tracker.tsv000.gz", nil
	default:
		return "", fmt.Errorf("unsupported Redfin level: %s (expected national, state, metro, county, city, zip)", level)
	}
}

// downloadCSV downloads a CSV file from the given URL and returns the raw bytes.
// Suitable for Zillow CSVs which are <100MB uncompressed.
func downloadCSV(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Estara-AI-MarketData/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// Limit read size
	reader := io.LimitReader(resp.Body, maxCSVSize)

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return data, nil
}

// downloadGzipStream opens an HTTP connection and returns a gzip reader for streaming.
// Caller must close the returned reader and response body.
func downloadGzipStream(ctx context.Context, url string) (io.ReadCloser, *http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	// cancel is intentionally NOT deferred here — caller controls lifecycle via response.Body.Close()
	_ = cancel

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Estara-AI-MarketData/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// If URL ends with .gz, wrap in gzip reader
	if strings.HasSuffix(url, ".gz") {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			cancel()
			return nil, nil, fmt.Errorf("gzip reader: %w", err)
		}
		// Wrap to close both gzip and body
		return &gzipReadCloser{gz: gzReader, body: resp.Body, cancel: cancel}, resp, nil
	}

	return resp.Body, resp, nil
}

type gzipReadCloser struct {
	gz     *gzip.Reader
	body   io.ReadCloser
	cancel context.CancelFunc
}

func (g *gzipReadCloser) Read(p []byte) (n int, err error) {
	return g.gz.Read(p)
}

func (g *gzipReadCloser) Close() error {
	g.gz.Close()
	g.body.Close()
	g.cancel()
	return nil
}
