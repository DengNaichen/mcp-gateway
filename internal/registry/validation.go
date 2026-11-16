package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrInvalid = errors.New("invalid registry definition")

type ValidationError struct {
	Message string
	Cause   error
}

func (e *ValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}
func (e *ValidationError) Unwrap() error        { return e.Cause }
func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }
func invalid(message string) error              { return &ValidationError{Message: message} }

var pathVariable = regexp.MustCompile(`\{([^{}]+)\}`)

func CompileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	if schema == nil {
		return nil, invalid("工具需要 input_schema")
	}
	// The validator accepts JSON-native []any, not typed Go slices (e.g. the
	// []string required lists built by deterministic importers and demo tools).
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(b, &normalized); err != nil {
		return nil, fmt.Errorf("normalize input schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:gateway:input", normalized); err != nil {
		return nil, fmt.Errorf("register input schema: %w", err)
	}
	s, err := c.Compile("urn:gateway:input")
	if err != nil {
		return nil, fmt.Errorf("compile input schema: %w", err)
	}
	return s, nil
}

func ValidID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r))
	}) == -1
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return invalid("endpoint 必须是有效的 HTTP(S) 地址，凭据请使用环境变量请求头")
	}
	return nil
}

func ValidateMapping(t Tool) error {
	if t.HTTP == nil {
		return nil
	}
	if t.Adapter != "" {
		return invalid("工具不能同时定义脚本和 HTTP 映射")
	}
	seen := map[string]bool{}
	for _, p := range t.HTTP.Parameters {
		if p.Name == "" || !SupportedParameterStyle(p.In, p.Style, p.Explode) || seen[p.In+":"+p.Name] {
			return invalid("HTTP 参数映射无效或重复")
		}
		seen[p.In+":"+p.Name] = true
	}
	for _, match := range pathVariable.FindAllStringSubmatch(t.Path, -1) {
		if !seen["path:"+match[1]] {
			return invalid("HTTP 路径模板缺少映射")
		}
	}
	if len(t.HTTP.Responses) == 0 {
		return invalid("HTTP 映射缺少响应定义")
	}
	for _, r := range t.HTTP.Responses {
		if r.MediaType != "application/json" && r.MediaType != "text/plain" && r.MediaType != "empty" {
			return invalid("HTTP 响应 media type 无效")
		}
		if r.Schema != nil {
			if _, err := CompileSchema(r.Schema); err != nil {
				return err
			}
		}
	}
	return nil
}
func SupportedParameterStyle(in, style string, explode bool) bool {
	switch in {
	case "path", "header":
		return style == "simple"
	case "query":
		return style == "form" || (style == "deepObject" && explode) || ((style == "spaceDelimited" || style == "pipeDelimited") && !explode)
	case "cookie":
		return style == "form"
	}
	return false
}
