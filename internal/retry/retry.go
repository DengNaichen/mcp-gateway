// Package retry runs an idempotent operation with bounded, jittered exponential
// backoff. It honours server-supplied delays (Retry-After / RetryInfo), never
// sleeps past the caller's context deadline, and always returns the last
// operation error rather than a bare context error, so callers can still
// classify and report what the upstream actually said.
//
// Only use it for operations that are safe to repeat: reads, classification
// calls, handshakes. Never wrap side-effecting tool executions.
package retry

import (
	"context"
	"math/rand/v2"
	"time"
)

// Policy bounds one retried operation. The caller's context deadline is the
// total time budget; MaxAttempts bounds the number of tries within it. All
// fields should be set: a zero MaxDelay retries immediately and never honours
// a server-requested delay.
type Policy struct {
	// MaxAttempts is the total number of tries, including the first one.
	MaxAttempts int
	// BaseDelay is the backoff before the second try; it doubles after each
	// failure and is fully jittered.
	BaseDelay time.Duration
	// MaxDelay caps computed backoff. A server-requested delay above it is not
	// truncated (retrying earlier would only be rejected again): Do gives up
	// and returns the error so the caller can surface the server's delay.
	MaxDelay time.Duration
	// OnRetry, if set, is called before each wait.
	OnRetry func(attempt int, wait time.Duration, err error)
}

// Verdict is a classifier's judgement of one failed attempt.
type Verdict struct {
	Retry bool
	// After is the server-requested minimum delay; zero means "use backoff".
	After time.Duration
}

// Do calls op until it succeeds, classify rejects its error, attempts run out,
// or the next wait (plus the time the last attempt took) would not fit in the
// context deadline. It returns the result of the last attempt.
func Do[T any](ctx context.Context, p Policy, classify func(error) Verdict, op func(context.Context) (T, error)) (T, error) {
	attempts := max(p.MaxAttempts, 1)
	for attempt := 1; ; attempt++ {
		started := time.Now()
		v, err := op(ctx)
		if err == nil || attempt >= attempts || ctx.Err() != nil {
			return v, err
		}
		verdict := classify(err)
		if !verdict.Retry {
			return v, err
		}
		wait := verdict.After
		if wait > p.MaxDelay {
			return v, err
		}
		if wait <= 0 {
			wait = backoff(p, attempt)
		} else {
			// Spread clients that were all told the same delay.
			wait += rand.N(wait/10 + 1) //nolint:gosec // G404: jitter only spreads retries; it needs no unpredictability
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait+time.Since(started) {
			return v, err
		}
		if p.OnRetry != nil {
			p.OnRetry(attempt, wait, err)
		}
		if !sleep(ctx, wait) {
			return v, err
		}
	}
}

// backoff returns a fully jittered delay in [0, min(MaxDelay, BaseDelay·2^(attempt-1))].
func backoff(p Policy, attempt int) time.Duration {
	ceiling := p.MaxDelay
	if shift := attempt - 1; shift < 30 && p.BaseDelay <= ceiling>>shift {
		ceiling = p.BaseDelay << shift
	}
	if ceiling <= 0 {
		return 0
	}
	return rand.N(ceiling + 1) //nolint:gosec // G404: jitter only spreads retries; it needs no unpredictability
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
