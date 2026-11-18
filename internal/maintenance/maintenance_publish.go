package maintenance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/jsonutil"
	"gatewaylab/internal/store"
)

func sourceIdentity(s MaintenanceSource) string {
	s.NextRunAt = time.Time{}
	s.Enabled = false
	s.IntervalMinutes = 0
	s.AutoPublish = false
	return jsonutil.MustDigest(s)
}

type publishOptions struct {
	ID       string
	Rollback bool
	Approved bool
	Actor    string
}

// Publish changes registry and job state in one SQLite transaction. Both the
// discovery path and invocation path observe the same new in-memory snapshot.
func (m *Maintainer) Publish(id string, rollback bool) error {
	return m.publish(publishOptions{ID: id, Rollback: rollback, Approved: true, Actor: "local-owner"})
}

// PublishAs preserves the public API used by internal/api; new callers should
// prefer publishOptions internally.
func (m *Maintainer) PublishAs(id string, rollback, approved bool, actor string) error {
	return m.publish(publishOptions{ID: id, Rollback: rollback, Approved: approved, Actor: actor})
}

func (m *Maintainer) publish(opts publishOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return domainError(ErrUnavailable, "维护运行时正在停止", nil)
	}
	r, ok := m.runs[opts.ID]
	if !ok {
		return domainError(ErrNotFound, "任务不存在", nil)
	}
	if err := m.validatePublishPreconditionsLocked(r, opts); err != nil {
		return err
	}
	fingerprint := ""
	if !opts.Rollback {
		fingerprint = r.Acceptance.RoutingFingerprint
	}
	return m.catalog.UpdateCatalog(r.ServerID, fingerprint, func(reg Registry) (Registry, error) {
		return buildPublishedCatalog(reg, &r, opts)
	}, func(updated Registry) error {
		return m.commitPublishedRunLocked(r, updated)
	})
}

func (m *Maintainer) validatePublishPreconditionsLocked(r MaintenanceRun, opts publishOptions) error {
	if (!opts.Rollback && r.Status != "ready") || (opts.Rollback && r.Status != "published") {
		return domainError(ErrConflict, "当前版本不能执行此操作", nil)
	}
	if opts.Rollback {
		return nil
	}
	if !r.Acceptance.Configured || !r.Acceptance.Passed {
		return domainError(ErrConflict, "独立契约与路由验收未通过，不能发布", nil)
	}
	suite, ok := m.suites[r.ServerID]
	if !ok || jsonutil.MustDigest(suite) != r.Acceptance.Fingerprint {
		return domainError(ErrConflict, "验收用例已变化，请重新检查", nil)
	}
	if m.router.RoutingFingerprint(r.ServerID) != r.Acceptance.RoutingFingerprint {
		return domainError(ErrConflict, "路由配置已变化，请重新验收", nil)
	}
	if r.Review.Required && !opts.Approved {
		return domainError(ErrConflict, "此变更需要负责人确认", nil)
	}
	s, ok := m.sources[r.ServerID]
	if !ok || sourceIdentity(s) != sourceIdentity(r.Source) {
		return domainError(ErrConflict, "维护配置已变化，请重新检查来源", nil)
	}
	return checkArtifacts(filepath.Join(m.directory(r.ID), "revision"), r.Artifacts)
}

func buildPublishedCatalog(reg Registry, r *MaintenanceRun, opts publishOptions) (Registry, error) {
	i := slices.IndexFunc(reg.Servers, func(s Server) bool { return s.ID == r.ServerID })
	if i < 0 {
		return Registry{}, domainError(ErrConflict, "服务已被移除", nil)
	}
	if !opts.Rollback && opts.Approved {
		r.Tools = slices.Clone(r.Tools)
		for idx, tool := range r.Tools {
			if !slices.ContainsFunc(r.Before, func(old Tool) bool { return old.ID == tool.ID }) {
				r.Tools[idx].Enabled = true
			}
		}
	}
	expected := r.Baseline
	replacement := r.Tools
	if opts.Rollback {
		expected = catalogDigest(r.Server, r.Tools)
		replacement = r.Before
	}
	if catalogDigest(reg.Servers[i], serverTools(reg, r.ServerID)) != expected {
		return Registry{}, domainError(ErrConflict, "服务或工具已被修改；为避免覆盖，请重新检查来源", nil)
	}
	updated := Registry{Servers: reg.Servers, Tools: []Tool{}}
	for _, t := range reg.Tools {
		if t.ServerID != r.ServerID {
			updated.Tools = append(updated.Tools, t)
		}
	}
	updated.Tools = append(updated.Tools, replacement...)
	now := time.Now()
	if opts.Rollback {
		r.RolledBackBy = opts.Actor
		r.RolledBackAt = &now
		r.Status = "rolled_back"
		r.Stage = "已回退到发布前版本"
	} else {
		r.PublishedBy = opts.Actor
		r.PublishedAt = &now
		r.Status = "published"
		r.Stage = "已发布，路由候选已更新"
	}
	r.Error = ""
	return updated, nil
}

func (m *Maintainer) commitPublishedRunLocked(r MaintenanceRun, updated Registry) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode published run: %w", err)
	}
	if err := store.RunTx(m.db, func(tx store.DBTX) error {
		if err := store.WriteRegistry(tx, updated); err != nil {
			return err
		}
		if err := store.MaintenanceRuns[json.RawMessage](tx).Update(r.ID, b); err != nil {
			return fmt.Errorf("persist published run: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("publish maintenance: %w", err)
	}
	m.runs[r.ID] = r
	return nil
}

type ToolChange struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Before *Tool  `json:"before,omitempty"`
	After  *Tool  `json:"after,omitempty"`
}

func changes(r MaintenanceRun) []ToolChange {
	out := []ToolChange{}
	for _, t := range r.Tools {
		i := slices.IndexFunc(r.Before, func(old Tool) bool { return old.ID == t.ID })
		if i < 0 {
			out = append(out, ToolChange{ID: t.ID, Kind: "added", After: &t})
		} else if jsonutil.MustDigest(r.Before[i]) != jsonutil.MustDigest(t) {
			out = append(out, ToolChange{ID: t.ID, Kind: "changed", Before: &r.Before[i], After: &t})
		}
	}
	for _, t := range r.Before {
		if !slices.ContainsFunc(r.Tools, func(next Tool) bool { return next.ID == t.ID }) {
			out = append(out, ToolChange{ID: t.ID, Kind: "removed", Before: &t})
		}
	}
	slices.SortFunc(out, func(a, b ToolChange) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func checkArtifacts(dir string, artifacts map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read revision: %w", err)
	}
	if len(entries) != len(artifacts) || len(artifacts) == 0 {
		return domainError(ErrConflict, "版本文件集合已变化，请重新生成", nil)
	}
	for _, entry := range entries {
		want, ok := artifacts[entry.Name()]
		if !ok {
			return domainError(ErrConflict, "版本中出现未验证的文件", nil)
		}
		b, err := regularArtifact(dir, entry.Name())
		if err != nil {
			return err
		}
		if jsonutil.MustDigest(string(b)) != want {
			return domainError(ErrConflict, "版本文件已被修改，请重新生成", nil)
		}
	}
	return nil
}
