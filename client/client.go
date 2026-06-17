// Package client provides an official Go client for the rate-limiter-service.
//
// Usage:
//
//	client := client.New("http://localhost:8080")
//	resp, err := client.Check(ctx, client.CheckRequest{
//	    Key: "user:123",
//	    MaxTokens: 100,
//	    WindowSeconds: 60,
//	})
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/crypto/rate-limiter-service/limiter"
)

// Client is the HTTP client for the rate limiter service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new Client.
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// CheckRequest mirrors the service request.
type CheckRequest = limiter.CheckRequest

// CheckResponse mirrors the service response.
type CheckResponse = limiter.CheckResponse

// Check performs a rate limit check.
func (c *Client) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/check", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("check failed: %s (status %d)", string(body), resp.StatusCode)
	}

	var result CheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// VisualizeRequest for visualization.
type VisualizeRequest struct {
	Key           string            `json:"key"`
	Algorithm     string            `json:"algorithm"`
	MaxTokens     uint32            `json:"max_tokens"`
	WindowSeconds uint32            `json:"window_seconds"`
	IncludeHistory bool             `json:"include_history,omitempty"`
}

// Visualize gets visualization data.
func (c *Client) Visualize(ctx context.Context, req VisualizeRequest) (map[string]interface{}, error) {
	// For simplicity, use query params as the service does for GET
	url := fmt.Sprintf("%s/v1/visualize?key=%s&algorithm=%s&max_tokens=%d&window_seconds=%d&include_history=%t",
		c.baseURL, req.Key, req.Algorithm, req.MaxTokens, req.WindowSeconds, req.IncludeHistory)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// SimulateRequest for what-if simulation.
type SimulateRequest struct {
	Key           string   `json:"key"`
	MaxTokens     uint32   `json:"max_tokens"`
	WindowSeconds uint32   `json:"window_seconds"`
	Algorithm     string   `json:"algorithm"`
	Costs         []uint32 `json:"costs"`
}

// Simulate runs a simulation.
func (c *Client) Simulate(ctx context.Context, req SimulateRequest) (map[string]interface{}, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/simulate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// Replicate emits a replication event.
func (c *Client) Replicate(ctx context.Context, ev map[string]interface{}) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/replicate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("replicate failed: %s", string(body))
	}
	return nil
}