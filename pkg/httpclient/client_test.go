package httpclient_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/httpclient"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPClient(t *testing.T) {
	t.Run("Get and Custom Headers", func(t *testing.T) {
		var receivedUA string
		var receivedHeader string

		client := httpclient.New(httpclient.Config{
			Timeout:    5 * time.Second,
			UserAgent:  "custom-test-agent",
			MaxRetries: 1,
		})

		client.SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			receivedUA = req.Header.Get("User-Agent")
			receivedHeader = req.Header.Get("X-Account-ID")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
				Header:     make(http.Header),
			}, nil
		}))

		resp, err := client.Get(context.Background(), "https://api.example.com/health", map[string]string{
			"X-Account-ID": "99",
		})
		if err != nil {
			t.Fatalf("get request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", resp.StatusCode)
		}
		if receivedUA != "custom-test-agent" {
			t.Errorf("expected User-Agent custom-test-agent, got %s", receivedUA)
		}
		if receivedHeader != "99" {
			t.Errorf("expected X-Account-ID 99, got %s", receivedHeader)
		}
	})

	t.Run("Retry on 5xx Errors", func(t *testing.T) {
		var attempts int32

		client := httpclient.New(httpclient.Config{
			Timeout:      2 * time.Second,
			MaxRetries:   2,
			RetryWaitMin: 10 * time.Millisecond,
			RetryWaitMax: 50 * time.Millisecond,
		})

		client.SetTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			count := atomic.AddInt32(&attempts, 1)
			if count < 3 {
				// Return 503 on first two attempts
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader("upstream error")),
					Header:     make(http.Header),
				}, nil
			}
			// Return 200 on 3rd attempt
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"result":"success"}`)),
				Header:     make(http.Header),
			}, nil
		}))

		resp, err := client.PostJSON(context.Background(), "https://api.example.com/messages", map[string]string{"msg": "hi"}, nil)
		if err != nil {
			t.Fatalf("postJSON request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK after retries, got %d", resp.StatusCode)
		}
		if atomic.LoadInt32(&attempts) != 3 {
			t.Errorf("expected 3 total attempts, got %d", attempts)
		}
	})
}
