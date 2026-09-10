package app

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/store"
)

const (
	resetKeyA = "11111111-1111-4111-8111-111111111111"
	resetKeyB = "22222222-2222-4222-8222-222222222222"
)

func TestResetPersistsPendingBeforeConsumeAndReplaysOutcome(t *testing.T) {
	fx := newResetFixture(t)
	fx.quota.OnReset = func() {
		records, err := fx.audit.Load()
		if err != nil || len(records) != 1 || records[0].Outcome != domain.ResetPending {
			t.Fatalf("audit before consume: %#v %v", records, err)
		}
	}

	first, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
	if err != nil || first.Outcome != domain.ResetSucceeded {
		t.Fatalf("first reset = %#v, err=%v", first, err)
	}
	second, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
	if err != nil || second.Outcome != domain.ResetSucceeded || fx.quota.ResetCalls() != 1 {
		t.Fatalf("replay = %#v, calls=%d, err=%v", second, fx.quota.ResetCalls(), err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay changed durable result: first=%#v second=%#v", first, second)
	}
}

func TestResetRejectsIdempotencyReuseForDifferentAccount(t *testing.T) {
	fx := newResetFixture(t)
	if _, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA); err != nil {
		t.Fatal(err)
	}

	_, err := fx.runtime.ResetQuota(context.Background(), "acct-b", resetKeyA)
	if domain.CodeOf(err) != domain.CodeIdempotencyConflict {
		t.Fatalf("error code = %q, want %q (%v)", domain.CodeOf(err), domain.CodeIdempotencyConflict, err)
	}
	if got := fx.quota.ResetCalls(); got != 1 {
		t.Fatalf("reset calls = %d, want 1", got)
	}
}

func TestResetRejectsInvalidCanonicalUUID(t *testing.T) {
	fx := newResetFixture(t)
	for _, key := range []string{
		"",
		"11111111-1111-6111-8111-111111111111",
		"11111111-1111-4111-7111-111111111111",
		"11111111111141118111111111111111",
		"11111111-1111-4111-8111-11111111111Z",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := fx.runtime.ResetQuota(context.Background(), "acct-a", key)
			if domain.CodeOf(err) != domain.CodeConfigInvalid {
				t.Fatalf("error code = %q, want %q (%v)", domain.CodeOf(err), domain.CodeConfigInvalid, err)
			}
		})
	}
	if got := fx.quota.ResetCalls(); got != 0 {
		t.Fatalf("reset calls = %d, want 0", got)
	}
}

func TestResetRequiresCompletePositiveApplicableCredits(t *testing.T) {
	cases := []struct {
		name     string
		complete bool
		credits  int
	}{
		{name: "incomplete", complete: false, credits: 2},
		{name: "zero", complete: true, credits: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newResetFixture(t)
			fx.quota.SetSnapshot(resetSnapshot("acct-a", tc.complete, tc.credits))
			if _, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA); err == nil {
				t.Fatal("reset without applicable credit succeeded")
			}
			if got := fx.quota.ResetCalls(); got != 0 {
				t.Fatalf("reset calls = %d, want 0", got)
			}
		})
	}
}

func TestResetRejectsDisabledAccount(t *testing.T) {
	fx := newResetFixture(t)
	fx.accounts.Set(accounts.Account{Key: "acct-a", MaskedIdentity: "a***@example.com", Disabled: true})

	if _, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA); domain.CodeOf(err) != domain.CodeAccountDisabled {
		t.Fatalf("error code = %q, want %q (%v)", domain.CodeOf(err), domain.CodeAccountDisabled, err)
	}
	if got := fx.quota.ResetCalls(); got != 0 {
		t.Fatalf("reset calls = %d, want 0", got)
	}
}

