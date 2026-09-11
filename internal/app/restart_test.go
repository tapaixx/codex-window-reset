package app

import (
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

func TestRuntimeStartMarksPersistedPreheatMissed(t *testing.T) {
	fx := newTask7Fixture(t)
	defer fx.runtime.Stop()
	occurrence := domain.OccurrenceState{
		PlannedOccurrence: domain.PlannedOccurrence{ID: "2026-09-09/p0/a", AccountKey: "a", PlannedAt: time.Date(2026, 9, 9, 6, 45, 0, 0, time.UTC)},
		Status:            domain.OccurrencePlanned,
	}
	if err := fx.state.Save(domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{occurrence.ID: occurrence}}); err != nil {
		t.Fatal(err)
	}
	fx.runtime.Start()
	got, ok := fx.state.Occurrence(occurrence.ID)
	if !ok || got.Status != domain.OccurrenceMissed {
		t.Fatalf("state=%#v, found=%t", got, ok)
	}
	if got := fx.probes.Calls(); got != 0 {
		t.Fatalf("probe calls=%d, want 0", got)
	}
}

func TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule(t *testing.T) {
	now := time.Date(2026, 9, 9, 6, 30, 0, 0, time.UTC)
	id := "2026-09-09/p0/a"
	state := &task7StateRepository{state: domain.RuntimeState{Occurrences: map[string]domain.OccurrenceState{
		id: {
			PlannedOccurrence: domain.PlannedOccurrence{ID: id, AccountKey: "a", PlannedAt: now.Add(time.Hour)},
			Status:            domain.OccurrencePlanned,
		},
	}}}
	configErr := &domain.Error{Code: domain.CodeStoreCorrupt, Message: "config is corrupt"}
	runtime, err := New(Dependencies{
		Accounts: &task7AccountService{accounts: map[string]accounts.Account{"a": {Key: "a"}}},
		Probe:    &task7Probe{entered: make(chan struct{}, 1), release: make(chan struct{})},
		Quota:    &task7Quota{snapshots: make(map[string]domain.UsageSnapshot), errors: make(map[string]error)},
		Config:   &task7ConfigRepository{config: domain.DefaultConfig(), err: configErr},
		History:  &task7HistoryRepository{},
		State:    state,
		Clock:    &task7Clock{now: now},
	})
	if err != nil {
		t.Fatalf("New returned error for corrupt config: %v", err)
	}
	defer runtime.Stop()

	runtime.Start()
	got, ok := state.Occurrence(id)
	if !ok || got.Status != domain.OccurrenceMissed {
		t.Fatalf("state=%#v, found=%t", got, ok)
	}
	status := runtime.Status()
	if status.StoreErrorCode != domain.CodeStoreCorrupt {
		t.Fatalf("store error=%s, want %s", status.StoreErrorCode, domain.CodeStoreCorrupt)
	}
	if status.Enabled {
		t.Fatal("corrupt config activated scheduling")
	}
}

func TestRuntimeStatusCountsPersistentGuardrailHolds(t *testing.T) {
	fx := newTask7Fixture(t)
	defer fx.runtime.Stop()
	if err := fx.state.Save(domain.RuntimeState{GuardrailHolds: map[string]domain.GuardrailHold{
		"a": {AccountKey: "a"},
		"b": {AccountKey: "b"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := fx.runtime.Status().GuardrailHoldCount; got != 2 {
		t.Fatalf("guardrail hold count = %d, want 2", got)
	}
}
