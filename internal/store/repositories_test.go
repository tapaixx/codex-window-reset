package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

func (fixedClock) AfterFunc(time.Duration, func()) domain.Timer { return noopTimer{} }

type noopTimer struct{}

func (noopTimer) Stop() bool { return true }

func instant(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestConfigLoadCreatesDefaultOnlyWhenMissing(t *testing.T) {
	dir := t.TempDir()
	repo := NewConfigRepository(dir)

	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, domain.DefaultConfig()) {
		t.Fatalf("default config = %#v, want %#v", got, domain.DefaultConfig())
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("default config was not persisted: %v", err)
	}

	got.Enabled = true
	got.Revision = 8
	got.ScheduledAccountKeys = []string{"acct-a"}
	if err := repo.Save(got); err != nil {
		t.Fatal(err)
	}
	reloaded, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded, got) {
		t.Fatalf("reloaded config = %#v, want %#v", reloaded, got)
	}
}

func TestConfigCorruptionIsReportedNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := NewConfigRepository(dir)
	_, err := repo.Load()
	var corrupt *domain.Error
	if !errors.As(err, &corrupt) || corrupt.Code != domain.CodeStoreCorrupt {
		t.Fatalf("got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{" {
		t.Fatalf("corrupt source was overwritten: %q", got)
	}
}

func TestConfigUnsupportedSchemaIsReportedNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := []byte(`{"schema_version":2,"revision":4}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewConfigRepository(dir).Load()
	var corrupt *domain.Error
	if !errors.As(err, &corrupt) || corrupt.Code != domain.CodeStoreCorrupt {
		t.Fatalf("got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("unsupported-schema source was overwritten: %q", got)
	}
}

func TestHistoryAppendKeepsNewestConfiguredLimit(t *testing.T) {
	dir := t.TempDir()
	repo := NewHistoryRepository(dir, 3)
	for index := 1; index <= 5; index++ {
		if err := repo.Append(domain.OperationRecord{ID: string(rune('0' + index))}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("history length = %d, want 3", len(got))
	}
	for index, want := range []string{"3", "4", "5"} {
		if got[index].ID != want {
			t.Fatalf("history[%d].ID = %q, want %q", index, got[index].ID, want)
		}
	}
}

func TestHistoryPersistsDomainJSONTimeNormalization(t *testing.T) {
	dir := t.TempDir()
	repo := NewHistoryRepository(dir, 100)
	location := time.FixedZone("test+8", 8*60*60)
	want := time.Date(2026, 1, 2, 3, 4, 5, 123, location)
	if err := repo.Append(domain.OperationRecord{ID: "op-1", StartedAt: want, FinishedAt: want}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].StartedAt.Equal(want) || got[0].StartedAt.Location() != time.UTC {
		t.Fatalf("persisted StartedAt = %#v, want UTC representation of %v", got, want)
	}
}

func TestHistoryClearOnlyClearsHistoryDocument(t *testing.T) {
	dir := t.TempDir()
	history := NewHistoryRepository(dir, 100)
	if err := history.Append(domain.OperationRecord{ID: "op-1"}); err != nil {
		t.Fatal(err)
	}
	if err := history.Clear(); err != nil {
		t.Fatal(err)
	}
	got, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("history after clear = %#v", got)
	}
}

func TestRuntimeStateUpdateIsAtomicAcrossConcurrentMutations(t *testing.T) {
	dir := t.TempDir()
	repo := NewRuntimeStateRepository(dir)
	const updates = 16
	var wait sync.WaitGroup
	wait.Add(updates)
	for index := 0; index < updates; index++ {
		index := index
		go func() {
			defer wait.Done()
			if err := repo.Update(func(state *domain.RuntimeState) error {
				key := string(rune('a' + index))
				state.Occurrences[key] = domain.OccurrenceState{PlannedOccurrence: domain.PlannedOccurrence{ID: key}}
				time.Sleep(time.Millisecond)
				return nil
			}); err != nil {
				t.Errorf("update %d: %v", index, err)
			}
		}()
	}
	wait.Wait()

	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Occurrences) != updates {
		t.Fatalf("occurrence count = %d, want %d: %#v", len(got.Occurrences), updates, got.Occurrences)
	}
}

func TestRuntimeStateUpdateErrorDoesNotPersistMutation(t *testing.T) {
	dir := t.TempDir()
	repo := NewRuntimeStateRepository(dir)
	initial := domain.RuntimeState{SchemaVersion: 1, Occurrences: map[string]domain.OccurrenceState{
		"existing": {PlannedOccurrence: domain.PlannedOccurrence{ID: "existing"}},
	}}
	if err := repo.Save(initial); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("stop update")
	if err := repo.Update(func(state *domain.RuntimeState) error {
		state.Occurrences["discarded"] = domain.OccurrenceState{}
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("update error = %v, want %v", err, wantErr)
	}

	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Occurrences["discarded"]; ok {
		t.Fatalf("failed update was persisted: %#v", got.Occurrences)
	}
}

func TestRuntimeStateSaveIsCompleteReplacement(t *testing.T) {
	dir := t.TempDir()
	repo := NewRuntimeStateRepository(dir)
	first := domain.RuntimeState{SchemaVersion: 1, Occurrences: map[string]domain.OccurrenceState{
		"old": {PlannedOccurrence: domain.PlannedOccurrence{ID: "old"}},
	}}
	second := domain.RuntimeState{SchemaVersion: 1, Occurrences: map[string]domain.OccurrenceState{
		"new": {PlannedOccurrence: domain.PlannedOccurrence{ID: "new"}},
	}}
	if err := repo.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(second); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Occurrences, second.Occurrences) {
		t.Fatalf("replacement occurrences = %#v, want %#v", got.Occurrences, second.Occurrences)
	}
}

func TestRuntimeStateUnsupportedSchemaIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"occurrences":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewRuntimeStateRepository(dir).Load()
	var corrupt *domain.Error
	if !errors.As(err, &corrupt) || corrupt.Code != domain.CodeStoreCorrupt {
		t.Fatalf("got %v", err)
	}
}

func TestResetAuditRetentionKeepsExactCutoffAndRecoversOnlyExplicitly(t *testing.T) {
	dir := t.TempDir()
	now := instant("2026-09-09T12:00:00Z")
	clock := fixedClock{now: now}
	newRepo := func() *ResetAuditRepository {
		return NewResetAuditRepository(dir, 365*24*time.Hour, clock)
	}
	old := domain.ResetAudit{IdempotencyKey: "old", RequestedAt: instant("2025-09-09T11:59:59Z"), Outcome: domain.ResetSucceeded}
	cutoff := domain.ResetAudit{IdempotencyKey: "cutoff", RequestedAt: instant("2025-09-09T12:00:00Z"), Outcome: domain.ResetPending}
	current := domain.ResetAudit{IdempotencyKey: "current", RequestedAt: now, Outcome: domain.ResetPending}
	if err := newRepo().AppendPending(old); err != nil {
		t.Fatal(err)
	}
	if err := newRepo().AppendPending(cutoff); err != nil {
		t.Fatal(err)
	}
	if err := newRepo().AppendPending(current); err != nil {
		t.Fatal(err)
	}

	reconstructed := newRepo()
	loaded, err := reconstructed.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("retained audits = %#v, want cutoff and current", loaded)
	}
	if loaded[0].IdempotencyKey != "cutoff" || loaded[1].IdempotencyKey != "current" {
		t.Fatalf("retained audit order/content = %#v", loaded)
	}
	if loaded[0].Outcome != domain.ResetPending {
		t.Fatalf("ordinary Load recovered pending record: %#v", loaded[0])
	}

	if err := reconstructed.RecoverPending(now); err != nil {
		t.Fatal(err)
	}
	recovered, err := newRepo().Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range recovered {
		if record.Outcome != domain.ResetUnknown || !record.FinishedAt.Equal(now) || record.FinishedAt.Location() != time.UTC {
			t.Fatalf("pending record was not recovered at explicit startup step: %#v", record)
		}
	}
}

func TestResetAuditDuplicateKeyReturnsConflict(t *testing.T) {
	repo := NewResetAuditRepository(t.TempDir(), 365*24*time.Hour, fixedClock{now: instant("2026-09-09T12:00:00Z")})
	record := domain.ResetAudit{IdempotencyKey: "same", Outcome: domain.ResetPending}
	if err := repo.AppendPending(record); err != nil {
		t.Fatal(err)
	}
	err := repo.AppendPending(record)
	var conflict *domain.Error
	if !errors.As(err, &conflict) || conflict.Code != domain.CodeIdempotencyConflict || conflict.HTTPStatus != 409 {
		t.Fatalf("duplicate error = %#v", err)
	}
}

func TestResetAuditReplaceUpdatesExistingRecord(t *testing.T) {
	repo := NewResetAuditRepository(t.TempDir(), 365*24*time.Hour, fixedClock{now: instant("2026-09-09T12:00:00Z")})
	pending := domain.ResetAudit{IdempotencyKey: "replace-me", Outcome: domain.ResetPending}
	if err := repo.AppendPending(pending); err != nil {
		t.Fatal(err)
	}
	finished := pending
	finished.Outcome = domain.ResetSucceeded
	finished.FinishedAt = instant("2026-09-09T12:01:00Z")
	if err := repo.Replace(finished); err != nil {
		t.Fatal(err)
	}
	got, found, err := repo.FindByKey(pending.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(got, finished) {
		t.Fatalf("replaced audit = %#v (found=%v), want %#v", got, found, finished)
	}
}

func TestResetAuditDeleteBeforeUsesExclusiveCutoff(t *testing.T) {
	repo := NewResetAuditRepository(t.TempDir(), 10*365*24*time.Hour, fixedClock{now: instant("2026-09-09T12:00:00Z")})
	older := domain.ResetAudit{IdempotencyKey: "older", RequestedAt: instant("2025-01-01T00:00:00Z"), Outcome: domain.ResetSucceeded}
	equal := domain.ResetAudit{IdempotencyKey: "equal", RequestedAt: instant("2025-06-01T00:00:00Z"), Outcome: domain.ResetSucceeded}
	if err := repo.AppendPending(older); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendPending(equal); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteBefore(equal.RequestedAt); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].IdempotencyKey != "equal" {
		t.Fatalf("DeleteBefore result = %#v", got)
	}
}

func TestOrdinaryClearCannotDeleteAuditOrRuntimeState(t *testing.T) {
	dir := t.TempDir()
	history := NewHistoryRepository(dir, 100)
	states := NewRuntimeStateRepository(dir)
	audit := NewResetAuditRepository(dir, 365*24*time.Hour, fixedClock{now: instant("2026-09-09T12:00:00Z")})
	if err := history.Append(domain.OperationRecord{ID: "op-1"}); err != nil {
		t.Fatal(err)
	}
	state := domain.RuntimeState{SchemaVersion: 1, Occurrences: map[string]domain.OccurrenceState{
		"2026-09-09/p0/acct-a": {PlannedOccurrence: domain.PlannedOccurrence{ID: "2026-09-09/p0/acct-a"}, Status: domain.OccurrencePlanned},
	}}
	if err := states.Save(state); err != nil {
		t.Fatal(err)
	}
	record := domain.ResetAudit{IdempotencyKey: "11111111-1111-4111-8111-111111111111", AccountKey: "acct-a", Outcome: domain.ResetPending}
	if err := audit.AppendPending(record); err != nil {
		t.Fatal(err)
	}
	if err := history.Clear(); err != nil {
		t.Fatal(err)
	}
	gotHistory, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	gotState, err := states.Load()
	if err != nil {
		t.Fatal(err)
	}
	gotAudit, err := audit.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotHistory) != 0 || len(gotState.Occurrences) != 1 || len(gotAudit) != 1 {
		t.Fatalf("history=%#v state=%#v audit=%#v", gotHistory, gotState, gotAudit)
	}
}

func TestResetAuditClearOnlyClearsAuditDocument(t *testing.T) {
	dir := t.TempDir()
	repo := NewResetAuditRepository(dir, 365*24*time.Hour, fixedClock{now: instant("2026-09-09T12:00:00Z")})
	if err := repo.AppendPending(domain.ResetAudit{IdempotencyKey: "audit-1", Outcome: domain.ResetPending}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Clear(); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("audit after clear = %#v", got)
	}
}

func TestRepositoryDocumentsRejectMalformedHistoryRuntimeAndAudit(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		path string
		load func() error
	}{
		{name: "history", path: "history.json", load: func() error { _, err := NewHistoryRepository(dir, 100).Load(); return err }},
		{name: "runtime", path: "runtime-state.json", load: func() error { _, err := NewRuntimeStateRepository(dir).Load(); return err }},
		{name: "audit", path: "reset-audit.json", load: func() error {
			_, err := NewResetAuditRepository(dir, 365*24*time.Hour, fixedClock{now: instant("2026-09-09T12:00:00Z")}).Load()
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(dir, testCase.path)
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			var corrupt *domain.Error
			if err := testCase.load(); !errors.As(err, &corrupt) || corrupt.Code != domain.CodeStoreCorrupt {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestHistoryDocumentHasVersionAndRecordsEnvelope(t *testing.T) {
	dir := t.TempDir()
	if err := NewHistoryRepository(dir, 100).Append(domain.OperationRecord{ID: "op-1"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		SchemaVersion int                      `json:"schema_version"`
		Records       []domain.OperationRecord `json:"records"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || len(document.Records) != 1 || document.Records[0].ID != "op-1" {
		t.Fatalf("history document = %#v", document)
	}
}
