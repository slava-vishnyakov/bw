// Package example demonstrates idiomatic Go patterns from the go-kata skill
package example

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// APIClient demonstrates HTTP client hygiene (Rule 4.1-4.4)
type APIClient struct {
	client  *http.Client
	limiter *rate.Limiter
	logger  *slog.Logger
}

// Option demonstrates functional options pattern (Rule 7.1)
type Option func(*APIClient)

// WithTimeout configures request timeout
func WithTimeout(d time.Duration) Option {
	return func(c *APIClient) {
		c.client.Timeout = d
	}
}

// WithRateLimit configures rate limiting
func WithRateLimit(rps float64, burst int) Option {
	return func(c *APIClient) {
		c.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}
}

// WithLogger configures structured logging
func WithLogger(l *slog.Logger) Option {
	return func(c *APIClient) {
		c.logger = l
	}
}

// NewAPIClient creates a properly configured HTTP client
func NewAPIClient(opts ...Option) *APIClient {
	client := &APIClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
		limiter: rate.NewLimiter(rate.Limit(10), 20),
		logger:  slog.Default(),
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// FetchJSON demonstrates context-first API, error wrapping, and HTTP hygiene (Rules 1.1, 4.2-4.3, 5.1)
func (c *APIClient) FetchJSON(ctx context.Context, url string) ([]byte, error) {
	// Rule 2.1: Rate limiting with x/time/rate
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}

	// Rule 4.2: Use NewRequestWithContext
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close() // Rule 4.3: Always close body

	// Rule 10.1: Structured logging
	c.logger.Info("request completed",
		"url", url,
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	if resp.StatusCode != http.StatusOK {
		// Rule 4.3: Drain body for connection reuse
		io.CopyN(io.Discard, resp.Body, 512)
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return body, nil
}

// FetchAllConcurrent demonstrates errgroup, fail-fast, and bounded concurrency (Rules 1.2, 1.3, 2.2)
func (c *APIClient) FetchAllConcurrent(ctx context.Context, urls []string) (map[string][]byte, error) {
	// Rule 1.2: Use errgroup, not sync.WaitGroup
	g, ctx := errgroup.WithContext(ctx)
	results := make(map[string][]byte)
	var mu sync.Mutex

	for _, url := range urls {
		url := url // Rule 9.2: Capture loop variable
		g.Go(func() error {
			body, err := c.FetchJSON(ctx, url)
			if err != nil {
				return err // Rule 1.3: Fail-fast - returns first error
			}

			mu.Lock()
			results[url] = body
			mu.Unlock()
			return nil
		})
	}

	// Wait for all goroutines to complete or first error
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("fetch all: %w", err)
	}

	return results, nil
}

// DataPipeline demonstrates context-aware channel sender (Rule 1.5, 1.6)
type DataPipeline struct {
	logger *slog.Logger
}

// Result holds pipeline result
type Result struct {
	URL  string
	Data []byte
	Err  error
}

// ProcessURLs demonstrates proper channel coordination and goroutine leak prevention
func (p *DataPipeline) ProcessURLs(ctx context.Context, urls []string) <-chan Result {
	out := make(chan Result)

	go func() {
		defer close(out) // Rule 1.6: Only sender closes channel

		for _, url := range urls {
			// Rule 1.7: Check context before doing work
			select {
			case <-ctx.Done():
				return
			default:
			}

			result := Result{URL: url}
			// Simulate processing
			result.Data = []byte(url)

			// Rule 1.5: Select on every send
			select {
			case out <- result:
				// Sent successfully
			case <-ctx.Done():
				return // Exit immediately on cancellation
			}
		}
	}()

	return out
}

// CustomError demonstrates typed errors (Rule 5.2)
type APIError struct {
	Op         string
	StatusCode int
	Err        error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error during %s: status=%d: %v", e.Op, e.StatusCode, e.Err)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// Temporary indicates if error is temporary
func (e *APIError) Temporary() bool {
	return e.StatusCode == 429 || e.StatusCode >= 500
}

// RetryableRequest demonstrates context-aware retry with timer (Rule 5.5, 5.6)
func RetryableRequest(ctx context.Context, maxAttempts int, fn func(context.Context) error) error {
	var lastErr error
	backoff := 100 * time.Millisecond

	// Rule 5.5: Use Timer, not Sleep
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	for attempt := 0; attempt < maxAttempts; attempt++ {
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}

		// Rule 5.6: Classify errors
		if !isRetryable(lastErr) {
			return fmt.Errorf("non-retryable error (attempt %d/%d): %w",
				attempt+1, maxAttempts, lastErr)
		}

		if attempt < maxAttempts-1 {
			select {
			case <-timer.C:
				backoff *= 2
				if backoff > 5*time.Second {
					backoff = 5 * time.Second
				}
				timer.Reset(backoff)
			case <-ctx.Done():
				return fmt.Errorf("retry canceled after %d attempts: %w", attempt+1, ctx.Err())
			}
		}
	}

	return fmt.Errorf("max attempts (%d) reached: %w", maxAttempts, lastErr)
}

// isRetryable checks if error should be retried
func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Temporary()
	}

	return false
}

// CleanupExample demonstrates defer cleanup with named returns (Rule 5.7)
func CleanupExample(filename string) (err error) {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}

	// Rule 5.7: Defer cleanup immediately with named return
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			// Rule 5.4: Aggregate errors with errors.Join
			err = errors.Join(err, fmt.Errorf("close file: %w", closeErr))
		}
	}()

	// Process file...
	return nil
}

// AvoidTypedNilExample demonstrates the typed nil trap fix (Rule 5.3)
func AvoidTypedNilExample(shouldError bool) error {
	var err *APIError
	if shouldError {
		err = &APIError{Op: "example"}
	}

	// Rule 5.3: Return literal nil, not typed nil
	if err != nil {
		return err
	}
	return nil // Explicit nil return
}
