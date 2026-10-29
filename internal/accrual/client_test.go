package accrual

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClientFetchStatuses(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		wantStatus  string
		wantAccrual bool
	}{
		{name: "registered", status: StatusRegistered, wantStatus: StatusRegistered},
		{name: "processing", status: StatusProcessing, wantStatus: StatusProcessing},
		{name: "invalid", status: StatusInvalid, wantStatus: StatusInvalid},
		{name: "processed", status: StatusProcessed, wantStatus: StatusProcessed, wantAccrual: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if tt.wantAccrual {
					return jsonResponse(http.StatusOK, `{"order":"12345678903","status":"`+tt.status+`","accrual":500}`), nil
				}
				return jsonResponse(http.StatusOK, `{"order":"12345678903","status":"`+tt.status+`"}`), nil
			})

			result, err := NewClient("http://accrual.test", &http.Client{Transport: transport}).Fetch(context.Background(), "12345678903")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", result.Status, tt.wantStatus)
			}
			if (result.Accrual != nil) != tt.wantAccrual {
				t.Fatalf("accrual presence = %v, want %v", result.Accrual != nil, tt.wantAccrual)
			}
		})
	}
}

func TestClientFetchNoContent(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusNoContent, ""), nil
	})

	result, err := NewClient("http://accrual.test", &http.Client{Transport: transport}).Fetch(context.Background(), "12345678903")
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoContent {
		t.Fatal("expected NoContent result")
	}
}

func TestClientFetchRateLimited(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp := response(http.StatusTooManyRequests, "")
		resp.Header.Set("Retry-After", "2")
		return resp, nil
	})

	result, err := NewClient("http://accrual.test", &http.Client{Transport: transport}).Fetch(context.Background(), "12345678903")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if result.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %v", result.RetryAfter)
	}
}

func TestClientFetchNoAddress(t *testing.T) {
	_, err := NewClient("", nil).Fetch(context.Background(), "12345678903")
	if !errors.Is(err, ErrNoAddress) {
		t.Fatalf("err = %v, want ErrNoAddress", err)
	}
}

func TestClientFetchUnexpectedStatus(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusInternalServerError, ""), nil
	})

	client := NewClient("http://accrual.test", &http.Client{Transport: transport})
	client.firstDelay = 0
	_, err := client.Fetch(context.Background(), "12345678903")
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != maxAttempts {
		t.Fatalf("calls = %d, want %d", calls, maxAttempts)
	}
}

func TestClientRetriesTemporaryError(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(http.StatusInternalServerError, ""), nil
		}
		return jsonResponse(http.StatusOK, `{"order":"12345678903","status":"PROCESSED","accrual":500}`), nil
	})
	client := NewClient("http://accrual.test", &http.Client{Transport: transport})
	client.firstDelay = 0
	result, err := client.Fetch(context.Background(), "12345678903")
	if err != nil || result.Status != StatusProcessed || calls != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestClientRetryStopsOnContextCancellation(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, ""), nil
	})
	client := NewClient("http://accrual.test", &http.Client{Transport: transport})
	client.firstDelay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Fetch(ctx, "12345678903")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
}

func TestClientDoesNotRetryNonServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests} {
		calls := 0
		transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return response(status, ""), nil
		})
		client := NewClient("http://accrual.test", &http.Client{Transport: transport})
		result, err := client.Fetch(context.Background(), "12345678903")
		if calls != 1 {
			t.Fatalf("status %d calls = %d", status, calls)
		}
		if status == http.StatusTooManyRequests && !errors.Is(err, ErrRateLimited) {
			t.Fatalf("err = %v, want rate limited", err)
		}
		_ = result
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got, err := parseRetryAfter(""); err != nil || got != time.Second {
		t.Fatalf("empty: got %v err %v", got, err)
	}
	if got, err := parseRetryAfter("2"); err != nil || got != 2*time.Second {
		t.Fatalf("seconds: got %v err %v", got, err)
	}
	if got, err := parseRetryAfter(time.Now().Add(time.Second).UTC().Format(http.TimeFormat)); err != nil || got <= 0 {
		t.Fatalf("date: got %v err %v", got, err)
	}
	if got, err := parseRetryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)); err != nil || got != time.Second {
		t.Fatalf("past date: got %v err %v", got, err)
	}
	if got, err := parseRetryAfter("invalid"); !errors.Is(err, ErrInvalidRetryAfter) || got != time.Second {
		t.Fatalf("invalid: got %v err %v", got, err)
	}
}

func TestRateLimitKeepsDiagnosticError(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp := response(http.StatusTooManyRequests, "")
		resp.Header.Set("Retry-After", "invalid")
		return resp, nil
	})
	result, err := NewClient("http://accrual.test", &http.Client{Transport: transport}).Fetch(context.Background(), "12345678903")
	if !errors.Is(err, ErrRateLimited) || !errors.Is(err, ErrInvalidRetryAfter) || result.RetryAfter != time.Second {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClientFetchBadJSON(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{`), nil
	})

	_, err := NewClient("http://accrual.test", &http.Client{Transport: transport}).Fetch(context.Background(), "12345678903")
	if err == nil {
		t.Fatal("expected error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body string) *http.Response {
	resp := response(status, body)
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
