package schedule

import (
	"reflect"
	"testing"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

func task4ValidConfig(timezone, workStart, workEnd string, lead, span int, accounts []string) domain.Config {
	cfg := domain.DefaultConfig()
	cfg.Enabled = true
	cfg.Timezone = timezone
	cfg.Weekdays = []int{1, 7}
	cfg.WorkPeriods = []domain.LocalPeriod{{Start: workStart, End: workEnd}}
	cfg.ScheduledAccountKeys = append([]string(nil), accounts...)
	cfg.PreheatLeadMinutes = &lead
	cfg.PreheatSpanMinutes = &span
	return cfg
}

func task4LocalDate(t *testing.T, timezone, value string) time.Time {
	t.Helper()
	location, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestPlanDayUsesPreheatFormulaAndDeterministicSlots(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "12:00", 120, 60, []string{"acct-b", "acct-a"})
	date := task4LocalDate(t, cfg.Timezone, "2026-09-14")

	first, err := PlanDay(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (Planner{}).PlanDay(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic plan: %#v %#v", first, second)
	}
	if len(first) != 2 {
		t.Fatalf("planned %d occurrences, want 2: %#v", len(first), first)
	}

	wantIDs := []string{
		"2026-09-14/p0/acct-b",
		"2026-09-14/p0/acct-a",
	}
	wantTimes := []string{"06:15:00", "06:45:00"}
	for index, occurrence := range first {
		if occurrence.ID != wantIDs[index] {
			t.Errorf("occurrence %d ID = %q, want %q", index, occurrence.ID, wantIDs[index])
		}
		if got := occurrence.PlannedAt.In(date.Location()).Format("15:04:05"); got != wantTimes[index] {
			t.Errorf("occurrence %d planned at %s, want %s", index, got, wantTimes[index])
		}
		if got := occurrence.WindowStart.In(date.Location()).Format("15:04"); got != "06:00" {
			t.Errorf("occurrence %d window starts at %s, want 06:00", index, got)
		}
		if got := occurrence.WindowEnd.In(date.Location()).Format("15:04"); got != "07:00" {
			t.Errorf("occurrence %d window ends at %s, want 07:00", index, got)
		}
	}
	assertStrictlyIncreasingInstants(t, first)
}

func TestOccurrenceIDUsesLocalDatePeriodAndAccount(t *testing.T) {
	if got := OccurrenceID("2026-09-14", 3, "acct-a"); got != "2026-09-14/p3/acct-a" {
		t.Fatalf("occurrence ID = %q, want 2026-09-14/p3/acct-a", got)
	}
}

func TestPlanDayRepeatsPreheatAcrossWindowCycle(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "19:00", 30, 15, []string{"acct-a"})
	cfg.WindowHours = 5
	occurrences, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-09-14"))
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 3 {
		t.Fatalf("occurrences=%d, want 3: %#v", len(occurrences), occurrences)
	}
	want := []string{"08:22:30", "13:22:30", "18:22:30"}
	for index, occurrence := range occurrences {
		if got := occurrence.PlannedAt.In(task4LocalDate(t, cfg.Timezone, "2026-09-14").Location()).Format("15:04:05"); got != want[index] {
			t.Fatalf("occurrence %d=%s, want %s", index, got, want[index])
		}
	}
}

func TestPlannerNeverDuplicatesFallbackHour(t *testing.T) {
	cfg := task4ValidConfig("America/New_York", "03:30", "04:00", 30, 120, []string{"acct-a"})
	cfg.Weekdays = []int{7}
	occurrences, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-11-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 1 {
		t.Fatalf("planned %d occurrences, want 1: %#v", len(occurrences), occurrences)
	}
	if len(task4UniqueOccurrenceIDs(occurrences)) != len(occurrences) {
		t.Fatalf("duplicate local occurrence: %#v", occurrences)
	}
}

func TestPlannerAdvancesSpringForwardBoundaryIntoValidTime(t *testing.T) {
	cfg := task4ValidConfig("America/New_York", "05:00", "06:00", 60, 120, []string{"acct-a"})
	cfg.Weekdays = []int{7}
	occurrences, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-03-08"))
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 1 || occurrences[0].Missed {
		t.Fatalf("spring-forward boundary was missed: %#v", occurrences)
	}
	occurrence := occurrences[0]
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	if got := occurrence.WindowStart.In(location).Format("15:04"); got != "03:00" {
		t.Errorf("adjusted window starts at %s, want 03:00", got)
	}
	if got := occurrence.PlannedAt.In(location).Format("15:04"); got != "03:30" {
		t.Errorf("planned time = %s, want 03:30", got)
	}
}

func TestPlannerMarksEntirelyNonexistentAllowedIntervalMissed(t *testing.T) {
	cfg := task4ValidConfig("America/New_York", "04:00", "05:00", 60, 60, []string{"acct-a", "acct-b"})
	cfg.Weekdays = []int{7}
	occurrences, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-03-08"))
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 2 {
		t.Fatalf("planned %d occurrences, want 2: %#v", len(occurrences), occurrences)
	}
	for _, occurrence := range occurrences {
		if !occurrence.Missed || occurrence.MissedReason != "no_allowed_time" {
			t.Errorf("occurrence = %#v, want no_allowed_time miss", occurrence)
		}
	}
}

func TestPlannerConcatenatesAllowedIntervalsAroundBlackout(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "12:00", 60, 120, []string{"acct-a", "acct-b"})
	cfg.BlackoutPeriods = []domain.LocalPeriod{{Start: "06:30", End: "07:00"}}
	occurrences, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-09-14"))
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 2 {
		t.Fatalf("planned %d occurrences, want 2: %#v", len(occurrences), occurrences)
	}
	wantTimes := []string{"06:22:30", "07:37:30"}
	for index, occurrence := range occurrences {
		if occurrence.Missed {
			t.Fatalf("occurrence %d unexpectedly missed: %#v", index, occurrence)
		}
		if got := occurrence.PlannedAt.In(occurrence.PlannedAt.Location()).Format("15:04:05"); got != wantTimes[index] {
			t.Errorf("occurrence %d planned at %s, want %s", index, got, wantTimes[index])
		}
		if got := occurrence.PlannedAt.In(occurrence.PlannedAt.Location()).Format("15:04"); got >= "06:30" && got < "07:00" {
			t.Errorf("occurrence %d was placed in blackout: %s", index, got)
		}
	}
}

