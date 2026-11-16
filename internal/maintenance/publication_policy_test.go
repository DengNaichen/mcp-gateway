package maintenance

import (
	"encoding/json"
	"testing"

	"gatewaylab/internal/registry"
)

func TestSchemaCompatibilityConservative(t *testing.T) {
	old := objectSchema(map[string]any{"service": map[string]any{"type": "string"}}, "service")
	next := objectSchema(map[string]any{"service": map[string]any{"type": "string", "description": "renamed description"}, "limit": map[string]any{"type": "integer"}}, "service")
	if !inputCompatible(old, next) {
		t.Fatal("optional extension should be compatible")
	}
	if inputCompatible(next, old) {
		t.Fatal("removed parameter considered compatible")
	}
}

func TestPropertyNamesAreNotSchemaAnnotations(t *testing.T) {
	old := objectSchema(map[string]any{"description": map[string]any{"type": "string"}}, "description")
	changed := objectSchema(map[string]any{"description": map[string]any{"type": "integer"}}, "description")
	if inputCompatible(old, changed) {
		t.Fatal("property named description was mistaken for annotation")
	}
}
func TestMappingAndExecutableChangesRequireReview(t *testing.T) {
	tools := []Tool{{ID: "ci.create_build", HTTP: &HTTPMapping{Parameters: []registry.HTTPParameter{{Name: "project", In: "path", Style: "simple"}, {Name: "branch", In: "query", Style: "form"}}, Responses: map[string]registry.HTTPResponse{"200": {MediaType: "application/json"}}}}}
	before := tools[0]
	var after Tool
	if err := json.Unmarshal(mustJSON(t, before), &after); err != nil {
		t.Fatal(err)
	}
	// Serialization can change while the advertised input schema stays identical.
	after.HTTP.Parameters[1].Explode = !after.HTTP.Parameters[1].Explode
	if !publicationReview([]Tool{before}, []Tool{after}).Required {
		t.Fatal("serialization change auto-published")
	}
	m := testMaintainer(t)
	old := Tool{ID: "service.logs", Adapter: "adapter.mjs", Revision: "previous"}
	m.runs["previous"] = MaintenanceRun{Artifacts: map[string]string{"adapter.mjs": "old-hash"}}
	r := MaintenanceRun{Before: []Tool{old}, Tools: []Tool{old}, Artifacts: map[string]string{"adapter.mjs": "changed-hash"}}
	review := publicationReview(r.Before, r.Tools)
	m.reviewArtifacts(r, &review)
	if !review.Required {
		t.Fatal("changed executable published without owner review")
	}
}

func TestBreakingPublicationChangesRequireReview(t *testing.T) {
	// Required input widening and deletion require explicit review even if tests pass.
	previous := Tool{ID: "service.logs", ReadOnly: true, InputSchema: objectSchema(map[string]any{"service": map[string]any{"type": "string"}}, "service")}
	changed := previous
	changed.InputSchema = objectSchema(map[string]any{"service": map[string]any{"type": "string"}, "region": map[string]any{"type": "string"}}, "service", "region")
	if !publicationReview([]Tool{previous}, []Tool{changed}).Required {
		t.Fatal("new required argument was considered compatible")
	}
	changed = previous
	changed.ReadOnly = false
	if !publicationReview([]Tool{previous}, []Tool{changed}).Required {
		t.Fatal("write escalation allowed")
	}
	if !publicationReview([]Tool{previous}, nil).Required {
		t.Fatal("removal did not require approval")
	}
}
