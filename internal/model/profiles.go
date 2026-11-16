package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/registry"
	"gatewaylab/internal/secrets"
	"gatewaylab/internal/store"

	"github.com/google/uuid"
)

type Settings struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	MaxTools       int    `json:"max_tools"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// ModelProfile is a saved Decisions model configuration. API keys remain in
// process memory and are never persisted to SQLite.
type ModelProfile struct {
	ID             string `json:"id"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	MaxTools       int    `json:"max_tools"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Active         bool   `json:"active"`
	KeyConfigured  bool   `json:"key_configured"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// PingModelResult reports a direct model connectivity check.
type PingModelResult struct {
	OK         bool   `json:"ok"`
	Model      string `json:"model"`
	DurationMS int64  `json:"duration_ms"`
}

type Service struct {
	mu          sync.RWMutex
	db          *sql.DB
	settings    Settings
	apiKey      string
	profiles    []ModelProfile
	profileKeys secrets.Store
}

func New(db *sql.DB) (*Service, error) {
	g := &Service{db: db, settings: Settings{BaseURL: "https://api.openai.com/v1", Model: "gpt-6-luna", MaxTools: 4, TimeoutSeconds: 45}}
	count, err := store.Settings[Settings](db).Count()
	if err != nil {
		return nil, fmt.Errorf("read settings count: %w", err)
	}
	if count == 0 {
		if err := g.persistSettings(g.settings); err != nil {
			return nil, err
		}
	}
	raw, err := store.Settings[json.RawMessage](db).Load()
	if err != nil {
		return nil, fmt.Errorf("load model settings: %w", err)
	}
	if err := json.Unmarshal(raw, &g.settings); err != nil {
		return nil, fmt.Errorf("decode model settings: %w", err)
	}
	if v := os.Getenv("GATEWAY_MODEL_BASE_URL"); v != "" {
		g.settings.BaseURL = v
	}
	if v := os.Getenv("GATEWAY_MODEL"); v != "" {
		g.settings.Model = v
	}
	g.apiKey = os.Getenv("GATEWAY_MODEL_API_KEY")
	if err := validateSettings(g.settings); err != nil {
		return nil, fmt.Errorf("invalid model settings: %w", err)
	}
	if err := g.initModelProfiles(); err != nil {
		return nil, err
	}
	return g, nil
}

// RuntimeConfig returns one consistent settings and credential snapshot.
func (g *Service) RuntimeConfig() (Settings, string) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.settings, g.apiKey
}
func (g *Service) Config() (Settings, bool) { s, key := g.RuntimeConfig(); return s, key != "" }
func validateSettings(s Settings) error {
	if err := validateURL(s.BaseURL); err != nil {
		return err
	}
	if s.MaxTools < 1 || s.MaxTools > 12 {
		return invalid("返回工具数需为 1–12")
	}
	if s.TimeoutSeconds < 5 || s.TimeoutSeconds > 180 {
		return invalid("超时需为 5–180 秒")
	}
	return nil
}

func (g *Service) initModelProfiles() error {
	profiles, err := g.loadModelProfiles()
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		profiles = []ModelProfile{{
			ID:             "default",
			BaseURL:        g.settings.BaseURL,
			Model:          g.settings.Model,
			MaxTools:       g.settings.MaxTools,
			TimeoutSeconds: g.settings.TimeoutSeconds,
			Active:         true,
			UpdatedAt:      time.Now().Format(time.RFC3339),
		}}
		if err := g.persistModelProfiles(profiles); err != nil {
			return err
		}
	}
	activeIdx := slices.IndexFunc(profiles, func(p ModelProfile) bool { return p.Active })
	if activeIdx < 0 {
		activeIdx = 0
		profiles[0].Active = true
	}
	// Keep env var overrides reflected in the active profile in memory.
	profiles[activeIdx].BaseURL = g.settings.BaseURL
	profiles[activeIdx].Model = g.settings.Model
	profiles[activeIdx].MaxTools = g.settings.MaxTools
	profiles[activeIdx].TimeoutSeconds = g.settings.TimeoutSeconds
	if g.apiKey != "" {
		g.profileKeys.Set(profiles[activeIdx].ID, g.apiKey)
	}
	g.profiles = profiles
	return nil
}

