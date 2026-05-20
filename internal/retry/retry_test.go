package retry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoSucceedsOnFirstAttempt(t *testing.T) {
	var calls int
	err := Do(context.Background(), func() error {
		calls++
		return nil
	})

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestDoSucceedsAfterRetries(t *testing.T) {
	var calls int
	err := Do(context.Background(), func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	}, WithMaxAttempts(5), WithBaseDelay(1*time.Millisecond))

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestDoExhaustsAttempts(t *testing.T) {
	errSentinel := errors.New("persistent")
	var calls int

	err := Do(context.Background(), func() error {
		calls++
		return errSentinel
	}, WithMaxAttempts(4), WithBaseDelay(1*time.Millisecond))

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errSentinel) {
		t.Errorf("expected errSentinel, got: %v", err)
	}
	if calls != 4 {
		t.Errorf("expected 4 calls, got %d", calls)
	}
}

func TestDoDefaultsToThreeAttempts(t *testing.T) {
	var calls int
	_ = Do(context.Background(), func() error {
		calls++
		return errors.New("fail")
	}, WithBaseDelay(1*time.Millisecond))

	if calls != 3 {
		t.Errorf("expected 3 calls (default), got %d", calls)
	}
}

func TestDoContextCancellationBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls int
	err := Do(ctx, func() error {
		calls++
		return errors.New("fail")
	}, WithBaseDelay(1*time.Millisecond))

	if calls != 0 {
		t.Errorf("expected 0 calls after context cancel, got %d", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestDoContextCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var calls int
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := Do(ctx, func() error {
		calls++
		return errors.New("fail")
	}, WithMaxAttempts(10), WithBaseDelay(500*time.Millisecond))

	// Should have made at least 1 call, but not all 10.
	if calls == 0 {
		t.Error("expected at least 1 call")
	}
	if calls == 10 {
		t.Error("expected context cancellation to stop retries before all attempts")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestDoWithRetryIf(t *testing.T) {
	retriable := errors.New("retriable")
	notRetriable := errors.New("not-retriable")

	var calls int
	err := Do(context.Background(), func() error {
		calls++
		if calls == 1 {
			return notRetriable
		}
		return retriable
	}, WithMaxAttempts(5), WithBaseDelay(1*time.Millisecond),
		WithRetryIf(func(err error) bool {
			return errors.Is(err, retriable)
		}),
	)

	if !errors.Is(err, notRetriable) {
		t.Errorf("expected notRetriable error, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call (non-retriable stops immediately), got %d", calls)
	}
}

func TestDoWithRetryIfRetriable(t *testing.T) {
	retriable := errors.New("retriable")

	var calls int
	err := Do(context.Background(), func() error {
		calls++
		if calls < 3 {
			return retriable
		}
		return nil
	}, WithMaxAttempts(5), WithBaseDelay(1*time.Millisecond),
		WithRetryIf(func(err error) bool {
			return errors.Is(err, retriable)
		}),
	)

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestDoNilRetryIfDefaultsToAll(t *testing.T) {
	var calls int
	err := Do(context.Background(), func() error {
		calls++
		return errors.New("any error")
	}, WithMaxAttempts(3), WithBaseDelay(1*time.Millisecond),
		WithRetryIf(nil),
	)

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls (nil RetryIf = retry all), got %d", calls)
	}
}

func TestDoMaxDelayIsRespected(t *testing.T) {
	var calls int
	start := time.Now()

	_ = Do(context.Background(), func() error {
		calls++
		return errors.New("fail")
	}, WithMaxAttempts(5), WithBaseDelay(100*time.Millisecond),
		WithMaxDelay(50*time.Millisecond), WithJitter(0),
	)

	elapsed := time.Since(start)

	// Without maxDelay cap, 4 retries at base*2^N would be:
	// 100 + 200 + 400 + 800 = 1500ms.
	// With maxDelay=50ms, each delay is capped at 50ms:
	// 50 + 50 + 50 + 50 = 200ms.
	// Allow generous tolerance for timer scheduling.
	if elapsed > 600*time.Millisecond {
		t.Errorf("expected total delay < 600ms with maxDelay=50ms, got %v", elapsed)
	}
}

func TestDoBaseDelayGrowsExponentially(t *testing.T) {
	var delays []time.Duration
	var lastTime time.Time

	_ = Do(context.Background(), func() error {
		now := time.Now()
		if !lastTime.IsZero() {
			delays = append(delays, now.Sub(lastTime))
		}
		lastTime = now
		return errors.New("fail")
	}, WithMaxAttempts(5), WithBaseDelay(10*time.Millisecond),
		WithMaxDelay(500*time.Millisecond), WithJitter(0),
	)

	// delays[0] = delay before attempt 1 (retry 1): should be ~10ms
	// delays[1] = delay before attempt 2 (retry 2): should be ~20ms
	// delays[2] = delay before attempt 3 (retry 3): should be ~40ms
	// delays[3] = delay before attempt 4 (retry 4): should be ~80ms
	for i, d := range delays {
		expected := 10 * time.Millisecond * (1 << i)
		// Allow 50% tolerance for timer scheduling.
		lower := expected / 2
		upper := expected * 3 / 2
		if d < lower || d > upper {
			t.Errorf("delay[%d]: expected ~%v, got %v", i, expected, d)
		}
	}
}

func TestDoJitterAddsRandomness(t *testing.T) {
	// Run multiple times and verify not all delays are identical.
	uniqueDelays := make(map[time.Duration]bool)

	for run := 0; run < 20; run++ {
		var lastTime time.Time
		_ = Do(context.Background(), func() error {
			now := time.Now()
			if !lastTime.IsZero() {
				uniqueDelays[now.Sub(lastTime)] = true
			}
			lastTime = now
			return errors.New("fail")
		}, WithMaxAttempts(3), WithBaseDelay(10*time.Millisecond),
			WithJitter(5*time.Millisecond),
		)
	}

	// With jitter, we should see more than 1 unique delay value.
	if len(uniqueDelays) <= 1 {
		t.Errorf("expected multiple unique delay values with jitter, got %d", len(uniqueDelays))
	}
}

func TestDoZeroJitterIsAllowed(t *testing.T) {
	var calls int
	err := Do(context.Background(), func() error {
		calls++
		if calls < 2 {
			return errors.New("fail")
		}
		return nil
	}, WithMaxAttempts(3), WithBaseDelay(1*time.Millisecond), WithJitter(0))

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestDoSingleAttempt(t *testing.T) {
	var calls int
	err := Do(context.Background(), func() error {
		calls++
		return errors.New("fail")
	}, WithMaxAttempts(1))

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestDoConcurrentUsage(t *testing.T) {
	const goroutines = 20
	var wg sync.WaitGroup
	var successes atomic.Int32
	var failures atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var calls int
			err := Do(context.Background(), func() error {
				calls++
				if calls < 2 {
					return fmt.Errorf("transient-%d", id)
				}
				return nil
			}, WithMaxAttempts(3), WithBaseDelay(1*time.Millisecond))
			if err != nil {
				failures.Add(1)
			} else {
				successes.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if int(failures.Load()) > 0 {
		t.Errorf("unexpected failures: %d", failures.Load())
	}
	if int(successes.Load()) != goroutines {
		t.Errorf("expected %d successes, got %d", goroutines, successes.Load())
	}
}

func TestDoReturnsLastError(t *testing.T) {
	firstErr := errors.New("first")
	secondErr := errors.New("second")
	thirdErr := errors.New("third")

	var calls int
	err := Do(context.Background(), func() error {
		calls++
		switch calls {
		case 1:
			return firstErr
		case 2:
			return secondErr
		default:
			return thirdErr
		}
	}, WithMaxAttempts(3), WithBaseDelay(1*time.Millisecond))

	if !errors.Is(err, thirdErr) {
		t.Errorf("expected thirdErr (last attempt), got: %v", err)
	}
}

func TestDoWrappedError(t *testing.T) {
	baseErr := errors.New("base")
	var calls int

	err := Do(context.Background(), func() error {
		calls++
		return fmt.Errorf("wrapped: %w", baseErr)
	}, WithMaxAttempts(2), WithBaseDelay(1*time.Millisecond))

	if !errors.Is(err, baseErr) {
		t.Errorf("expected errors.Is match for baseErr, got: %v", err)
	}
}
