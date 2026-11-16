package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/store"
)

var deliveryName = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,200}$`)

type sourceSignal struct {
	ServerID   string             `json:"server_id"`
	Trigger    MaintenanceTrigger `json:"trigger"`
	ReceivedAt time.Time          `json:"received_at"`
}

func verifyHMAC(body []byte, signature, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write(body); err != nil {
		return false
	}
	return hmac.Equal(got, mac.Sum(nil))
}
func parseSourceSignal(provider string, headers http.Header, body []byte, repo RepositorySource) (MaintenanceTrigger, bool, error) {
	trigger := MaintenanceTrigger{Provider: provider}
	secret := os.Getenv(repo.WebhookSecretEnv)
	if secret == "" {
		return trigger, false, problem(503, "仓库事件密钥尚未配置", nil)
	}
	valid := false
	switch provider {
	case "github":
		valid = verifyHMAC(body, headers.Get("X-Hub-Signature-256"), secret)
		trigger.DeliveryID = headers.Get("X-GitHub-Delivery")
	case "gitlab":
		valid = subtle.ConstantTimeCompare([]byte(headers.Get("X-Gitlab-Token")), []byte(secret)) == 1
		trigger.DeliveryID = headers.Get("X-Gitlab-Event-UUID")
	case "generic":
		valid = verifyHMAC(body, headers.Get("X-Gateway-Signature"), secret)
		trigger.DeliveryID = headers.Get("X-Gateway-Delivery")
	default:
		return trigger, false, invalid("支持 github、gitlab 或 generic 来源事件")
	}
	if !valid {
		return trigger, false, problem(401, "事件签名或密钥无效", nil)
	}
	if !deliveryName.MatchString(trigger.DeliveryID) {
		return trigger, false, invalid("事件需要有效的唯一 delivery ID")
	}
	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Commit     string `json:"commit"`
		Event      string `json:"event"`
		ProjectID  string `json:"project_id"`
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Project struct {
			Path string `json:"path_with_namespace"`
		} `json:"project"`
		Attributes struct {
			Ref    string `json:"ref"`
			SHA    string `json:"sha"`
			Status string `json:"status"`
		} `json:"object_attributes"`
		Workflow struct {
			Branch string `json:"head_branch"`
			SHA    string `json:"head_sha"`
		} `json:"workflow_run"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return trigger, false, invalid("事件不是有效的 JSON")
	}
	project := ""
	switch provider {
	case "github":
		project = payload.Repository.FullName
		switch headers.Get("X-GitHub-Event") {
		case "push":
			trigger.Kind = "push"
			trigger.Ref = payload.Ref
			trigger.Commit = payload.After
		case "workflow_run":
			if payload.Action != "completed" {
				return trigger, false, nil
			}
			trigger.Kind = "ci_completed"
			trigger.Ref = payload.Workflow.Branch
			trigger.Commit = payload.Workflow.SHA
		case "ping":
			return trigger, false, nil
		default:
			return trigger, false, nil
		}
	case "gitlab":
		project = payload.Project.Path
		switch headers.Get("X-Gitlab-Event") {
		case "Push Hook":
			trigger.Kind = "push"
			trigger.Ref = payload.Ref
			trigger.Commit = payload.After
		case "Pipeline Hook":
			if !slices.Contains([]string{"success", "failed", "canceled", "skipped"}, payload.Attributes.Status) {
				return trigger, false, nil
			}
			trigger.Kind = "ci_completed"
			trigger.Ref = payload.Attributes.Ref
			trigger.Commit = payload.Attributes.SHA
		default:
			return trigger, false, nil
		}
	case "generic":
		project = payload.ProjectID
		if !slices.Contains([]string{"push", "ci_completed", "release", "deploy"}, payload.Event) {
			return trigger, false, invalid("不支持的事件类型")
		}
		trigger.Kind = payload.Event
		trigger.Ref = payload.Ref
		trigger.Commit = payload.Commit
	}
	if project != repo.Project {
		return trigger, false, problem(403, "事件仓库与配置不匹配", nil)
	}
	if strings.TrimPrefix(trigger.Ref, "refs/heads/") != strings.TrimPrefix(repo.Ref, "refs/heads/") {
		return trigger, false, nil
	}
	if !gitSHA.MatchString(trigger.Commit) {
		return trigger, false, invalid("事件缺少有效 commit SHA")
	}
	return trigger, true, nil
}
func (g *Gateway) registerSourceEvents(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/maintenance/sources/{id}/webhook/{provider}", func(w http.ResponseWriter, r *http.Request) {
		m := g.maintenance
		m.mu.Lock()
		source, ok := m.sources[r.PathValue("id")]
		m.mu.Unlock()
		if !ok || source.Repository == nil || !source.Repository.EventsEnabled {
			fail(w, problem(404, "此来源未开启仓库事件", nil))
			return
		}
		body, err := readBounded(r.Body, 256<<10)
		if err != nil {
			fail(w, problem(413, "事件内容过大", err))
			return
		}
		trigger, relevant, err := parseSourceSignal(r.PathValue("provider"), r.Header, body, *source.Repository)
		if err != nil {
			fail(w, err)
			return
		}
		if !relevant {
			respond(w, 200, map[string]bool{"ignored": true})
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			fail(w, problem(503, "维护运行时正在停止", nil))
			return
		}
		if sourceIdentity(m.sources[source.ServerID]) != sourceIdentity(source) {
			fail(w, problem(409, "来源配置已变化，请重新投递事件", nil))
			return
		}
		runID, err := store.MaintenanceDeliveries[sourceSignal](m.g.db).RunID(source.ServerID, trigger.Provider, trigger.DeliveryID)
		if err == nil {
			respond(w, 200, map[string]any{"accepted": true, "duplicate": true, "run_id": runID})
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			fail(w, fmt.Errorf("read source delivery: %w", err))
			return
		}
		signal := sourceSignal{ServerID: source.ServerID, Trigger: trigger, ReceivedAt: time.Now()}
		encoded, err := json.Marshal(signal)
		if err != nil {
			fail(w, fmt.Errorf("encode source signal: %w", err))
			return
		}
		if err := store.MaintenanceDeliveries[json.RawMessage](m.g.db).Insert(source.ServerID, trigger.Provider, trigger.DeliveryID, signal.ReceivedAt.Format(time.RFC3339Nano), encoded); err != nil {
			fail(w, fmt.Errorf("persist source signal: %w", err))
			return
		}
		m.signal()
		respond(w, 202, map[string]any{"accepted": true, "pending": true})
	})
}

