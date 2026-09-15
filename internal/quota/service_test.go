package quota

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
	"github.com/tapaixx/codex-window-reset/internal/store"
)

const (
	quotaUsageURL        = "https://chatgpt.com/backend-api/wham/usage"
	quotaResetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
)

type quotaClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *quotaClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (*quotaClock) AfterFunc(time.Duration, func()) domain.Timer { return quotaNoopTimer{} }

type quotaNoopTimer struct{}

func (quotaNoopTimer) Stop() bool { return true }

type quotaTestHost struct {
	mu sync.Mutex

	auth          map[string]json.RawMessage
	authCalls     []string
	requests      []host.HTTPRequest
	responses     map[string]host.HTTPResponse
	errors        map[string]error
	requestHook   func(host.HTTPRequest)
	usageStarted  chan struct{}
	releaseUsage  chan struct{}
	usageStartOne sync.Once
}

func (h *quotaTestHost) ListAuthFiles(context.Context) ([]host.AuthFile, error) { return nil, nil }

func (h *quotaTestHost) GetAuth(_ context.Context, authIndex string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authCalls = append(h.authCalls, authIndex)
	raw, ok := h.auth[authIndex]
	if !ok {
		return nil, errors.New("credential fixture unavailable")
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (h *quotaTestHost) HTTPDo(_ context.Context, request host.HTTPRequest) (host.HTTPResponse, error) {
	h.mu.Lock()
	h.requests = append(h.requests, cloneHTTPRequest(request))
	hook := h.requestHook
	response := h.responses[request.URL]
	err := h.errors[request.URL]
	usageStarted := h.usageStarted
	releaseUsage := h.releaseUsage
	h.mu.Unlock()

	if hook != nil {
		hook(request)
	}
	if request.URL == quotaUsageURL && usageStarted != nil {
		h.usageStartOne.Do(func() { close(usageStarted) })
		<-releaseUsage
	}
	if err != nil {
		return host.HTTPResponse{}, err
	}
	response.Body = append([]byte(nil), response.Body...)
	return response, nil
}

func (*quotaTestHost) Log(context.Context, string, string, map[string]any) {}

func cloneHTTPRequest(request host.HTTPRequest) host.HTTPRequest {
	request.Body = append([]byte(nil), request.Body...)
	if request.Headers != nil {
		headers := request.Headers
		request.Headers = make(map[string][]string, len(headers))
		for key, values := range headers {
			request.Headers[key] = append([]string(nil), values...)
		}
	}
	return request
}

func newQuotaHost(now time.Time) *quotaTestHost {
	return &quotaTestHost{
		auth: map[string]json.RawMessage{
			"auth-one": json.RawMessage(`{"access_token":"fixture-token","account_id":"upstream-one"}`),
			"auth-two": json.RawMessage(`{"access_token":"fixture-token-two","account_id":"upstream-two"}`),
		},
		responses: map[string]host.HTTPResponse{
			quotaUsageURL:        {StatusCode: 200, Body: usageFixture(now, 10, 20, true)},
			quotaResetCreditsURL: {StatusCode: 200, Body: []byte(`{"available_count":2,"applicable_available_count":2,"credits":[{"id":"credit","reset_type":"codex_rate_limits","status":"available","expires_at":"2026-09-16T00:00:00Z"}]}`)},
		},
		errors: map[string]error{},
	}
}

func usageFixture(now time.Time, shortUsed, longUsed float64, includeLong bool) []byte {
	windows := map[string]any{
		"primary_window": map[string]any{
			"limit_window_seconds": 18000,
			"used_percent":         shortUsed,
			"reset_at":             now.Add(2 * time.Hour).Unix(),
		},
	}
	if includeLong {
		windows["secondary_window"] = map[string]any{
			"limit_window_seconds": 604800,
			"used_percent":         longUsed,
			"reset_at":             now.Add(7 * 24 * time.Hour).Unix(),
		}
	}
	body, err := json.Marshal(map[string]any{
		"rate_limit": windows,
		"rate_limit_reset_credits": map[string]any{
			"available": 2,
		},
	})
	if err != nil {
		panic(err)
	}
	return body
}

func testAccount(key, authIndex string) accounts.Account {
	return accounts.Account{Key: key, AuthIndex: authIndex}
}

type failingUpdateRepository struct {
	state      domain.RuntimeState
	updateErr  error
	updateCall int
}

func (r *failingUpdateRepository) Load() (domain.RuntimeState, error) {
	return r.state, nil
}

func (r *failingUpdateRepository) Update(func(*domain.RuntimeState) error) error {
	r.updateCall++
	return r.updateErr
}

func requestCount(h *quotaTestHost) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func requestURLs(h *quotaTestHost) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	urls := make([]string, len(h.requests))
	for i, request := range h.requests {
		urls[i] = request.URL
	}
	return urls
}

func authCallCount(h *quotaTestHost) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.authCalls)
}

