package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type task7Clock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *task7Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *task7Clock) AfterFunc(time.Duration, func()) domain.Timer { return task7Timer{} }

type task7Timer struct{}

func (task7Timer) Stop() bool { return true }

type task7AccountService struct {
	mu       sync.Mutex
	accounts map[string]accounts.Account
}

func (s *task7AccountService) List(context.Context) ([]accounts.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]accounts.Account, 0, len(s.accounts))
	for _, account := range s.accounts {
		result = append(result, account)
	}
	return result, nil
}

func (s *task7AccountService) Find(_ context.Context, key string) (accounts.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[normalizeKey(key)]
	if !ok {
		return accounts.Account{}, errors.New("missing account")
	}
	return account, nil
}

func (s *task7AccountService) Set(account accounts.Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account.Key = normalizeKey(account.Key)
	s.accounts[account.Key] = account
}

func (s *task7AccountService) Remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, normalizeKey(key))
}

type task7Probe struct {
	mu          sync.Mutex
	active      int
	max         int
	calls       int
	executed    []string
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	result      domain.ProbeResult
}

func (p *task7Probe) Execute(ctx context.Context, account accounts.Account, _ string, _ time.Duration) domain.ProbeResult {
	p.mu.Lock()
	p.active++
	p.calls++
	p.executed = append(p.executed, normalizeKey(account.Key))
	if p.active > p.max {
		p.max = p.active
	}
	entered := p.entered
	release := p.release
	p.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	result := p.result
	if result.Outcome == "" {
		result.Outcome = domain.RequestSucceeded
	}
	return result
}

func (p *task7Probe) MaxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max
}

func (p *task7Probe) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *task7Probe) ExecutedKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.executed...)
}

func (p *task7Probe) ReleaseAll() { p.releaseOnce.Do(func() { close(p.release) }) }

type task7Quota struct {
	mu             sync.Mutex
	snapshots      map[string]domain.UsageSnapshot
	errors         map[string]error
	refreshActive  int
	refreshMax     int
	refreshCalls   int
	getCalls       int
	refreshEntered chan struct{}
	refreshRelease chan struct{}
	decision       domain.QuotaDecision
}

func (q *task7Quota) Refresh(ctx context.Context, account accounts.Account) (domain.UsageSnapshot, error) {
	key := normalizeKey(account.Key)
	q.mu.Lock()
	q.refreshActive++
	q.refreshCalls++
	if q.refreshActive > q.refreshMax {
		q.refreshMax = q.refreshActive
	}
	entered, release := q.refreshEntered, q.refreshRelease
	snapshot := q.snapshots[key]
	err := q.errors[key]
	q.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	q.mu.Lock()
	q.refreshActive--
	q.mu.Unlock()
	if err != nil {
		return snapshot, err
	}
	if snapshot.AccountKey == "" {
		snapshot.AccountKey = key
	}
	return snapshot, nil
}

func (q *task7Quota) Get(key string, now time.Time) (domain.SnapshotView, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.getCalls++
	snapshot, ok := q.snapshots[normalizeKey(key)]
	if !ok {
		return domain.SnapshotView{}, false
	}
	return domain.SnapshotView{Snapshot: snapshot, LastAttemptAt: now.UTC()}, true
}

func (q *task7Quota) Reset(context.Context, accounts.Account, string) (domain.ResetHTTPResult, error) {
	return domain.ResetHTTPResult{}, nil
}

func (q *task7Quota) MaxConcurrent() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.refreshMax
}

func (q *task7Quota) Calls() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.refreshCalls
}

type task7ConfigRepository struct {
	mu     sync.Mutex
	config domain.Config
	err    error
}

func (r *task7ConfigRepository) Load() (domain.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneConfig(r.config), r.err
}

func (r *task7ConfigRepository) Save(config domain.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = cloneConfig(config)
	return nil
}

type task7HistoryRepository struct {
	mu      sync.Mutex
	records []domain.OperationRecord
	err     error
}

func (r *task7HistoryRepository) Load() ([]domain.OperationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.OperationRecord(nil), r.records...), r.err
}

func (r *task7HistoryRepository) Append(record domain.OperationRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.records = append(r.records, record)
	return nil
}

func (r *task7HistoryRepository) Clear() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = nil
	return nil
}

