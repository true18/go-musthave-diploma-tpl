// Package accrual talks to the external loyalty accrual service.
package accrual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/true18/go-musthave-diploma-tpl/internal/money"
)

const (
	maxAttempts = 4
	firstDelay  = 100 * time.Millisecond
)

// External accrual statuses.
const (
	StatusRegistered = "REGISTERED"
	StatusProcessing = "PROCESSING"
	StatusInvalid    = "INVALID"
	StatusProcessed  = "PROCESSED"
)

// ErrRateLimited is returned when accrual service responds with 429.
var ErrRateLimited = errors.New("accrual rate limited")

// ErrNoAddress is returned when accrual address is empty.
var ErrNoAddress = errors.New("accrual system address is empty")

// ErrInvalidRetryAfter reports a malformed Retry-After header.
var ErrInvalidRetryAfter = errors.New("invalid Retry-After header")

// Result is the parsed response from the accrual service.
type Result struct {
	Order      string
	Status     string
	Accrual    *money.Amount
	NoContent  bool
	RetryAfter time.Duration
}

// Client fetches order accrual state over HTTP.
type Client struct {
	baseURL     string
	httpClient  *http.Client
	maxAttempts int
	firstDelay  time.Duration
}

// NewClient creates a Client.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient, maxAttempts: maxAttempts, firstDelay: firstDelay}
}

// Fetch returns accrual information for one order.
func (c *Client) Fetch(ctx context.Context, number string) (Result, error) {
	if c.baseURL == "" {
		return Result{}, ErrNoAddress
	}

	endpoint := c.baseURL + "/api/orders/" + url.PathEscape(number)
	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		result, retry, err := c.fetchOnce(ctx, endpoint)
		if !retry || err == nil {
			return result, err
		}
		lastErr = err
		if err := wait(ctx, c.firstDelay*time.Duration(1<<attempt)); err != nil {
			return Result{}, fmt.Errorf("wait before accrual retry: %w", err)
		}
	}
	return Result{}, fmt.Errorf("accrual request failed after %d attempts: %w", c.maxAttempts, lastErr)
}

func (c *Client) fetchOnce(ctx context.Context, endpoint string) (Result, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, false, fmt.Errorf("create accrual request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Result{}, false, fmt.Errorf("fetch accrual: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		defer resp.Body.Close()
		var result Result
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return Result{}, false, fmt.Errorf("decode accrual response: %w", err)
		}
		return result, false, nil
	case http.StatusNoContent:
		resp.Body.Close()
		return Result{NoContent: true}, false, nil
	case http.StatusTooManyRequests:
		defer resp.Body.Close()
		retryAfter, parseErr := parseRetryAfter(resp.Header.Get("Retry-After"))
		result := Result{RetryAfter: retryAfter}
		if parseErr != nil {
			return result, false, errors.Join(ErrRateLimited, parseErr)
		}
		return result, false, ErrRateLimited
	default:
		status := resp.StatusCode
		resp.Body.Close()
		if status >= http.StatusInternalServerError && status <= 599 {
			return Result{}, true, fmt.Errorf("accrual server returned status %d", status)
		}
		return Result{}, false, fmt.Errorf("unexpected accrual status: %d", status)
	}
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRetryAfter(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Second, nil
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return time.Second, fmt.Errorf("%w: %q", ErrInvalidRetryAfter, value)
		}
		return time.Duration(seconds) * time.Second, nil
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return time.Second, fmt.Errorf("%w: %q", ErrInvalidRetryAfter, value)
	}
	duration := time.Until(when)
	if duration <= 0 {
		return time.Second, nil
	}
	return duration, nil
}
