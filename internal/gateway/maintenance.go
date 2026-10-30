package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// MaintenanceSource is an owner's instruction to maintain one server's complete
// tool catalog. Files and URLs are explicit inputs, not repositories to modify.
type MaintenanceSource struct {
	ServerID        string            `json:"server_id"`
	Files           []string          `json:"files"`
	URLs            []string          `json:"urls"`
	Instructions    string            `json:"instructions"`
	Enabled         bool              `json:"enabled"`
	AutoPublish     bool              `json:"auto_publish"`
	IntervalMinutes int               `json:"interval_minutes"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	Provider        string            `json:"provider,omitempty"`
	Model           string            `json:"model,omitempty"`
	Format          string            `json:"format,omitempty"`
	OperationIDs    []string          `json:"operation_ids,omitempty"`
	Repository      *RepositorySource `json:"repository,omitempty"`
	NextRunAt       time.Time         `json:"next_run_at"`
}

type MaintenanceRun struct {
	ID              string             `json:"id"`
	ServerID        string             `json:"server_id"`
	Status          string             `json:"status"`
	Stage           string             `json:"stage"`
	CreatedAt       time.Time          `json:"created_at"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty"`
	Fingerprint     string             `json:"fingerprint,omitempty"`
	Baseline        string             `json:"baseline"`
	Source          MaintenanceSource  `json:"source"`
	Server          Server             `json:"server"`
	Before          []Tool             `json:"before"`
	Tools           []Tool             `json:"tools"`
	Summary         string             `json:"summary,omitempty"`
	Error           string             `json:"error,omitempty"`
	Events          []string           `json:"events"`
	Artifacts       map[string]string  `json:"artifacts,omitempty"`
	Force           bool               `json:"force"`
	AcceptanceInput *AcceptanceSuite   `json:"acceptance_input,omitempty"`
	Acceptance      AcceptanceReport   `json:"acceptance"`
	Review          PublicationReview  `json:"review"`
	Trigger         MaintenanceTrigger `json:"trigger"`
	Provenance      SourceProvenance   `json:"provenance"`
	PublishedBy     string             `json:"published_by,omitempty"`
	PublishedAt     *time.Time         `json:"published_at,omitempty"`
	RolledBackBy    string             `json:"rolled_back_by,omitempty"`
	RolledBackAt    *time.Time         `json:"rolled_back_at,omitempty"`
}

type Maintainer struct {
	g       *Gateway
	mu      sync.Mutex
	sources map[string]MaintenanceSource
	suites  map[string]AcceptanceSuite
	runs    map[string]MaintenanceRun
	ctx     context.Context
	stop    context.CancelFunc
	active  map[string]context.CancelFunc
	wake    chan struct{}
	done    chan struct{}
	started bool
	closed  bool
	command string
}

func newMaintainer(g *Gateway) (*Maintainer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Maintainer{g: g, sources: map[string]MaintenanceSource{}, suites: map[string]AcceptanceSuite{}, runs: map[string]MaintenanceRun{}, ctx: ctx, stop: cancel, active: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), done: make(chan struct{}), command: os.Getenv("GATEWAY_PI_COMMAND")}
	if m.command == "" {
		m.command = "pi"
	}
	for _, table := range []string{"maintenance_sources", "maintenance_runs", "maintenance_acceptance"} {
		rows, err := g.db.Query("SELECT definition FROM " + table)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("load maintenance: %w", err)
		}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				closeRows(rows)
				cancel()
				return nil, fmt.Errorf("read maintenance: %w", err)
			}
			if table == "maintenance_sources" {
				var s MaintenanceSource
				if err := json.Unmarshal(raw, &s); err != nil {
					closeRows(rows)
					cancel()
					return nil, fmt.Errorf("decode maintenance source: %w", err)
				}
				m.sources[s.ServerID] = s
			} else if table == "maintenance_acceptance" {
				var suite AcceptanceSuite
				if err := json.Unmarshal(raw, &suite); err != nil {
					closeRows(rows)
					cancel()
					return nil, fmt.Errorf("decode acceptance suite: %w", err)
				}
				m.suites[suite.ServerID] = suite
			} else {
				var r MaintenanceRun
				if err := json.Unmarshal(raw, &r); err != nil {
					closeRows(rows)
					cancel()
					return nil, fmt.Errorf("decode maintenance run: %w", err)
				}
				m.runs[r.ID] = r
			}
		}
		err = rows.Err()
		closeRows(rows)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("iterate maintenance: %w", err)
		}
	}
	// Never replay a partially executed coding task after a process crash.
	for _, r := range m.runs {
		if r.Status == "running" {
			now := time.Now()
			r.Status = "interrupted"
			r.FinishedAt = &now
			r.Error = "网关重启中断了任务；可重新检查来源"
			if err := m.saveRun(r); err != nil {
				cancel()
				return nil, err
			}
		}
	}
	return m, nil
}