func TestQuotaRefreshStoresSnapshotAndUsesDedicatedResetInfo(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	c := &quotaClock{now: now}
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), c)
	account := testAccount("acct-one", "auth-one")

	got, err := s.Refresh(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountKey != account.Key || !got.CapturedAt.Equal(now) || len(got.Windows) != 2 {
		t.Fatalf("snapshot = %#v", got)
	}
	if !got.ResetInfoComplete {
		t.Fatal("successful dedicated reset-info response should complete reset info")
	}
	if got.ResetApplicableCount == nil || *got.ResetApplicableCount != 2 || len(got.ResetCredits) != 1 {
		t.Fatalf("reset info = %#v", got)
	}
	if requestCount(h) != 2 || authCallCount(h) != 2 {
		t.Fatalf("refresh calls = HTTP %d, auth %d; want one credential/request pair per endpoint", requestCount(h), authCallCount(h))
	}
}

func TestQuotaRefreshSameAccountSingleFlightMakesExactlyOneUpstreamPair(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	h.usageStarted = make(chan struct{})
	h.releaseUsage = make(chan struct{})
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")

	const callers = 8
	start := make(chan struct{})
	ready := make(chan struct{}, callers)
	results := make(chan domain.UsageSnapshot, callers)
	errorsOut := make(chan error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wait.Done()
			<-start
			ready <- struct{}{}
			result, err := s.Refresh(context.Background(), account)
			results <- result
			errorsOut <- err
		}()
	}
	close(start)
	for i := 0; i < callers; i++ {
		<-ready
	}
	select {
	case <-h.usageStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("single-flight owner did not reach usage request")
	}
	// Let all ready callers enter the existing flight before allowing its
	// owner to finish; otherwise a late caller would correctly start a new
	// flight after the first one completed.
	time.Sleep(25 * time.Millisecond)
	close(h.releaseUsage)
	wait.Wait()
	close(results)
	close(errorsOut)

	for err := range errorsOut {
		if err != nil {
			t.Fatalf("refresh error = %v", err)
		}
	}
	for result := range results {
		if result.AccountKey != account.Key || len(result.Windows) != 2 {
			t.Fatalf("refresh result = %#v", result)
		}
	}
	if requestCount(h) != 2 || authCallCount(h) != 2 {
		t.Fatalf("single-flight calls = HTTP %d, auth %d; want exactly 2 each", requestCount(h), authCallCount(h))
	}
	if got := requestURLs(h); !reflect.DeepEqual(got, []string{quotaUsageURL, quotaResetCreditsURL}) {
		t.Fatalf("upstream URLs = %#v", got)
	}
}

func TestQuotaRefreshDifferentAccountsRemainIndependent(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	var mu sync.Mutex
	started := map[string]chan struct{}{
		"upstream-one": make(chan struct{}),
		"upstream-two": make(chan struct{}),
	}
	release := map[string]chan struct{}{
		"upstream-one": make(chan struct{}),
		"upstream-two": make(chan struct{}),
	}
	h.requestHook = func(request host.HTTPRequest) {
		if request.URL != quotaUsageURL {
			return
		}
		accountID := request.Headers["Chatgpt-Account-Id"][0]
		mu.Lock()
		start := started[accountID]
		releaseGate := release[accountID]
		mu.Unlock()
		select {
		case <-start:
		default:
			close(start)
		}
		<-releaseGate
	}

	// The host's request hook blocks both account usage requests concurrently.
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})
	results := make(chan error, 2)
	go func() { _, err := s.Refresh(context.Background(), testAccount("acct-one", "auth-one")); results <- err }()
	go func() { _, err := s.Refresh(context.Background(), testAccount("acct-two", "auth-two")); results <- err }()
	for _, accountID := range []string{"upstream-one", "upstream-two"} {
		select {
		case <-started[accountID]:
		case <-time.After(2 * time.Second):
			t.Fatalf("account %s did not reach usage request", accountID)
		}
	}
	close(release["upstream-one"])
	close(release["upstream-two"])
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("independent refresh error = %v", err)
		}
	}
	if requestCount(h) != 4 || authCallCount(h) != 4 {
		t.Fatalf("independent calls = HTTP %d, auth %d; want four each", requestCount(h), authCallCount(h))
	}
}

