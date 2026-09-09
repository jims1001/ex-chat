package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Config defines settings for resilient HTTP client
type Config struct {
	Timeout      time.Duration
	MaxRetries   int
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	UserAgent    string
}

// DefaultConfig provides standard sensible settings
func DefaultConfig() Config {
	return Config{
		Timeout:      30 * time.Second,
		MaxRetries:   2,
		RetryWaitMin: 50 * time.Millisecond,
		RetryWaitMax: 500 * time.Millisecond,
		UserAgent:    "ex-chat/1.0 (+https://github.com/OracleBetX-Projects/ex-chat)",
	}
}

// Client wraps an *http.Client with automatic retry and timeout
type Client struct {
	httpClient *http.Client
	cfg        Config
}

// New creates a Client with custom configuration
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultConfig().UserAgent
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		cfg: cfg,
	}
}

// NewDefault creates a Client with default configuration
func NewDefault() *Client {
	return New(DefaultConfig())
}

// SetTransport allows injecting a custom RoundTripper (e.g. for testing)
func (c *Client) SetTransport(rt http.RoundTripper) {
	c.httpClient.Transport = rt
}

// Do executes an HTTP request with retry logic for network errors and 5xx responses
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.cfg.UserAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}

	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to buffer request body: %w", err)
		}
		_ = req.Body.Close()
	}

	var lastErr error
	var resp *http.Response

	retries := c.cfg.MaxRetries
	if retries < 0 {
		retries = 0
	}

	wait := c.cfg.RetryWaitMin
	if wait <= 0 {
		wait = 50 * time.Millisecond
	}

	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(wait):
			}
			wait *= 2
			if c.cfg.RetryWaitMax > 0 && wait > c.cfg.RetryWaitMax {
				wait = c.cfg.RetryWaitMax
			}
		}

		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, lastErr = c.httpClient.Do(req)
		if lastErr == nil && resp.StatusCode < 500 {
			return resp, nil
		}

		// If status is >= 500, close body and retry if attempts remain
		if resp != nil && attempt < retries {
			_ = resp.Body.Close()
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("http request failed after %d retries: %w", retries, lastErr)
	}
	return resp, nil
}

// Get performs an HTTP GET request
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.Do(req)
}

// PostJSON performs an HTTP POST request with a JSON payload
func (c *Client) PostJSON(ctx context.Context, url string, payload any, headers map[string]string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal json payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.Do(req)
}
