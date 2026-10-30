package gateway

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
	"strconv"
	"strings"
)

func argumentGroup(in string) string {
	if in == "header" {
		return "headers"
	}
	if in == "cookie" {
		return "cookies"
	}
	return in
}
func scalar(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case json.Number:
		return x.String(), nil
	case nil:
		return "", nil
	default:
		return "", invalid("参数需要标量值")
	}
}
func parameterParts(v any) (parts []string, object bool, err error) {
	switch x := v.(type) {
	case []any:
		for _, value := range x {
			part, err := scalar(value)
			if err != nil {
				return nil, false, err
			}
			parts = append(parts, part)
		}
		return parts, false, nil
	case map[string]any:
		for _, key := range sortedKeys(x) {
			part, err := scalar(x[key])
			if err != nil {
				return nil, true, err
			}
			parts = append(parts, key, part)
		}
		return parts, true, nil
	default:
		part, err := scalar(v)
		return []string{part}, false, err
	}
}
func simpleParameter(v any, explode bool, escape bool) (string, error) {
	parts, obj, err := parameterParts(v)
	if err != nil {
		return "", err
	}
	if escape {
		for i := range parts {
			if parts[i] == "." || parts[i] == ".." {
				return "", invalid("路径参数不能是相对路径片段")
			}
			parts[i] = url.PathEscape(parts[i])
		}
	}
	if obj && explode {
		pairs := []string{}
		for i := 0; i < len(parts); i += 2 {
			pairs = append(pairs, parts[i]+"="+parts[i+1])
		}
		return strings.Join(pairs, ","), nil
	}
	return strings.Join(parts, ","), nil
}
func addQuery(q url.Values, p HTTPParameter, v any) error {
	parts, obj, err := parameterParts(v)
	if err != nil {
		return err
	}
	if obj && p.Style == "deepObject" {
		for i := 0; i < len(parts); i += 2 {
			q.Add(p.Name+"["+parts[i]+"]", parts[i+1])
		}
		return nil
	}
	if p.Style == "deepObject" && !obj {
		return invalid("deepObject 参数必须是对象")
	}
	if obj && p.Explode {
		for i := 0; i < len(parts); i += 2 {
			q.Add(parts[i], parts[i+1])
		}
		return nil
	}
	if _, array := v.([]any); array && p.Explode {
		for _, part := range parts {
			q.Add(p.Name, part)
		}
		return nil
	}
	delimiter := ","
	if p.Style == "spaceDelimited" {
		delimiter = " "
	}
	if p.Style == "pipeDelimited" {
		delimiter = "|"
	}
	q.Add(p.Name, strings.Join(parts, delimiter))
	return nil
}
func buildMappedRequest(ctx context.Context, s Server, t Tool, args map[string]any) (*http.Request, error) {
	if t.HTTP == nil {
		return nil, invalid("缺少 HTTP 映射")
	}
	if !slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, t.Method) {
		return nil, invalid("不支持的 HTTP 方法")
	}
	if !strings.HasPrefix(t.Path, "/") || strings.HasPrefix(t.Path, "//") || strings.ContainsAny(t.Path, "?#") {
		return nil, invalid("HTTP 映射路径无效")
	}
	path := t.Path
	query := url.Values{}
	headers := http.Header{}
	cookies := []*http.Cookie{}
	for _, p := range t.HTTP.Parameters {
		if !supportedParameterStyle(p.In, p.Style, p.Explode) {
			return nil, invalid("HTTP 参数序列化无效")
		}
		group, _ := args[argumentGroup(p.In)].(map[string]any)
		v, exists := group[p.Name]
		if !exists {
			continue
		}
		switch p.In {
		case "path":
			encoded, err := simpleParameter(v, p.Explode, true)
			if err != nil {
				return nil, err
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", encoded)
		case "query":
			if err := addQuery(query, p, v); err != nil {
				return nil, err
			}
		case "header":
			if slices.Contains([]string{"authorization", "proxy-authorization", "host", "cookie", "connection", "content-length", "content-type"}, strings.ToLower(p.Name)) {
				return nil, invalid("参数不能覆盖认证或协议请求头")
			}
			value, err := simpleParameter(v, p.Explode, false)
			if err != nil {
				return nil, err
			}
			headers.Set(p.Name, value)
		case "cookie":
			parts, obj, err := parameterParts(v)
			if err != nil {
				return nil, err
			}
			if obj && p.Explode {
				for i := 0; i < len(parts); i += 2 {
					cookies = append(cookies, &http.Cookie{Name: parts[i], Value: url.QueryEscape(parts[i+1])})
				}
			} else {
				if arr, ok := v.([]any); ok && p.Explode {
					for _, element := range arr {
						value, err := scalar(element)
						if err != nil {
							return nil, err
						}
						cookies = append(cookies, &http.Cookie{Name: p.Name, Value: url.QueryEscape(value)})
					}
				} else {
					cookies = append(cookies, &http.Cookie{Name: p.Name, Value: url.QueryEscape(strings.Join(parts, ","))})
				}
			}
		}
	}
	if pathVariable.MatchString(path) {
		return nil, invalid("缺少路径参数")
	}
	endpoint := strings.TrimRight(s.Endpoint, "/") + path
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse mapped endpoint: %w", err)
	}
	if u.RawQuery != "" {
		return nil, invalid("服务基地址不能包含 query")
	}
	u.RawQuery = query.Encode()
	var body io.Reader
	if t.HTTP.Body {
		if value, exists := args["body"]; exists {
			b, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("encode mapped body: %w", err)
			}
			body = bytes.NewReader(b)
		}
	}
	req, err := http.NewRequestWithContext(ctx, t.Method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create mapped request: %w", err)
	}
	req.Header = headers
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/plain")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	for key, value := range s.Headers {
		req.Header.Set(key, os.ExpandEnv(value))
	}
	return req, nil
}
func mappedResponse(t Tool, status int, b []byte) (any, error) {
	binding, known := t.HTTP.Responses[strconv.Itoa(status)]
	if !known {
		binding, known = t.HTTP.Responses[strconv.Itoa(status/100)+"XX"]
	}
	if !known {
		binding, known = t.HTTP.Responses["default"]
	}
	if !known {
		return nil, fmt.Errorf("response status %d is not declared in openapi", status)
	}
	var data any
	switch binding.MediaType {
	case "empty":
		if len(bytes.TrimSpace(b)) > 0 {
			return nil, fmt.Errorf("unexpected body for empty response")
		}
		return nil, nil
	case "text/plain":
		data = string(b)
	case "application/json":
		if err := json.Unmarshal(b, &data); err != nil {
			return nil, fmt.Errorf("invalid json response: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported response media type %q", binding.MediaType)
	}
	if binding.Schema != nil {
		schema, err := compileSchema(binding.Schema)
		if err != nil {
			return nil, err
		}
		if err := schema.Validate(data); err != nil {
			return nil, fmt.Errorf("upstream response violates schema: %w", err)
		}
	}
	if binding.MediaType == "text/plain" {
		return map[string]any{"text": data}, nil
	}
	return data, nil
}
func callMappedHTTP(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	if err := checkEnvironment(s); err != nil {
		return nil, err
	}
	req, err := buildMappedRequest(ctx, s, t, args)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("send mapped request: %w", err)
	}
	defer closeBody(resp)
	b, err := readBounded(resp.Body, 8<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("downstream returned http %d", resp.StatusCode)
	}
	return mappedResponse(t, resp.StatusCode, b)
}
