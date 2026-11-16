package maintenance

import (
	"encoding/json"
	"slices"
	"strings"
)

// Queries detach nested maps and slices so callers never share mutable state
// with the worker after its mutex is released.
func detached[T any](value T) T {
	b, err := json.Marshal(value)
	if err != nil {
		panic("non-JSON maintenance value: " + err.Error())
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		panic("non-JSON maintenance value: " + err.Error())
	}
	return out
}

type Status struct {
	Runtime map[string]any      `json:"runtime"`
	Sources []MaintenanceSource `json:"sources"`
	Runs    []MaintenanceRun    `json:"runs"`
}

func (m *Maintainer) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := Status{Runtime: m.runtimeStatus(), Sources: []MaintenanceSource{}, Runs: []MaintenanceRun{}}
	for _, source := range m.sources {
		out.Sources = append(out.Sources, source)
	}
	slices.SortFunc(out.Sources, func(a, b MaintenanceSource) int { return strings.Compare(a.ServerID, b.ServerID) })
	for _, run := range m.runs {
		out.Runs = append(out.Runs, run)
	}
	slices.SortFunc(out.Runs, func(a, b MaintenanceRun) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if len(out.Runs) > 50 {
		out.Runs = out.Runs[:50]
	}
	return detached(out)
}
func (m *Maintainer) Source(id string) (MaintenanceSource, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, ok := m.sources[id]
	return detached(source), ok
}
func (m *Maintainer) Acceptance(id string) AcceptanceSuite {
	m.mu.Lock()
	defer m.mu.Unlock()
	suite, ok := m.suites[id]
	if !ok {
		suite = AcceptanceSuite{ServerID: id, Contracts: []ContractCase{}, Routing: []RoutingCase{}}
	}
	return detached(suite)
}
func (m *Maintainer) GetRun(id string) (MaintenanceRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok {
		return MaintenanceRun{}, domainError(ErrNotFound, "任务不存在", nil)
	}
	return detached(run), nil
}
func (m *Maintainer) RunDetails(id string) (map[string]any, error) {
	run, err := m.GetRun(id)
	if err != nil {
		return nil, err
	}
	diff := []ToolChange{}
	if run.Tools != nil {
		diff = changes(run)
	}
	return map[string]any{"run": run, "changes": diff}, nil
}
func (m *Maintainer) Artifact(id, name string) ([]byte, error) {
	run, err := m.GetRun(id)
	if err != nil {
		return nil, err
	}
	if _, ok := run.Artifacts[name]; !ok {
		return nil, domainError(ErrNotFound, "产物不存在", nil)
	}
	return regularArtifact(m.directory(run.ID)+"/revision", name)
}
