package fault

import (
	"errors"
	"testing"
	"time"
)

func TestDetailedAndHelpers(t *testing.T) {
	cause := errors.New("boom")
	base := New(RateLimited, "too fast", cause)
	withCode := WithCode(base, "rate_limited")
	withRetry := WithRetryAfter(withCode, 12*time.Second)

	var fe *Error
	if !errors.As(withRetry, &fe) {
		t.Fatal("expected *fault.Error")
	}
	if fe.Kind != RateLimited || fe.Code != "rate_limited" || fe.RetryAfter != 12*time.Second || !errors.Is(fe, cause) {
		t.Fatalf("unexpected fault error: %+v", fe)
	}
	// Ensure helpers did not mutate the original error value.
	if orig := base.(*Error); orig.Code != "" || orig.RetryAfter != 0 {
		t.Fatalf("base error was mutated: %+v", orig)
	}

	detailed := NewDetailed(Timeout, "gateway_timeout", "timed out", 3*time.Second, cause)
	if !errors.As(detailed, &fe) || fe.Kind != Timeout || fe.Code != "gateway_timeout" || fe.RetryAfter != 3*time.Second {
		t.Fatalf("unexpected detailed error: %+v", fe)
	}
}