func TestQuotaGetDoesNotMakeHostCalls(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	before := requestCount(h)
	view, ok := s.Get(account.Key, now)
	if !ok || view.Snapshot.AccountKey != account.Key {
		t.Fatalf("view = %#v, ok = %t", view, ok)
	}
	if requestCount(h) != before || authCallCount(h) != 2 {
		t.Fatalf("Get made upstream calls: HTTP %d -> %d, auth %d", before, requestCount(h), authCallCount(h))
	}
}

func TestQuotaGetMarksSnapshotStaleAtExactlyFiveMinutes(t *testing.T) {
	captured := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(captured)
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: captured})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		now   time.Time
		stale bool
	}{
		{name: "before boundary", now: captured.Add(5*time.Minute - time.Nanosecond), stale: false},
		{name: "at boundary", now: captured.Add(5 * time.Minute), stale: true},
		{name: "after boundary", now: captured.Add(5*time.Minute + time.Nanosecond), stale: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			view, ok := s.Get(account.Key, tt.now)
			if !ok || view.Stale != tt.stale {
				t.Fatalf("view = %#v, ok = %t, want stale=%t", view, ok, tt.stale)
			}
		})
	}
}

func TestQuotaRefreshFailurePreservesSnapshotAndExposesOnlyStableError(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	repo := store.NewRuntimeStateRepository(t.TempDir())
	s := New(h, repo, &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	first, err := s.Refresh(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}

	h.mu.Lock()
	h.errors[quotaUsageURL] = errors.New("upstream body contains fixture-secret-token")
	h.mu.Unlock()
	second, err := s.Refresh(context.Background(), account)
	if err == nil || domain.CodeOf(err) != domain.CodeQuotaRefreshFailed {
		t.Fatalf("refresh error = %v, code = %q", err, domain.CodeOf(err))
	}
	if strings.Contains(err.Error(), "fixture-secret-token") || strings.Contains(err.Error(), "upstream body") {
		t.Fatalf("refresh error leaked upstream detail: %v", err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("failed refresh snapshot = %#v, want prior %#v", second, first)
	}
	view, ok := s.Get(account.Key, now)
	if !ok || view.RefreshErrorCode != domain.CodeQuotaRefreshFailed || !view.LastAttemptAt.Equal(now) {
		t.Fatalf("failed refresh view = %#v, ok = %t", view, ok)
	}
}

func TestQuotaEvaluateFailsOpenWithoutKnownHold(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	repo := store.NewRuntimeStateRepository(t.TempDir())
	s := New(h, repo, &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.errors[quotaUsageURL] = errors.New("temporary upstream failure")
	h.mu.Unlock()
	if _, err := s.Refresh(context.Background(), account); err == nil {
		t.Fatal("failed refresh unexpectedly succeeded")
	}
	if got := s.Evaluate(domain.DefaultConfig(), account.Key, now); got != domain.DecisionUnknownFailOpen {
		t.Fatalf("decision = %q, want %q", got, domain.DecisionUnknownFailOpen)
	}
}

func TestQuotaEvaluateSkipsAnyRunningWindow(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	h.responses[quotaUsageURL] = host.HTTPResponse{StatusCode: 200, Body: usageFixture(now, 80, 20, true)}
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	cfg := domain.DefaultConfig()
	if got := s.Evaluate(cfg, account.Key, now); got != domain.DecisionWindowActive {
		t.Fatalf("decision = %q, want %q for a running window", got, domain.DecisionWindowActive)
	}
}

func TestQuotaEvaluateNeverAuthorizesSufficientWindowFromStaleSnapshot(t *testing.T) {
	captured := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(captured)
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: captured})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(domain.DefaultConfig(), account.Key, captured.Add(5*time.Minute)); got == domain.DecisionSufficientWindow {
		t.Fatal("stale snapshot authorized sufficient window")
	}
}

func TestQuotaGuardrailHoldPersistsAcrossFailureStalenessAndRestart(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	repo := store.NewRuntimeStateRepository(dir)
	h := newQuotaHost(now)
	s := New(h, repo, &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	cfg := domain.DefaultConfig()
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	// A Long Window at the configured floor establishes a hold.
	h.mu.Lock()
	h.responses[quotaUsageURL] = host.HTTPResponse{StatusCode: 200, Body: usageFixture(now, 10, 90, true)}
	h.mu.Unlock()
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(cfg, account.Key, now); got != domain.DecisionGuardrailHold {
		t.Fatalf("low long-window decision = %q", got)
	}
	state, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.GuardrailHolds[account.Key]; !ok {
		t.Fatalf("hold was not persisted: %#v", state.GuardrailHolds)
	}

	h.mu.Lock()
	h.errors[quotaUsageURL] = errors.New("temporary failure")
	h.mu.Unlock()
	if _, err := s.Refresh(context.Background(), account); err == nil {
		t.Fatal("failed refresh unexpectedly succeeded")
	}
	if got := s.Evaluate(cfg, account.Key, now.Add(5*time.Minute)); got != domain.DecisionGuardrailHold {
		t.Fatalf("stale known hold decision = %q", got)
	}

	// A new service has no in-memory snapshot, but the persistent hold still
	// blocks automatic actions after restart.
	restarted := New(h, repo, &quotaClock{now: now.Add(5 * time.Minute)})
	if got := restarted.Evaluate(cfg, account.Key, now.Add(5*time.Minute)); got != domain.DecisionGuardrailHold {
		t.Fatalf("restart hold decision = %q", got)
	}
}

func TestQuotaGuardrailHoldClearsOnlyAfterEveryLongWindowRecovers(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	repo := store.NewRuntimeStateRepository(t.TempDir())
	s := New(h, repo, &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	cfg := domain.DefaultConfig()

	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(cfg, account.Key, now); got != domain.DecisionProceed && got != domain.DecisionWindowActive {
		t.Fatalf("initial decision = %q", got)
	}
	h.mu.Lock()
	h.responses[quotaUsageURL] = host.HTTPResponse{StatusCode: 200, Body: []byte(`{
		"rateLimit":{"windows":[
			{"limitWindowSeconds":18000,"usedPercent":10,"resetAt":"2026-09-09T14:00:00Z"},
			{"limitWindowSeconds":604800,"usedPercent":95,"resetAt":"2026-09-16T12:00:00Z"},
			{"limitWindowSeconds":2592000,"usedPercent":50,"resetAt":"2026-10-09T12:00:00Z"}
		]},
		"rateLimitResetCredits":{"availableCount":2}
	}`)}
	h.mu.Unlock()
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(cfg, account.Key, now); got != domain.DecisionGuardrailHold {
		t.Fatalf("hold establishment decision = %q", got)
	}

	// Recovering only one of two Long Windows must not clear the hold.
	h.mu.Lock()
	h.responses[quotaUsageURL] = host.HTTPResponse{StatusCode: 200, Body: []byte(`{
		"rate_limit":{"windows":[
			{"limit_window_seconds":18000,"used_percent":10,"reset_at":1788973200},
			{"limit_window_seconds":604800,"used_percent":80,"reset_at":1789578000},
			{"limit_window_seconds":2592000,"used_percent":95,"reset_at":1791547200}
		]},
		"rate_limit_reset_credits":{"available":2}
	}`)}
	h.mu.Unlock()
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(cfg, account.Key, now); got != domain.DecisionGuardrailHold {
		t.Fatalf("partial recovery decision = %q", got)
	}

	// Only the successful refresh proving all Long Windows above the floor can
	// clear the persistent hold.
	h.mu.Lock()
	h.responses[quotaUsageURL] = host.HTTPResponse{StatusCode: 200, Body: usageFixture(now, 10, 20, true)}
	h.mu.Unlock()
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	// The hold is gone, so the decision falls through to the window rule; this
	// fixture's short window is running, which is skipped on its own merits.
	if got := s.Evaluate(cfg, account.Key, now); got != domain.DecisionWindowActive {
		t.Fatalf("full recovery decision = %q, want the hold released", got)
	}
	state, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.GuardrailHolds[account.Key]; ok {
		t.Fatalf("hold remained after complete recovery: %#v", state.GuardrailHolds)
	}
}

func TestQuotaRefreshKeepsEmbeddedCreditsPartialWhenDetailFails(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	h.errors[quotaResetCreditsURL] = errors.New("reset detail unavailable")
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})

	got, err := s.Refresh(context.Background(), testAccount("acct-one", "auth-one"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ResetInfoComplete {
		t.Fatal("failed dedicated endpoint marked reset info complete")
	}
	if got.ResetApplicableCount == nil || *got.ResetApplicableCount != 2 {
		t.Fatalf("embedded partial count = %#v", got.ResetApplicableCount)
	}
}

func TestQuotaRefreshKeepsEmbeddedResetInfoPartialForMalformedDedicatedResponses(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{"available_count":`},
		{name: "null count", body: `{"available_count":null}`},
		{name: "wrong typed count", body: `{"available_count":"two"}`},
		{name: "null credit list", body: `{"credits":null}`},
		{name: "wrong typed credit list", body: `{"credits":{}}`},
		{name: "malformed credit item", body: `{"credits":[null]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newQuotaHost(now)
			h.responses[quotaResetCreditsURL] = host.HTTPResponse{StatusCode: 200, Body: []byte(tt.body)}
			s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})

			got, err := s.Refresh(context.Background(), testAccount("acct-one", "auth-one"))
			if err != nil {
				t.Fatal(err)
			}
			if got.ResetInfoComplete {
				t.Fatal("malformed dedicated reset info was marked complete")
			}
			if got.ResetApplicableCount == nil || *got.ResetApplicableCount != 2 {
				t.Fatalf("embedded partial count = %#v, want 2", got.ResetApplicableCount)
			}
		})
	}
}

