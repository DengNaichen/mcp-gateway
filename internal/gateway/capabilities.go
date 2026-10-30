package gateway

import (
	"context"
	"net/http"
	"slices"
)

// Capability excludes schemas, adapter paths, credentials and policy internals.
// The routing model only receives these summaries after authorization filtering.
type Capability struct {
	ID          string   `json:"id"`
	ServerID    string   `json:"server_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	ReadOnly    bool     `json:"read_only"`
	Score       float64  `json:"score,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

func capability(t Tool) Capability {
	return Capability{ID: t.ID, ServerID: t.ServerID, Name: t.Name, Description: t.Description, Tags: t.Tags, ReadOnly: t.ReadOnly}
}
func summarizeCandidates(candidates []Candidate) []Capability {
	result := make([]Capability, 0, len(candidates))
	for _, c := range candidates {
		summary := capability(c.Tool)
		summary.Score = c.Score
		result = append(result, summary)
	}
	return result
}
func discoveryPresentation(result DiscoverResult, summary bool) any {
	if !summary {
		return result
	}
	result.Candidates = nil
	out := struct {
		DiscoverResult
		Tools []Capability `json:"tools"`
	}{DiscoverResult: result, Tools: []Capability{}}
	for _, t := range result.Tools {
		c := capability(t.Tool)
		c.Reason = t.Reason
		out.Tools = append(out.Tools, c)
	}
	return out
}
func (g *Gateway) ToolSchema(ctx context.Context, id, actor string) (Tool, error) {
	reg := g.Snapshot()
	i := slices.IndexFunc(reg.Tools, func(t Tool) bool { return t.ID == id })
	if i < 0 {
		return Tool{}, problem(404, "工具不存在", nil)
	}
	tool := reg.Tools[i]
	server := slices.IndexFunc(reg.Servers, func(s Server) bool { return s.ID == tool.ServerID && s.Enabled })
	if server < 0 || !g.authorizedTool(ctx, tool, actor) {
		return Tool{}, problem(403, "工具已禁用或没有访问权限", nil)
	}
	return tool, nil
}
func (g *Gateway) registerCapabilities(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, g.Catalog(r.Context(), r.URL.Query().Get("actor"), r.URL.Query().Get("include_demo") == "true"))
	})
	mux.HandleFunc("GET /api/tools/{id}/schema", func(w http.ResponseWriter, r *http.Request) {
		tool, err := g.ToolSchema(r.Context(), r.PathValue("id"), r.URL.Query().Get("actor"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, tool)
	})
}

type CatalogServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ToolCount   int    `json:"tool_count"`
}
type Catalog struct {
	Servers []CatalogServer `json:"servers"`
	Tools   []Capability    `json:"tools"`
}

func (g *Gateway) Catalog(ctx context.Context, actor string, includeDemo bool) Catalog {
	registry := g.Snapshot()
	out := Catalog{Servers: []CatalogServer{}, Tools: []Capability{}}
	for _, server := range registry.Servers {
		if !server.Enabled || server.Demo && !includeDemo {
			continue
		}
		count := 0
		for _, tool := range registry.Tools {
			if tool.ServerID == server.ID && g.authorizedTool(ctx, tool, actor) {
				out.Tools = append(out.Tools, capability(tool))
				count++
			}
		}
		if count > 0 {
			out.Servers = append(out.Servers, CatalogServer{server.ID, server.Name, server.Description, count})
		}
	}
	return out
}
