package main

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type catalogFilterIntent struct {
	serverID string
	access   string // "", "all", "ro", "rw"
}

type catalogToolIntent struct {
	toolID   string
	serverID string
}

type playgroundReplayIntent struct {
	intent   string
	actor    string
	repoKey  string
	hasScope bool
}

// windowHost owns the attached mygo.Window reference, background polling
// cancellation, and window visibility flag.
type windowHost struct {
	mu        sync.Mutex
	win       *mygo.Window
	stopPoll  context.CancelFunc
	winHidden atomic.Bool
}

func (h *windowHost) attach(win *mygo.Window) context.Context {
	h.mu.Lock()
	if h.stopPoll != nil {
		h.stopPoll()
		h.stopPoll = nil
	}
	h.win = win
	pollCtx, cancel := context.WithCancel(context.Background())
	h.stopPoll = cancel
	h.mu.Unlock()
	return pollCtx
}

func (h *windowHost) detach() {
	h.mu.Lock()
	if h.stopPoll != nil {
		h.stopPoll()
		h.stopPoll = nil
	}
	h.win = nil
	h.mu.Unlock()
}

func (h *windowHost) current() *mygo.Window {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.win
}

func (h *windowHost) wake() {
	if win := h.current(); win != nil {
		win.Update(func() {})
	}
}

func (h *windowHost) setHidden(hidden bool) {
	h.winHidden.Store(hidden)
}

func (h *windowHost) isHidden() bool {
	return h.winHidden.Load()
}

// toastQueue collects toast messages from any goroutine and drains them on the UI thread.
type toastQueue struct {
	mu      sync.Mutex
	pending []string
}

func (q *toastQueue) push(msg string) bool {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return false
	}
	q.mu.Lock()
	q.pending = append(q.pending, msg)
	q.mu.Unlock()
	return true
}

func (q *toastQueue) drain(c *ui.Context) {
	q.mu.Lock()
	if len(q.pending) == 0 {
		q.mu.Unlock()
		return
	}
	msgs := q.pending
	q.pending = nil
	q.mu.Unlock()

	for _, msg := range msgs {
		c.Toast(msg)
	}
}

// headlessSync serializes UI state mutations and frame rendering when no
// native window is attached (e.g. headless ui.Tester under -race).
type headlessSync struct {
	viewMu  sync.Mutex
	queueMu sync.Mutex
	pending []func()
}

func (h *headlessSync) runOrQueue(fn func()) {
	if h.viewMu.TryLock() {
		h.drainLocked()
		fn()
		h.drainLocked()
		h.viewMu.Unlock()
		return
	}
	h.queueMu.Lock()
	h.pending = append(h.pending, fn)
	h.queueMu.Unlock()
}

func (h *headlessSync) enterFrame() func() {
	h.viewMu.Lock()
	h.drainLocked()
	return func() {
		h.drainLocked()
		h.viewMu.Unlock()
	}
}

func (h *headlessSync) drainLocked() {
	for {
		h.queueMu.Lock()
		if len(h.pending) == 0 {
			h.queueMu.Unlock()
			return
		}
		batch := h.pending
		h.pending = nil
		h.queueMu.Unlock()
		for _, fn := range batch {
			fn()
		}
	}
}

// navIntent holds cross-page navigation handoffs and the thread-safe active ViewID snapshot.
type navIntent struct {
	viewAtomic    atomic.Value // stores ViewID
	serviceID     string
	eventID       string
	repoKey       string
	addService    bool
	catalogFilter *catalogFilterIntent
	catalogTool   *catalogToolIntent
	replay        *playgroundReplayIntent
}

func (n *navIntent) currentView() ViewID {
	if v, ok := n.viewAtomic.Load().(ViewID); ok {
		return v
	}
	return ViewOverview
}

func (n *navIntent) setView(view ViewID) {
	n.viewAtomic.Store(view)
}

func (n *navIntent) navigate(view ViewID) {
	if view != ViewServices {
		n.serviceID = ""
	}
	if view != ViewLogs {
		n.eventID = ""
	}
	n.setView(view)
}

func (n *navIntent) openService(serverID string) {
	n.serviceID = serverID
	n.setView(ViewServices)
}

func (n *navIntent) openAddService() {
	n.addService = true
	n.serviceID = ""
	n.setView(ViewServices)
}

func (n *navIntent) consumeAddService() bool {
	if !n.addService {
		return false
	}
	n.addService = false
	return true
}

func (n *navIntent) openCatalogFilter(serverID, access string) {
	n.catalogFilter = &catalogFilterIntent{
		serverID: serverID,
		access:   access,
	}
	n.catalogTool = nil
	n.setView(ViewCatalog)
}

func (n *navIntent) openCatalogTool(toolID, serverID string) {
	n.catalogTool = &catalogToolIntent{
		toolID:   toolID,
		serverID: serverID,
	}
	n.catalogFilter = nil
	n.setView(ViewCatalog)
}

func (n *navIntent) openReplay(intent, actor string, scope ...string) {
	hasScope := len(scope) > 0
	if intent != "" || hasScope {
		next := &playgroundReplayIntent{
			intent:   intent,
			actor:    actor,
			hasScope: hasScope,
		}
		if len(scope) >= 1 {
			next.repoKey = scope[0]
		}
		n.replay = next
	}
	n.setView(ViewPlayground)
}

func (n *navIntent) openRepos(repoKey string) {
	if repoKey != "" {
		n.repoKey = repoKey
	}
	n.setView(ViewRepos)
}

func (n *navIntent) consumeRepoKey() string {
	key := n.repoKey
	n.repoKey = ""
	return key
}

func (n *navIntent) openLogsEvent(eventID string) {
	n.eventID = eventID
	n.setView(ViewLogs)
}

func (n *navIntent) consumeEventID() string {
	id := n.eventID
	n.eventID = ""
	return id
}

func takeIntent[T any](slot **T) (T, bool) {
	if *slot == nil {
		var zero T
		return zero, false
	}
	val := **slot
	*slot = nil
	return val, true
}

// sessionState tracks authentication, bootstrap loading, refresh, and async busy count.
// Embedded in DesktopUI so existing callers and tests keep direct field access.
type sessionState struct {
	NeedsLogin bool
	LoginToken string
	LoginError string
	LoggingIn  bool
	Loading    bool
	BootError  string
	Refreshing bool
	BusyCount  int
}

func (s *sessionState) beginBusy() {
	s.BusyCount++
}

func (s *sessionState) endBusy() {
	if s.BusyCount > 0 {
		s.BusyCount--
	}
}

func (s *sessionState) beginRefresh(hasState bool) {
	s.Refreshing = true
	if !hasState {
		s.Loading = true
	}
}

func (s *sessionState) finishRefresh() {
	s.Refreshing = false
	s.Loading = false
}

func (s *sessionState) markUnauthorized() {
	s.NeedsLogin = true
	s.BootError = ""
}

func (s *sessionState) markAuthenticated() {
	s.LoginToken = ""
	s.LoginError = ""
	s.NeedsLogin = false
	s.BootError = ""
	s.Loading = false
}

func (s *sessionState) prepareLogin(hasClient bool) (string, bool) {
	token := strings.TrimSpace(s.LoginToken)
	if token == "" || s.LoggingIn || !hasClient {
		return "", false
	}
	s.LoggingIn = true
	s.LoginError = ""
	return token, true
}

func (s *sessionState) failLogin(err error) {
	s.LoggingIn = false
	s.LoginError = err.Error()
}
