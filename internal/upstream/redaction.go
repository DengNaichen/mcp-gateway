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
	referenced, literal := appendServerSecrets(u.TokenSecrets(), nil, s, u.serverLookup(s))
	return redact(data, s, referenced, literal, false)
}

func RedactResponse(data any, s Server) any {
	return RedactResponseWithLookup(data, s, os.Getenv)
}

func RedactResponseWithLookup(data any, s Server, lookup func(string) string) any {
	referenced, literal := appendServerSecrets(nil, nil, s, lookup)
	return redact(data, s, referenced, literal, false)
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
	referenced := append(u.TokenSecrets(), extraSecrets...)
	var literal []string
	for _, s := range servers {
		referenced, literal = appendServerSecrets(referenced, literal, s, u.serverLookup(s))
	}
	return json.Marshal(redact(value, Server{}, referenced, literal, true))
}

func appendServerSecrets(referenced, literal []string, s Server, lookup func(string) string) ([]string, []string) {
	if lookup == nil {
		lookup = os.Getenv
	}
	for _, value := range s.Headers {
		resolvedAny := false
		expanded := os.Expand(value, func(name string) string {
			v := lookup(name)
			if v != "" {
				resolvedAny = true
				referenced = append(referenced, v)
			}
			return v
		})
		if resolvedAny {
			referenced = append(referenced, expanded, strings.TrimPrefix(expanded, "Bearer "))
		} else {
			literal = append(literal, expanded, strings.TrimPrefix(expanded, "Bearer "))
		}
	}
	// Environment literals often contain ordinary settings such as ports.
	// Expand only to collect values referenced by the configuration.
	for _, value := range s.Env {
		os.Expand(value, func(name string) string {
			v := lookup(name)
			if v != "" {
				referenced = append(referenced, v)
			}
			return v
		})
	}
	return referenced, literal
}

func redact(data any, s Server, referencedSecrets, literalSecrets []string, audit bool) any {
	const (
		replacement            = "[redacted]"
		minLiteralSecretLength = 8
	)
	secrets := make([]string, 0, len(referencedSecrets)+len(literalSecrets))
	for _, secret := range referencedSecrets {
		if secret != "" {
			secrets = append(secrets, secret)
		}
	}
	for _, secret := range literalSecrets {
		if len(secret) >= minLiteralSecretLength {
			secrets = append(secrets, secret)
		}
	}
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	pairs := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		pairs = append(pairs, secret, replacement)
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