// Called under m.mu. Deliveries persist before acknowledgement. A single follow-up
// job consumes all pending events for a source after its active job completes.
func (m *Maintainer) dispatchSignals() error {
	signals, err := store.MaintenanceDeliveries[sourceSignal](m.g.db).Pending()
	if err != nil {
		return err
	}
	latest := map[string]sourceSignal{}
	for _, signal := range signals {
		if _, exists := latest[signal.ServerID]; !exists {
			latest[signal.ServerID] = signal
		}
	}
	for _, id := range sortedKeys(latest) {
		source, ok := m.sources[id]
		if !ok || source.Repository == nil || !source.Repository.EventsEnabled {
			if err := store.MaintenanceDeliveries[sourceSignal](m.g.db).Assign(id, "disabled"); err != nil {
				return fmt.Errorf("retire disabled signals: %w", err)
			}
			continue
		}
		run, err := m.makeRun(id, false)
		if err != nil {
			var app *appError
			if errors.As(err, &app) && app.status == 409 {
				continue
			}
			return err
		}
		run.Trigger = latest[id].Trigger
		if err := m.assignSignals(run); err != nil {
			return err
		}
		m.runs[run.ID] = run
	}
	return nil
}
func (m *Maintainer) assignSignals(run MaintenanceRun) error {
	encoded, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode signal job: %w", err)
	}
	tx, err := m.g.db.Begin()
	if err != nil {
		return fmt.Errorf("begin signal dispatch: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if err := tx.Rollback(); err != nil {
				slog.Error("rollback signal dispatch", "error", err)
			}
		}
	}()
	if err := store.MaintenanceRuns[json.RawMessage](tx).Insert(run.ID, encoded); err != nil {
		return fmt.Errorf("persist signal job: %w", err)
	}
	if err := store.MaintenanceDeliveries[sourceSignal](tx).Assign(run.ServerID, run.ID); err != nil {
		return fmt.Errorf("assign pending signals: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit signal dispatch: %w", err)
	}
	committed = true
	return nil
}
