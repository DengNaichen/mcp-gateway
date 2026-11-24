package gateway

import (
	"errors"
	"log/slog"
	"slices"
	"time"

	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
)

func (g *Gateway) Skills() ([]registry.Skill, error) {
	return store.Skills[registry.Skill](g.db).List()
}

func (g *Gateway) SaveSkill(s registry.Skill) (registry.Skill, error) {
	if !registry.ValidID(s.ID) || !validSkillAllowlist(s.AllowedRepos) || !validSkillAllowlist(s.AllowedServices) {
		return registry.Skill{}, invalid("A skill requires a valid ID and allowlists containing IDs or a single wildcard")
	}
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	s.AllowedRepos = append([]string{}, s.AllowedRepos...)
	s.AllowedServices = append([]string{}, s.AllowedServices...)
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s, store.Skills[registry.Skill](g.db).Put(s.ID, s)
}

func validSkillAllowlist(ids []string) bool {
	return len(ids) == 1 && ids[0] == "*" || !slices.ContainsFunc(ids, func(id string) bool { return !registry.ValidID(id) })
}

func (g *Gateway) DeleteSkill(id string) error {
	if !registry.ValidID(id) {
		return invalid("Invalid skill ID")
	}
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	return store.Skills[registry.Skill](g.db).Delete(id)
}

func (g *Gateway) skillAdmission(id string) policy.SkillAdmission {
	s, err := store.Skills[registry.Skill](g.db).Get(id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Error("read skill admission", "error", err)
	}
	return policy.SkillAdmission{Enabled: err == nil && s.Enabled, AllowedRepos: s.AllowedRepos, AllowedServices: s.AllowedServices}
}
