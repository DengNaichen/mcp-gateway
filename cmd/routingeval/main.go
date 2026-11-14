// Command routingeval scores a running gateway's tool routing against a case file.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

type caseSpec struct {
	Intent     string   `json:"intent"`
	Expected   []string `json:"expected,omitempty"`   // every one must be returned
	AnyOf      []string `json:"any_of,omitempty"`     // at least one must be returned
	Acceptable []string `json:"acceptable,omitempty"` // fine to return, not required
	NoMatch    bool     `json:"no_match,omitempty"`
	Tag        string   `json:"tag,omitempty"` // groups cases in the summary
	Note       string   `json:"note,omitempty"`
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

// score judges one case. A no_match case passes only when nothing is returned.
// Any other case passes when every expected tool and at least one any_of tool is
// returned; tools in none of expected, any_of or acceptable are Extra.
func score(c caseSpec, got []string) outcome {
	if c.NoMatch {
		return outcome{Pass: len(got) == 0, Extra: got}
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

type discovery struct {
	Status       string `json:"status"`
	DurationMS   int64  `json:"duration_ms"`
	PromptTokens int    `json:"prompt_tokens"`
	Tools        []struct {
		ID string `json:"id"`
	} `json:"tools"`
}

func discover(client *http.Client, url, token, intent string) (discovery, error) {
	var d discovery
	body, err := json.Marshal(map[string]any{"intent": intent, "summary_only": true})
	if err != nil {
		return d, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(url, "/")+"/api/discover", bytes.NewReader(body))
	if err != nil {
		return d, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return d, fmt.Errorf("call gateway: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close response:", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return d, fmt.Errorf("gateway returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return d, fmt.Errorf("decode response: %w", err)
	}
	return d, nil
}

// discoverWithRetry waits and retries on failure: model providers rate-limit by
// input tokens per minute, and the gateway reports their 429 as a 502.
func discoverWithRetry(client *http.Client, url, token, intent string) (discovery, error) {
	const attempts, wait = 4, 20 * time.Second
	var d discovery
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if d, err = discover(client, url, token, intent); err == nil {
			return d, nil
		}
		if attempt < attempts {
			fmt.Fprintf(os.Stderr, "  retrying in %s after: %v\n", wait, err)
			time.Sleep(wait)
		}
	}
	return d, err
}

func short(ids []string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id[strings.LastIndex(id, ".")+1:]
	}
	return strings.Join(names, ", ")
}

func run() error {
	url := flag.String("url", "http://127.0.0.1:8788", "Gateway URL")
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
	client := &http.Client{Timeout: 90 * time.Second}
	token := os.Getenv("GATEWAY_TOKEN")
	var positives, positivePass, negatives, negativePass, clean, returned, relevant, tokens int
	var latency int64
	type tally struct{ pass, total int }
	byTag := map[string]*tally{}
	for i, c := range s.Cases {
		d, err := discoverWithRetry(client, *url, token, c.Intent)
		if err != nil {
			return fmt.Errorf("case %d %q: %w", i+1, c.Intent, err)
		}
		got := make([]string, len(d.Tools))
		for j, t := range d.Tools {
			got[j] = t.ID
		}
		o := score(c, got)
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