func (r *task7HistoryRepository) Records() []domain.OperationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.OperationRecord(nil), r.records...)
}

type task7StateRepository struct {
	mu    sync.Mutex
	state domain.RuntimeState
	err   error
}

func (r *task7StateRepository) Load() (domain.RuntimeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneRuntimeState(r.state), r.err
}

func (r *task7StateRepository) Save(state domain.RuntimeState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.state = cloneRuntimeState(state)
	return nil
}

func (r *task7StateRepository) Update(mutate func(*domain.RuntimeState) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	state := cloneRuntimeState(r.state)
	if err := mutate(&state); err != nil {
		return err
	}
	r.state = state
	return nil
}

func (r *task7StateRepository) Occurrence(id string) (domain.OccurrenceState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.state.Occurrences[id]
	return entry, ok
}

func cloneRuntimeState(state domain.RuntimeState) domain.RuntimeState {
	copy := state
	copy.Occurrences = make(map[string]domain.OccurrenceState, len(state.Occurrences))
	for key, value := range state.Occurrences {
		copy.Occurrences[key] = value
	}
	copy.NextRuns = make(map[string]time.Time, len(state.NextRuns))
	for key, value := range state.NextRuns {
		copy.NextRuns[key] = value
	}
	copy.GuardrailHolds = make(map[string]domain.GuardrailHold, len(state.GuardrailHolds))
	for key, value := range state.GuardrailHolds {
		copy.GuardrailHolds[key] = value
	}
	return copy
}

type task7Fixture struct {
	runtime  *Runtime
	accounts *task7AccountService
	probes   *task7Probe
	quota    *task7Quota
	history  *task7HistoryRepository
	state    *task7StateRepository
}

func newTask7Fixture(t *testing.T, values ...accounts.Account) task7Fixture {
	t.Helper()
	byKey := make(map[string]accounts.Account, len(values))
	for _, account := range values {
		account.Key = normalizeKey(account.Key)
		byKey[account.Key] = account
	}
	accountService := &task7AccountService{accounts: byKey}
	clock := &task7Clock{now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
	config := domain.DefaultConfig()
	config.ProbeModel = "test-model"
	config.ProbeTimeoutSeconds = 5
	config.ScheduledAccountKeys = make([]string, 0, len(values))
	for _, account := range values {
		config.ScheduledAccountKeys = append(config.ScheduledAccountKeys, account.Key)
	}
	config.Enabled = len(values) > 0
	lead, span := 120, 60
	config.PreheatLeadMinutes, config.PreheatSpanMinutes = &lead, &span
	quota := &task7Quota{snapshots: make(map[string]domain.UsageSnapshot), errors: make(map[string]error)}
	probes := &task7Probe{entered: make(chan struct{}, 32), release: make(chan struct{})}
	history := &task7HistoryRepository{}
	state := &task7StateRepository{state: domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{}, NextRuns: map[string]time.Time{}, GuardrailHolds: map[string]domain.GuardrailHold{}}}
	runtime, err := New(Dependencies{
		Accounts: accountService,
		Probe:    probes,
		Quota:    quota,
		Config:   &task7ConfigRepository{config: config},
		History:  history,
		State:    state,
		Clock:    clock,
		IDs: func() string {
			return "task7-id-" + time.Now().Format("150405.000000000")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task7Fixture{runtime: runtime, accounts: accountService, probes: probes, quota: quota, history: history, state: state}
}

func task7Accounts(count int) []accounts.Account {
	result := make([]accounts.Account, 0, count)
	for index := 0; index < count; index++ {
		result = append(result, accounts.Account{Key: "acct-" + string(rune('a'+index)), MaskedIdentity: "a***@example.com"})
	}
	return result
}

func waitTask7Run(t *testing.T, run *runState) {
	t.Helper()
	select {
	case <-run.done:
	case <-time.After(5 * time.Second):
		t.Fatal("manual run did not finish")
	}
}

func TestManualProbeLimitsConcurrencyToThree(t *testing.T) {
	accountsList := task7Accounts(5)
	fx := newTask7Fixture(t, accountsList...)
	defer fx.runtime.Stop()
	keys := make([]string, 0, len(accountsList))
	for _, account := range accountsList {
		keys = append(keys, account.Key)
	}
	runID, err := fx.runtime.StartManualProbes(context.Background(), keys, true)
	if err != nil || runID == "" {
		t.Fatalf("%q %v", runID, err)
	}
	for index := 0; index < 3; index++ {
		select {
		case <-fx.probes.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("three probe workers did not start")
		}
	}
	if got := fx.probes.MaxConcurrent(); got != 3 {
		t.Fatalf("max=%d", got)
	}
	fx.probes.ReleaseAll()
	fx.runtime.mu.RLock()
	run := fx.runtime.run
	fx.runtime.mu.RUnlock()
	waitTask7Run(t, run)
}

func TestManualProbeAllowsUnavailableOverrideButRejectsDisabled(t *testing.T) {
	fx := newTask7Fixture(t,
		accounts.Account{Key: "a", Unavailable: true},
		accounts.Account{Key: "b", Disabled: true},
	)
	defer fx.runtime.Stop()
	if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"a"}, false); domain.CodeOf(err) != domain.CodeAccountUnavailable {
		t.Fatalf("got %v", err)
	}
	runID, err := fx.runtime.StartManualProbes(context.Background(), []string{"a"}, true)
	if err != nil || runID == "" {
		t.Fatal(err)
	}
	fx.probes.ReleaseAll()
	fx.runtime.mu.RLock()
	run := fx.runtime.run
	fx.runtime.mu.RUnlock()
	waitTask7Run(t, run)
	if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"b"}, true); domain.CodeOf(err) != domain.CodeAccountDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestManualProbeRejectsOverlappingBulkRuns(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"a"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"a"}, true); domain.CodeOf(err) != domain.CodeRunInProgress {
		t.Fatalf("got %v", err)
	}
	fx.probes.ReleaseAll()
	fx.runtime.mu.RLock()
	run := fx.runtime.run
	fx.runtime.mu.RUnlock()
	waitTask7Run(t, run)
}

