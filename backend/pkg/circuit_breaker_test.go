package pkg

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		MaxHalfOpen:      1,
		OpenTimeout:      time.Minute,
	})
	breaker.now = func() time.Time { return now }

	callError := errors.New("dependency unavailable")
	for range 2 {
		_, err := Do(breaker, func() (int, error) { return 0, callError })
		if !errors.Is(err, callError) {
			t.Fatalf("Do() error = %v, want %v", err, callError)
		}
	}
	if state := breaker.State(); state != StateOpen {
		t.Fatalf("State() = %s, want %s", state, StateOpen)
	}

	var calls int
	_, err := Do(breaker, func() (int, error) {
		calls++
		return 1, nil
	})
	if !errors.Is(err, CircuitBreakerOpenError) {
		t.Fatalf("Do() error = %v, want %v", err, CircuitBreakerOpenError)
	}
	if calls != 0 {
		t.Fatalf("callable invoked %d times while breaker was open", calls)
	}

	now = now.Add(time.Minute)
	result, err := Do(breaker, func() (int, error) {
		calls++
		return 42, nil
	})
	if err != nil {
		t.Fatalf("half-open Do() error = %v", err)
	}
	if result != 42 {
		t.Fatalf("half-open Do() result = %d, want 42", result)
	}
	if state := breaker.State(); state != StateClosed {
		t.Fatalf("State() = %s, want %s", state, StateClosed)
	}
}

func TestCircuitBreakerFailureInHalfOpenReopensIt(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		MaxHalfOpen:      1,
		OpenTimeout:      time.Minute,
	})
	breaker.now = func() time.Time { return now }

	callError := errors.New("dependency unavailable")
	_, _ = Do(breaker, func() (struct{}, error) { return struct{}{}, callError })
	now = now.Add(time.Minute)
	_, _ = Do(breaker, func() (struct{}, error) { return struct{}{}, callError })

	if state := breaker.State(); state != StateOpen {
		t.Fatalf("State() = %s, want %s", state, StateOpen)
	}
	if !breaker.openedAt.Equal(now) {
		t.Fatalf("openedAt = %s, want %s", breaker.openedAt, now)
	}
}

func TestCircuitBreakerLimitsConcurrentHalfOpenRequests(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		MaxHalfOpen:      1,
		OpenTimeout:      time.Minute,
	})
	breaker.now = func() time.Time { return now }

	_, _ = Do(breaker, func() (struct{}, error) {
		return struct{}{}, errors.New("dependency unavailable")
	})
	now = now.Add(time.Minute)

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Do(breaker, func() (struct{}, error) {
			close(started)
			<-release
			return struct{}{}, nil
		})
		done <- err
	}()
	<-started

	var secondCalled atomic.Bool
	_, err := Do(breaker, func() (struct{}, error) {
		secondCalled.Store(true)
		return struct{}{}, nil
	})
	if !errors.Is(err, CircuitBreakerTooManyRequestsError) {
		t.Fatalf("second Do() error = %v, want %v", err, CircuitBreakerTooManyRequestsError)
	}
	if secondCalled.Load() {
		t.Fatal("second callable was invoked after half-open limit was reached")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first half-open Do() error = %v", err)
	}
}

func TestCircuitBreakerIgnoresOldGenerationResult(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		MaxHalfOpen:      1,
		OpenTimeout:      time.Minute,
	})
	breaker.now = func() time.Time { return now }

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Do(breaker, func() (struct{}, error) {
			close(started)
			<-release
			return struct{}{}, nil
		})
		done <- err
	}()
	<-started

	_, _ = Do(breaker, func() (struct{}, error) {
		return struct{}{}, errors.New("dependency unavailable")
	})
	if state := breaker.State(); state != StateOpen {
		t.Fatalf("State() = %s, want %s", state, StateOpen)
	}

	now = now.Add(time.Minute)
	if state := breaker.State(); state != StateHalfOpen {
		t.Fatalf("State() = %s, want %s", state, StateHalfOpen)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("old request error = %v", err)
	}
	if state := breaker.State(); state != StateHalfOpen {
		t.Fatalf("old result changed state to %s, want %s", state, StateHalfOpen)
	}
}

func TestCircuitBreakerExcludesCancelledRequest(t *testing.T) {
	breaker := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1})

	_, err := Do(breaker, func() (struct{}, error) {
		return struct{}{}, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	if state := breaker.State(); state != StateClosed {
		t.Fatalf("State() = %s, want %s", state, StateClosed)
	}
}

func TestCircuitBreakerUsesConfiguredFailureClassifier(t *testing.T) {
	applicationError := errors.New("not found")
	breaker := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		IsFailure: func(err error) bool {
			return err != nil && !errors.Is(err, applicationError)
		},
	})

	_, err := Do(breaker, func() (struct{}, error) {
		return struct{}{}, applicationError
	})
	if !errors.Is(err, applicationError) {
		t.Fatalf("Do() error = %v, want %v", err, applicationError)
	}
	if state := breaker.State(); state != StateClosed {
		t.Fatalf("State() = %s, want %s", state, StateClosed)
	}
}

func TestIsInfrastructureError(t *testing.T) {
	for _, check := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", want: false},
		{name: "client response", err: testStatusError(404), want: false},
		{name: "server response", err: testStatusError(503), want: true},
		{name: "network", err: errors.New("connection refused"), want: true},
	} {
		t.Run(check.name, func(t *testing.T) {
			if got := IsInfrastructureError(check.err); got != check.want {
				t.Fatalf("IsInfrastructureError() = %t, want %t", got, check.want)
			}
		})
	}
}

type testStatusError int

func (err testStatusError) Error() string {
	return "HTTP response error"
}

func (err testStatusError) StatusCode() int {
	return int(err)
}
