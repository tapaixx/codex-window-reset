package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
)

const (
	usageEndpoint        = "https://chatgpt.com/backend-api/wham/usage"
	resetCreditsEndpoint = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	snapshotStaleAfter   = 5 * time.Minute
)

// RuntimeStateRepository is the transactional state boundary needed for
// Guardrail Holds. A store.RuntimeStateRepository satisfies it directly.
type RuntimeStateRepository interface {
	Load() (domain.RuntimeState, error)
	Update(func(*domain.RuntimeState) error) error
}

type entry struct {
	Snapshot      domain.UsageSnapshot
	LastError     *domain.Error
	LastAttemptAt time.Time
}

type flight struct {
	done     chan struct{}
	snapshot domain.UsageSnapshot
	err      error
}

type Service struct {
	mu      sync.RWMutex
	entries map[string]entry
	flights map[string]*flight
	host    host.API
	states  RuntimeStateRepository
	clock   domain.Clock
}

var _ interface {
	Refresh(context.Context, accounts.Account) (domain.UsageSnapshot, error)
	Get(string, time.Time) (domain.SnapshotView, bool)
	ClearSnapshots()
	Evaluate(domain.Config, string, time.Time) domain.QuotaDecision
} = (*Service)(nil)

// New constructs a quota service. Snapshots are intentionally owned by this
// instance and are never persisted; only Guardrail Holds use states.
func New(api host.API, states RuntimeStateRepository, clock domain.Clock) *Service {
	return &Service{
		entries: make(map[string]entry),
		flights: make(map[string]*flight),
		host:    api,
		states:  states,
		clock:   clock,
	}
}

// Refresh obtains usage and reset-credit details for one account. Calls for
// one account share a flight, while different account keys perform I/O
// independently.
func (s *Service) Refresh(ctx context.Context, account accounts.Account) (domain.UsageSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.host == nil {
		return domain.UsageSnapshot{}, quotaRefreshError()
	}
	key := strings.TrimSpace(account.Key)
	if key == "" {
		return domain.UsageSnapshot{}, quotaRefreshError()
	}
	attemptAt := s.now()
	s.mu.Lock()
	if s.entries == nil {
		s.entries = make(map[string]entry)
	}
	if s.flights == nil {
		s.flights = make(map[string]*flight)
	}
	if existing, ok := s.flights[key]; ok {
		s.mu.Unlock()
		return waitForFlight(ctx, existing)
	}
	current := s.entries[key]
	currentSnapshot := cloneSnapshot(current.Snapshot)
	owner := &flight{done: make(chan struct{})}
	s.flights[key] = owner
	s.mu.Unlock()

	fresh, err := s.refreshOwner(ctx, account, key, attemptAt)
	s.mu.Lock()
	if err != nil {
		current = s.entries[key]
		current.Snapshot = currentSnapshot
		if current.Snapshot.AccountKey == "" {
			current.Snapshot.AccountKey = key
		}
		current.LastError = quotaRefreshError()
		current.LastAttemptAt = attemptAt
		s.entries[key] = current
		owner.snapshot = cloneSnapshot(current.Snapshot)
		owner.err = current.LastError
	} else {
		fresh.AccountKey = key
		fresh.CapturedAt = fresh.CapturedAt.UTC()
		fresh = cloneSnapshot(fresh)
		s.entries[key] = entry{Snapshot: fresh, LastAttemptAt: attemptAt}
		owner.snapshot = cloneSnapshot(fresh)
		owner.err = nil
	}
	delete(s.flights, key)
	close(owner.done)
	s.mu.Unlock()
	if err != nil {
		return cloneSnapshot(owner.snapshot), owner.err
	}
	return cloneSnapshot(owner.snapshot), nil
}

func (s *Service) refreshOwner(ctx context.Context, account accounts.Account, key string, capturedAt time.Time) (domain.UsageSnapshot, error) {
	usage, err := s.getJSON(ctx, account, usageEndpoint, nil, nil)
	if err != nil {
		return domain.UsageSnapshot{}, err
	}
	if !isSuccess(usage.StatusCode) {
		return domain.UsageSnapshot{}, errors.New("usage request failed")
	}
	snapshot, err := ParseUsage(usage.Body, capturedAt)
	if err != nil {
		return domain.UsageSnapshot{}, err
	}
	snapshot.AccountKey = key

	// Reset detail is deliberately best effort. Usage remains useful for
	// decisions when this secondary endpoint is unavailable, but its embedded
	// credit count remains incomplete and cannot authorize a reset.
	detail, detailErr := s.getJSON(ctx, account, resetCreditsEndpoint, map[string][]string{
		"OpenAI-Beta": {"codex-1"},
		"Originator":  {"Codex Desktop"},
	}, nil)
	if detailErr == nil && isSuccess(detail.StatusCode) {
		info := parseResetBody(detail.Body)
		if info.valid {
			snapshot.ResetInfoComplete = true
			if info.creditsPresent {
				snapshot.ResetCredits = cloneCredits(info.credits)
			}
			if count := resetApplicableCount(info); count != nil {
				snapshot.ResetApplicableCount = count
			} else if info.creditsPresent {
				zero := 0
				snapshot.ResetApplicableCount = &zero
			}
		}
	}
	return snapshot, nil
}