func TestManualProbeReportsAccountBusyForConcurrentOperation(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	occurrence := domain.PlannedOccurrence{ID: "occ-a", AccountKey: "a"}
	firstDone := make(chan domain.OperationRecord, 1)
	go func() { firstDone <- fx.runtime.ExecutePreheat(context.Background(), occurrence) }()
	select {
	case <-fx.probes.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first operation did not reach probe")
	}
	second := fx.runtime.ExecutePreheat(context.Background(), occurrence)
	if second.ErrorCode != domain.CodeAccountBusy {
		t.Fatalf("second record = %#v", second)
	}
	if _, err := fx.runtime.StartManualProbes(context.Background(), []string{"a"}, true); domain.CodeOf(err) != domain.CodeAccountBusy {
		t.Fatalf("manual operation while busy: %v", err)
	}
	fx.probes.ReleaseAll()
	<-firstDone
	records := fx.history.Records()
	if len(records) != 2 {
		t.Fatalf("history records = %d, want 2", len(records))
	}
}

func TestRefreshQuotasUsesThreeWorkersAndPreservesOrder(t *testing.T) {
	accountsList := task7Accounts(5)
	fx := newTask7Fixture(t, accountsList...)
	defer fx.runtime.Stop()
	for index, account := range accountsList {
		fx.quota.snapshots[account.Key] = domain.UsageSnapshot{AccountKey: account.Key, CapturedAt: time.Unix(int64(index+1), 0).UTC()}
	}
	keys := []string{"acct-e", "acct-a", "acct-c", "acct-b", "acct-d"}
	views, err := fx.runtime.RefreshQuotas(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != len(keys) {
		t.Fatalf("views = %d, want %d", len(views), len(keys))
	}
	for index, key := range keys {
		if got := views[index].Snapshot.AccountKey; got != key {
			t.Fatalf("view[%d] key=%q, want %q", index, got, key)
		}
	}
	if got := fx.quota.MaxConcurrent(); got > 3 {
		t.Fatalf("quota max=%d", got)
	}
	if fx.probes.Calls() != 0 {
		t.Fatalf("quota refresh unexpectedly probed: %d", fx.probes.Calls())
	}
}

func TestRefreshQuotasRejectsEmptyAndDuplicateKeys(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	for _, keys := range [][]string{nil, {}, {"a", "a"}, {""}} {
		if _, err := fx.runtime.RefreshQuotas(context.Background(), keys); domain.CodeOf(err) != domain.CodeConfigInvalid {
			t.Fatalf("keys=%v err=%v", keys, err)
		}
	}
}

func TestRefreshQuotasKeepsPriorSnapshotWhenRefreshFails(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"}, accounts.Account{Key: "b"})
	defer fx.runtime.Stop()
	prior := domain.UsageSnapshot{AccountKey: "a", CapturedAt: time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC)}
	fx.quota.snapshots["a"] = prior
	fx.quota.errors["b"] = errors.New("upstream")
	views, err := fx.runtime.RefreshQuotas(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !views[0].Snapshot.CapturedAt.Equal(prior.CapturedAt) {
		t.Fatalf("prior snapshot lost: %#v", views[0].Snapshot)
	}
	if views[1].RefreshErrorCode != domain.CodeQuotaRefreshFailed {
		t.Fatalf("failed view = %#v", views[1])
	}
}

func TestRefreshQuotasKeepsPriorSnapshotWhenAccountLookupFails(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	prior := domain.UsageSnapshot{
		AccountKey: "a",
		CapturedAt: time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC),
		Windows: []domain.UsageWindow{{
			DurationMinutes:  300,
			RemainingPercent: 45,
			ResetAt:          time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC),
			Short:            true,
		}},
	}
	fx.quota.snapshots["a"] = prior
	fx.accounts.Remove("a")

	views, err := fx.runtime.RefreshQuotas(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	if !reflect.DeepEqual(views[0].Snapshot, prior) {
		t.Fatalf("prior snapshot lost: %#v, want %#v", views[0].Snapshot, prior)
	}
	if views[0].RefreshErrorCode != domain.CodeQuotaRefreshFailed {
		t.Fatalf("failed view = %#v", views[0])
	}
}

