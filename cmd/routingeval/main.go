// Command routingeval scores a running gateway's tool routing against a case file.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
)

type caseSpec struct {
	Intent     string   `json:"intent"`
	Expected   []string `json:"expected,omitempty"`   // every one must be returned
	AnyOf      []string `json:"any_of,omitempty"`     // at least one must be returned
	Acceptable []string `json:"acceptable,omitempty"` // fine to return, not required
	NoMatch    bool     `json:"no_match,omitempty"`   // nothing but acceptable tools may be returned
	// The intent fits several services equally: a needs_context answer passes too.
	NeedsContext bool   `json:"needs_context,omitempty"`
	Tag          string `json:"tag,omitempty"` // groups cases in the summary
	Note         string `json:"note,omitempty"`
}

type suite struct {
	Description string     `json:"description"`
	Cases       []caseSpec `json:"cases"`
}

type outcome struct {
	Pass    bool
	Missing []string // expected tools that were not returned
	Extra   []string // returned tools that are neither expected nor acceptable
}

// score judges one case. A no_match case passes only when nothing but acceptable
// tools is returned. Any other case passes when every expected tool and at least
// one any_of tool is returned, or when it allows needs_context and the gateway asked
// for clarification; tools in none of expected, any_of or acceptable are Extra.
func score(c caseSpec, status string, got []string) outcome {
	if c.NoMatch {
		var o outcome
		for _, id := range got {
			if !slices.Contains(c.Acceptable, id) {
				o.Extra = append(o.Extra, id)
			}
		}
		o.Pass = len(o.Extra) == 0
		return o
	}
	if c.NeedsContext && status == "needs_context" && len(got) == 0 {
		return outcome{Pass: true}
	}
	var o outcome
	for _, id := range c.Expected {
		if !slices.Contains(got, id) {
			o.Missing = append(o.Missing, id)
		}
	}
	if len(c.AnyOf) > 0 && !slices.ContainsFunc(c.AnyOf, func(id string) bool { return slices.Contains(got, id) }) {
		o.Missing = append(o.Missing, c.AnyOf...)
	}
	for _, id := range got {
		if !slices.Contains(c.Expected, id) && !slices.Contains(c.AnyOf, id) && !slices.Contains(c.Acceptable, id) {
			o.Extra = append(o.Extra, id)
		}
	}
	o.Pass = len(o.Missing) == 0
	return o
}

// discoverWithRetry calls /api/discover and waits out rate limits the gateway
// could not absorb within its own retry budget: on HTTP 429 it sleeps for the
// Retry-After delay the gateway passes on from the provider. Other failures
// are returned at once.
func discoverWithRetry(ctx context.Context, client *apiclient.Client, intent string, sleep func(time.Duration)) (apitypes.DiscoverSummaryResult, error) {
	const attempts = 4
	if sleep == nil {
		sleep = time.Sleep
	}
	req := apitypes.DiscoverRequest{Intent: intent, SummaryOnly: true}
	for attempt := 1; ; attempt++ {
		d, err := client.DiscoverSummary(ctx, req)
		if err == nil {
			return d, nil
		}
		var apiErr *apiclient.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests || apiErr.RetryAfter <= 0 || attempt == attempts {
			return d, err
		}
		fmt.Fprintf(os.Stderr, "  rate limited; retrying in %s\n", apiErr.RetryAfter)
		sleep(apiErr.RetryAfter)
	}
}

func short(ids []string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id[strings.LastIndex(id, ".")+1:]
	}
	return strings.Join(names, ", ")
}

func run() error {
	url := flag.String("url", "http://"+buildinfo.DefaultAddr(), "Gateway URL")
	file := flag.String("cases", "evals/routing/github.json", "Case file")
	flag.Parse()
	raw, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read cases: %w", err)
	}
	var s suite
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("decode cases: %w", err)
	}
	client := apiclient.New(
		*url,
		apiclient.WithToken(os.Getenv("GATEWAY_TOKEN")),
		apiclient.WithTimeout(90*time.Second),
	)
	var positives, positivePass, negatives, negativePass, clean, returned, relevant, tokens int
	var latency int64
	type tally struct{ pass, total int }
	byTag := map[string]*tally{}
	ctx := context.Background()
	for i, c := range s.Cases {
		d, err := discoverWithRetry(ctx, client, c.Intent, time.Sleep)
		if err != nil {
			return fmt.Errorf("case %d %q: %w", i+1, c.Intent, err)
		}
		got := make([]string, len(d.Tools))
		for j, t := range d.Tools {
			got[j] = t.ID
		}
		o := score(c, d.Status, got)
		latency += d.DurationMS
		tokens += d.PromptTokens
		verdict := "PASS"
		if !o.Pass {
			verdict = "MISS"
		}
		if c.Tag != "" {
			t := byTag[c.Tag]
			if t == nil {
				t = &tally{}
				byTag[c.Tag] = t
			}
			t.total++
			if o.Pass {
				t.pass++
			}
		}
		switch {
		case c.NoMatch:
			negatives++
			if o.Pass {
				negativePass++
			}
		default:
			positives++
			if o.Pass {
				positivePass++
				if len(o.Extra) == 0 {
					clean++
				}
			}
			returned += len(got)
			relevant += len(got) - len(o.Extra)
		}
		detail := ""
		if len(o.Missing) > 0 {
			detail += " missing=[" + short(o.Missing) + "]"
		}
		if len(o.Extra) > 0 {
			detail += " noise=[" + short(o.Extra) + "]"
		}
		fmt.Printf("%2d %s %-62s %s (%dms)\n", i+1, verdict, c.Intent, strings.TrimSpace(detail), d.DurationMS)
	}
	n := len(s.Cases)
	fmt.Printf("\nrecall     %d/%d positive cases return every expected tool\n", positivePass, positives)
	fmt.Printf("clean      %d/%d positive cases also return no noise\n", clean, positives)
	fmt.Printf("no_match   %d/%d negative cases correctly return nothing\n", negativePass, negatives)
	if returned > 0 {
		fmt.Printf("precision  %d/%d returned tools were expected or acceptable (%.0f%%)\n", relevant, returned, 100*float64(relevant)/float64(returned))
	}
	fmt.Printf("cost       avg %dms, avg %d input tokens per call\n", latency/int64(n), tokens/n)
	tags := make([]string, 0, len(byTag))
	for tag := range byTag {
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	for _, tag := range tags {
		fmt.Printf("tag %-12s %d/%d pass\n", tag, byTag[tag].pass, byTag[tag].total)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "routingeval:", err)
		os.Exit(1)
	}
}
