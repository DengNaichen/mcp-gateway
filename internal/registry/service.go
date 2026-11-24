package registry

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Service publishes immutable snapshots only after persistence succeeds. Writers
// serialize here; readers never acquire the writer mutex or a gateway lock.
type Service struct {
	mu      sync.Mutex
	current atomic.Pointer[Registry]
	persist func(Registry) error
}

func New(initial Registry, persist func(Registry) error) *Service {
	s := &Service{persist: persist}
	value := Clone(initial)
	s.current.Store(&value)
	return s
}
func (s *Service) Snapshot() Registry { return Clone(*s.current.Load()) }

// Servers snapshots service configuration without copying the tool schemas.
func (s *Service) Servers() []Server {
	return Clone(Registry{Servers: s.current.Load().Servers}).Servers
}

// Update also supports use cases whose callback persists multiple tables in one
// transaction. UpdateWithCommit uses that transaction instead of the default repo.
func (s *Service) Update(change func(Registry) (Registry, error)) error {
	return s.UpdateWithCommit(change, s.persist)
}
func (s *Service) UpdateWithCommit(change func(Registry) (Registry, error), commit func(Registry) error) error {
	return s.updateWithCommit(change, commit, "")
}

func (s *Service) updateWithCommit(change func(Registry) (Registry, error), commit func(Registry) error, cliServiceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := *s.current.Load()
	next, err := change(Clone(current))
	if err != nil {
		return err
	}
	if err := protectCLITools(current, next, cliServiceID); err != nil {
		return err
	}
	next = Clone(next)
	if commit != nil {
		if err := commit(next); err != nil {
			return err
		}
	}
	published := Clone(next)
	s.current.Store(&published)
	return nil
}

