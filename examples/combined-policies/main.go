// Package main performs an HTTP call with timeout, retry, rate limit, breaker, and bulkhead.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/harshwagh21/reliq/pkg/bulkhead"
	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/circuitbreaker"
	"github.com/harshwagh21/reliq/pkg/ratelimit"
	"github.com/harshwagh21/reliq/pkg/retry"
	"github.com/harshwagh21/reliq/pkg/timeout"
)

func main() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	total, err := timeout.New(time.Second)
	if err != nil {
		fmt.Println(err)
		return
	}
	retries, err := retry.New(retry.Config{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	limiter, err := ratelimit.New(1, 1)
	if err != nil {
		fmt.Println(err)
		return
	}
	breaker, err := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 5,
		OpenTimeout:      time.Second,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	bulk, err := bulkhead.New(1)
	if err != nil {
		fmt.Println(err)
		return
	}
	perAttempt, err := timeout.New(time.Second)
	if err != nil {
		fmt.Println(err)
		return
	}

	err = chain.Do(context.Background(), func(ctx context.Context) error {
		return get(ctx, srv.URL)
	},
		total.Policy(),
		retries.Policy(),
		limiter.Policy(),
		breaker.Policy(),
		bulk.Policy(),
		perAttempt.Policy(),
	)
	fmt.Println(err == nil)
}

func get(ctx context.Context, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
