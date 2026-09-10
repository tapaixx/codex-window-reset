package app

import (
	"testing"
	"time"

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
