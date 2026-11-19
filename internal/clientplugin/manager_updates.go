package clientplugin

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// SupportsReleaseUpdates reports whether a release feed is available for this platform.
func (m *Manager) SupportsReleaseUpdates() bool { return m.updates != nil && m.binPath == "" }

// CheckUpdates immediately refreshes release metadata without downloading or applying a Hook.
func (m *Manager) CheckUpdates(ctx context.Context) (time.Time, error) {
	if !m.SupportsReleaseUpdates() {
		return time.Time{}, nil
	}
	state, _, err := m.updates.check(ctx, true)
	if state.LastSuccess.IsZero() && err != nil {
		state = m.updates.readState()
	}
	return state.LastSuccess, err
}

// WatchUpdates checks on startup when due and reschedules after each outcome.
// changed only refreshes the caller's view of on-disk plugin status.
func (m *Manager) WatchUpdates(ctx context.Context, changed func()) {
	if !m.SupportsReleaseUpdates() {
		return
	}
	for ctx.Err() == nil {
		state, checked, err := m.updates.check(ctx, false)
		if checked && err == nil {
			changed()
		}
		wait := max(time.Minute, time.Until(state.nextCheck()))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Manager) latestRelease() (*releaseManifest, []byte) {
	if !m.SupportsReleaseUpdates() {
		return nil, nil
	}
	raw := m.updates.readState().Release
	if len(raw) == 0 {
		return nil, nil
	}
	release, err := m.updates.parse(raw)
	if err != nil {
		return nil, nil
	}
	return release, raw
}

func (m *Manager) activateRelease(release *releaseManifest) func() {
	version := m.version
	m.activeRelease = release
	m.version = release.Version
	return func() {
		m.activeRelease = nil
		m.version = version
	}
}

func (m *Manager) installedRelease(r *pluginReceipt) (*releaseManifest, error) {
	if r == nil || !r.Release {
		return nil, nil
	}
	if m.updates == nil {
		return nil, fmt.Errorf("installed plugin release %s is unavailable on this platform", r.Version)
	}
	return m.updates.cachedRelease(r.Version)
}

func (m *Manager) applyRelease(id string, update bool, op func() error) error {
	r, err := m.loadReceipt(id)
	if err != nil {
		return err
	}
	latest, raw := m.latestRelease()
	current := m.version
	if r != nil {
		current = r.Version
	}
	switch {
	case latest != nil && (r == nil || update) && releaseIsNewer(latest.Version, current):
		if err := m.updates.stage(context.Background(), latest, raw); err != nil {
			return err
		}
		defer m.activateRelease(latest)()
	default:
		installed, err := m.installedRelease(r)
		if err != nil {
			return err
		}
		if installed != nil {
			// Repair a missing or damaged cached Hook before installing a copy of
			// the same release again, including when no newer release is offered.
			raw, err := readOptionalFile(m.updates.cachedManifest(installed.Version))
			if err != nil {
				return err
			}
			if err := m.updates.stage(context.Background(), installed, raw); err != nil {
				return err
			}
			defer m.activateRelease(installed)()
		}
	}
	return op()
}

func (m *Manager) markAvailableUpdate(row Status, latest *releaseManifest) Status {
	if latest == nil || !row.CanRemove || !releaseIsNewer(latest.Version, row.InstalledVersion) {
		return row
	}
	row.CanUpdate = true
	row.TargetVersion = latest.Version
	row.AvailableVersion = latest.Version
	return row
}

func (m *Manager) withInstallLock(op func() error) error {
	if m.updates == nil {
		return op()
	}
	return withPluginLock(context.Background(), filepath.Join(m.stateDir, ".plugin-operation.lock"), op)
}