func TestResetReturnsAccountBusyForDistinctSimultaneousKey(t *testing.T) {
	fx := newResetFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	fx.quota.BeforeConsume = func(context.Context) error {
		close(entered)
		<-release
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first reset did not reach consume")
	}

	_, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyB)
	if domain.CodeOf(err) != domain.CodeAccountBusy {
		t.Fatalf("error code = %q, want %q (%v)", domain.CodeOf(err), domain.CodeAccountBusy, err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestResetSameKeyWaitsForCurrentFlightAndReloadsFinalRecord(t *testing.T) {
	fx := newResetFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	fx.quota.BeforeConsume = func(context.Context) error {
		close(entered)
		<-release
		return nil
	}

	firstDone := make(chan struct {
		audit domain.ResetAudit
		err   error
	}, 1)
	go func() {
		audit, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
		firstDone <- struct {
			audit domain.ResetAudit
			err   error
		}{audit: audit, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first reset did not reach consume")
	}

	secondDone := make(chan struct {
		audit domain.ResetAudit
		err   error
	}, 1)
	go func() {
		audit, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
		secondDone <- struct {
			audit domain.ResetAudit
			err   error
		}{audit: audit, err: err}
	}()
	select {
	case <-secondDone:
		t.Fatal("same-key replay returned before current flight completed")
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	first := <-firstDone
	second := <-secondDone
	if first.err != nil || second.err != nil {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if !reflect.DeepEqual(first.audit, second.audit) {
		t.Fatalf("same-key results differ: first=%#v second=%#v", first.audit, second.audit)
	}
	if got := fx.quota.ResetCalls(); got != 1 {
		t.Fatalf("reset calls = %d, want 1", got)
	}
}

func TestResetRefreshesAfterSuccessfulAndDefiniteFailedConsume(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		callErr    error
		outcome    domain.ResetOutcome
	}{
		{name: "success", statusCode: 200, outcome: domain.ResetSucceeded},
		{name: "http failure", statusCode: 503, outcome: domain.ResetFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newResetFixture(t)
			fx.quota.resetResult = domain.ResetHTTPResult{StatusCode: tc.statusCode, Category: "upstream_error"}
			fx.quota.resetErr = tc.callErr

			audit, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
			if err != nil {
				t.Fatalf("reset error = %v", err)
			}
			if audit.Outcome != tc.outcome {
				t.Fatalf("outcome = %s, want %s", audit.Outcome, tc.outcome)
			}
			if got := fx.quota.RefreshCalls(); got != 2 {
				t.Fatalf("refresh calls = %d, want pre and post refresh", got)
			}
		})
	}
}

func TestResetClassifiesAmbiguousConsumeAsUnknown(t *testing.T) {
	fx := newResetFixture(t)
	fx.quota.resetResult = domain.ResetHTTPResult{}
	fx.quota.resetErr = errors.New("connection dropped")

	audit, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
	if err == nil || domain.CodeOf(err) != domain.CodeResetOutcomeUnknown {
		t.Fatalf("error = %v, want %q", err, domain.CodeResetOutcomeUnknown)
	}
	if audit.Outcome != domain.ResetUnknown {
		t.Fatalf("outcome = %s, want %s", audit.Outcome, domain.ResetUnknown)
	}
	if got := fx.quota.RefreshCalls(); got != 2 {
		t.Fatalf("refresh calls = %d, want 2", got)
	}
}

func TestResetFinalAuditWriteFailureReturnsUnknownAndKeepsPendingEvidence(t *testing.T) {
	fx := newResetFixture(t)
	fx.audit.replaceErr = errors.New("final audit write failed")

	audit, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
	if domain.CodeOf(err) != domain.CodeResetOutcomeUnknown || audit.Outcome != domain.ResetUnknown {
		t.Fatalf("audit=%#v err=%v", audit, err)
	}
	records, loadErr := fx.audit.Load()
	if loadErr != nil || len(records) != 1 || records[0].Outcome != domain.ResetPending {
		t.Fatalf("pending evidence = %#v, err=%v", records, loadErr)
	}
	if got := fx.quota.ResetCalls(); got != 1 {
		t.Fatalf("reset calls = %d, want 1", got)
	}
}

func TestResetRecoversPendingOnStartupWithoutConsuming(t *testing.T) {
	fx := newResetFixture(t)
	pending := domain.ResetAudit{
		IdempotencyKey: resetKeyA,
		RequestedAt:    fx.clock.Now(),
		AccountKey:     "acct-a",
		MaskedIdentity: "a***@example.com",
		Outcome:        domain.ResetPending,
		CorrelationID:  "corr-pending",
	}
	if err := fx.audit.AppendPending(pending); err != nil {
		t.Fatal(err)
	}
	fx.runtime.Stop()

	reconstructed := newResetRuntime(t, fx)
	defer reconstructed.Stop()
	reconstructed.Start()

	recovered, found, err := fx.audit.FindByKey(resetKeyA)
	if err != nil || !found {
		t.Fatalf("recovered record = %#v, found=%t, err=%v", recovered, found, err)
	}
	if recovered.Outcome != domain.ResetUnknown {
		t.Fatalf("recovered outcome = %s, want %s", recovered.Outcome, domain.ResetUnknown)
	}
	if got := fx.quota.ResetCalls(); got != 0 {
		t.Fatalf("reset calls = %d, want 0", got)
	}
	replayed, err := reconstructed.ResetQuota(context.Background(), "acct-a", resetKeyA)
	if err != nil || replayed.Outcome != domain.ResetUnknown {
		t.Fatalf("replay = %#v, err=%v", replayed, err)
	}
	if got := fx.quota.ResetCalls(); got != 0 {
		t.Fatalf("replay reset calls = %d, want 0", got)
	}
}

func TestResetProcessStopBeforeConsumeIsRecoveredWithoutRetry(t *testing.T) {
	fx := newResetFixture(t)
	fx.quota.resetResult = domain.ResetHTTPResult{}
	entered := make(chan struct{})
	stopBeforeConsume := make(chan struct{})
	fx.quota.BeforeConsume = func(context.Context) error {
		close(entered)
		<-stopBeforeConsume
		return context.Canceled
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA)
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reset did not stop between append and consume")
	}
	fx.runtime.Stop()

	reconstructed := newResetRuntime(t, fx)
	reconstructed.Start()
	close(stopBeforeConsume)
	if err := <-firstDone; err == nil || domain.CodeOf(err) != domain.CodeResetOutcomeUnknown {
		t.Fatalf("stopped reset error = %v", err)
	}
	defer reconstructed.Stop()

	recovered, err := reconstructed.ResetQuota(context.Background(), "acct-a", resetKeyA)
	if err != nil || recovered.Outcome != domain.ResetUnknown {
		t.Fatalf("replay = %#v, err=%v", recovered, err)
	}
	if got := fx.quota.ResetCalls(); got != 0 {
		t.Fatalf("reset calls = %d, want 0", got)
	}
}

func TestResetAuditDoesNotPersistSecrets(t *testing.T) {
	fx := newResetFixture(t)
	fx.quota.resetResult = domain.ResetHTTPResult{StatusCode: 502, Category: "upstream_error"}
	if _, err := fx.runtime.ResetQuota(context.Background(), "acct-a", resetKeyA); err != nil {
		t.Fatal(err)
	}
	data, err := fx.audit.Raw()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"access_token", "fixture-token", "raw-upstream-body", "management-key"} {
		if string(data) == "" || contains(data, secret) {
			t.Fatalf("audit persisted %q: %s", secret, data)
		}
	}
}

func TestClearResetAuditRequiresExactPhrase(t *testing.T) {
	fx := newResetFixture(t)
	for _, phrase := range []string{"", "delete audit", "DELETE AUDIT ", " DELETE AUDIT"} {
		if err := fx.runtime.ClearResetAudit(phrase); err == nil {
			t.Fatalf("accepted %q", phrase)
		}
	}
	if err := fx.runtime.ClearResetAudit("DELETE AUDIT"); err != nil {
		t.Fatal(err)
	}
}

func contains(data []byte, value string) bool {
	for index := 0; index+len(value) <= len(data); index++ {
		if string(data[index:index+len(value)]) == value {
			return true
		}
	}
	return false
}

type resetFixture struct {
	runtime  *Runtime
	accounts *task7AccountService
	quota    *resetQuotaFixture
	audit    *resetAuditFixture
	clock    *task7Clock
}

type resetQuotaFixture struct {
	mu            sync.Mutex
	snapshots     map[string]domain.UsageSnapshot
	refreshCalls  int
	resetCalls    int
	resetResult   domain.ResetHTTPResult
	resetErr      error
	OnReset       func()
	BeforeConsume func(context.Context) error
}

func (q *resetQuotaFixture) Refresh(_ context.Context, account accounts.Account) (domain.UsageSnapshot, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.refreshCalls++
	snapshot := q.snapshots[normalizeKey(account.Key)]
	if snapshot.AccountKey == "" {
		snapshot.AccountKey = normalizeKey(account.Key)
	}
	return snapshot, nil
}

func (q *resetQuotaFixture) Get(key string, now time.Time) (domain.SnapshotView, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	snapshot, ok := q.snapshots[normalizeKey(key)]
	if !ok {
		return domain.SnapshotView{}, false
	}
	return domain.SnapshotView{Snapshot: snapshot, LastAttemptAt: now.UTC()}, true
}

func (q *resetQuotaFixture) Reset(ctx context.Context, _ accounts.Account, _ string) (domain.ResetHTTPResult, error) {
	q.mu.Lock()
	onReset := q.OnReset
	beforeConsume := q.BeforeConsume
	result := q.resetResult
	callErr := q.resetErr
	q.mu.Unlock()
	if onReset != nil {
		onReset()
	}
	if beforeConsume != nil {
		if err := beforeConsume(ctx); err != nil {
			return result, err
		}
	}
	q.mu.Lock()
	q.resetCalls++
	q.mu.Unlock()
	return result, callErr
}

func (q *resetQuotaFixture) SetSnapshot(snapshot domain.UsageSnapshot) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.snapshots[normalizeKey(snapshot.AccountKey)] = snapshot
}

