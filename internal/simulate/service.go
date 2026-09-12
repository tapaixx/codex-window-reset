package simulate

import (
	"sort"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/schedule"
)

type Service struct{}

func (Service) Run(cfg domain.Config, date time.Time) (domain.SimulationResult, error) {
	occurrences, err := schedule.PlanDay(cfg, date)
	if err != nil {
		return domain.SimulationResult{}, err
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return domain.SimulationResult{}, err
	}
	workday, err := schedule.IsWorkday(cfg, date)
	if err != nil {
		return domain.SimulationResult{}, err
	}

	resolvedWork := make([]timeInterval, 0, len(cfg.WorkPeriods))
	if workday {
		for _, period := range cfg.WorkPeriods {
			start, end, ok, resolveErr := schedule.ResolveLocalPeriod(location, date, period)
			if resolveErr != nil {
				return domain.SimulationResult{}, resolveErr
			}
			if ok {
				resolvedWork = append(resolvedWork, timeInterval{start: start, end: end})
			}
		}
	}

	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location)
	dayEnd := dayStart.AddDate(0, 0, 1)
	workPeriods := make([]timeInterval, 0, len(resolvedWork))
	for _, period := range resolvedWork {
		period = clipInterval(period, dayStart, dayEnd)
		if period.end.After(period.start) {
			workPeriods = append(workPeriods, period)
		}
	}
	work := mergeIntervals(workPeriods)
	workMinutes := wholeMinutes(totalDuration(work))

	productivity := time.Duration(cfg.ProductivityMinutes) * time.Minute
	if cfg.ProductivityMinutes <= 0 {
		return domain.SimulationResult{}, &domain.Error{
			Code:       domain.CodeConfigInvalid,
			Message:    "productivity minutes must be positive",
			HTTPStatus: 400,
		}
	}

	baselineWindows := make([]timeInterval, 0, len(workPeriods))
	for _, period := range workPeriods {
		baselineWindows = append(baselineWindows, clipInterval(timeInterval{
			start: period.start,
			end:   period.start.Add(productivity),
		}, dayStart, dayEnd))
	}

	scheduledWindows := make([]timeInterval, 0, len(occurrences))
	for _, occurrence := range occurrences {
		if occurrence.Missed || occurrence.PlannedAt.IsZero() {
			continue
		}
		scheduledWindows = append(scheduledWindows, clipInterval(timeInterval{
			// A preheat request establishes the next request window at the
			// configured lead boundary; model availability from that boundary,
			// rather than counting the preheat request itself as work time.
			start: occurrence.WindowEnd.Add(time.Duration(maxInt(cfg.PreheatLeadMinutes, 0)) * time.Minute),
			end:   occurrence.WindowEnd.Add(time.Duration(maxInt(cfg.PreheatLeadMinutes, 0))*time.Minute + productivity),
		}, dayStart, dayEnd))
	}
	baselineWindows = mergeIntervals(baselineWindows)
	scheduledWindows = mergeIntervals(scheduledWindows)

	baselineCoverage := coverageDuration(baselineWindows, work)
	scheduledCoverage := coverageDuration(scheduledWindows, work)
	baselineIdle := idleDuration(baselineWindows, baselineCoverage)
	scheduledIdle := idleDuration(scheduledWindows, scheduledCoverage)

	return domain.SimulationResult{
		WorkMinutes: workMinutes,
		Baseline: domain.StrategyMetrics{
			AvailableCoverageMinutes: wholeMinutes(baselineCoverage),
			IdleWindowMinutes:        wholeMinutes(baselineIdle),
			TimelineSegments:         strategyTimeline(dayStart, dayEnd, work, baselineWindows),
		},
		Scheduled: domain.StrategyMetrics{
			AvailableCoverageMinutes: wholeMinutes(scheduledCoverage),
			IdleWindowMinutes:        wholeMinutes(scheduledIdle),
			TimelineSegments:         strategyTimeline(dayStart, dayEnd, work, scheduledWindows),
		},
		NetGainMinutes:   wholeMinutes(scheduledCoverage) - wholeMinutes(baselineCoverage),
		PreheatWindows:   occurrences,
		TimelineSegments: timelineSegments(dayStart, dayEnd, work, occurrences),
		Assumptions:      map[string]int{"productivity_minutes": cfg.ProductivityMinutes},
	}, nil
}

