package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
)

func CallHTTP(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	return CallHTTPWithLookup(ctx, s, t, args, os.Getenv)
}

func CallHTTPWithLookup(ctx context.Context, s Server, t Tool, args map[string]any, lookup func(string) string) (any, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	if t.HTTP != nil {
		return callMappedHTTPWithLookup(ctx, s, t, args, lookup)
	}
	if err := CheckEnvironment(s, lookup); err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(s.Endpoint, "/") + "/" + strings.TrimLeft(t.Path, "/")
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse tool endpoint: %w", err)
		}
		q := u.Query()
		for k, v := range args {
			if text, ok := v.(string); ok {
				q.Set(k, text)
			} else {
				b, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("encode query argument: %w", err)
				}
				q.Set(k, string(b))
			}
		}
		u.RawQuery = q.Encode()
		endpoint = u.String()
	} else {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("encode tool arguments: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create downstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.Headers {
		req.Header.Set(k, os.Expand(v, lookup))
	}
	resp, err := (&http.Client{CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("send downstream request: %w", err)
	}
	defer closeBody(resp)
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read downstream response: %w", err)
	}
	if len(b) > 8<<20 {
		return nil, fmt.Errorf("downstream response exceeds 8 mb")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("downstream returned http %d", resp.StatusCode)
	}
	var data any
	if err := json.Unmarshal(b, &data); err != nil {
		return map[string]any{"text": string(b)}, nil
	}
	return data, nil
}

// project applies nested field paths to objects and each item of an array.
// It is a response-size feature, not a substitute for authorization/redaction.
func Project(value any, fields []string) any {
	if arr, ok := value.([]any); ok {
		out := make([]any, len(arr))
		for i, v := range arr {
			out[i] = Project(v, fields)
		}
		return out
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	groups := map[string][]string{}
	for _, field := range fields {
		head, tail, found := strings.Cut(strings.TrimSpace(field), ".")
		if head == "" {
			continue
		}
		if !found {
			groups[head] = append(groups[head], "")
		} else {
			groups[head] = append(groups[head], tail)
		}
	}
	out := map[string]any{}
	for head, children := range groups {
		if v, ok := obj[head]; ok {
			if slices.Contains(children, "") {
				out[head] = v
			} else {
				out[head] = Project(v, children)
			}
		}
	}
	return out
}
