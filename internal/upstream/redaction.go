package upstream

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
)

var emailPattern = regexp.MustCompile(`\b[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\b`)

// RedactResponse redacts configured secrets, runtime lookup secrets, and
// active OAuth access tokens held by u.
func (u *Service) RedactResponse(data any, s Server) any {
	if u == nil {
		return RedactResponse(data, s)
	}
	return redact(data, s, appendServerSecrets(u.TokenSecrets(), s, u.serverLookup(s), false), false)
}

func RedactResponse(data any, s Server) any {
	return RedactResponseWithLookup(data, s, os.Getenv)
}

func RedactResponseWithLookup(data any, s Server, lookup func(string) string) any {
	return redact(data, s, appendServerSecrets(nil, s, lookup, false), false)
}

// RedactAuditDetail applies the shared redactor to the serialized detail so
// typed structs, slices, and JSON values are all visited before persistence.
func (u *Service) RedactAuditDetail(detail json.RawMessage, servers []Server, extraSecrets ...string) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(detail))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	secrets := append(u.TokenSecrets(), extraSecrets...)
	for _, s := range servers {
		secrets = appendServerSecrets(secrets, s, u.serverLookup(s), true)
	}
	return json.Marshal(redact(value, Server{}, secrets, true))
}

func appendServerSecrets(secrets []string, s Server, lookup func(string) string, includeReferences bool) []string {
	if lookup == nil {
		lookup = os.Getenv
	}
	expand := func(value string) string {
		return os.Expand(value, func(name string) string {
			value := lookup(name)
			if includeReferences {
				secrets = append(secrets, value)
			}
			return value
		})
	}
	for _, value := range s.Headers {
		expanded := expand(value)
		secrets = append(secrets, expanded, strings.TrimPrefix(expanded, "Bearer "))
	}
	for _, value := range s.Env {
		expanded := expand(value)
		secrets = append(secrets, expanded)
	}
	return secrets
}

func redact(data any, s Server, secrets []string, audit bool) any {
	const replacement = "[redacted]"
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	pairs := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		if secret != "" {
			pairs = append(pairs, secret, replacement)
		}
	}
	replace := strings.NewReplacer(pairs...)
	var walk func(any, string) any
	walk = func(value any, path string) any {
		for _, field := range s.RedactFields {
			if path == field {
				return replacement
			}
		}
		switch x := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(x))
			for key, child := range x {
				normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
				outKey := key
				if audit {
					outKey = replace.Replace(key)
				}
				switch normalized {
				case "password", "secret", "clientsecret", "authorization", "apikey", "accesstoken", "refreshtoken", "token":
					out[outKey] = replacement
					continue
				}
				next := key
				if path != "" {
					next = path + "." + key
				}
				out[outKey] = walk(child, next)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, child := range x {
				out[i] = walk(child, path)
			}
			return out
		case string:
			text := replace.Replace(x)
			if s.RedactEmails {
				text = emailPattern.ReplaceAllString(text, "[redacted-email]")
			}
			return text
		case json.Number:
			if audit {
				if text := replace.Replace(string(x)); text != string(x) {
					return text
				}
			}
			return value
		default:
			return value
		}
	}
	return walk(data, "")
}
