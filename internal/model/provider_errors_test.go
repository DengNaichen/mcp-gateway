package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/retry"
)

// fastClient keeps retry tests quick without mutating package-global state:
// tiny backoff, and any server delay above 100ms is surfaced instead of waited out.
func fastClient() decisionsClient {
	return decisionsClient{retry: retry.Policy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 100 * time.Millisecond}}
}

func okDecisions(w http.ResponseWriter) {
	respond(w, 200, map[string]any{"answers": []map[string]any{
		{"type": "choice", "name": "route_status", "choice": "selected"},
		{"type": "predicate", "name": "tool_0", "probability": 0.9},
	}})
}

// flakyServer fails with the given handler for the first `failures` calls,
// then answers normally. It returns the server and the call counter.
func flakyServer(t *testing.T, failures int32, fail func(http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= failures {
			fail(w)
			return
		}
		okDecisions(w)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func ask(t *testing.T, server *httptest.Server) (Answers, *Error) {
	t.Helper()
	answers, err := fastClient().askDecisions(context.Background(), Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5}, "secret-test-key", "input", []string{"question"})
	if err == nil {
		return answers, nil
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("not a model error: %T %v", err, err)
	}
	return answers, e
}

func TestRateLimitIsRetriedThenSucceeds(t *testing.T) {
	server, calls := flakyServer(t, 2, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After-Ms", "5")
		respond(w, 429, map[string]any{"error": map[string]any{"code": "rate_limit_exceeded", "message": "slow down"}})
	})
	answers, e := ask(t, server)
	if e != nil || answers.Status != "selected" || calls.Load() != 3 {
		t.Fatalf("answers=%+v err=%v calls=%d", answers, e, calls.Load())
	}
}

func TestRateLimitExhaustsRetriesAndKeepsRetryAfter(t *testing.T) {
	server, calls := flakyServer(t, 100, func(w http.ResponseWriter) {
		respond(w, 429, map[string]any{"error": map[string]any{
			"code": 429, "status": "RESOURCE_EXHAUSTED", "message": "Rate limit exceeded. Please retry in 0.01s.",
			"details": []map[string]any{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.01s"}},
		}})
	})
	_, e := ask(t, server)
	if e == nil || !errors.Is(e, ErrRateLimited) || e.Code != CodeRateLimited || e.RetryAfter != 10*time.Millisecond || e.Status != 429 {
		t.Fatalf("err=%+v", e)
	}
	if calls.Load() != int32(fastClient().retry.MaxAttempts) {
		t.Fatalf("retries not bounded: %d calls", calls.Load())
	}
}

func TestLongServerDelayIsSurfacedNotWaited(t *testing.T) {
	server, calls := flakyServer(t, 100, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "11")
		respond(w, 429, map[string]any{"error": map[string]any{"message": "retry in 11s"}})
	})
	started := time.Now()
	_, e := ask(t, server)
	if e == nil || e.Code != CodeRateLimited || e.RetryAfter != 11*time.Second || calls.Load() != 1 || time.Since(started) > 2*time.Second {
		t.Fatalf("err=%+v calls=%d elapsed=%s", e, calls.Load(), time.Since(started))
	}
}

func TestPermanentFailuresAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   map[string]any
		kind   error
		code   string
	}{
		{"unauthorized", 401, map[string]any{"error": map[string]any{"message": "bad key secret-test-key"}}, ErrUpstream, CodeAuthFailed},
		{"forbidden", 403, map[string]any{"error": map[string]any{"status": "PERMISSION_DENIED"}}, ErrUpstream, CodeAuthFailed},
		{"gemini-invalid-key", 400, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT", "message": "API key not valid.", "details": []map[string]any{{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "API_KEY_INVALID"}}}}, ErrUpstream, CodeAuthFailed},
		{"safety", 400, map[string]any{"error": map[string]any{"code": "safety", "message": "blocked"}}, ErrUpstream, CodeContentBlocked},
		{"bad-request", 400, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT"}}, ErrUpstream, CodeRequestRejected},
		{"openai-quota", 429, map[string]any{"error": map[string]any{"code": "insufficient_quota", "type": "insufficient_quota"}}, ErrRateLimited, CodeQuotaExhausted},
		{"gemini-daily-quota", 429, map[string]any{"error": map[string]any{"code": 429, "details": []map[string]any{{"@type": "type.googleapis.com/google.rpc.QuotaFailure", "violations": []map[string]any{{"quotaId": "GenerateRequestsPerDayPerProjectPerModel"}}}}}}, ErrRateLimited, CodeQuotaExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, calls := flakyServer(t, 100, func(w http.ResponseWriter) { respond(w, tc.status, tc.body) })
			_, e := ask(t, server)
			if e == nil || !errors.Is(e, tc.kind) || e.Code != tc.code || e.Status != tc.status || calls.Load() != 1 {
				t.Fatalf("err=%+v calls=%d", e, calls.Load())
			}
			if e.RetryAfter != 0 {
				t.Fatalf("permanent failure carries RetryAfter %s", e.RetryAfter)
			}
		})
	}
}

