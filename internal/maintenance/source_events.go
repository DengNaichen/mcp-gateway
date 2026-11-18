package maintenance

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	return parseSourceSignalWithLookup(provider, headers, body, repo, os.Getenv)
}

func parseSourceSignalWithLookup(provider string, headers http.Header, body []byte, repo RepositorySource, lookup func(string) string) (MaintenanceTrigger, bool, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	trigger := MaintenanceTrigger{Provider: provider}
	secret := lookup(repo.WebhookSecretEnv)
	if secret == "" {
		return trigger, false, domainError(ErrUnavailable, "仓库事件密钥尚未配置", nil)
	}
	deliveryID, err := verifySignalAuth(provider, headers, body, secret)
	if err != nil {
		return trigger, false, err
	}
	trigger.DeliveryID = deliveryID
	if !deliveryName.MatchString(trigger.DeliveryID) {
		return trigger, false, invalid("事件需要有效的唯一 delivery ID")
	}
	project, kind, ref, commit, matched, err := extractSignalEvent(provider, headers, body)
	if err != nil || !matched {
		return trigger, false, err
	}
	trigger.Kind, trigger.Ref, trigger.Commit = kind, ref, commit
	if project != repo.Project {
		return trigger, false, domainError(ErrForbidden, "事件仓库与配置不匹配", nil)
	}
	if strings.TrimPrefix(trigger.Ref, "refs/heads/") != strings.TrimPrefix(repo.Ref, "refs/heads/") {
		return trigger, false, nil
	}
	if !gitSHA.MatchString(trigger.Commit) {
		return trigger, false, invalid("事件缺少有效 commit SHA")
	}
	return trigger, true, nil
}

func verifySignalAuth(provider string, headers http.Header, body []byte, secret string) (string, error) {
	var valid bool
	var deliveryID string
	switch provider {
	case "github":
		valid = verifyHMAC(body, headers.Get("X-Hub-Signature-256"), secret)
		deliveryID = headers.Get("X-GitHub-Delivery")
	case "gitlab":
		valid = subtle.ConstantTimeCompare([]byte(headers.Get("X-Gitlab-Token")), []byte(secret)) == 1
		deliveryID = headers.Get("X-Gitlab-Event-UUID")
	case "generic":
		valid = verifyHMAC(body, headers.Get("X-Gateway-Signature"), secret)
		deliveryID = headers.Get("X-Gateway-Delivery")
	default:
		return "", invalid("支持 github、gitlab 或 generic 来源事件")
	}
	if !valid {
		return "", domainError(ErrUnauthorized, "事件签名或密钥无效", nil)
	}
	return deliveryID, nil
}

type signalWebhookPayload struct {
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

func extractSignalEvent(provider string, headers http.Header, body []byte) (project, kind, ref, commit string, matched bool, err error) {
	var payload signalWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", "", "", false, invalid("事件不是有效的 JSON")
	}
	switch provider {
	case "github":
		project = payload.Repository.FullName
		switch headers.Get("X-GitHub-Event") {
		case "push":
			return project, "push", payload.Ref, payload.After, true, nil
		case "workflow_run":
			if payload.Action != "completed" {
				return "", "", "", "", false, nil
			}
			return project, "ci_completed", payload.Workflow.Branch, payload.Workflow.SHA, true, nil
		default:
			return "", "", "", "", false, nil
		}
	case "gitlab":
		project = payload.Project.Path
		switch headers.Get("X-Gitlab-Event") {
		case "Push Hook":
			return project, "push", payload.Ref, payload.After, true, nil
		case "Pipeline Hook":
			if !slices.Contains([]string{"success", "failed", "canceled", "skipped"}, payload.Attributes.Status) {
				return "", "", "", "", false, nil
			}
			return project, "ci_completed", payload.Attributes.Ref, payload.Attributes.SHA, true, nil
		default:
			return "", "", "", "", false, nil
		}
	case "generic":
		if !slices.Contains([]string{"push", "ci_completed", "release", "deploy"}, payload.Event) {
			return "", "", "", "", false, invalid("不支持的事件类型")
		}
		return payload.ProjectID, payload.Event, payload.Ref, payload.Commit, true, nil
	}
	return "", "", "", "", false, nil
}

