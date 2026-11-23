package cliexec

import (
	"encoding/base64"
	"reflect"
	"testing"
)

func TestProjectRowPreservesAndRedactsNestedJSON(t *testing.T) {
	const token = "gateway-secret"
	encoded := base64.StdEncoding.EncodeToString([]byte(token))
	row := map[string]any{
		"author":           map[string]any{"login": "prefix " + token, "name": nil},
		"labels":           []any{map[string]any{"name": encoded}, "bug"},
		"commits":          []any{map[string]any{"message": token}},
		"files":            []any{},
		"comments":         []any{map[string]any{"body": token, token: true}},
		"defaultBranchRef": map[string]any{"name": "main"},
		"outside_template": token,
	}
	want := map[string]any{
		"author":           map[string]any{"login": "prefix [redacted]", "name": nil},
		"labels":           []any{map[string]any{"name": "[redacted]"}, "bug"},
		"commits":          []any{map[string]any{"message": "[redacted]"}},
		"files":            []any{},
		"comments":         []any{map[string]any{"body": "[redacted]", "[redacted]": true}},
		"defaultBranchRef": map[string]any{"name": "main"},
	}
	fields := []string{"author", "labels", "commits", "files", "comments", "defaultBranchRef", "absent"}
	if out := projectRow(row, fields, secretReplacer(token)); !reflect.DeepEqual(out, want) {
		t.Fatalf("unexpected nested projection: %#v", out)
	}
}