func (q *resetQuotaFixture) ResetCalls() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.resetCalls
}

func (q *resetQuotaFixture) RefreshCalls() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.refreshCalls
}

type resetAuditFixture struct {
	*store.ResetAuditRepository
	path       string
	replaceErr error
}

func (r *resetAuditFixture) Replace(audit domain.ResetAudit) error {
	if r.replaceErr != nil {
		return r.replaceErr
	}
	return r.ResetAuditRepository.Replace(audit)
}

func (r *resetAuditFixture) Raw() ([]byte, error) {
	return readFile(r.path)
}

func newResetFixture(t *testing.T) resetFixture {
	t.Helper()
	clock := &task7Clock{now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
	accountsService := &task7AccountService{accounts: map[string]accounts.Account{
		"acct-a": {Key: "acct-a", MaskedIdentity: "a***@example.com", AuthIndex: "fixture-a"},
		"acct-b": {Key: "acct-b", MaskedIdentity: "b***@example.com", AuthIndex: "fixture-b"},
	}}
	quota := &resetQuotaFixture{
		snapshots: map[string]domain.UsageSnapshot{
			"acct-a": resetSnapshot("acct-a", true, 2),
			"acct-b": resetSnapshot("acct-b", true, 2),
		},
		resetResult: domain.ResetHTTPResult{StatusCode: 200, Category: "success"},
	}
	dir := t.TempDir()
	repository := store.NewResetAuditRepository(dir, 365*24*time.Hour, clock)
	audit := &resetAuditFixture{ResetAuditRepository: repository, path: dir + "/reset-audit.json"}
	runtime := newResetRuntimeWith(t, clock, accountsService, quota, audit)
	return resetFixture{runtime: runtime, accounts: accountsService, quota: quota, audit: audit, clock: clock}
}

func newResetRuntime(t *testing.T, fx resetFixture) *Runtime {
	t.Helper()
	return newResetRuntimeWith(t, fx.clock, fx.accounts, fx.quota, fx.audit)
}

func newResetRuntimeWith(t *testing.T, clock *task7Clock, accountService *task7AccountService, quota QuotaService, audit ResetAuditRepository) *Runtime {
	t.Helper()
	config := domain.DefaultConfig()
	config.Enabled = false
	state := &task7StateRepository{state: domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{}, NextRuns: map[string]time.Time{}, GuardrailHolds: map[string]domain.GuardrailHold{}}}
	runtime, err := New(Dependencies{
		Accounts: accountService,
		Probe:    &task7Probe{},
		Quota:    quota,
		Config:   &task7ConfigRepository{config: config},
		History:  &task7HistoryRepository{},
		State:    state,
		Audit:    audit,
		Clock:    clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func resetSnapshot(key string, complete bool, credits int) domain.UsageSnapshot {
	return domain.UsageSnapshot{
		AccountKey:           key,
		CapturedAt:           time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		ResetInfoComplete:    complete,
		ResetApplicableCount: intPointer(credits),
	}
}

func intPointer(value int) *int { return &value }

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
