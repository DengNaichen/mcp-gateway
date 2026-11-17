package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gatewaylab/internal/retry"
)

// decisionsClient bounds retries of one provider HTTP call. The caller's
// TimeoutSeconds context is the total budget; retry.Do never sleeps past it.
// Server-requested delays above MaxDelay are surfaced to the caller instead.
type decisionsClient struct {
	retry retry.Policy
}

func defaultClient() decisionsClient {
	return decisionsClient{
		retry: retry.Policy{MaxAttempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 30 * time.Second},
	}
}

func retryVerdict(err error) retry.Verdict {
	var e *Error
	if errors.As(err, &e) && e.retryable {
		return retry.Verdict{Retry: true, After: e.RetryAfter}
	}
	return retry.Verdict{}
}

// transportError classifies a failed round trip. Context expiry is reported as
// a timeout and is never retried (retry.Do stops once ctx is done).
func transportError(ctx context.Context, err error) error {
	var netErr net.Error
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &Error{Kind: ErrTimeout, Code: CodeTimeout, Message: "Decisions 服务请求超时", Cause: err}
	case ctx.Err() != nil:
		return &Error{Kind: ErrUpstream, Code: CodeUnavailable, Message: "Decisions 请求已取消", Cause: err}
	case errors.As(err, &netErr) && netErr.Timeout():
		return &Error{Kind: ErrTimeout, Code: CodeTimeout, Message: "Decisions 服务请求超时", Cause: err, retryable: true}
	default:
		return &Error{Kind: ErrUpstream, Code: CodeUnavailable, Message: "Decisions 服务连接失败", Cause: err, retryable: true}
	}
}

// providerError is the union of the Google (google.rpc.Status) and OpenAI
// error envelopes; fields absent from one provider simply stay empty.
type providerError struct {
	Error struct {
		Code    json.RawMessage `json:"code"` // number (Google) or string (OpenAI)
		Status  string          `json:"status"`
		Type    string          `json:"type"`
		Details []struct {
			Type       string `json:"@type"`
			Reason     string `json:"reason"`
			RetryDelay string `json:"retryDelay"`
			Violations []struct {
				QuotaID string `json:"quotaId"`
			} `json:"violations"`
		} `json:"details"`
	} `json:"error"`
}

// responseError classifies a non-2xx provider response. The message keeps the
// provider's status and (redacted, truncated) body for humans; Kind, Code and
// RetryAfter carry the classification for machines.
func responseError(status int, header http.Header, body []byte, key string) error {
	message := string(body)
	if key != "" {
		message = strings.ReplaceAll(message, key, "[redacted]")
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	e := &Error{Kind: ErrUpstream, Status: status, Message: fmt.Sprintf("Decisions 服务返回 HTTP %d: %s", status, message)}
	var p providerError
	_ = json.Unmarshal(body, &p) // best effort: unknown bodies fall back to status-only classification
	// tokens holds the provider's machine-readable labels (never free text).
	tokens := map[string]bool{}
	add := func(v string) {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			tokens[v] = true
		}
	}
	add(strings.Trim(string(p.Error.Code), `"`))
	add(p.Error.Status)
	add(p.Error.Type)
	var retryDelay string
	dailyQuota := false
	for _, d := range p.Error.Details {
		add(d.Reason)
		if strings.HasSuffix(d.Type, "google.rpc.RetryInfo") {
			retryDelay = d.RetryDelay
		}
		for _, v := range d.Violations {
			dailyQuota = dailyQuota || strings.Contains(strings.ToLower(v.QuotaID), "perday")
		}
	}
	has := func(labels ...string) bool {
		for _, l := range labels {
			if tokens[l] {
				return true
			}
		}
		return false
	}
	switch {
	case status == 429:
		e.Kind = ErrRateLimited
		if dailyQuota || has("insufficient_quota", "billing_hard_limit_reached", "billing_not_active") {
			e.Code = CodeQuotaExhausted
			break
		}
		e.Code, e.retryable = CodeRateLimited, true
		e.RetryAfter = retryAfter(header, retryDelay, time.Now())
	case status == 401 || status == 403 || has("api_key_invalid"):
		e.Code = CodeAuthFailed
	case status >= 400 && status < 500 && status != 408 && has(contentBlockedLabels...):
		e.Code = CodeContentBlocked
	case status == 408 || status == 504:
		e.Kind, e.Code, e.retryable = ErrTimeout, CodeTimeout, true
		e.RetryAfter = retryAfter(header, retryDelay, time.Now())
	case status >= 500:
		e.Code, e.retryable = CodeUnavailable, true
		e.RetryAfter = retryAfter(header, retryDelay, time.Now())
	default:
		e.Code = CodeRequestRejected
	}
	return e
}

// contentBlockedLabels are provider codes/reasons for safety refusals:
// Google (SAFETY, PROHIBITED_CONTENT, BLOCKLIST, SPII), OpenAI
// (content_policy_violation) and Azure OpenAI (content_filter).
var contentBlockedLabels = []string{"safety", "prohibited_content", "blocklist", "spii", "content_policy_violation", "content_filter"}

// maxRetryAfter bounds provider-supplied delays so absurd values (Inf, 1e30,
// centuries) cannot overflow or leak into our own Retry-After header.
const maxRetryAfter = 24 * time.Hour

// retryAfter returns the provider's requested delay, preferring the most
// precise source: OpenAI's retry-after-ms, then Google's RetryInfo.retryDelay
// (a protobuf Duration such as "11s" or "30.34s"), then the standard
// Retry-After header (delta-seconds or HTTP-date, RFC 9110 §10.2.3).
func retryAfter(header http.Header, retryDelay string, now time.Time) time.Duration {
	if ms, err := strconv.ParseFloat(header.Get("Retry-After-Ms"), 64); err == nil && ms > 0 && !math.IsInf(ms, 0) {
		return min(time.Duration(min(ms, float64(maxRetryAfter/time.Millisecond))*float64(time.Millisecond)), maxRetryAfter)
	}
	if d, err := time.ParseDuration(retryDelay); err == nil && d > 0 {
		return min(d, maxRetryAfter)
	}
	v := strings.TrimSpace(header.Get("Retry-After"))
	if s, err := strconv.ParseInt(v, 10, 64); err == nil && s > 0 {
		return time.Duration(min(s, int64(maxRetryAfter/time.Second))) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return min(t.Sub(now), maxRetryAfter)
	}
	return 0
}