func (g *Service) ModelProfiles() []ModelProfile {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]ModelProfile, len(g.profiles))
	for i, p := range g.profiles {
		p.KeyConfigured = g.profileKeys.Get(p.ID) != "" || (p.Active && g.apiKey != "")
		out[i] = p
	}
	return out
}

func (g *Service) SaveSettings(s Settings, key string, clearKey bool) error {
	if err := validateSettings(s); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.persistSettings(s); err != nil {
		return err
	}
	g.settings = s
	if clearKey {
		g.apiKey = ""
	} else if key != "" {
		g.apiKey = key
	}
	if idx := slices.IndexFunc(g.profiles, func(p ModelProfile) bool { return p.Active }); idx >= 0 {
		g.profiles[idx].BaseURL = s.BaseURL
		g.profiles[idx].Model = s.Model
		g.profiles[idx].MaxTools = s.MaxTools
		g.profiles[idx].TimeoutSeconds = s.TimeoutSeconds
		g.profiles[idx].UpdatedAt = time.Now().Format(time.RFC3339)
		if clearKey {
			g.profileKeys.Set(g.profiles[idx].ID, "")
		} else if key != "" {
			g.profileKeys.Set(g.profiles[idx].ID, key)
		}
		if err := g.persistModelProfiles(g.profiles); err != nil {
			return err
		}
	}
	return nil
}

