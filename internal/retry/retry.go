// Package retry provides configurable exponential-backoff retry logic.
// The primary entry point is Do, which calls an operation repeatedly until
// it succeeds, the attempt limit is reached, or the context is cancelled.
//
// Backoff formula: delay = min(baseDelay * 2^attempt, maxDelay) + jitter,
// where jitter is a random duration in [0, jitter).
package retry

import (
	"context"
	"math/rand"
	"time"
)

// Option configures retry behavior.
type Option func(*options)

// options holds the resolved configuration for a Do call.
type options struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	jitter      time.Duration
	retryIf     func(error) bool
}

const (
	defaultMaxAttempts = 3
	// DefaultBaseDelay is the default initial backoff duration.
	// Exported so that consumers (e.g. proxy retry) can reference it.
	DefaultBaseDelay = 1 * time.Second
	// DefaultMaxDelay is the default maximum backoff duration.
	// Exported so that consumers can reference it.
	DefaultMaxDelay = 30 * time.Second
	defaultJitter   = 500 * time.Millisecond
)

// WithMaxAttempts sets the total number of attempts (initial + retries).
// n must be >= 1; values below 1 are clamped to 1.
func WithMaxAttempts(n int) Option {
	return func(o *options) {
		if n < 1 {
			n = 1
		}
		o.maxAttempts = n
	}
}

// WithBaseDelay sets the initial backoff duration before the first retry.
// Subsequent retries multiply this value by 2^attempt.
func WithBaseDelay(d time.Duration) Option {
	return func(o *options) {
		o.baseDelay = d
	}
}

// WithMaxDelay caps the exponential backoff at this duration.
// Without a cap, delays grow without bound.
func WithMaxDelay(d time.Duration) Option {
	return func(o *options) {
		o.maxDelay = d
	}
}

// WithJitter sets the maximum random duration added to each backoff delay.
// Set to 0 to disable jitter entirely.
func WithJitter(d time.Duration) Option {
	return func(o *options) {
		o.jitter = d
	}
}

// WithRetryIf sets a predicate that determines which errors are retriable.
// Only errors for which the predicate returns true trigger a retry.
// A nil predicate is treated as "retry all errors".
func WithRetryIf(fn func(error) bool) Option {
	return func(o *options) {
		if fn != nil {
			o.retryIf = fn
		}
	}
}

// Do calls op repeatedly until it returns nil, the maximum number of attempts
// is reached, or ctx is cancelled.
//
// Backoff is exponential: delay = min(baseDelay * 2^attempt, maxDelay) + jitter,
// where attempt starts at 0 for the first retry.
//
// If the operation returns a non-nil error that does not satisfy the RetryIf
// predicate, Do returns that error immediately without further attempts.
//
// If ctx is already cancelled when Do is called, it returns ctx.Err() immediately
// without calling op.
func Do(ctx context.Context, op func() error, opts ...Option) error {
	// Resolve options.
	o := options{
		maxAttempts: defaultMaxAttempts,
		baseDelay:   DefaultBaseDelay,
		maxDelay:    DefaultMaxDelay,
		jitter:      defaultJitter,
		retryIf:     func(error) bool { return true },
	}
	for _, opt := range opts {
		opt(&o)
	}

	var lastErr error

	for attempt := 0; attempt < o.maxAttempts; attempt++ {
		// Check context before each attempt.
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = op()
		if lastErr == nil {
			return nil
		}

		// Not retriable -- stop immediately.
		if !o.retryIf(lastErr) {
			return lastErr
		}

		// Don't sleep after the last attempt -- just return the error.
		if attempt == o.maxAttempts-1 {
			return lastErr
		}

		// Calculate backoff delay.
		delay := o.baseDelay * time.Duration(1<<attempt) // base * 2^attempt
		if delay < 0 {
			delay = o.maxDelay // overflow guard
		}
		if delay > o.maxDelay {
			delay = o.maxDelay
		}

		// Add jitter: random duration in [0, jitter).
		if o.jitter > 0 {
			delay += time.Duration(rand.Int63n(int64(o.jitter)))
		}

		// Sleep with context awareness.
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Continue to next attempt.
		}
	}

	return lastErr
}
