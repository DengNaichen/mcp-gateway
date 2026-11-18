package model

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// ModelProfile is a saved Decisions model configuration. Keys are kept in the
// credential store, never in SQLite or API responses.
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
	mu            sync.RWMutex
	db            *sql.DB
	settings      Settings
	apiKey        string
	profiles      []ModelProfile
	profileKeys   *secrets.Store
	envProfileID  string
	envKey        string
	verifiedPings map[string]time.Time
}

func New(db *sql.DB, credentials *secrets.Store) (*Service, error) {
	g := &Service{db: db, profileKeys: credentials, settings: Settings{BaseURL: "https://api.openai.com/v1", Model: "gpt-6-luna", MaxTools: 4, TimeoutSeconds: 45}}
	count, err := store.Settings[Settings](db).Count()
	if err != nil {
		return nil, fmt.Errorf("read settings count: %w", err)
	}
	if count == 0 {
		if err := persistSettingsTx(db, g.settings); err != nil {
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
	g.envKey = os.Getenv("GATEWAY_MODEL_API_KEY")
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
		if err := store.RunTx(g.db, func(tx store.DBTX) error {
			return persistModelProfilesTx(tx, profiles)
		}); err != nil {
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
	g.envProfileID = profiles[activeIdx].ID
	g.apiKey = g.profileKey(g.envProfileID)
	g.profiles = profiles
	return nil
}

func (g *Service) profileKey(id string) string {
	if id == g.envProfileID && g.envKey != "" {
		return g.envKey
	}
	return g.profileKeys.Get("model/" + id)
}
func (g *Service) clearOverride(id string) {
	if id == g.envProfileID {
		g.envKey = ""
	}
}

func (g *Service) ModelProfiles() []ModelProfile {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]ModelProfile, len(g.profiles))
	for i, p := range g.profiles {
		p.KeyConfigured = g.profileKey(p.ID) != "" || (p.Active && g.apiKey != "")
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

	idx := slices.IndexFunc(g.profiles, func(p ModelProfile) bool { return p.Active })
	var nextProfiles []ModelProfile
	if idx >= 0 {
		nextProfiles = slices.Clone(g.profiles)
		nextProfiles[idx].BaseURL = s.BaseURL
		nextProfiles[idx].Model = s.Model
		nextProfiles[idx].MaxTools = s.MaxTools
		nextProfiles[idx].TimeoutSeconds = s.TimeoutSeconds
		nextProfiles[idx].UpdatedAt = time.Now().Format(time.RFC3339)
	}
	changes := map[string]string{}
	if idx >= 0 && (clearKey || key != "") {
		if clearKey {
			key = ""
		}
		changes["model/"+g.profiles[idx].ID] = key
	}
	if err := g.profileKeys.Update(changes, func() error {
		return store.RunTx(g.db, func(tx store.DBTX) error {
			if err := persistSettingsTx(tx, s); err != nil {
				return err
			}
			if nextProfiles != nil {
				if err := persistModelProfilesTx(tx, nextProfiles); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		return err
	}

	g.settings = s
	if clearKey {
		g.apiKey = ""
	} else if key != "" {
		g.apiKey = key
	}
	if idx >= 0 {
		g.profiles = nextProfiles
		if clearKey || key != "" {
			g.clearOverride(g.profiles[idx].ID)
		}
	}
	return nil
}

// ValidateModelProfile normalizes and validates a model profile before saving or verifying.
func ValidateModelProfile(p ModelProfile) (Settings, error) {
	s := Settings{
		BaseURL:        strings.TrimSpace(p.BaseURL),
		Model:          strings.TrimSpace(p.Model),
		MaxTools:       p.MaxTools,
		TimeoutSeconds: p.TimeoutSeconds,
	}
	if s.Model == "" {
		return Settings{}, invalid("请填写路由模型名称")
	}
	if err := validateSettings(s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

func (g *Service) SaveModelProfile(p ModelProfile, key string, clearKey bool) (ModelProfile, error) {
	s, err := ValidateModelProfile(p)
	if err != nil {
		return ModelProfile{}, err
	}
	p.BaseURL = s.BaseURL
	p.Model = s.Model

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

	changes := map[string]string{}
	if clearKey || key != "" {
		if clearKey {
			key = ""
		}
		changes["model/"+savedID] = key
	}
	if err := g.profileKeys.Update(changes, func() error {
		return store.RunTx(g.db, func(tx store.DBTX) error {
			if err := persistModelProfilesTx(tx, next); err != nil {
				return err
			}
			return persistSettingsTx(tx, s)
		})
	}); err != nil {
		return ModelProfile{}, err
	}

	g.profiles = next
	g.settings = s
	if clearKey || key != "" {
		g.clearOverride(savedID)
	}
	g.apiKey = g.profileKey(savedID)

	outIdx := slices.IndexFunc(g.profiles, func(item ModelProfile) bool { return item.ID == savedID })
	out := g.profiles[outIdx]
	out.KeyConfigured = g.profileKey(savedID) != ""
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
	if err := store.RunTx(g.db, func(tx store.DBTX) error {
		if err := persistModelProfilesTx(tx, next); err != nil {
			return err
		}
		return persistSettingsTx(tx, s)
	}); err != nil {
		return ModelProfile{}, err
	}
	g.profiles = next
	g.settings = s
	g.apiKey = g.profileKey(id)

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
		nextKey = g.profileKey(next[0].ID)
	}
	if err := g.profileKeys.Update(map[string]string{"model/" + id: ""}, func() error {
		return store.RunTx(g.db, func(tx store.DBTX) error {
			if wasActive {
				if err := persistSettingsTx(tx, nextSettings); err != nil {
					return err
				}
			}
			return persistModelProfilesTx(tx, next)
		})
	}); err != nil {
		return err
	}
	g.clearOverride(id)
	g.profiles = next
	g.settings = nextSettings
	g.apiKey = nextKey
	return nil
}

func persistSettingsTx(db store.DBTX, s Settings) error {
	if _, err := json.Marshal(s); err != nil {
		return fmt.Errorf("encode model settings: %w", err)
	}
	if err := store.Settings[Settings](db).Save(s); err != nil {
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

func persistModelProfilesTx(tx store.DBTX, profiles []ModelProfile) error {
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
	return nil
}

const verifiedPingTTL = 15 * time.Minute

// ResolveEffectiveKey returns the API key that will be used for a profile or
// unsaved connection draft, accounting for explicit input, clearKey, and env overrides.
func (g *Service) ResolveEffectiveKey(profileID, apiKey string, clearKey bool) string {
	return g.resolveEffectiveKey(profileID, apiKey, clearKey)
}

func (g *Service) resolveEffectiveKey(profileID, apiKey string, clearKey bool) string {
	effectiveKey := strings.TrimSpace(apiKey)
	if clearKey {
		return ""
	}
	if effectiveKey != "" {
		return effectiveKey
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if profileID != "" {
		effectiveKey = g.profileKey(profileID)
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
	return effectiveKey
}

func pingSignature(s Settings, effectiveKey string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00%s", strings.TrimSpace(s.BaseURL), strings.TrimSpace(s.Model), s.TimeoutSeconds, effectiveKey)
	return hex.EncodeToString(h.Sum(nil))
}

func (g *Service) recordVerifiedPing(s Settings, effectiveKey string) {
	sig := pingSignature(s, effectiveKey)
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.verifiedPings == nil {
		g.verifiedPings = make(map[string]time.Time)
	}
	for k, ts := range g.verifiedPings {
		if now.Sub(ts) > verifiedPingTTL {
			delete(g.verifiedPings, k)
		}
	}
	g.verifiedPings[sig] = now
}

func (g *Service) isPingVerified(s Settings, effectiveKey string) bool {
	sig := pingSignature(s, effectiveKey)
	now := time.Now()
	g.mu.RLock()
	defer g.mu.RUnlock()
	ts, ok := g.verifiedPings[sig]
	return ok && now.Sub(ts) <= verifiedPingTTL
}

// VerifyModelConnection ensures the target profile passes a connectivity ping,
// reusing a recent verified ping for the exact same endpoint, model, timeout, and key.
func (g *Service) VerifyModelConnection(ctx context.Context, p ModelProfile, apiKey string, clearKey bool) error {
	s, err := ValidateModelProfile(p)
	if err != nil {
		return err
	}
	effectiveKey := g.resolveEffectiveKey(p.ID, apiKey, clearKey)
	if g.isPingVerified(s, effectiveKey) {
		return nil
	}
	_, err = g.PingModel(ctx, s, p.ID, apiKey, clearKey)
	return err
}

// NormalizePingTarget validates and normalizes ping settings and resolves the effective API key.
func (g *Service) NormalizePingTarget(s Settings, profileID, apiKey string, clearKey bool) (Settings, string, error) {
	s.BaseURL = strings.TrimSpace(s.BaseURL)
	s.Model = strings.TrimSpace(s.Model)
	if err := validateURL(s.BaseURL); err != nil {
		return Settings{}, "", err
	}
	if s.Model == "" {
		return Settings{}, "", invalid("请填写路由模型名称")
	}
	if s.TimeoutSeconds < 5 || s.TimeoutSeconds > 180 {
		s.TimeoutSeconds = 30
	}
	return s, g.resolveEffectiveKey(profileID, apiKey, clearKey), nil
}

func (g *Service) PingModel(ctx context.Context, s Settings, profileID, apiKey string, clearKey bool) (PingModelResult, error) {
	s, effectiveKey, err := g.NormalizePingTarget(s, profileID, apiKey, clearKey)
	if err != nil {
		return PingModelResult{}, err
	}

	started := time.Now()
	if _, err := AskDecisions(ctx, s, effectiveKey, "ping", []string{"Connectivity check."}); err != nil {
		return PingModelResult{}, err
	}
	g.recordVerifiedPing(s, effectiveKey)
	return PingModelResult{
		OK:         true,
		Model:      s.Model,
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}
