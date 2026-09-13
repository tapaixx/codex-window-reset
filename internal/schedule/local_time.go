package schedule

import (
	"fmt"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

const minutesPerLocalDay = 24 * 60

type wallInterval struct {
	start int
	end   int
}

type instantInterval struct {
	start time.Time
	end   time.Time
}

// resolveLocalMinute maps a local wall-clock minute to the earliest UTC
// instant that displays that minute in location. Scanning UTC instants rather
// than relying on time.Date is important because time.Date silently chooses a
// side of an ambiguous time and normalizes a nonexistent time.
func resolveLocalMinute(location *time.Location, date time.Time, minute int) (time.Time, bool) {
	if location == nil || minute < 0 || minute >= minutesPerLocalDay {
		return time.Time{}, false
	}

	year, month, day := date.Date()
	wall := time.Date(year, month, day, minute/60, minute%60, 0, 0, time.UTC)
	_, expectedOffset := wall.In(location).Zone()
	center := wall.Add(-time.Duration(expectedOffset) * time.Second)

	// Modern IANA offsets are comfortably inside this range. The one-minute
	// cadence is intentional: the planner deals in strict HH:mm values.
	const scanRadius = 36 * time.Hour
	start := center.Add(-scanRadius)
	end := center.Add(scanRadius)
	for candidate := start; !candidate.After(end); candidate = candidate.Add(time.Minute) {
		localized := candidate.In(location)
		if localized.Year() == year && localized.Month() == month && localized.Day() == day &&
			localized.Hour() == minute/60 && localized.Minute() == minute%60 && localized.Second() == 0 {
			return localized, true
		}
	}
	return time.Time{}, false
}

func parseClock(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return 0, fmt.Errorf("invalid local clock %q", value)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func parsePeriod(label string, period domain.LocalPeriod) (wallInterval, error) {
	start, err := parseClock(period.Start)
	if err != nil {
		return wallInterval{}, fmt.Errorf("%s start: %w", label, err)
	}
	end, err := parseClock(period.End)
	if err != nil {
		return wallInterval{}, fmt.Errorf("%s end: %w", label, err)
	}
	if start >= end {
		return wallInterval{}, fmt.Errorf("%s must end after it starts", label)
	}
	return wallInterval{start: start, end: end}, nil
}

func firstValidMinute(location *time.Location, date time.Time, from, before int) (time.Time, bool) {
	if from < 0 {
		from = 0
	}
	if before > minutesPerLocalDay {
		before = minutesPerLocalDay
	}
	for minute := from; minute < before; minute++ {
		if instant, ok := resolveLocalMinute(location, date, minute); ok {
			return instant, true
		}
	}
	return time.Time{}, false
}

// resolveWallInterval converts a wall-clock interval into the corresponding
// usable instant interval. A nonexistent start is advanced to the first valid
// minute still inside the interval. A nonexistent end is advanced to the
// first valid boundary after the interval, preserving the valid portion before
// a spring-forward gap. If the interval contains no valid minute it is
// unusable.
func resolveWallInterval(location *time.Location, date time.Time, interval wallInterval) (instantInterval, bool) {
	if interval.start < 0 || interval.start >= minutesPerLocalDay || interval.start >= interval.end {
		return instantInterval{}, false
	}
	start, ok := firstValidMinute(location, date, interval.start, interval.end)
	if !ok {
		return instantInterval{}, false
	}
	// The final derived preheat window may extend past midnight. Resolve its
	// original end without clipping; PlanDay filters out next-day slots after
	// staggering so same-day accounts are not moved to an earlier time.
	endDate := date.AddDate(0, 0, interval.end/minutesPerLocalDay)
	end, ok := firstValidMinute(location, endDate, interval.end%minutesPerLocalDay, minutesPerLocalDay)
	if !ok || !end.After(start) {
		return instantInterval{}, false
	}
	return instantInterval{start: start, end: end}, true
}

// ResolveLocalPeriod is shared by the simulator for converting a validated
// local period into an instant interval. It intentionally exposes no schedule
// state and follows the same DST rules as PlanDay.
func ResolveLocalPeriod(location *time.Location, date time.Time, period domain.LocalPeriod) (time.Time, time.Time, bool, error) {
	interval, err := parsePeriod("local period", period)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	resolved, ok := resolveWallInterval(location, date, interval)
	if !ok {
		return time.Time{}, time.Time{}, false, nil
	}
	return resolved.start, resolved.end, true, nil
}

func subtractBlackouts(base wallInterval, blackouts []wallInterval) []wallInterval {
	allowed := make([]wallInterval, 0, len(blackouts)+1)
	cursor := base.start
	for _, blackout := range blackouts {
		if blackout.end <= cursor {
			continue
		}
		if blackout.start >= base.end {
			break
		}
		if blackout.start > cursor {
			end := blackout.start
			if end > base.end {
				end = base.end
			}
			if cursor < end {
				allowed = append(allowed, wallInterval{start: cursor, end: end})
			}
		}
		if blackout.end > cursor {
			cursor = blackout.end
			if cursor >= base.end {
				return allowed
			}
		}
	}
	if cursor < base.end {
		allowed = append(allowed, wallInterval{start: cursor, end: base.end})
	}
	return allowed
}

func resolveWindowBoundaries(location *time.Location, date time.Time, interval wallInterval) (time.Time, time.Time, bool) {
	resolved, ok := resolveWallInterval(location, date, interval)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	return resolved.start, resolved.end, true
}
