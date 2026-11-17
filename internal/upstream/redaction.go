package upstream

import (
	"os"
	"regexp"
	"strings"
)

var emailPattern = regexp.MustCompile(`\b[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\b`)

// RedactResponse redacts configured secrets, runtime lookup secrets, and
// active OAuth access tokens held by u.
func (u *Service) RedactResponse(data any, s Server) any {
	if u == nil {
		return RedactResponse(data, s)
	}
	return redactResponse(data, s, u.serverLookup(s), u.TokenSecrets()...)
}

func RedactResponse(data any, s Server) any {
	return RedactResponseWithLookup(data, s, os.Getenv)
}

func RedactResponseWithLookup(data any, s Server, lookup func(string) string) any {
	return redactResponse(data, s, lookup)
}

func redactResponse(data any, s Server, lookup func(string) string, extraSecrets ...string) any {
	if lookup == nil {
		lookup = os.Getenv
	}
	secrets := append([]string(nil), extraSecrets...)
	for _, value := range s.Headers {
		expanded := os.Expand(value, lookup)
		secrets = append(secrets, expanded, strings.TrimPrefix(expanded, "Bearer "))
	}
	for _, value := range s.Env {
		secrets = append(secrets, os.Expand(value, lookup))
	}
	var walk func(any, string) any
	walk = func(value any, path string) any {
		for _, field := range s.RedactFields {
			if path == field {
				return "[redacted]"
			}
		}
		switch x := value.(type) {
		case map[string]any:
			out := map[string]any{}
			for key, child := range x {
				normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
				switch normalized {
				case "password", "secret", "authorization", "apikey", "accesstoken", "refreshtoken", "token":
					out[key] = "[redacted]"
					continue
				}
				next := key
				if path != "" {
					next = path + "." + key
				}
				out[key] = walk(child, next)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, child := range x {
				out[i] = walk(child, path)
			}
			return out
		case string:
			text := x
			for _, secret := range secrets {
				if secret != "" {
					text = strings.ReplaceAll(text, secret, "[redacted]")
				}
			}
			if s.RedactEmails {
				text = emailPattern.ReplaceAllString(text, "[redacted-email]")
			}
			return text
		default:
			return value
		}
	}
	return walk(data, "")
}
