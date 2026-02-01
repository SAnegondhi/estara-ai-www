package pdf

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// QuickChartClient renders Chart.js configs to PNG via quickchart.io.
type QuickChartClient struct {
	HTTP *http.Client
}

// NewQuickChartClient returns a QuickChart client with sane defaults.
func NewQuickChartClient() *QuickChartClient {
	return &QuickChartClient{
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
}

// RenderPNG renders a Chart.js config to PNG bytes.
func (c *QuickChartClient) RenderPNG(ctx context.Context, config map[string]interface{}, width, height int, background string) ([]byte, error) {
	if c == nil {
		c = NewQuickChartClient()
	}

	payload, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	bg := background
	if bg == "" {
		bg = "#FFFFFF"
	}

	query := url.Values{}
	query.Set("width", fmt.Sprintf("%d", width))
	query.Set("height", fmt.Sprintf("%d", height))
	query.Set("backgroundColor", bg)
	query.Set("c", string(payload))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://quickchart.io/chart?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("quickchart error %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// EncodePNGBase64 returns a data URI for a PNG byte slice.
func EncodePNGBase64(png []byte) string {
	if len(png) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}
