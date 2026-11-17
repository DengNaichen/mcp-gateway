package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/apiclient"
)

func TestScore(t *testing.T) {
	c := caseSpec{Expected: []string{"s.a", "s.b"}, Acceptable: []string{"s.c"}}
	for _, tc := range []struct {
		name        string
		c           caseSpec
		status      string
		got         []string
		pass        bool
		missing, ex []string
	}{
		{"all expected", c, "selected", []string{"s.a", "s.b"}, true, nil, nil},
		{"acceptable is not noise", c, "selected", []string{"s.a", "s.b", "s.c"}, true, nil, nil},
		{"noise is reported but passes", c, "selected", []string{"s.a", "s.b", "s.z"}, true, nil, []string{"s.z"}},
		{"missing expected fails", c, "selected", []string{"s.a", "s.c"}, false, []string{"s.b"}, nil},
		{"empty fails a positive case", c, "no_match", nil, false, []string{"s.a", "s.b"}, nil},
		{"any_of passes with one", caseSpec{AnyOf: []string{"s.x", "s.y"}}, "selected", []string{"s.y"}, true, nil, nil},
		{"any_of fails with none", caseSpec{AnyOf: []string{"s.x", "s.y"}}, "selected", []string{"s.z"}, false, []string{"s.x", "s.y"}, []string{"s.z"}},
		{"any_of alternative is not noise", caseSpec{AnyOf: []string{"s.x", "s.y"}}, "selected", []string{"s.x", "s.y"}, true, nil, nil},
		{"no_match passes on empty", caseSpec{NoMatch: true}, "no_match", nil, true, nil, nil},
		{"no_match fails on any tool", caseSpec{NoMatch: true}, "selected", []string{"s.a"}, false, nil, []string{"s.a"}},
		{"no_match tolerates an acceptable tool", caseSpec{NoMatch: true, Acceptable: []string{"s.w"}}, "selected", []string{"s.w"}, true, nil, nil},
		{"needs_context passes when the gateway asks", caseSpec{AnyOf: []string{"s.x", "t.x"}, NeedsContext: true}, "needs_context", nil, true, nil, nil},
		{"needs_context case still passes with a listed tool", caseSpec{AnyOf: []string{"s.x", "t.x"}, NeedsContext: true}, "selected", []string{"t.x"}, true, nil, nil},
		{"needs_context case fails on no_match", caseSpec{AnyOf: []string{"s.x", "t.x"}, NeedsContext: true}, "no_match", nil, false, []string{"s.x", "t.x"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := score(tc.c, tc.status, tc.got)
			if o.Pass != tc.pass || !reflect.DeepEqual(o.Missing, tc.missing) || !reflect.DeepEqual(o.Extra, tc.ex) {
				t.Fatalf("got %+v", o)
			}
		})
	}
}

func TestDiscoverWithRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer eval-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"selected","tools":[{"id":"github.list_prs"}],"duration_ms":15,"prompt_tokens":80}`))
	}))
	defer srv.Close()

	var slept []time.Duration
	client := apiclient.New(srv.URL, apiclient.WithToken("eval-token"))
	res, err := discoverWithRetry(context.Background(), client, "list prs", func(d time.Duration) {
		slept = append(slept, d)
	})
	if err != nil {
		t.Fatalf("discoverWithRetry: %v", err)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("expected one 2s retry sleep, got %v", slept)
	}
	if len(res.Tools) != 1 || res.Tools[0].ID != "github.list_prs" || res.PromptTokens != 80 {
		t.Fatalf("unexpected discover result: %+v", res)
	}
}