func TestPlannerMarksEveryAccountMissedWhenBlackoutUsesAllAllowedTime(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "12:00", 60, 120, []string{"acct-a", "acct-b"})
	cfg.BlackoutPeriods = []domain.LocalPeriod{{Start: "06:00", End: "08:00"}}
	occurrences, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-09-14"))
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != len(cfg.ScheduledAccountKeys) {
		t.Fatalf("planned %d occurrences, want one per account", len(occurrences))
	}
	for _, occurrence := range occurrences {
		if !occurrence.Missed || occurrence.MissedReason != "no_allowed_time" {
			t.Errorf("occurrence = %#v, want no_allowed_time miss", occurrence)
		}
	}
}

func assertStrictlyIncreasingInstants(t *testing.T, occurrences []domain.PlannedOccurrence) {
	t.Helper()
	for index := 1; index < len(occurrences); index++ {
		if !occurrences[index-1].PlannedAt.Before(occurrences[index].PlannedAt) {
			t.Fatalf("planned instants are not increasing at %d: %s then %s", index, occurrences[index-1].PlannedAt, occurrences[index].PlannedAt)
		}
	}
}

func task4UniqueOccurrenceIDs(occurrences []domain.PlannedOccurrence) map[string]struct{} {
	ids := make(map[string]struct{}, len(occurrences))
	for _, occurrence := range occurrences {
		ids[occurrence.ID] = struct{}{}
	}
	return ids
}
