package routing

import (
	"gatewaylab/internal/registry"
)

type Tool = registry.Tool

// Capability excludes schemas, adapter paths, credentials and policy internals.
// The routing model only receives these summaries after authorization filtering.
type Capability struct {
	ID          string   `json:"id"`
	ServerID    string   `json:"server_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	ReadOnly    bool     `json:"read_only"`
	Reason      string   `json:"reason,omitempty"`
}

func Summarize(t Tool) Capability {
	return Capability{ID: t.ID, ServerID: t.ServerID, Name: t.Name, Description: t.Description, Tags: t.Tags, ReadOnly: t.ReadOnly}
}
func SummarizeTools(tools []Tool) []Capability {
	result := make([]Capability, 0, len(tools))
	for _, t := range tools {
		result = append(result, Summarize(t))
	}
	return result
}