// Called under m.mu. Deliveries persist before acknowledgement. A single follow-up
// job consumes all pending events for a source after its active job completes.
func (m *Maintainer) dispatchSignals() error {
	signals, err := store.MaintenanceDeliveries[sourceSignal](m.db).Pending()
	if err != nil {
		return err
	}
	latest := map[string]sourceSignal{}
	for _, signal := range signals {
		if _, exists := latest[signal.ServerID]; !exists {
			latest[signal.ServerID] = signal
		}
	}
	for _, id := range slices.Sorted(maps.Keys(latest)) {
		source, ok := m.sources[id]
		if !ok || source.Repository == nil || !source.Repository.EventsEnabled {
			if err := store.MaintenanceDeliveries[sourceSignal](m.db).Assign(id, "disabled"); err != nil {
				return fmt.Errorf("retire disabled signals: %w", err)
			}
			continue
		}
		run, err := m.makeRun(id, false)
		if err != nil {
			if errors.Is(err, ErrConflict) {
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
	return store.RunTx(m.db, func(tx store.DBTX) error {
		if err := store.MaintenanceRuns[json.RawMessage](tx).Insert(run.ID, encoded); err != nil {
			return fmt.Errorf("persist signal job: %w", err)
		}
		if err := store.MaintenanceDeliveries[sourceSignal](tx).Assign(run.ServerID, run.ID); err != nil {
			return fmt.Errorf("assign pending signals: %w", err)
		}
		return nil
	})
}

type DeliveryResult struct {
	Ignored   bool
	Duplicate bool
	RunID     string
}

// ReceiveSourceEvent verifies the configured source and persists deliveries
// before acknowledgement. Dispatch consumes them under the worker's mutex.
func (m *Maintainer) ReceiveSourceEvent(source MaintenanceSource, provider string, headers http.Header, body []byte) (DeliveryResult, error) {
	if source.Repository == nil || !source.Repository.EventsEnabled {
		return DeliveryResult{}, domainError(ErrNotFound, "此来源未开启仓库事件", nil)
	}
	trigger, relevant, err := parseSourceSignalWithLookup(provider, headers, body, *source.Repository, m.lookupEnv)
	if err != nil {
		return DeliveryResult{}, err
	}
	if !relevant {
		return DeliveryResult{Ignored: true}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return DeliveryResult{}, domainError(ErrUnavailable, "维护运行时正在停止", nil)
	}
	if sourceIdentity(m.sources[source.ServerID]) != sourceIdentity(source) {
		return DeliveryResult{}, domainError(ErrConflict, "来源配置已变化，请重新投递事件", nil)
	}
	runID, err := store.MaintenanceDeliveries[sourceSignal](m.db).RunID(source.ServerID, trigger.Provider, trigger.DeliveryID)
	if err == nil {
		return DeliveryResult{Duplicate: true, RunID: runID}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return DeliveryResult{}, fmt.Errorf("read source delivery: %w", err)
	}
	signal := sourceSignal{ServerID: source.ServerID, Trigger: trigger, ReceivedAt: time.Now()}
	encoded, err := json.Marshal(signal)
	if err != nil {
		return DeliveryResult{}, fmt.Errorf("encode source signal: %w", err)
	}
	if err := store.MaintenanceDeliveries[json.RawMessage](m.db).Insert(source.ServerID, trigger.Provider, trigger.DeliveryID, signal.ReceivedAt.Format(time.RFC3339Nano), encoded); err != nil {
		return DeliveryResult{}, fmt.Errorf("persist source signal: %w", err)
	}
	m.signal()
	return DeliveryResult{}, nil
}
