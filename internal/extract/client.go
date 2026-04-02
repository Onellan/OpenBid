package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}
type Result struct {
	Excerpt string            `json:"excerpt"`
	Facts   map[string]string `json:"facts"`
	Type    string            `json:"type"`
}

func New(base string) *Client {
	return &Client{BaseURL: base, HTTP: &http.Client{Timeout: 90 * time.Second}}
}
func (c *Client) Extract(ctx context.Context, url string) (Result, error) {
	payload, _ := json.Marshal(map[string]string{"url": url})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/extract", bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("extractor returned %d", resp.StatusCode)
	}
	var out Result
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Result{}, err
	}
	return out, nil
}