func (g *Service) SaveModelProfile(p ModelProfile, key string, clearKey bool) (ModelProfile, error) {
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.Model = strings.TrimSpace(p.Model)
	if p.Model == "" {
		return ModelProfile{}, invalid("请填写路由模型名称")
	}
	s := Settings{
		BaseURL:        p.BaseURL,
		Model:          p.Model,
		MaxTools:       p.MaxTools,
		TimeoutSeconds: p.TimeoutSeconds,
	}
	if err := validateSettings(s); err != nil {
		return ModelProfile{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().Format(time.RFC3339)
	idx := -1
	if p.ID != "" {
		idx = slices.IndexFunc(g.profiles, func(item ModelProfile) bool { return item.ID == p.ID })
	}
	if idx < 0 {
		idx = slices.IndexFunc(g.profiles, func(item ModelProfile) bool {
			return item.BaseURL == p.BaseURL && item.Model == p.Model
		})
	}

	next := slices.Clone(g.profiles)
	for i := range next {
		next[i].Active = false
	}

	var savedID string
	if idx >= 0 {
		next[idx].BaseURL = p.BaseURL
		next[idx].Model = p.Model
		next[idx].MaxTools = p.MaxTools
		next[idx].TimeoutSeconds = p.TimeoutSeconds
		next[idx].Active = true
		next[idx].UpdatedAt = now
		savedID = next[idx].ID
	} else {
		savedID = p.ID
		if savedID == "" || !registry.ValidID(savedID) {
			savedID = "mp-" + uuid.NewString()[:8]
		}
		p.ID = savedID
		p.Active = true
		p.UpdatedAt = now
		next = append([]ModelProfile{p}, next...)
	}

	if err := g.persistModelProfiles(next); err != nil {
		return ModelProfile{}, err
	}
	if err := g.persistSettings(s); err != nil {
		return ModelProfile{}, err
	}

	g.profiles = next
	g.settings = s
	if clearKey {
		g.profileKeys.Set(savedID, "")
		g.apiKey = ""
	} else if key != "" {
		g.profileKeys.Set(savedID, key)
		g.apiKey = key
	} else {
		g.apiKey = g.profileKeys.Get(savedID)
	}

	outIdx := slices.IndexFunc(g.profiles, func(item ModelProfile) bool { return item.ID == savedID })
	out := g.profiles[outIdx]
	out.KeyConfigured = g.profileKeys.Get(savedID) != ""
	return out, nil
}

func (g *Service) ActivateModelProfile(id string) (ModelProfile, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	idx := slices.IndexFunc(g.profiles, func(p ModelProfile) bool { return p.ID == id })
	if idx < 0 {
		return ModelProfile{}, domainError(ErrNotFound, "未找到指定的模型配置", nil)
	}

	next := slices.Clone(g.profiles)
	for i := range next {
		next[i].Active = (i == idx)
	}
	chosen := next[idx]
	s := Settings{
		BaseURL:        chosen.BaseURL,
		Model:          chosen.Model,
		MaxTools:       chosen.MaxTools,
		TimeoutSeconds: chosen.TimeoutSeconds,
	}
	if err := g.persistModelProfiles(next); err != nil {
		return ModelProfile{}, err
	}
	if err := g.persistSettings(s); err != nil {
		return ModelProfile{}, err
	}
	g.profiles = next
	g.settings = s
	g.apiKey = g.profileKeys.Get(id)

	chosen.KeyConfigured = g.apiKey != ""
	return chosen, nil
}

func (g *Service) DeleteModelProfile(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.profiles) <= 1 {
		return invalid("至少保留一个模型配置")
	}
	idx := slices.IndexFunc(g.profiles, func(p ModelProfile) bool { return p.ID == id })
	if idx < 0 {
		return domainError(ErrNotFound, "未找到指定的模型配置", nil)
	}
	wasActive := g.profiles[idx].Active
	next := slices.Delete(slices.Clone(g.profiles), idx, idx+1)

	nextSettings := g.settings
	nextKey := g.apiKey
	if wasActive {
		next[0].Active = true
		nextSettings = Settings{
			BaseURL:        next[0].BaseURL,
			Model:          next[0].Model,
			MaxTools:       next[0].MaxTools,
			TimeoutSeconds: next[0].TimeoutSeconds,
		}
		nextKey = g.profileKeys.Get(next[0].ID)
		if err := g.persistSettings(nextSettings); err != nil {
			return err
		}
	}
	if err := g.persistModelProfiles(next); err != nil {
		return err
	}
	g.profileKeys.Set(id, "")
	g.profiles = next
	g.settings = nextSettings
	g.apiKey = nextKey
	return nil
}

func (g *Service) persistSettings(s Settings) error {
	if _, err := json.Marshal(s); err != nil {
		return fmt.Errorf("encode model settings: %w", err)
	}
	if err := store.Settings[Settings](g.db).Save(s); err != nil {
		return fmt.Errorf("persist model settings: %w", err)
	}
	return nil
}

func (g *Service) loadModelProfiles() ([]ModelProfile, error) {
	profiles, err := store.ModelProfiles[ModelProfile](g.db).List()
	if err != nil {
		return nil, err
	}
	for i := range profiles {
		profiles[i].KeyConfigured = false
	}
	return profiles, nil
}

func (g *Service) persistModelProfiles(profiles []ModelProfile) error {
	tx, err := g.db.Begin()
	if err != nil {
		return fmt.Errorf("begin model profiles transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	repo := store.ModelProfiles[ModelProfile](tx)
	if err := repo.Clear(); err != nil {
		return fmt.Errorf("clear model profiles: %w", err)
	}
	for _, p := range profiles {
		p.KeyConfigured = false
		if _, err := json.Marshal(p); err != nil {
			return fmt.Errorf("encode model profile: %w", err)
		}
		if err := repo.Insert(p.ID, p); err != nil {
			return fmt.Errorf("persist model profile: %w", err)
		}
	}
	return tx.Commit()
}

func (g *Service) PingModel(ctx context.Context, s Settings, profileID, apiKey string, clearKey bool) (PingModelResult, error) {
	s.BaseURL = strings.TrimSpace(s.BaseURL)
	s.Model = strings.TrimSpace(s.Model)
	if err := validateURL(s.BaseURL); err != nil {
		return PingModelResult{}, err
	}
	if s.Model == "" {
		return PingModelResult{}, invalid("请填写路由模型名称")
	}
	if s.TimeoutSeconds < 5 || s.TimeoutSeconds > 180 {
		s.TimeoutSeconds = 30
	}

	effectiveKey := strings.TrimSpace(apiKey)
	if clearKey {
		effectiveKey = ""
	} else if effectiveKey == "" {
		g.mu.RLock()
		if profileID != "" {
			effectiveKey = g.profileKeys.Get(profileID)
		}
		if effectiveKey == "" && profileID == "" {
			effectiveKey = g.apiKey
		}
		if effectiveKey == "" {
			// Fallback to active key if editing the currently active profile before it was re-keyed.
			for _, p := range g.profiles {
				if p.ID == profileID && p.Active {
					effectiveKey = g.apiKey
					break
				}
			}
		}
		g.mu.RUnlock()
	}

	started := time.Now()
	if _, err := AskDecisions(ctx, s, effectiveKey, "ping", []string{"Connectivity check."}); err != nil {
		return PingModelResult{}, err
	}
	return PingModelResult{
		OK:         true,
		Model:      s.Model,
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}
