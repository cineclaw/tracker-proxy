package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	endpoint   string
	httpClient *http.Client
}

type Request struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout,omitempty"`
}

type Response struct {
	Status         string   `json:"status"`
	Message        string   `json:"message"`
	StartTimestamp int64    `json:"startTimestamp"`
	EndTimestamp   int64    `json:"endTimestamp"`
	Version        string   `json:"version"`
	Solution       Solution `json:"solution"`
}

type Solution struct {
	URL       string   `json:"url"`
	Status    int      `json:"status"`
	Cookies   []Cookie `json:"cookies"`
	UserAgent string   `json:"userAgent"`
	Response  string   `json:"response"`
}

type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expiry   float64 `json:"expiry"`
	HttpOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
}

func NewClient(endpoint string, timeoutSeconds int) *Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutSeconds) * time.Second,
		},
	}
}

func (c *Client) Solve(ctx context.Context, targetURL string) (*Solution, error) {
	if c == nil || c.endpoint == "" {
		return nil, fmt.Errorf("flaresolverr client not configured")
	}

	reqPayload := Request{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: 60000,
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal flaresolverr request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create flaresolverr request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("flaresolverr request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("flaresolverr returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var res Response
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode flaresolverr response: %w", err)
	}

	if res.Status != "ok" {
		return nil, fmt.Errorf("flaresolverr error: %s", res.Message)
	}

	return &res.Solution, nil
}