func TestRefreshQuotasStopWaitsForHostIO(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	fx.quota.refreshEntered = make(chan struct{}, 1)
	fx.quota.refreshRelease = make(chan struct{})

	refreshDone := make(chan struct{})
	go func() {
		_, _ = fx.runtime.RefreshQuotas(context.Background(), []string{"a"})
		close(refreshDone)
	}()
	select {
	case <-fx.quota.refreshEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("quota refresh did not reach host I/O")
	}

	stopDone := make(chan struct{})
	go func() {
		fx.runtime.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("Runtime.Stop returned while quota host I/O was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(fx.quota.refreshRelease)
	select {
	case <-refreshDone:
	case <-time.After(5 * time.Second):
		t.Fatal("quota refresh did not finish after host release")
	}
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Runtime.Stop did not finish after quota host I/O completed")
	}
}

func TestManualProbeRechecksQueuedAccountEligibility(t *testing.T) {
	tests := []struct {
		name      string
		allow     bool
		mutated   func(accounts.Account) accounts.Account
		errorCode domain.ErrorCode
	}{
		{
			name: "disabled",
			mutated: func(account accounts.Account) accounts.Account {
				account.Disabled = true
				return account
			},
			errorCode: domain.CodeAccountDisabled,
		},
		{
			name: "unavailable without override",
			mutated: func(account accounts.Account) accounts.Account {
				account.Unavailable = true
				return account
			},
			errorCode: domain.CodeAccountUnavailable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accountsList := task7Accounts(5)
			fx := newTask7Fixture(t, accountsList...)
			defer fx.runtime.Stop()
			keys := make([]string, 0, len(accountsList))
			for _, account := range accountsList {
				keys = append(keys, account.Key)
			}
			runID, err := fx.runtime.StartManualProbes(context.Background(), keys, tc.allow)
			if err != nil || runID == "" {
				t.Fatalf("%q %v", runID, err)
			}
			for index := 0; index < 3; index++ {
				select {
				case <-fx.probes.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("three probe workers did not start")
				}
			}

			queued := tc.mutated(accountsList[3])
			fx.accounts.Set(queued)
			fx.runtime.mu.RLock()
			run := fx.runtime.run
			fx.runtime.mu.RUnlock()
			fx.probes.ReleaseAll()
			waitTask7Run(t, run)

			for _, key := range fx.probes.ExecutedKeys() {
				if key == queued.Key {
					t.Fatalf("queued %s account was probed after becoming %s", queued.Key, tc.errorCode)
				}
			}
		})
	}
}