func parseResetBody(body []byte) resetCreditInfo {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return resetCreditInfo{}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return resetCreditInfo{}
	}
	return parseResetCreditValue(value)
}

func (s *Service) getJSON(ctx context.Context, account accounts.Account, endpoint string, extraHeaders map[string][]string, body []byte) (host.HTTPResponse, error) {
	material, err := accounts.AuthMaterial(ctx, s.host, account)
	if err != nil {
		return host.HTTPResponse{}, errors.New("credential request failed")
	}
	headers := map[string][]string{
		"Authorization":      {"Bearer " + material.AccessToken},
		"Chatgpt-Account-Id": {material.AccountID},
		"Accept":             {"application/json"},
		"Content-Type":       {"application/json"},
		"User-Agent":         {quotaUserAgent()},
	}
	for key, values := range extraHeaders {
		headers[key] = append([]string(nil), values...)
	}
	request := host.HTTPRequest{Method: "GET", URL: endpoint, Headers: headers, Body: body}
	response, err := s.host.HTTPDo(ctx, request)
	material = accounts.Material{}
	if err != nil {
		return host.HTTPResponse{}, err
	}
	return response, nil
}

// Get reads only the memory cache. It never refreshes or contacts the host.
func (s *Service) Get(key string, now time.Time) (domain.SnapshotView, bool) {
	if s == nil {
		return domain.SnapshotView{}, false
	}
	key = strings.TrimSpace(key)
	s.mu.RLock()
	item, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return domain.SnapshotView{}, false
	}
	snapshot := cloneSnapshot(item.Snapshot)
	current := now.UTC()
	stale := false
	if !snapshot.CapturedAt.IsZero() {
		stale = current.Sub(snapshot.CapturedAt.UTC()) >= snapshotStaleAfter
	}
	view := domain.SnapshotView{
		Snapshot:      snapshot,
		Stale:         stale,
		LastAttemptAt: item.LastAttemptAt.UTC(),
	}
	if item.LastError != nil {
		view.RefreshErrorCode = item.LastError.Code
	}
	return view, true
}

// ClearSnapshots removes only this service's memory cache. Persistent holds
// deliberately remain in runtime state.
func (s *Service) ClearSnapshots() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.entries = make(map[string]entry)
	s.mu.Unlock()
}

// Evaluate applies the current configurable guardrail to a cached snapshot
// and atomically transitions the persistent hold when a fresh successful
// snapshot proves exhaustion or recovery.
func (s *Service) Evaluate(cfg domain.Config, key string, now time.Time) domain.QuotaDecision {
	key = strings.TrimSpace(key)
	view, ok := s.Get(key, now)
	if !ok {
		if hold, loadErr := s.loadHold(key); loadErr != nil {
			return domain.DecisionGuardrailHold
		} else if hold != nil {
			return domain.DecisionGuardrailHold
		}
		return domain.DecisionUnknownFailOpen
	}

	var hold *domain.GuardrailHold
	var stateErr error
	if s != nil && s.states != nil {
		hold, stateErr = s.transitionHold(view, cfg, key, now)
	}
	if stateErr != nil {
		return domain.DecisionGuardrailHold
	}
	return decide(view, hold, cfg, now)
}

func (s *Service) loadHold(key string) (*domain.GuardrailHold, error) {
	if s == nil || s.states == nil || key == "" {
		return nil, nil
	}
	state, err := s.states.Load()
	if err != nil {
		return nil, err
	}
	hold, ok := state.GuardrailHolds[key]
	if !ok {
		return nil, nil
	}
	hold.EstablishedAt = hold.EstablishedAt.UTC()
	return &hold, nil
}

func (s *Service) transitionHold(view domain.SnapshotView, cfg domain.Config, key string, now time.Time) (*domain.GuardrailHold, error) {
	hold, err := s.loadHold(key)
	if err != nil {
		return nil, err
	}
	if view.RefreshErrorCode != "" || view.Stale {
		return hold, nil
	}
	longLow := anyLongAtOrBelow(view.Snapshot, cfg.LongWindowFloorPercent)
	hasLong := hasLongWindow(view.Snapshot.Windows)
	if longLow && hold == nil {
		var resulting *domain.GuardrailHold
		err := s.states.Update(func(state *domain.RuntimeState) error {
			if state.GuardrailHolds == nil {
				state.GuardrailHolds = make(map[string]domain.GuardrailHold)
			}
			if existing, ok := state.GuardrailHolds[key]; ok {
				existing.EstablishedAt = existing.EstablishedAt.UTC()
				resulting = &existing
				return nil
			}
			created := domain.GuardrailHold{AccountKey: key, EstablishedAt: now.UTC(), FloorPercent: cfg.LongWindowFloorPercent}
			state.GuardrailHolds[key] = created
			resulting = &created
			return nil
		})
		if err != nil {
			return hold, err
		}
		return resulting, nil
	}
	if hold != nil && hasLong && allLongAbove(view.Snapshot, cfg.LongWindowFloorPercent) {
		err := s.states.Update(func(state *domain.RuntimeState) error {
			_, ok := state.GuardrailHolds[key]
			if !ok {
				return nil
			}
			delete(state.GuardrailHolds, key)
			return nil
		})
		if err != nil {
			return hold, err
		}
		return nil, nil
	}
	return hold, nil
}

