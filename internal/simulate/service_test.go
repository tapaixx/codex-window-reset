package simulate

import (
	"reflect"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/schedule"
)

func task4SimulationConfig(t *testing.T) domain.Config {
	t.Helper()
	lead, span := 120, 60
	cfg := domain.DefaultConfig()
	cfg.Enabled = true
	cfg.Timezone = "Asia/Shanghai"
	cfg.Weekdays = []int{1}
	cfg.WorkPeriods = []domain.LocalPeriod{
		{Start: "09:00", End: "12:00"},
		{Start: "13:30", End: "19:00"},
	}
	cfg.PreheatLeadMinutes = &lead
	cfg.PreheatSpanMinutes = &span
	cfg.ProductivityMinutes = 60
	cfg.ScheduledAccountKeys = []string{"acct-a", "acct-b", "acct-c"}
	return cfg
}

func TestSimulationUsesPlannerInstantsAndProductivityAssumption(t *testing.T) {
	cfg := task4SimulationConfig(t)
	date := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.FixedZone("operator", 8*60*60))

	wantOccurrences, err := schedule.PlanDay(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (Service{}).Run(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.PreheatWindows, wantOccurrences) {
		t.Fatalf("simulator timeline diverged from planner:\n got %#v\nwant %#v", got.PreheatWindows, wantOccurrences)
	}
	if got.Assumptions["productivity_minutes"] != cfg.ProductivityMinutes {
		t.Fatalf("productivity assumption = %#v, want %d", got.Assumptions, cfg.ProductivityMinutes)
	}
	if got.WorkMinutes != 510 {
		t.Fatalf("work minutes = %d, want 510", got.WorkMinutes)
	}
	if got.NetGainMinutes != got.Scheduled.AvailableCoverageMinutes-got.Baseline.AvailableCoverageMinutes {
		t.Fatalf("net gain = %d, does not equal scheduled-baseline coverage", got.NetGainMinutes)
	}
	for _, occurrence := range wantOccurrences {
		if occurrence.Missed {
			continue
		}
		found := false
		for _, segment := range got.TimelineSegments {
			if segment.Kind == "preheat" && segment.AccountKey == occurrence.AccountKey && segment.Start.Equal(occurrence.PlannedAt) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no preheat segment starts at %s for %s", occurrence.PlannedAt, occurrence.ID)
		}
	}
	task4AssertTimelineIsNonOverlappingAndWithinDay(t, got.TimelineSegments, date)
}

func TestSimulationComparesBaselineForEachWorkPeriod(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.WorkPeriods[1] = domain.LocalPeriod{Start: "13:30", End: "14:00"}
	date := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.FixedZone("operator", 8*60*60))

	got, err := (Service{}).Run(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if got.Baseline.AvailableCoverageMinutes != 90 {
		t.Fatalf("baseline coverage = %d, want 90 across both work periods", got.Baseline.AvailableCoverageMinutes)
	}
	if got.Baseline.IdleWindowMinutes != 30 {
		t.Fatalf("baseline idle = %d, want 30 across both work periods", got.Baseline.IdleWindowMinutes)
	}
	if got.Scheduled.AvailableCoverageMinutes != 60 {
		t.Fatalf("scheduled coverage = %d, want 60", got.Scheduled.AvailableCoverageMinutes)
	}
	if got.NetGainMinutes != -30 {
		t.Fatalf("net gain = %d, want -30 after comparing both work periods", got.NetGainMinutes)
	}
}

func TestSimulationPreservesBaselinesForAdjacentWorkPeriods(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.WorkPeriods = []domain.LocalPeriod{
		{Start: "09:00", End: "09:30"},
		{Start: "09:30", End: "10:15"},
	}
	cfg.PreheatLeadMinutes = intPointer(15)
	cfg.PreheatSpanMinutes = intPointer(60)
	cfg.ScheduledAccountKeys = []string{"acct-a"}
	date := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.FixedZone("operator", 8*60*60))

	got, err := (Service{}).Run(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if got.Baseline.AvailableCoverageMinutes != 75 {
		t.Fatalf("baseline coverage = %d, want 75 across adjacent work periods", got.Baseline.AvailableCoverageMinutes)
	}
	if got.Baseline.IdleWindowMinutes != 15 {
		t.Fatalf("baseline idle = %d, want 15 across adjacent work periods", got.Baseline.IdleWindowMinutes)
	}
	if got.Scheduled.AvailableCoverageMinutes != 60 {
		t.Fatalf("scheduled coverage = %d, want 60", got.Scheduled.AvailableCoverageMinutes)
	}
	if got.NetGainMinutes != -15 {
		t.Fatalf("net gain = %d, want -15 after comparing adjacent work periods", got.NetGainMinutes)
	}
}

func TestSimulationIgnoresSnapshotLikeFixtureChanges(t *testing.T) {
	cfg := task4SimulationConfig(t)
	date := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.UTC)
	snapshots := []domain.UsageSnapshot{{AccountKey: "acct-a", CapturedAt: date, Windows: []domain.UsageWindow{{DurationMinutes: 300, RemainingPercent: 80}}}}

	first, err := (Service{}).Run(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	// Usage snapshots are deliberately not part of Service.Run's input. Change
	// an external fixture between runs; the result must remain input-stable.
	snapshots[0].Windows[0].RemainingPercent = 1
	snapshots[0].Windows[0].ResetAt = date.Add(24 * time.Hour)
	second, err := (Service{}).Run(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("simulation changed without a config/date input change:\nfirst %#v\nsecond %#v", first, second)
	}
}

func task4AssertTimelineIsNonOverlappingAndWithinDay(t *testing.T, segments []domain.TimelineSegment, date time.Time) {
	t.Helper()
	if len(segments) == 0 {
		t.Fatal("simulation returned no timeline segments")
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location)
	dayEnd := dayStart.AddDate(0, 0, 1)
	for index, segment := range segments {
		if segment.Start.Before(dayStart) || segment.End.After(dayEnd) || segment.End.Before(segment.Start) {
			t.Fatalf("segment %d outside 24-hour day or inverted: %#v", index, segment)
		}
		if index > 0 && segment.Start.Before(segments[index-1].End) {
			t.Fatalf("segments overlap at %d: %#v then %#v", index, segments[index-1], segment)
		}
	}
}

func intPointer(value int) *int {
	return &value
}
