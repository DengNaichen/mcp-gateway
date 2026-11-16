package registry

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture() Registry {
	return Registry{Servers: []Server{{ID: "svc", Name: "Service", Kind: "http", Endpoint: "https://example.com", Headers: map[string]string{"Authorization": "${TOKEN}"}, Args: []string{"arg"}, Env: map[string]string{"KEY": "${KEY}"}, RedactFields: []string{"secret"}}}, Tools: []Tool{{ID: "svc.tool", ServerID: "svc", Name: "tool", Description: "description", Actors: []string{"developer"}, Tags: []string{"tag"}, InputSchema: map[string]any{"type": "object", "required": []string{"key"}, "properties": map[string]any{"key": map[string]any{"type": "string"}}}, Example: map[string]any{"nested": []any{map[string]any{"key": "value"}}}, HTTP: &HTTPMapping{Parameters: []HTTPParameter{{Name: "key", In: "query", Style: "form"}}, Responses: map[string]HTTPResponse{"200": {MediaType: "application/json", Schema: map[string]any{"type": "object"}}}}}}}
}

func TestSnapshotsOwnNestedValues(t *testing.T) {
	initial := fixture()
	service := New(initial, nil)
	initial.Servers[0].Headers["Authorization"] = "changed"
	snapshot := service.Snapshot()
	snapshot.Servers[0].Headers["Authorization"] = "changed"
	snapshot.Servers[0].Args[0] = "changed"
	snapshot.Servers[0].Env["KEY"] = "changed"
	snapshot.Servers[0].RedactFields[0] = "changed"
	tool := &snapshot.Tools[0]
	tool.InputSchema["required"].([]string)[0] = "changed"
	tool.InputSchema["properties"].(map[string]any)["key"].(map[string]any)["type"] = "changed"
	tool.Example["nested"].([]any)[0].(map[string]any)["key"] = "changed"
	tool.Actors[0] = "changed"
	tool.Tags[0] = "changed"
	tool.HTTP.Parameters[0].Name = "changed"
	tool.HTTP.Responses["200"].Schema["type"] = "changed"
	got := service.Snapshot()
	if !reflect.DeepEqual(got, fixture()) {
		t.Fatalf("snapshot mutated service: %+v", got)
	}
	// Writes also sever references held by callers.
	server := fixture().Servers[0]
	if err := service.SaveServer(server); err != nil {
		t.Fatal(err)
	}
	server.Headers["Authorization"] = "changed"
	if service.Snapshot().Servers[0].Headers["Authorization"] != "${TOKEN}" {
		t.Fatal("saved server aliases input")
	}
}

func TestSnapshotDoesNotWaitForPersistenceAndFailureDoesNotPublish(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	want := errors.New("disk failure")
	service := New(fixture(), func(Registry) error { close(entered); <-release; return want })
	go func() { s := fixture().Servers[0]; s.Name = "changed"; done <- service.SaveServer(s) }()
	<-entered
	read := make(chan Registry, 1)
	go func() { read <- service.Snapshot() }()
	select {
	case got := <-read:
		if got.Servers[0].Name != "Service" {
			t.Fatal("uncommitted snapshot visible")
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("snapshot waited for writer")
	}
	close(release)
	if err := <-done; !errors.Is(err, want) {
		t.Fatal(err)
	}
	if service.Snapshot().Servers[0].Name != "Service" {
		t.Fatal("failed write was published")
	}
}

func TestConcurrentWritersDoNotLoseUpdates(t *testing.T) {
	service := New(Registry{}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Go(func() {
			id := fmt.Sprintf("svc-%d", i)
			if err := service.SaveServer(Server{ID: id, Name: id, Kind: "http", Endpoint: "https://example.com"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(service.Snapshot().Servers) != 40 {
		t.Fatal("lost concurrent write")
	}
}

func TestRegistryValidationUsesDomainErrors(t *testing.T) {
	service := New(fixture(), nil)
	if err := service.SaveServer(Server{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid error, got %v", err)
	}
	tool := fixture().Tools[0]
	tool.InputSchema = nil
	var invalid *ValidationError
	if err := service.SaveTool(tool); !errors.As(err, &invalid) || invalid.Message != "input_schema 无效" {
		t.Fatalf("unexpected schema error: %v", err)
	}
	tool = fixture().Tools[0]
	tool.ServerID = "missing"
	if err := service.SaveTool(tool); !errors.Is(err, ErrInvalid) || err.Error() != "server_id 不存在" {
		t.Fatal(err)
	}
}

// FuzzValidateMapping checks the promise that a mapping accepted by
// ValidateMapping defines a path parameter for every {variable} in the path.
func FuzzValidateMapping(f *testing.F) {
	f.Add("/items/{id}", "id")
	f.Add("/items/{id}/sub/{other}", "id")
	f.Add("/items/{id}", "")
	f.Add("/{a}{b}", "a")
	f.Add("/{{id}}", "id")
	f.Add("/{ id }", " id ")
	f.Add("/{}", "")
	f.Add("/plain", "unused")
	f.Add("/{é}/{\x00}", "é")
	f.Fuzz(func(t *testing.T, path, name string) {
		tool := Tool{
			Path: path,
			HTTP: &HTTPMapping{
				Parameters: []HTTPParameter{{Name: name, In: "path", Style: "simple"}},
				Responses:  map[string]HTTPResponse{"200": {MediaType: "empty"}},
			},
		}
		if err := ValidateMapping(tool); err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("unexpected error class: %v", err)
			}
			return
		}
		for _, match := range PathVariable.FindAllStringSubmatch(path, -1) {
			if match[1] != name || strings.ContainsAny(match[1], "{}") {
				t.Fatalf("accepted path %q with unmapped variable %q (mapped %q)", path, match[1], name)
			}
		}
	})
}