// Clone preserves JSON-native values and typed slices used by deterministic
// importers, while preventing callers from mutating a published snapshot.
func Clone(r Registry) Registry {
	r.Servers = slices.Clone(r.Servers)
	r.Tools = slices.Clone(r.Tools)
	for i := range r.Servers {
		s := &r.Servers[i]
		s.Headers = maps.Clone(s.Headers)
		s.Env = maps.Clone(s.Env)
		s.Args = slices.Clone(s.Args)
		s.RedactFields = slices.Clone(s.RedactFields)
	}
	for i := range r.Tools {
		t := &r.Tools[i]
		t.Tags = slices.Clone(t.Tags)
		t.Actors = slices.Clone(t.Actors)
		t.InputSchema = cloneMap(t.InputSchema)
		t.OutputSchema = cloneMap(t.OutputSchema)
		t.Example = cloneMap(t.Example)
		if t.HTTP != nil {
			h := *t.HTTP
			h.Parameters = slices.Clone(h.Parameters)
			h.Responses = maps.Clone(h.Responses)
			for k, v := range h.Responses {
				v.Schema = cloneMap(v.Schema)
				h.Responses[k] = v
			}
			t.HTTP = &h
		}
	}
	return r
}
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return cloneValue(reflect.ValueOf(m)).Interface().(map[string]any)
}
func cloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneValue(v.Elem()))
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			out.SetMapIndex(it.Key(), cloneValue(it.Value()))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneValue(v.Index(i)))
		}
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(cloneValue(v.Elem()))
		return out.Convert(v.Type())
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneValue(v.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				out.Field(i).Set(cloneValue(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}

// protectCLITools covers catalog updates from all producers, including imports
// and maintenance publication. Ordinary edits can only toggle enablement.
func protectCLITools(current, next Registry, cliServiceID string) error {
	controlled := map[string]bool{}
	for _, s := range current.Servers {
		if s.Kind != "cli" {
			continue
		}
		controlled[s.ID] = true
		if s.ID == cliServiceID {
			continue
		}
		i := slices.IndexFunc(next.Servers, func(candidate Server) bool { return candidate.ID == s.ID })
		if i < 0 || !reflect.DeepEqual(next.Servers[i], s) {
			return invalid("CLI sources must be managed through the server endpoint")
		}
	}
	for _, s := range next.Servers {
		if s.Kind == "cli" && s.ID != cliServiceID && !controlled[s.ID] {
			return invalid("CLI sources must be registered through the server endpoint")
		}
	}
	if len(controlled) == 0 {
		return nil
	}

	protected := map[string]Tool{}
	for _, t := range current.Tools {
		if t.ServerID != cliServiceID && controlled[t.ServerID] {
			protected[t.ID] = t
		}
	}
	for _, t := range next.Tools {
		old, exists := protected[t.ID]
		if !exists && (t.ServerID == cliServiceID || !controlled[t.ServerID]) {
			continue
		}
		old.Enabled = t.Enabled
		if !exists || !reflect.DeepEqual(old, t) {
			return invalid("Only enablement can be edited on controlled CLI tools")
		}
		delete(protected, t.ID)
	}
	if len(protected) != 0 {
		return invalid("Controlled CLI tools cannot be removed individually")
	}
	return nil
}

func (g *Service) SaveServer(s Server, controlledTools ...Tool) error {
	if !ValidID(s.ID) || s.Name == "" {
		return invalid("请提供合法 ID、名称及协议类型 http/mcp/cli")
	}
	cliServiceID := ""
	switch s.Kind {
	case "mcp":
		if s.Transport == "stdio" {
			if strings.TrimSpace(s.Command) == "" {
				return invalid("stdio 服务需要填写启动命令")
			}
			break
		}
		fallthrough
	case "http":
		if s.Transport != "" && s.Transport != "http" {
			return invalid("支持的 MCP 传输为 http 或 stdio")
		}
		if err := ValidateURL(s.Endpoint); err != nil {
			return err
		}
	case "cli":
		if len(controlledTools) == 0 || s.CommandSHA256 == "" {
			return invalid("CLI sources require a pinned executable and generated tools")
		}
		cliServiceID = s.ID
	default:
		return invalid("协议类型必须为 http/mcp/cli")
	}
	for _, value := range s.Headers {
		if !strings.Contains(value, "${") || !strings.Contains(value, "}") {
			return invalid("请求头的凭据请使用 ${ENV_VAR} 环境变量引用")
		}
	}
	for _, value := range s.Env {
		if !strings.Contains(value, "${") || !strings.Contains(value, "}") {
			return invalid("环境变量值请使用 ${ENV_VAR} 引用，由网关启动环境提供")
		}
	}
	return g.updateWithCommit(func(current Registry) (Registry, error) {
		i := slices.IndexFunc(current.Servers, func(old Server) bool { return old.ID == s.ID })
		if i >= 0 {
			old := current.Servers[i]
			if (s.Kind == "cli" || old.Kind == "cli") && s.Kind != old.Kind {
				return Registry{}, invalid("Delete the existing source before changing its protocol to or from CLI")
			}
			s.Demo = old.Demo
			current.Servers[i] = s
		} else {
			current.Servers = append(current.Servers, s)
		}
		if cliServiceID != "" {
			var err error
			current.Tools, err = replaceCLITools(current.Tools, s.ID, controlledTools)
			if err != nil {
				return Registry{}, err
			}
		}
		return current, nil
	}, g.persist, cliServiceID)
}

func replaceCLITools(current []Tool, serverID string, generated []Tool) ([]Tool, error) {
	enabled := map[string]bool{}
	occupied := map[string]bool{}
	for _, t := range current {
		if t.ServerID == serverID {
			enabled[t.ID] = t.Enabled
		} else {
			occupied[t.ID] = true
		}
	}
	out := slices.DeleteFunc(current, func(t Tool) bool { return t.ServerID == serverID })
	for _, t := range generated {
		if !ValidID(t.ID) || t.ServerID != serverID || occupied[t.ID] {
			return nil, invalid("Invalid or conflicting controlled CLI tool ID")
		}
		occupied[t.ID] = true
		t.Enabled = enabled[t.ID]
		out = append(out, t)
	}
	return out, nil
}

func (g *Service) DeleteServer(id string) error {
	return g.updateWithCommit(func(r Registry) (Registry, error) {
		r.Servers = slices.DeleteFunc(r.Servers, func(s Server) bool { return s.ID == id })
		r.Tools = slices.DeleteFunc(r.Tools, func(t Tool) bool { return t.ServerID == id })
		return r, nil
	}, g.persist, id)
}

func (g *Service) SaveTool(t Tool) error {
	if err := ValidateMapping(t); err != nil {
		return err
	}
	if !ValidID(t.ID) || t.Name == "" || t.Description == "" || len(t.Actors) == 0 {
		return invalid("工具需要合法 ID、名称、描述及至少一个模拟身份")
	}
	if _, err := CompileSchema(t.InputSchema); err != nil {
		return &ValidationError{Message: "input_schema 无效", Cause: err}
	}
	return g.Update(func(current Registry) (Registry, error) {
		i := slices.IndexFunc(current.Servers, func(s Server) bool { return s.ID == t.ServerID })
		if i < 0 {
			return Registry{}, invalid("server_id 不存在")
		}
		if current.Servers[i].Kind == "cli" {
			return Registry{}, invalid("Use the server tool enablement endpoint for controlled CLI tools")
		}
		if _, err := json.Marshal(t); err != nil {
			return Registry{}, fmt.Errorf("encode tool: %w", err)
		}
		r := Registry{Servers: current.Servers, Tools: slices.Clone(current.Tools)}
		i = slices.IndexFunc(r.Tools, func(old Tool) bool { return old.ID == t.ID })
		if i >= 0 {
			r.Tools[i] = t
		} else {
			r.Tools = append(r.Tools, t)
		}
		return r, nil
	})
}