func (g *Gateway) StartMaintenance(ctx context.Context) { g.maintenance.Start(ctx) }
func (m *Maintainer) Start(parent context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.closed {
		return
	}
	m.started = true
	go func() {
		select {
		case <-parent.Done():
			m.stop()
		case <-m.ctx.Done():
		}
	}()
	go m.loop()
}
func (m *Maintainer) Close() {
	m.mu.Lock()
	m.closed = true
	m.stop()
	started := m.started
	m.mu.Unlock()
	if started {
		<-m.done
	}
}
func (m *Maintainer) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Maintainer) saveSource(s MaintenanceSource) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode maintenance source: %w", err)
	}
	if _, err = m.g.db.Exec("INSERT INTO maintenance_sources(id,definition) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET definition=excluded.definition", s.ServerID, string(b)); err != nil {
		return fmt.Errorf("save maintenance source: %w", err)
	}
	m.sources[s.ServerID] = s
	return nil
}
func (m *Maintainer) saveRun(r MaintenanceRun) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode maintenance run: %w", err)
	}
	if _, err = m.g.db.Exec("INSERT INTO maintenance_runs(id,definition) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET definition=excluded.definition", r.ID, string(b)); err != nil {
		return fmt.Errorf("save maintenance run: %w", err)
	}
	m.runs[r.ID] = r
	return nil
}
func (m *Maintainer) SaveSource(s MaintenanceSource) error {
	if s.Repository != nil {
		if err := validateRepository(*s.Repository); err != nil {
			return err
		}
	}
	if !slices.Contains([]string{"", "pi", "openapi", "native"}, s.Format) {
		return invalid("维护格式必须是 pi、openapi 或 native")
	}
	if s.IntervalMinutes < 1 || s.IntervalMinutes > 43200 {
		return invalid("检查周期需为 1–43200 分钟")
	}
	if s.TimeoutSeconds < 30 || s.TimeoutSeconds > 3600 {
		return invalid("任务超时需为 30–3600 秒")
	}
	if len(s.Instructions) > 16000 || len(s.Files)+len(s.URLs) > 32 {
		return invalid("维护指引或参考来源过多")
	}
	if s.Provider != "" && s.Model == "" {
		return invalid("指定 provider 时也需要指定 model")
	}
	for i, p := range s.Files {
		if !filepath.IsAbs(p) {
			return invalid("参考文件请使用绝对路径")
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return invalid("参考文件不存在或不是普通文件：" + p)
		}
		s.Files[i] = filepath.Clean(p)
	}
	for _, u := range s.URLs {
		if err := validateURL(u); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	reg := m.g.Snapshot()
	i := slices.IndexFunc(reg.Servers, func(v Server) bool { return v.ID == s.ServerID })
	if i < 0 || reg.Servers[i].Demo {
		return invalid("请选择已接入的非演示服务")
	}
	if (s.Format == "openapi" && reg.Servers[i].Kind != "http") || (s.Format == "native" && reg.Servers[i].Kind != "mcp") {
		return invalid("OpenAPI 对应 HTTP 服务，native 对应原生 MCP 服务")
	}
	if reg.Servers[i].Kind != "mcp" && len(s.Files)+len(s.URLs) == 0 && s.Repository == nil {
		return invalid("请指定至少一个参考文件或文档 URL")
	}
	s.NextRunAt = time.Now().Add(time.Duration(s.IntervalMinutes) * time.Minute)
	if err := m.saveSource(s); err != nil {
		return err
	}
	m.signal()
	return nil
}

func serverTools(reg Registry, id string) []Tool {
	out := []Tool{}
	for _, t := range reg.Tools {
		if t.ServerID == id {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Tool) int { return strings.Compare(a.ID, b.ID) })
	return out
}
func maintenanceDigest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("non-JSON maintenance value: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func catalogDigest(s Server, ts []Tool) string {
	return maintenanceDigest(struct {
		Server Server
		Tools  []Tool
	}{s, ts})
}
func (m *Maintainer) makeRun(id string, force bool) (MaintenanceRun, error) {
	if m.closed {
		return MaintenanceRun{}, problem(503, "维护运行时正在停止", nil)
	}
	s, ok := m.sources[id]
	if !ok {
		return MaintenanceRun{}, invalid("请先保存该服务的维护配置")
	}
	for _, r := range m.runs {
		if r.ServerID == id && (r.Status == "running" || r.Status == "queued") {
			return MaintenanceRun{}, problem(409, "该服务已有维护任务", nil)
		}
	}
	reg := m.g.Snapshot()
	i := slices.IndexFunc(reg.Servers, func(v Server) bool { return v.ID == id })
	if i < 0 {
		return MaintenanceRun{}, invalid("服务不存在")
	}
	before := serverTools(reg, id)
	r := MaintenanceRun{ID: newID(), ServerID: id, Source: s, Server: reg.Servers[i], Before: before, Baseline: catalogDigest(reg.Servers[i], before), Status: "queued", Stage: "等待检查", CreatedAt: time.Now(), Events: []string{}, Force: force}
	if suite, ok := m.suites[id]; ok {
		r.AcceptanceInput = &suite
	}
	r.Trigger.Kind = "manual"
	return r, nil
}
func (m *Maintainer) enqueue(id string, force bool) (MaintenanceRun, error) {
	return m.enqueueTriggered(id, force, "manual")
}
func (m *Maintainer) enqueueTriggered(id string, force bool, kind string) (MaintenanceRun, error) {
	r, err := m.makeRun(id, force)
	if err != nil {
		return r, err
	}
	r.Trigger.Kind = kind
	if err := m.saveRun(r); err != nil {
		return MaintenanceRun{}, err
	}
	m.signal()
	return r, nil
}
func (m *Maintainer) Run(id string, force bool) (MaintenanceRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enqueue(id, force)
}
func (m *Maintainer) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return problem(404, "任务不存在", nil)
	}
	if r.Status != "queued" && r.Status != "running" {
		return problem(409, "任务已经结束", nil)
	}
	now := time.Now()
	r.Status = "cancelled"
	r.Stage = "已取消"
	r.FinishedAt = &now
	if err := m.saveRun(r); err != nil {
		return err
	}
	if cancel := m.active[id]; cancel != nil {
		cancel()
	}
	return nil
}
func (m *Maintainer) loop() {
	defer close(m.done)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if m.ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		if err := m.dispatchSignals(); err != nil {
			slog.Error("dispatch source signals", "error", err)
		}
		now := time.Now()
		for _, s := range m.sources {
			if !s.Enabled || s.NextRunAt.After(now) {
				continue
			}
			s.NextRunAt = now.Add(time.Duration(s.IntervalMinutes) * time.Minute)
			if err := m.saveSource(s); err != nil {
				slog.Error("schedule maintenance", "error", err)
				continue
			}
			if _, err := m.enqueueTriggered(s.ServerID, false, "schedule"); err != nil {
				var app *appError
				if !errors.As(err, &app) || app.status != 409 {
					slog.Error("enqueue maintenance", "error", err)
				}
			}
		}
		var next MaintenanceRun
		for _, r := range m.runs {
			if r.Status == "queued" && (next.ID == "" || r.CreatedAt.Before(next.CreatedAt)) {
				next = r
			}
		}
		if next.ID != "" {
			next.Status = "running"
			next.Stage = "读取来源"
			if err := m.saveRun(next); err != nil {
				slog.Error("start maintenance", "error", err)
				next.ID = ""
			}
		}
		m.mu.Unlock()
		if next.ID != "" {
			m.execute(next)
			continue
		}
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
	}
}
func (m *Maintainer) update(id, stage string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r.Status != "running" {
		return context.Canceled
	}
	r.Stage = stage
	r.Events = append(slices.Clone(r.Events), time.Now().Format("15:04:05")+" "+stage)
	if len(r.Events) > 40 {
		r.Events = r.Events[len(r.Events)-40:]
	}
	return m.saveRun(r)
}
func (m *Maintainer) execute(r MaintenanceRun) {
	ctx, cancel := context.WithTimeout(m.ctx, time.Duration(r.Source.TimeoutSeconds)*time.Second)
	m.mu.Lock()
	m.active[r.ID] = cancel
	if m.runs[r.ID].Status != "running" {
		cancel()
	}
	m.mu.Unlock()
	err := m.build(ctx, &r)
	if err == nil {
		err = ctx.Err()
	}
	cancel()
	m.mu.Lock()
	delete(m.active, r.ID)
	current := m.runs[r.ID]
	if current.Status == "cancelled" {
		m.mu.Unlock()
		return
	}
	r.Events = current.Events
	if err != nil {
		r.Status = "failed"
		r.Stage = "维护失败"
		r.Error = m.g.errorDetail(err)
		if m.ctx.Err() != nil {
			r.Status = "interrupted"
			r.Stage = "运行时已停止"
		}
	}
	now := time.Now()
	r.FinishedAt = &now
	if saveErr := m.saveRun(r); saveErr != nil {
		slog.Error("finish maintenance", "error", saveErr)
		m.mu.Unlock()
		return
	}
	autoPublish := m.sources[r.ServerID].AutoPublish
	m.mu.Unlock()
	if r.Status == "ready" && r.Source.AutoPublish && autoPublish && r.Acceptance.Passed && !r.Review.Required {
		if err := m.publish(r.ID, false, false); err != nil {
			m.mu.Lock()
			r = m.runs[r.ID]
			if r.Status != "ready" {
				m.mu.Unlock()
				return
			}
			r.Error = m.g.errorDetail(err)
			if saveErr := m.saveRun(r); saveErr != nil {
				slog.Error("save publish error", "error", saveErr)
			}
			m.mu.Unlock()
		}
	}
	m.mu.Lock()
	r = m.runs[r.ID]
	m.mu.Unlock()
	m.g.record("maintenance", "developer", r.Status, r.ServerID, r.CreatedAt, map[string]any{"run_id": r.ID, "summary": r.Summary, "error": r.Error})
}
func (m *Maintainer) directory(id string) string { return filepath.Join(m.g.dir, "maintenance", id) }
func (m *Maintainer) runtimeStatus() map[string]any {
	_, err := exec.LookPath(m.command)
	return map[string]any{"execution": m.g.execution, "command": m.command, "available": err == nil, "started": m.started && !m.closed && m.ctx.Err() == nil, "model_source": "使用本机 pi 的模型与登录配置；可按服务覆盖 provider/model"}
}
