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
	if len(first) != 4 {
		t.Fatalf("planned %d occurrences, want 4: %#v", len(first), first)
	}

	wantIDs := []string{
		"2026-09-14/p0/acct-b",
		"2026-09-14/p0/acct-a",
		"2026-09-14/p1/acct-b",
		"2026-09-14/p1/acct-a",
	}
	wantTimes := []string{"06:15:00", "06:45:00", "11:15:00", "11:45:00"}
	wantStarts := []string{"06:00", "06:00", "11:00", "11:00"}
	wantEnds := []string{"07:00", "07:00", "12:00", "12:00"}
	for index, occurrence := range first {
		if occurrence.ID != wantIDs[index] {
			t.Errorf("occurrence %d ID = %q, want %q", index, occurrence.ID, wantIDs[index])
		}
		if got := occurrence.PlannedAt.In(date.Location()).Format("15:04:05"); got != wantTimes[index] {
			t.Errorf("occurrence %d planned at %s, want %s", index, got, wantTimes[index])
		}
		if got := occurrence.WindowStart.In(date.Location()).Format("15:04"); got != wantStarts[index] {
			t.Errorf("occurrence %d window starts at %s, want %s", index, got, wantStarts[index])
		}
		if got := occurrence.WindowEnd.In(date.Location()).Format("15:04"); got != wantEnds[index] {
			t.Errorf("occurrence %d window ends at %s, want %s", index, got, wantEnds[index])
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

func TestPlanDayRenewsAt1630BeforeWorkEndsAt1800(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "18:00", 120, 60, []string{"acct-a"})
	cfg.WorkPeriods = []domain.LocalPeriod{{Start: "09:00", End: "12:00"}, {Start: "13:30", End: "18:00"}}
	date := task4LocalDate(t, cfg.Timezone, "2026-09-14")
	occurrences, err := PlanDay(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, occurrence := range occurrences {
		got = append(got, occurrence.PlannedAt.In(date.Location()).Format("15:04"))
	}
	want := []string{"06:30", "11:30", "16:30"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preheats = %v, want %v: 16:30 renewal is still inside work", got, want)
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

func TestPlanDayRenewalUsesActualSlotsAndWorkBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, end string
		accounts  []string
		want      []string
	}{
		{"before_end", "16:31", []string{"a"}, []string{"06:30", "11:30", "16:30"}},
		{"at_end", "16:30", []string{"a"}, []string{"06:30", "11:30"}},
		{"after_end_is_not_pulled_forward", "16:20", []string{"a"}, []string{"06:30", "11:30"}},
		{"morning_renewal", "12:00", []string{"a"}, []string{"06:30", "11:30"}},
		{"only_early_staggered_account", "16:20", []string{"a", "b"}, []string{"06:15", "06:45", "11:15", "11:45", "16:15"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := task4ValidConfig("Asia/Shanghai", "09:00", tc.end, 120, 60, tc.accounts)
			date := task4LocalDate(t, cfg.Timezone, "2026-09-14")
			plan, err := PlanDay(cfg, date)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, occurrence := range plan {
				got = append(got, occurrence.PlannedAt.In(date.Location()).Format("15:04"))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("preheats = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanDayRenewalHonorsBlackoutsAndSkippedAnchors(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "18:00", 120, 60, []string{"a"})
	cfg.WorkPeriods = []domain.LocalPeriod{{Start: "09:00", End: "12:00"}, {Start: "15:00", End: "18:00"}}
	cfg.BlackoutPeriods = []domain.LocalPeriod{{Start: "16:00", End: "17:00"}}
	date := task4LocalDate(t, cfg.Timezone, "2026-09-14")
	plan, err := PlanDay(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 || plan[1].PlannedAt.Format("15:04") != "11:30" || !plan[2].Missed {
		t.Fatalf("renewal in morning must survive nominal anchor in lunch; blackout must miss final batch: %#v", plan)
	}
	cfg.SkipWindowTimes = []string{"19:00"}
	plan, err = PlanDay(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("skipped 19:00 anchor must remove its 16:00–17:00 preheat: %#v", plan)
	}
}

func TestPlanDayLateWorkKeepsOnlySameDaySlotsInMidnightWindow(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "22:00", "23:50", 30, 60, []string{"a", "b"})
	cfg.WindowHours = 1
	date := task4LocalDate(t, cfg.Timezone, "2026-09-14")
	plan, err := PlanDay(cfg, date)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, occurrence := range plan {
		got = append(got, occurrence.PlannedAt.Format("2006-01-02 15:04"))
	}
	want := []string{"2026-09-14 20:45", "2026-09-14 21:15", "2026-09-14 21:45", "2026-09-14 22:15", "2026-09-14 22:45", "2026-09-14 23:15", "2026-09-14 23:45"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("late-work preheats = %v, want %v", got, want)
	}
}

func TestPlanDayNewLunchRenewalPreservesExistingBatchIDsAndSlots(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "23:00", 120, 60, []string{"a", "b"})
	cfg.WorkPeriods = []domain.LocalPeriod{{Start: "09:00", End: "12:00"}, {Start: "18:00", End: "23:00"}}
	plan, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-09-14"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{"2026-09-14/p1/a": "16:15", "2026-09-14/p1/b": "16:45"}
	for _, occurrence := range plan {
		if want, exists := legacy[occurrence.ID]; exists {
			if got := occurrence.PlannedAt.Format("15:04"); got != want {
				t.Errorf("legacy %s moved to %s, want %s", occurrence.ID, got, want)
			}
			delete(legacy, occurrence.ID)
		}
	}
	if len(legacy) != 0 {
		t.Fatalf("legacy batches disappeared: %v", legacy)
	}
}

func TestPlanDayPreservesAdvancePreheatAtLegacyFinalAnchor(t *testing.T) {
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "19:00", 120, 60, []string{"a"})
	cfg.WorkPeriods = []domain.LocalPeriod{{Start: "09:00", End: "12:00"}, {Start: "18:00", End: "19:00"}}
	plan, err := PlanDay(cfg, task4LocalDate(t, cfg.Timezone, "2026-09-14"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, occurrence := range plan {
		got = append(got, occurrence.ID+"="+occurrence.PlannedAt.Format("15:04"))
	}
	want := []string{"2026-09-14/p0/a=06:30", "2026-09-14/p2/a=11:30", "2026-09-14/p1/a=16:30"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advance preheats = %v, want %v", got, want)
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
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "11:00", 60, 120, []string{"acct-a", "acct-b"})
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
	cfg := task4ValidConfig("Asia/Shanghai", "09:00", "11:00", 60, 120, []string{"acct-a", "acct-b"})
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