func decide(snapshot domain.SnapshotView, hold *domain.GuardrailHold, cfg domain.Config, now time.Time) domain.QuotaDecision {
	if hold != nil && (snapshot.RefreshErrorCode != "" || snapshot.Stale) {
		return domain.DecisionGuardrailHold
	}
	if snapshot.RefreshErrorCode != "" && hold == nil {
		return domain.DecisionUnknownFailOpen
	}
	if anyLongAtOrBelow(snapshot.Snapshot, cfg.LongWindowFloorPercent) {
		return domain.DecisionGuardrailHold
	}
	// A preheat request can only ever open a window while none is running.
	// Sending one into a running window cannot start another, so it buys
	// nothing and spends quota — and it is worst precisely when the running
	// window is nearly exhausted, which is where the previous quota and
	// remaining-time floors let it through.
	short, ok := shortestWindow(snapshot.Snapshot.Windows)
	if !snapshot.Stale && ok && short.ActiveAt(snapshot.Snapshot.CapturedAt) {
		return domain.DecisionWindowActive
	}
	return domain.DecisionProceed
}

func anyLongAtOrBelow(snapshot domain.UsageSnapshot, floor int) bool {
	shortIndex, ok := shortestIndex(snapshot.Windows)
	if !ok {
		return false
	}
	for index, window := range snapshot.Windows {
		if index != shortIndex && window.RemainingPercent <= floor {
			return true
		}
	}
	return false
}

func allLongAbove(snapshot domain.UsageSnapshot, floor int) bool {
	shortIndex, ok := shortestIndex(snapshot.Windows)
	if !ok || len(snapshot.Windows) < 2 {
		return false
	}
	for index, window := range snapshot.Windows {
		if index != shortIndex && window.RemainingPercent <= floor {
			return false
		}
	}
	return true
}

func hasLongWindow(windows []domain.UsageWindow) bool {
	_, ok := shortestIndex(windows)
	return ok && len(windows) >= 2
}

func shortestWindow(windows []domain.UsageWindow) (domain.UsageWindow, bool) {
	index, ok := shortestIndex(windows)
	if !ok {
		return domain.UsageWindow{}, false
	}
	return windows[index], true
}

func shortestIndex(windows []domain.UsageWindow) (int, bool) {
	if len(windows) == 0 {
		return 0, false
	}
	marked := -1
	for index, window := range windows {
		if !window.Short {
			continue
		}
		if marked == -1 || window.DurationMinutes < windows[marked].DurationMinutes {
			marked = index
		}
	}
	if marked >= 0 {
		return marked, true
	}
	index := 0
	for candidate := 1; candidate < len(windows); candidate++ {
		if windows[candidate].DurationMinutes < windows[index].DurationMinutes {
			index = candidate
		}
	}
	return index, true
}

func waitForFlight(ctx context.Context, current *flight) (domain.UsageSnapshot, error) {
	select {
	case <-current.done:
		return cloneSnapshot(current.snapshot), current.err
	case <-ctx.Done():
		return domain.UsageSnapshot{}, quotaRefreshError()
	}
}

func cloneSnapshot(snapshot domain.UsageSnapshot) domain.UsageSnapshot {
	snapshot.Windows = append([]domain.UsageWindow(nil), snapshot.Windows...)
	snapshot.ResetCredits = cloneCredits(snapshot.ResetCredits)
	snapshot.ResetApplicableCount = cloneInt(snapshot.ResetApplicableCount)
	snapshot.CapturedAt = snapshot.CapturedAt.UTC()
	return snapshot
}

func (s *Service) now() time.Time {
	if s != nil && s.clock != nil {
		return s.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func isSuccess(status int) bool { return status >= 200 && status < 300 }

func quotaUserAgent() string {
	return "codex-window-reset/0.0.0-dev (Linux; " + runtime.GOARCH + ")"
}

func statusCategory(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500 && status < 600:
		return "5xx"
	default:
		return "network_error"
	}
}

func quotaRefreshError() *domain.Error {
	return &domain.Error{Code: domain.CodeQuotaRefreshFailed, Message: "quota refresh failed", Retryable: true}
}
