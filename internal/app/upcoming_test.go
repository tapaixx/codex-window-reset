package app

import (
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/domain"
)

func upcomingOccurrence(id, key, date string, period int, windowStart, plannedAt time.Time, status domain.OccurrenceStatus) domain.OccurrenceState {
	return domain.OccurrenceState{
		PlannedOccurrence: domain.PlannedOccurrence{
			ID: id, AccountKey: key, LocalDate: date, PeriodIndex: period,
			WindowStart: windowStart, WindowEnd: windowStart.Add(time.Hour), PlannedAt: plannedAt,
		},
		Status: status,
	}
}

func TestUpcomingGroupsArmedOccurrencesByWindowInOrder(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"}, accounts.Account{Key: "b"})
	defer fx.runtime.Stop()
	window := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	later := time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC)
	state := domain.RuntimeState{
		Occurrences: map[string]domain.OccurrenceState{
			"2026-09-09/p1/b": upcomingOccurrence("2026-09-09/p1/b", "b", "2026-09-09", 1, window, window.Add(40*time.Minute), domain.OccurrencePlanned),
			"2026-09-09/p1/a": upcomingOccurrence("2026-09-09/p1/a", "a", "2026-09-09", 1, window, window.Add(20*time.Minute), domain.OccurrencePlanned),
			"2026-09-10/p0/a": upcomingOccurrence("2026-09-10/p0/a", "a", "2026-09-10", 0, later, later.Add(30*time.Minute), domain.OccurrencePlanned),
		},
		NextRuns: map[string]time.Time{
			"2026-09-09/p1/b": window.Add(40 * time.Minute),
			"2026-09-09/p1/a": window.Add(20 * time.Minute),
			"2026-09-10/p0/a": later.Add(30 * time.Minute),
		},
	}
	if err := fx.state.Save(state); err != nil {
		t.Fatal(err)
	}
	view := fx.runtime.Upcoming()
	if len(view.Batches) != 2 {
		t.Fatalf("batches=%#v, want 2", view.Batches)
	}
	first := view.Batches[0]
	if first.LocalDate != "2026-09-09" || first.PeriodIndex != 1 || !first.WindowStart.Equal(window) {
		t.Fatalf("first batch=%#v", first)
	}
	if len(first.Occurrences) != 2 || first.Occurrences[0].AccountKey != "a" || first.Occurrences[1].AccountKey != "b" {
		t.Fatalf("stagger order lost: %#v", first.Occurrences)
	}
	if got := view.Batches[1].LocalDate; got != "2026-09-10" {
		t.Fatalf("second batch date=%s", got)
	}
	if !view.NextRunAt.Equal(window.Add(20 * time.Minute)) {
		t.Fatalf("next run=%s, want %s", view.NextRunAt, window.Add(20*time.Minute))
	}
	if !view.Enabled {
		t.Fatal("enabled schedule reported as disabled")
	}
}

// An occurrence without a next run has no timer and will not fire. Reporting
// it as upcoming would have hidden the restart defect that left a whole
// horizon persisted but unarmed.
func TestUpcomingOmitsUnarmedAndNonPlannedOccurrences(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	window := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	state := domain.RuntimeState{
		Occurrences: map[string]domain.OccurrenceState{
			"2026-09-09/p1/a": upcomingOccurrence("2026-09-09/p1/a", "a", "2026-09-09", 1, window, window.Add(20*time.Minute), domain.OccurrencePlanned),
			"2026-09-10/p0/a": upcomingOccurrence("2026-09-10/p0/a", "a", "2026-09-10", 0, window.Add(24*time.Hour), window.Add(25*time.Hour), domain.OccurrenceMissed),
			"2026-09-11/p0/a": upcomingOccurrence("2026-09-11/p0/a", "a", "2026-09-11", 0, window.Add(48*time.Hour), window.Add(49*time.Hour), domain.OccurrencePlanned),
		},
		// The missed occurrence keeps a compensation-style entry; the third is
		// planned but unarmed.
		NextRuns: map[string]time.Time{
			"2026-09-09/p1/a": window.Add(20 * time.Minute),
			"2026-09-10/p0/a": window.Add(25 * time.Hour),
		},
	}
	if err := fx.state.Save(state); err != nil {
		t.Fatal(err)
	}
	view := fx.runtime.Upcoming()
	if len(view.Batches) != 1 || len(view.Batches[0].Occurrences) != 1 {
		t.Fatalf("batches=%#v, want only the armed planned occurrence", view.Batches)
	}
	if got := view.Batches[0].Occurrences[0].AccountKey; got != "a" || !view.Batches[0].WindowStart.Equal(window) {
		t.Fatalf("wrong occurrence surfaced: %#v", view.Batches[0])
	}
}

func TestUpcomingMarksGuardrailHoldWithoutPredictingQuota(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"}, accounts.Account{Key: "b"})
	defer fx.runtime.Stop()
	window := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	state := domain.RuntimeState{
		Occurrences: map[string]domain.OccurrenceState{
			"2026-09-09/p1/a": upcomingOccurrence("2026-09-09/p1/a", "a", "2026-09-09", 1, window, window.Add(20*time.Minute), domain.OccurrencePlanned),
			"2026-09-09/p1/b": upcomingOccurrence("2026-09-09/p1/b", "b", "2026-09-09", 1, window, window.Add(40*time.Minute), domain.OccurrencePlanned),
		},
		NextRuns: map[string]time.Time{
			"2026-09-09/p1/a": window.Add(20 * time.Minute),
			"2026-09-09/p1/b": window.Add(40 * time.Minute),
		},
		GuardrailHolds: map[string]domain.GuardrailHold{"b": {}},
	}
	if err := fx.state.Save(state); err != nil {
		t.Fatal(err)
	}
	view := fx.runtime.Upcoming()
	got := view.Batches[0].Occurrences
	if got[0].BlockedReason != "" {
		t.Fatalf("unheld account carries a reason: %#v", got[0])
	}
	if got[1].BlockedReason != domain.BlockedGuardrailHold {
		t.Fatalf("held account missing reason: %#v", got[1])
	}
}

// "Nothing is scheduled" must be distinguishable from "the schedule is off"
// and from "state could not be read": each one needs a different response from
// the Operator, and a single empty list cannot carry that.
func TestUpcomingSeparatesEmptyFromDisabledAndUnreadable(t *testing.T) {
	fx := newTask7Fixture(t, accounts.Account{Key: "a"})
	defer fx.runtime.Stop()
	view := fx.runtime.Upcoming()
	if !view.Enabled || len(view.Batches) != 0 || !view.NextRunAt.IsZero() || view.StoreErrorCode != "" {
		t.Fatalf("enabled-but-empty view=%#v", view)
	}

	disabled := newTask7Fixture(t)
	defer disabled.runtime.Stop()
	if got := disabled.runtime.Upcoming(); got.Enabled || len(got.Batches) != 0 {
		t.Fatalf("disabled view=%#v", got)
	}

	fx.state.err = &domain.Error{Code: domain.CodeStoreCorrupt, Message: "state is corrupt"}
	broken := fx.runtime.Upcoming()
	if broken.StoreErrorCode != domain.CodeStoreCorrupt {
		t.Fatalf("unreadable state reported as empty: %#v", broken)
	}
	if len(broken.Batches) != 0 {
		t.Fatalf("unreadable state invented batches: %#v", broken.Batches)
	}
}
