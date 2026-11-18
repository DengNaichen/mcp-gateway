package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"

	"github.com/egoist/mygo/ui"
)

const servicesToolPageSize = 30

var nonServiceIDRe = regexp.MustCompile(`[^a-z0-9._-]+`)

type servicesPageState struct {
	serviceListState
	serviceDetailState
	serviceConnFormState
	serviceMaintState

	add      serviceAddFormState
	toolEdit serviceToolEditState
	httpTool serviceHTTPToolState

	// Kept directly on servicesPageState because sibling credentials files
	// construct &servicesPageState{credentialServerID: ...} and access these directly.
	credentialServerID string
	credentialState    apitypes.ServerCredentials
	credentialValues   map[string]string
	credentialBusy     bool
	credentialError    string
}

func (u *DesktopUI) servicesState() *servicesPageState {
	if u.services == nil {
		u.services = &servicesPageState{
			serviceMaintState: defaultServiceMaintState(),
			add:               defaultServiceAddFormState(),
			httpTool:          defaultServiceHTTPToolState(),
		}
	}
	return u.services
}

func serviceProtocolTag(s apitypes.Server) string {
	if s.Kind == "http" {
		return "HTTP REST"
	}
	switch strings.ToLower(strings.TrimSpace(s.Transport)) {
	case "stdio":
		return "MCP Stdio"
	case "sse":
		return "MCP SSE"
	default:
		return "MCP Streamable HTTP"
	}
}

func slugServiceID(name, fallbackPrefix string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonServiceIDRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		return fmt.Sprintf("%s-%d", fallbackPrefix, time.Now().Unix()%100000)
	}
	return s
}

func (u *DesktopUI) renderServices(c *ui.Context) {
	st := u.servicesState()

	// Consume cross-page trigger to open Add Service modal.
	if u.consumePendingAddService() {
		st.add.open(0)
	}

	var selectedServer *apitypes.Server
	if activeID := u.activeServiceID(); activeID != "" {
		for i := range u.State.Registry.Servers {
			if u.State.Registry.Servers[i].ID == activeID {
				selectedServer = &u.State.Registry.Servers[i]
				break
			}
		}
		if selectedServer == nil {
			u.clearActiveService()
		}
	}

	if selectedServer != nil {
		u.renderServiceDetail(c, st, *selectedServer)
	} else {
		u.renderServiceList(c, st)
	}

	if st.add.show {
		u.renderAddServiceModal(c, st)
	}
	if st.toolEdit.show && st.toolEdit.tool != nil {
		u.renderToolEditModal(c, st)
	}
	if st.httpTool.show && selectedServer != nil {
		u.renderAddHTTPToolModal(c, st, *selectedServer)
	}
}

func (st *servicesPageState) enterDetail(serverID string) {
	if st.detailServerID != serverID {
		st.detailServerID = serverID
		st.detailSubtab = 0
		st.toolQuery = ""
		st.toolFilter = 0
		st.toolPage = 1
		st.confirmDeactivate = false
		st.connHydratedID = ""
		st.maintHydratedServerID = ""
		st.maintError = ""
		st.selectedRunID = ""
	}
}

// --- Parsing helpers ---

func parseStringSliceJSON(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func parseStringMapJSON(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func splitCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if v := strings.TrimSpace(l); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func splitCommaOrLineList(s string) []string {
	s = strings.ReplaceAll(s, "\n", ",")
	return splitCommaList(s)
}

func marshalMapOrDefault(v any) string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	strMap := make(map[string]string, len(m))
	for _, k := range keys {
		strMap[k] = fmt.Sprint(m[k])
	}
	return PrettyJSON(strMap)
}

func marshalSliceOrDefault(v any) string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return "[]"
	}
	strSlice := make([]string, 0, len(arr))
	for _, item := range arr {
		strSlice = append(strSlice, fmt.Sprint(item))
	}
	return PrettyJSON(strSlice)
}

func subtlePanel(c *ui.Context, minWidth, grow float32, fn func()) ui.Element {
	return ui.Column(c).
		Grow(grow).
		MinWidth(minWidth).
		Padding(14, 16).
		Gap(8).
		Radius(8).
		Background(SailBgSubtle).
		Children(fn)
}