// The chart and metrics consume the same intervals. The browser only lays out
// these segments; it must not invent coverage or reuse A's segments for B.
func strategyTimeline(dayStart, dayEnd time.Time, work, available []timeInterval) []domain.TimelineSegment {
	boundaries := []time.Time{dayStart, dayEnd}
	for _, intervals := range [][]timeInterval{work, available} {
		for _, interval := range intervals {
			boundaries = append(boundaries, interval.start, interval.end)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	boundaries = uniqueTimes(boundaries)
	segments := make([]domain.TimelineSegment, 0, len(boundaries))
	for i := 0; i+1 < len(boundaries); i++ {
		start, end := boundaries[i], boundaries[i+1]
		midpoint := start.Add(end.Sub(start) / 2)
		kind := "idle"
		if containsInstant(work, midpoint) {
			kind = "limited"
			if containsInstant(available, midpoint) {
				kind = "available"
			}
		} else if len(work) > 0 && midpoint.After(work[0].start) && midpoint.Before(work[len(work)-1].end) {
			kind = "break"
		}
		if len(segments) > 0 && segments[len(segments)-1].Kind == kind {
			segments[len(segments)-1].End = end
		} else {
			segments = append(segments, domain.TimelineSegment{Kind: kind, Start: start, End: end})
		}
	}
	return segments
}

func maxInt(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

type timeInterval struct {
	start time.Time
	end   time.Time
}

func mergeIntervals(intervals []timeInterval) []timeInterval {
	filtered := make([]timeInterval, 0, len(intervals))
	for _, interval := range intervals {
		if interval.end.After(interval.start) {
			filtered = append(filtered, interval)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].start.Equal(filtered[j].start) {
			return filtered[i].end.Before(filtered[j].end)
		}
		return filtered[i].start.Before(filtered[j].start)
	})
	merged := make([]timeInterval, 0, len(filtered))
	for _, interval := range filtered {
		if len(merged) == 0 || interval.start.After(merged[len(merged)-1].end) {
			merged = append(merged, interval)
			continue
		}
		if interval.end.After(merged[len(merged)-1].end) {
			merged[len(merged)-1].end = interval.end
		}
	}
	return merged
}

func clipInterval(interval timeInterval, lower, upper time.Time) timeInterval {
	if interval.start.Before(lower) {
		interval.start = lower
	}
	if interval.end.After(upper) {
		interval.end = upper
	}
	return interval
}

func clipIntervals(intervals []timeInterval, lower, upper time.Time) []timeInterval {
	clipped := make([]timeInterval, 0, len(intervals))
	for _, interval := range intervals {
		interval = clipInterval(interval, lower, upper)
		if interval.end.After(interval.start) {
			clipped = append(clipped, interval)
		}
	}
	return mergeIntervals(clipped)
}

func totalDuration(intervals []timeInterval) time.Duration {
	var total time.Duration
	for _, interval := range intervals {
		total += interval.end.Sub(interval.start)
	}
	return total
}

func coverageDuration(windows, work []timeInterval) time.Duration {
	intersections := make([]timeInterval, 0)
	for _, window := range windows {
		for _, period := range work {
			start := window.start
			if period.start.After(start) {
				start = period.start
			}
			end := window.end
			if period.end.Before(end) {
				end = period.end
			}
			if end.After(start) {
				intersections = append(intersections, timeInterval{start: start, end: end})
			}
		}
	}
	return totalDuration(mergeIntervals(intersections))
}

func idleDuration(windows []timeInterval, coverage time.Duration) time.Duration {
	idle := totalDuration(windows) - coverage
	if idle < 0 {
		return 0
	}
	return idle
}

func wholeMinutes(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(duration / time.Minute)
}

type preheatMarker struct {
	at      time.Time
	account string
}

func timelineSegments(dayStart, dayEnd time.Time, work []timeInterval, occurrences []domain.PlannedOccurrence) []domain.TimelineSegment {
	boundaries := []time.Time{dayStart, dayEnd}
	for _, interval := range work {
		boundaries = append(boundaries, interval.start, interval.end)
	}
	markers := make([]preheatMarker, 0, len(occurrences))
	for _, occurrence := range occurrences {
		if occurrence.Missed || occurrence.PlannedAt.IsZero() || occurrence.PlannedAt.Before(dayStart) || occurrence.PlannedAt.After(dayEnd) {
			continue
		}
		markers = append(markers, preheatMarker{at: occurrence.PlannedAt, account: occurrence.AccountKey})
		boundaries = append(boundaries, occurrence.PlannedAt)
	}
	sort.SliceStable(markers, func(i, j int) bool {
		if markers[i].at.Equal(markers[j].at) {
			return markers[i].account < markers[j].account
		}
		return markers[i].at.Before(markers[j].at)
	})
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	boundaries = uniqueTimes(boundaries)

	segments := make([]domain.TimelineSegment, 0, len(boundaries)+len(markers))
	markerIndex := 0
	for index := 0; index < len(boundaries); index++ {
		start := boundaries[index]
		for markerIndex < len(markers) && markers[markerIndex].at.Equal(start) {
			segments = append(segments, domain.TimelineSegment{
				Kind:       "preheat",
				Start:      start,
				End:        start,
				AccountKey: markers[markerIndex].account,
			})
			markerIndex++
		}
		if index+1 >= len(boundaries) {
			continue
		}
		end := boundaries[index+1]
		if !end.After(start) {
			continue
		}
		kind := "idle"
		midpoint := start.Add(end.Sub(start) / 2)
		if containsInstant(work, midpoint) {
			kind = "work"
		}
		segments = append(segments, domain.TimelineSegment{Kind: kind, Start: start, End: end})
	}
	return segments
}

func uniqueTimes(values []time.Time) []time.Time {
	if len(values) == 0 {
		return nil
	}
	unique := make([]time.Time, 0, len(values))
	for _, value := range values {
		if len(unique) == 0 || !value.Equal(unique[len(unique)-1]) {
			unique = append(unique, value)
		}
	}
	return unique
}

func containsInstant(intervals []timeInterval, instant time.Time) bool {
	for _, interval := range intervals {
		if !instant.Before(interval.start) && instant.Before(interval.end) {
			return true
		}
	}
	return false
}