func TestQuotaEvaluateKeepsLoadedHoldWhenClearingUpdateFails(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	account := testAccount("acct-one", "auth-one")
	cfg := domain.DefaultConfig()
	repo := &failingUpdateRepository{
		state: domain.RuntimeState{
			GuardrailHolds: map[string]domain.GuardrailHold{
				account.Key: {
					AccountKey:    account.Key,
					EstablishedAt: now.Add(-time.Hour),
					FloorPercent:  cfg.LongWindowFloorPercent,
				},
			},
		},
		updateErr: errors.New("persisted hold update failed"),
	}
	s := New(newQuotaHost(now), repo, &quotaClock{now: now})
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}

	if got := s.Evaluate(cfg, account.Key, now); got != domain.DecisionGuardrailHold {
		t.Fatalf("decision = %q, want %q after failed hold clearing update", got, domain.DecisionGuardrailHold)
	}
	if repo.updateCall != 1 {
		t.Fatalf("repository Update calls = %d, want one", repo.updateCall)
	}
	if _, ok := repo.state.GuardrailHolds[account.Key]; !ok {
		t.Fatal("loaded persisted hold disappeared after failed update")
	}
}

func TestQuotaClearSnapshotsRemovesMemoryOnlyEntries(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	h := newQuotaHost(now)
	s := New(h, store.NewRuntimeStateRepository(t.TempDir()), &quotaClock{now: now})
	account := testAccount("acct-one", "auth-one")
	if _, err := s.Refresh(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	s.ClearSnapshots()
	if _, ok := s.Get(account.Key, now); ok {
		t.Fatal("snapshot remained after ClearSnapshots")
	}
	state, err := store.NewRuntimeStateRepository(t.TempDir()).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.GuardrailHolds) != 0 {
		t.Fatalf("unrelated state fixture = %#v", state.GuardrailHolds)
	}
}
