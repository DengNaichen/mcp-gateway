package upstream

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRedactionPreservesOrdinaryValuesAndMasksSensitiveKeys(t *testing.T) {
	source := Server{
		ID: "svc",
		Headers: map[string]string{
			"Authorization": "Bearer ${HEADER_CREDENTIAL}",
			"X-Private":     "literal-header-secret",
			"X-Short":       "42",
		},
		Env: map[string]string{
			"PORT": "3000", "VERBOSE": "1", "NODE_ENV": "production",
			"KEY": "prefix-${ENV_CREDENTIAL}", "SHORT": "${SHORT_CREDENTIAL}",
		},
	}
	lookup := func(_ string, name string) string {
		return map[string]string{
			"HEADER_CREDENTIAL": "expanded-header-secret",
			"ENV_CREDENTIAL":    "env-1234",
			"SHORT_CREDENTIAL":  "seven77",
		}[name]
	}
	svc := New(Config{LookupEnv: lookup})
	raw := []byte(`{
		"field1-port3000": 3000,
		"count": 1,
		"mode": "production:3000/42/seven77",
		"note": "expanded-header-secret literal-header-secret env-1234",
		"nested": [{"password":"1", "Token":"abc", "client-secret":"42"}]
	}`)
	var want, data any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{
		"field1-port3000": 3000,
		"count": 1,
		"mode": "production:3000/42/seven77",
		"note": "[redacted] [redacted] [redacted]",
		"nested": [{"password":"[redacted]", "Token":"[redacted]", "client-secret":"[redacted]"}]
	}`), &want); err != nil {
		t.Fatal(err)
	}

	t.Run("audit detail", func(t *testing.T) {
		audit, err := svc.RedactAuditDetail(raw, []Server{source})
		if err != nil {
			t.Fatal(err)
		}
		var got any
		if err := json.Unmarshal(audit, &got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("audit = %s, want %+v (decode: %v)", audit, want, err)
		}
	})
	t.Run("upstream response", func(t *testing.T) {
		if got := svc.RedactResponse(data, source); !reflect.DeepEqual(got, want) {
			t.Fatalf("response = %+v, want %+v", got, want)
		}
	})
}
