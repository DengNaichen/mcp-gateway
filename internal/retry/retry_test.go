package retry

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

var errFlaky = errors.New("flaky")

func retryAll(error) Verdict { return Verdict{Retry: true} }

func policy() Policy {
	return Policy{MaxAttempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 8 * time.Second}
}

func TestRetriesUntilSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		v, err := Do(t.Context(), policy(), retryAll, func(context.Context) (int, error) {
			calls++
			if calls < 3 {
				return 0, errFlaky
			}
			return 42, nil
		})
		if err != nil || v != 42 || calls != 3 {
			t.Fatalf("v=%d err=%v calls=%d", v, err, calls)
		}
	})
}

func TestStopsAtMaxAttemptsAndReturnsLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		_, err := Do(t.Context(), policy(), retryAll, func(context.Context) (int, error) {
			calls++
			return 0, errFlaky
		})
		if !errors.Is(err, errFlaky) || calls != 4 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})
}

func TestPermanentErrorIsNotRetried(t *testing.T) {
	calls := 0
	_, err := Do(t.Context(), policy(), func(error) Verdict { return Verdict{} }, func(context.Context) (int, error) {
		calls++
		return 0, errFlaky
	})
	if !errors.Is(err, errFlaky) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestHonoursServerDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var gaps []time.Duration
		last := time.Now()
		calls := 0
		_, err := Do(t.Context(), policy(), func(error) Verdict { return Verdict{Retry: true, After: 3 * time.Second} }, func(context.Context) (int, error) {
			gaps = append(gaps, time.Since(last))
			last = time.Now()
			calls++
			if calls < 3 {
				return 0, errFlaky
			}
			return 1, nil
		})
		within := func(d time.Duration) bool { return d >= 3*time.Second && d <= 3300*time.Millisecond }
		if err != nil || len(gaps) != 3 || !within(gaps[1]) || !within(gaps[2]) {
			t.Fatalf("err=%v gaps=%v", err, gaps)
		}
	})
}

func TestServerDelayAboveCapGivesUpImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		_, err := Do(t.Context(), policy(), func(error) Verdict { return Verdict{Retry: true, After: time.Minute} }, func(context.Context) (int, error) {
			calls++
			return 0, errFlaky
		})
		if !errors.Is(err, errFlaky) || calls != 1 || time.Since(start) != 0 {
			t.Fatalf("err=%v calls=%d elapsed=%s", err, calls, time.Since(start))
		}
	})
}

func TestDoesNotSleepPastDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		start := time.Now()
		calls := 0
		_, err := Do(ctx, policy(), func(error) Verdict { return Verdict{Retry: true, After: 6 * time.Second} }, func(context.Context) (int, error) {
			calls++
			return 0, errFlaky
		})
		// The upstream error is kept, not replaced by context.DeadlineExceeded.
		if !errors.Is(err, errFlaky) || calls != 1 || time.Since(start) != 0 {
			t.Fatalf("err=%v calls=%d elapsed=%s", err, calls, time.Since(start))
		}
	})
}

func TestCancellationDuringWaitReturnsLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		_, err := Do(ctx, policy(), func(error) Verdict { return Verdict{Retry: true, After: 2 * time.Second} }, func(context.Context) (int, error) {
			calls++
			return 0, errFlaky
		})
		if !errors.Is(err, errFlaky) || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})
}

func TestBackoffIsBoundedAndGrows(t *testing.T) {
	p := policy()
	for attempt := 1; attempt <= 40; attempt++ {
		ceiling := min(p.MaxDelay, p.BaseDelay<<min(attempt-1, 20))
		for range 50 {
			if d := backoff(p, attempt); d < 0 || d > ceiling {
				t.Fatalf("attempt %d: %s outside [0, %s]", attempt, d, ceiling)
			}
		}
	}
}

func TestBackoffDoesNotOverflowLargeBase(t *testing.T) {
	p := Policy{MaxAttempts: 40, BaseDelay: time.Minute, MaxDelay: time.Hour}
	for attempt := 1; attempt <= 40; attempt++ {
		if d := backoff(p, attempt); d < 0 || d > p.MaxDelay {
			t.Fatalf("attempt %d: %s", attempt, d)
		}
	}
	// A positive ceiling means the delay is not stuck at zero (no hot loop).
	nonzero := false
	for range 20 {
		nonzero = nonzero || backoff(p, 30) > 0
	}
	if !nonzero {
		t.Fatal("backoff collapsed to zero")
	}
}