func TestServerErrorsAreRetried(t *testing.T) {
	server, calls := flakyServer(t, 1, func(w http.ResponseWriter) {
		respond(w, 503, map[string]any{"error": map[string]any{"status": "UNAVAILABLE"}})
	})
	if _, e := ask(t, server); e != nil || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d", e, calls.Load())
	}
	server, calls = flakyServer(t, 100, func(w http.ResponseWriter) { respond(w, 502, nil) })
	if _, e := ask(t, server); e == nil || !errors.Is(e, ErrUpstream) || e.Code != CodeUnavailable || calls.Load() != 4 {
		t.Fatalf("err=%+v calls=%d", e, calls.Load())
	}
	server, _ = flakyServer(t, 100, func(w http.ResponseWriter) { respond(w, 504, nil) })
	if _, e := ask(t, server); e == nil || !errors.Is(e, ErrTimeout) || e.Code != CodeTimeout {
		t.Fatalf("err=%+v", e)
	}
}

func TestConnectionFailureAndTimeoutAreClassified(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	_, e := ask(t, dead)
	if e == nil || !errors.Is(e, ErrUpstream) || e.Code != CodeUnavailable {
		t.Fatalf("connection failure: %+v", e)
	}

	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := fastClient().askDecisions(ctx, Settings{BaseURL: slow.URL, MaxTools: 1, TimeoutSeconds: 5}, "", "input", []string{"question"})
	if !errors.As(err, &e) || !errors.Is(e, ErrTimeout) || e.Code != CodeTimeout {
		t.Fatalf("timeout: %v", err)
	}
}

func TestBadResponseIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		respond(w, 200, map[string]any{"answers": []map[string]any{{"type": "refusal", "name": "route_status"}, {"type": "predicate", "name": "tool_0", "probability": 0.5}}})
	}))
	defer server.Close()
	_, e := ask(t, server)
	if e == nil || e.Code != CodeContentBlocked || calls.Load() != 1 {
		t.Fatalf("err=%+v calls=%d", e, calls.Load())
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	for _, tc := range []struct {
		name       string
		header     http.Header
		retryDelay string
		want       time.Duration
	}{
		{"none", h(), "", 0},
		{"seconds", h("Retry-After", "11"), "", 11 * time.Second},
		{"http-date", h("Retry-After", now.Add(30*time.Second).Format(http.TimeFormat)), "", 30 * time.Second},
		{"past-date", h("Retry-After", now.Add(-time.Minute).Format(http.TimeFormat)), "", 0},
		{"negative", h("Retry-After", "-5"), "", 0},
		{"garbage", h("Retry-After", "soon"), "", 0},
		{"retry-info", h(), "30.34s", 30340 * time.Millisecond},
		{"ms-wins", h("Retry-After-Ms", "1500", "Retry-After", "2"), "3s", 1500 * time.Millisecond},
		{"retry-info-beats-header", h("Retry-After", "2"), "3s", 3 * time.Second},
	} {
		if got := retryAfter(tc.header, tc.retryDelay, now); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}

func TestContentBlockedOnlyForClientErrors(t *testing.T) {
	server, calls := flakyServer(t, 1, func(w http.ResponseWriter) {
		respond(w, 503, map[string]any{"error": map[string]any{"status": "UNAVAILABLE", "details": []map[string]any{{"reason": "SAFETY"}}}})
	})
	if _, e := ask(t, server); e != nil || calls.Load() != 2 {
		t.Fatalf("5xx mentioning safety must still be retried: err=%v calls=%d", e, calls.Load())
	}
	server, _ = flakyServer(t, 100, func(w http.ResponseWriter) {
		respond(w, 400, map[string]any{"error": map[string]any{"code": "content_policy_violation", "type": "invalid_request_error"}})
	})
	if _, e := ask(t, server); e == nil || e.Code != CodeContentBlocked {
		t.Fatalf("OpenAI content policy: %+v", e)
	}
	server, _ = flakyServer(t, 100, func(w http.ResponseWriter) {
		respond(w, 400, map[string]any{"error": map[string]any{"status": "FAILED_PRECONDITION", "details": []map[string]any{{"reason": "API_KEY_IP_ADDRESS_BLOCKED"}}}})
	})
	if _, e := ask(t, server); e == nil || e.Code != CodeRequestRejected {
		t.Fatalf("a *_BLOCKED reason is not a safety block: %+v", e)
	}
}

func TestOversizedErrorBodyKeepsStatus(t *testing.T) {
	server, _ := flakyServer(t, 100, func(w http.ResponseWriter) {
		w.WriteHeader(400)
		_, _ = w.Write(make([]byte, maxDecisionsResponse+10))
	})
	if _, e := ask(t, server); e == nil || e.Status != 400 || e.Code != CodeRequestRejected {
		t.Fatalf("err=%+v", e)
	}
}

func TestRateLimitIsReportedWhenRetryRunsOutOfTime(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "20")
			respond(w, 429, map[string]any{"error": map[string]any{"code": "rate_limit_exceeded"}})
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := fastClient().askDecisions(ctx, Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5}, "", "input", []string{"question"})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeRateLimited || e.RetryAfter != 20*time.Millisecond || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestSystemOneRetriesOnlyTheFailedBatch(t *testing.T) {
	var calls, statusBatches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]any `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := calls.Add(1)
		if n == 2 { // first attempt of the second batch
			respond(w, 503, nil)
			return
		}
		answers := map[string]any{}
		for name := range req.Questions {
			if name == "route_status" {
				statusBatches.Add(1)
				answers[name] = map[string]any{"type": "choice", "choice": "selected"}
			} else {
				answers[name] = map[string]any{"type": "noul", "noul": 0.5}
			}
		}
		respond(w, 200, map[string]any{"answers": answers})
	}))
	defer server.Close()
	instructions := make([]string, 20)
	for i := range instructions {
		instructions[i] = "question"
	}
	answers, err := fastClient().askDecisions(context.Background(), Settings{BaseURL: server.URL, Model: "jev-1", MaxTools: 20, TimeoutSeconds: 5}, "", "input", instructions)
	if err != nil || len(answers.Relevance) != 20 || calls.Load() != 3 || statusBatches.Load() != 1 {
		t.Fatalf("err=%v relevance=%d calls=%d statusBatches=%d", err, len(answers.Relevance), calls.Load(), statusBatches.Load())
	}
}

func TestChoiceIsRetried(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			respond(w, 500, nil)
			return
		}
		respond(w, 200, map[string]any{"answers": []map[string]any{{"type": "choice", "name": ChoiceQuestionName, "choice": "a", "probabilities": map[string]float64{"a": 1}}}})
	}))
	defer server.Close()
	answer, _, err := fastClient().askChoice(context.Background(), choiceRequest{
		Settings:     Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5},
		Input:        "input",
		Instructions: "pick",
		Choices:      []Choice{{Value: "a"}, {Value: "b"}},
	})
	if err != nil || answer.Choice != "a" || calls.Load() != 2 {
		t.Fatalf("answer=%+v err=%v calls=%d", answer, err, calls.Load())
	}
}

func TestRetryAfterIsBounded(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name       string
		header     http.Header
		retryDelay string
	}{
		{"ms-inf", http.Header{"Retry-After-Ms": {"Inf"}}, ""},
		{"ms-huge", http.Header{"Retry-After-Ms": {"1e30"}}, ""},
		{"seconds-huge", http.Header{"Retry-After": {"9223372036854775807"}}, ""},
		{"retry-info-huge", http.Header{}, "2000000h"},
	} {
		if got := retryAfter(tc.header, tc.retryDelay, now); got < 0 || got > maxRetryAfter {
			t.Errorf("%s: %s", tc.name, got)
		}
	}
	if got := retryAfter(http.Header{"Retry-After-Ms": {"NaN"}}, "", now); got != 0 {
		t.Errorf("NaN: %s", got)
	}
}
