package simulate

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/schedule"
)

func TestSimulationExposesDistinctStrategyCoverageForTheTimeline(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.ScheduledAccountKeys = []string{"acct-a"}
	result, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Baseline struct {
			Timeline []domain.TimelineSegment `json:"timeline_segments"`
		} `json:"baseline"`
		Scheduled struct {
			Timeline []domain.TimelineSegment `json:"timeline_segments"`
		} `json:"scheduled"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		segments  []domain.TimelineSegment
		starts    []string
		available int
	}{
		{"A", wire.Baseline.Timeline, []string{"09:00", "14:00"}, 120},
		{"B", wire.Scheduled.Timeline, []string{"09:00", "11:30", "13:30", "16:30"}, 180},
	} {
		var starts []string
		available, limited, lunch := 0, 0, 0
		location, _ := time.LoadLocation("Asia/Shanghai")
		for _, segment := range tc.segments {
			minutes := int(segment.End.Sub(segment.Start) / time.Minute)
			if minutes <= 0 {
				t.Fatalf("%s has non-positive segment: %#v", tc.name, segment)
			}
			switch segment.Kind {
			case "available":
				starts = append(starts, segment.Start.In(location).Format("15:04"))
				available += minutes
			case "limited":
				limited += minutes
			case "break":
				lunch += minutes
			}
		}
		if !reflect.DeepEqual(starts, tc.starts) || available != tc.available || limited != 510-tc.available || lunch != 90 {
			t.Fatalf("%s timeline: starts=%v available=%d limited=%d lunch=%d", tc.name, starts, available, limited, lunch)
		}
	}
}

func TestSimulationIncludesThirdPreheatWhenWorkEndsAt1800(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.WorkPeriods[1].End = "18:00"
	cfg.ScheduledAccountKeys = []string{"acct-a"}
	result, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, occurrence := range result.PreheatWindows {
		got = append(got, occurrence.PlannedAt.Format("15:04"))
	}
	if want := []string{"06:30", "11:30", "16:30"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("overview preheats = %v, want %v", got, want)
	}
}

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

func TestSimulationCarriesOneWindowBudgetAcrossLunch(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.ScheduledAccountKeys = []string{"acct-a"}
	cfg.WorkPeriods[1] = domain.LocalPeriod{Start: "13:30", End: "14:00"}
	date := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.FixedZone("operator", 8*60*60))

	got, err := (Service{}).Run(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if got.Baseline.AvailableCoverageMinutes != 60 {
		t.Fatalf("baseline coverage = %d, want 60; lunch cannot renew quota", got.Baseline.AvailableCoverageMinutes)
	}
	if got.Baseline.IdleWindowMinutes != 90 {
		t.Fatalf("baseline idle = %d, want 90 minutes outside work in the 09:00–14:00 window", got.Baseline.IdleWindowMinutes)
	}
	if got.Scheduled.AvailableCoverageMinutes != 120 {
		t.Fatalf("scheduled coverage = %d, want 120", got.Scheduled.AvailableCoverageMinutes)
	}
	if got.NetGainMinutes != 60 {
		t.Fatalf("net gain = %d, want 60", got.NetGainMinutes)
	}
}

func TestSimulationAdjacentWorkPeriodsDoNotMintExtraQuota(t *testing.T) {
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
	if got.Baseline.AvailableCoverageMinutes != 60 {
		t.Fatalf("baseline coverage = %d, want 60 across adjacent work periods", got.Baseline.AvailableCoverageMinutes)
	}
	if got.Baseline.IdleWindowMinutes != 225 {
		t.Fatalf("baseline idle = %d, want 225 outside work in a 5-hour window", got.Baseline.IdleWindowMinutes)
	}
	if got.Scheduled.AvailableCoverageMinutes != 60 {
		t.Fatalf("scheduled coverage = %d, want 60", got.Scheduled.AvailableCoverageMinutes)
	}
	if got.NetGainMinutes != 0 {
		t.Fatalf("net gain = %d, want zero for equal coverage", got.NetGainMinutes)
	}
}

func TestSimulationRenewsFromActualPreheatAndCarriesLunchRemainder(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.ScheduledAccountKeys = []string{"acct-a"}
	result, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Baseline.AvailableCoverageMinutes != 120 || result.Scheduled.AvailableCoverageMinutes != 180 || result.NetGainMinutes != 60 {
		t.Errorf("expected A=120 B=180 gain=60, got A=%d B=%d gain=%d", result.Baseline.AvailableCoverageMinutes, result.Scheduled.AvailableCoverageMinutes, result.NetGainMinutes)
	}
	for _, tc := range []struct{ clock, a, b string }{
		{"09:30", "available", "available"}, {"10:30", "limited", "limited"},
		{"11:30", "limited", "available"}, {"12:00", "break", "break"},
		{"13:30", "limited", "available"}, {"14:00", "available", "limited"},
		{"16:30", "limited", "available"}, {"17:30", "limited", "limited"},
	} {
		at, _ := time.Parse(time.RFC3339, "2026-09-14T"+tc.clock+":00+08:00")
		for _, strategy := range []struct {
			name, want string
			segments   []domain.TimelineSegment
		}{{"A", tc.a, result.Baseline.TimelineSegments}, {"B", tc.b, result.Scheduled.TimelineSegments}} {
			kind := "missing"
			for _, segment := range strategy.segments {
				if !at.Before(segment.Start) && at.Before(segment.End) {
					kind = segment.Kind
					break
				}
			}
			if kind != strategy.want {
				t.Errorf("%s at %s: got %s, want %s", strategy.name, tc.clock, kind, strategy.want)
			}
		}
	}
}

func TestSimulationWindowCycleControlsRenewalEvenWithoutPreheat(t *testing.T) {
	for _, tc := range []struct{ hours, want int }{{1, 510}, {2, 300}, {5, 120}, {24, 60}} {
		cfg := task4SimulationConfig(t)
		cfg.WindowHours = tc.hours
		cfg.ScheduledAccountKeys = nil
		got, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if got.Baseline.AvailableCoverageMinutes != tc.want {
			t.Errorf("%dh cycle: got %d want %d", tc.hours, got.Baseline.AvailableCoverageMinutes, tc.want)
		}
		if !reflect.DeepEqual(got.Baseline, got.Scheduled) {
			t.Errorf("no preheat must fall back to ordinary first use, %dh", tc.hours)
		}
	}
}

func TestSimulationStaggeredAccountsNeverPoolTheirBudgets(t *testing.T) {
	cfg := task4SimulationConfig(t)
	got, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// Three accounts start at 06:10, 06:30, 06:50. Show the earliest single
	// account, not the union (which would falsely increase available minutes).
	if got.Scheduled.AvailableCoverageMinutes != 180 {
		t.Fatalf("pooled/incorrect coverage: %d, want 180", got.Scheduled.AvailableCoverageMinutes)
	}
	var starts []string
	location, _ := time.LoadLocation(cfg.Timezone)
	for _, s := range got.Scheduled.TimelineSegments {
		if s.Kind == "available" {
			starts = append(starts, s.Start.In(location).Format("15:04"))
		}
	}
	if !reflect.DeepEqual(starts, []string{"09:00", "11:10", "13:30", "16:10"}) {
		t.Fatalf("earliest-account phases: %v", starts)
	}
}

func TestSimulationClipsCyclesAtMidnightAndRejectsInvalidCycle(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.ScheduledAccountKeys = nil
	cfg.WorkPeriods = []domain.LocalPeriod{{Start: "23:00", End: "23:59"}}
	got, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.Baseline.AvailableCoverageMinutes != 59 || got.Baseline.IdleWindowMinutes != 1 {
		t.Fatalf("midnight clipping: %#v", got.Baseline)
	}
	task4AssertTimelineIsNonOverlappingAndWithinDay(t, got.Baseline.TimelineSegments, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	cfg.WindowHours = 0
	if _, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("invalid zero window cycle accepted")
	}
}

func TestSimulationSkippedPreheatsFallBackAndRestDaysStayEmpty(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.BlackoutPeriods = []domain.LocalPeriod{{Start: "00:00", End: "23:59"}}
	got, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Baseline, got.Scheduled) {
		t.Fatal("missed preheats must not manufacture a different window phase")
	}
	got, err = (Service{}).Run(cfg, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkMinutes != 0 || got.Baseline.AvailableCoverageMinutes != 0 || got.Scheduled.AvailableCoverageMinutes != 0 || got.NetGainMinutes != 0 {
		t.Fatal("rest day has nonzero coverage")
	}
}

func TestSimulationBudgetCannotExceedWorkingTimeOrRenewTwiceWithinACycle(t *testing.T) {
	cfg := task4SimulationConfig(t)
	cfg.ScheduledAccountKeys = nil
	for _, tc := range []struct{ hours, budget, want int }{{1, 120, 510}, {24, 600, 510}, {24, 1, 1}, {5, 90, 180}} {
		cfg.WindowHours = tc.hours
		cfg.ProductivityMinutes = tc.budget
		got, err := (Service{}).Run(cfg, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if got.Baseline.AvailableCoverageMinutes != tc.want {
			t.Errorf("%dh/%dm: got %d want %d", tc.hours, tc.budget, got.Baseline.AvailableCoverageMinutes, tc.want)
		}
		var total int
		for _, s := range got.Baseline.TimelineSegments {
			if s.Kind == "available" {
				total += int(s.End.Sub(s.Start) / time.Minute)
			}
		}
		if total != got.Baseline.AvailableCoverageMinutes {
			t.Fatal("metrics and chart use different budgets")
		}
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
