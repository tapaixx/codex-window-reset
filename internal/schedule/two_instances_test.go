package schedule

import (
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// A host that hot-loads a new plugin build without unloading the previous one
// leaves two schedulers alive over one data directory. Each holds its own
// in-process lock, so the occurrence claim in handleOccurrence is not atomic
// between them and both can flip the same slot from planned to running.
//
// Observed on a live deployment: one batch produced four records for three
// accounts, and the duplicated account's two records carried decision values
// from two different plugin versions.
func TestSupersededInstanceNeverExecutesTheSameOccurrence(t *testing.T) {
	now := schedulerInstant("2026-09-15T06:30:00Z")
	occurrence := domain.PlannedOccurrence{
		ID: "2026-09-15/p2/a", AccountKey: "a", LocalDate: "2026-09-15", PeriodIndex: 2,
		PlannedAt: now.Add(time.Hour), WindowStart: now.Add(30 * time.Minute), WindowEnd: now.Add(2 * time.Hour),
	}
	shared := &schedulerTestStateRepository{state: cloneSchedulerState(domain.RuntimeState{
		Occurrences: map[string]domain.OccurrenceState{occurrence.ID: {PlannedOccurrence: occurrence, Status: domain.OccurrencePlanned}},
		NextRuns:    map[string]time.Time{occurrence.ID: occurrence.PlannedAt},
	})}

	old := newSchedulerFixture(t, now, domain.RuntimeState{})
	old.states = shared
	old.scheduler = NewScheduler(old.clock, old.planner, shared, old.executor)
	old.planner.plans["2026-09-15"] = []domain.PlannedOccurrence{occurrence}
	old.scheduler.Start()
	defer old.scheduler.Stop()
	if err := old.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}

	// The replacement build starts against the same directory and claims
	// ownership, exactly as a hot-loaded .so would.
	fresh := newSchedulerFixture(t, now, domain.RuntimeState{})
	fresh.clock = old.clock
	fresh.scheduler = NewScheduler(old.clock, fresh.planner, shared, fresh.executor)
	fresh.planner.plans["2026-09-15"] = []domain.PlannedOccurrence{occurrence}
	fresh.scheduler.Start()
	defer fresh.scheduler.Stop()
	if err := fresh.scheduler.Reconcile(schedulerTestConfig()); err != nil {
		t.Fatal(err)
	}

	old.clock.Advance(time.Hour)
	select {
	case <-fresh.executor.entered:
	case <-time.After(2 * time.Second):
		t.Fatalf("the current instance did not execute: old=%d fresh=%d", len(old.executor.Calls()), len(fresh.executor.Calls()))
	}
	deadline := time.After(time.Second)
	for shared.saved().Occurrences[occurrence.ID].Status == domain.OccurrenceRunning {
		select {
		case <-deadline:
			t.Fatal("occurrence never reached a terminal state")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if got := len(old.executor.Calls()); got != 0 {
		t.Fatalf("superseded instance executed %d times: %#v", got, old.executor.Calls())
	}
	if got := len(fresh.executor.Calls()); got != 1 {
		t.Fatalf("current instance executed %d times, want 1", got)
	}
}

// State written before this field existed has no owner, and the single
// instance reading it must keep working rather than falling silent.
func TestStateWithoutAnOwnerStillExecutes(t *testing.T) {
	now := schedulerInstant("2026-09-15T06:30:00Z")
	scheduler := &Scheduler{}
	if !scheduler.ownsScheduling(domain.RuntimeState{}) {
		t.Fatal("state without an owner must not block execution")
	}
	scheduler.ownerID = "mine"
	if !scheduler.ownsScheduling(domain.RuntimeState{SchedulerOwner: "mine"}) {
		t.Fatal("the owning instance must execute")
	}
	if scheduler.ownsScheduling(domain.RuntimeState{SchedulerOwner: "someone-else"}) {
		t.Fatal("a superseded instance must not execute")
	}
	_ = now
}
