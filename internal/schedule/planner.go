package schedule

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/tapaixx/codex-window-reset/internal/domain"
)

// Planner contains no mutable state. Keeping planning pure makes a plan
// reproducible after restart and lets the simulator use exactly the same
// derivation.
type Planner struct{}

func (Planner) PlanDay(cfg domain.Config, date time.Time) ([]domain.PlannedOccurrence, error) {
	return PlanDay(cfg, date)
}

// PlanDay derives staggered occurrences across the short-window cycles needed
// on date. date's calendar components are treated as the local
// date; callers do not need to pass a time.Time in cfg.Timezone.
func PlanDay(cfg domain.Config, date time.Time) ([]domain.PlannedOccurrence, error) {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, configError("timezone must be a valid IANA location")
	}
	if len(cfg.ScheduledAccountKeys) == 0 || len(cfg.WorkPeriods) == 0 || !configuredWeekday(cfg, date, location) {
		return []domain.PlannedOccurrence{}, nil
	}
	if cfg.PreheatLeadMinutes == nil || cfg.PreheatSpanMinutes == nil {
		return nil, configError("preheat lead and span are required to plan occurrences")
	}
	if *cfg.PreheatLeadMinutes < 1 || *cfg.PreheatSpanMinutes < 1 {
		return nil, configError("preheat lead and span must be positive")
	}

	blackouts := make([]wallInterval, 0, len(cfg.BlackoutPeriods))
	for index, period := range cfg.BlackoutPeriods {
		interval, parseErr := parsePeriod(fmt.Sprintf("blackout period %d", index), period)
		if parseErr != nil {
			return nil, configError(parseErr.Error())
		}
		blackouts = append(blackouts, interval)
	}

	year, month, day := date.Date()
	dateString := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	accounts := append([]string(nil), cfg.ScheduledAccountKeys...)
	workPeriods := make([]wallInterval, 0, len(cfg.WorkPeriods))
	for periodIndex, period := range cfg.WorkPeriods {
		work, parseErr := parsePeriod(fmt.Sprintf("work period %d", periodIndex), period)
		if parseErr != nil {
			return nil, configError(parseErr.Error())
		}
		workPeriods = append(workPeriods, work)
	}
	windowMinutes := cfg.WindowHours * 60
	if windowMinutes <= 0 {
		windowMinutes = 5 * 60
	}
	skipTimes := make(map[int]struct{}, len(cfg.SkipWindowTimes))
	for _, value := range cfg.SkipWindowTimes {
		minutes, parseErr := time.Parse("15:04", value)
		if parseErr != nil || minutes.Format("15:04") != value {
			return nil, configError("skip window times must use HH:MM")
		}
		skipTimes[minutes.Hour()*60+minutes.Minute()] = struct{}{}
	}
	anchors := make([]int, 0, len(workPeriods))
	lastWorkEnd := workPeriods[len(workPeriods)-1].end
	insideWork := func(minute int) bool {
		for _, work := range workPeriods {
			if minute >= work.start && minute < work.end {
				return true
			}
		}
		return false
	}
	// Keep every legacy batch's identity AND rank seed. Inserting a newly
	// eligible renewal before an existing batch must not renumber persisted
	// successes/misses into new requests on upgrade. New batches use indices
	// after the legacy set; returned occurrences still follow time order.
	periodIndexes := make(map[int]int)
	for anchor := workPeriods[0].start; anchor <= lastWorkEnd; anchor += windowMinutes {
		if _, skipped := skipTimes[anchor]; !skipped && (insideWork(anchor) || anchor == lastWorkEnd) {
			periodIndexes[anchor] = len(periodIndexes)
		}
	}
	// A nominal anchor can fall after work even though its derived preheat
	// still falls inside work (19:00 -> 16:00–17:00). Keep the original
	// before-work batches and also consider every window overlapping work.
	for anchor := workPeriods[0].start; anchor-*cfg.PreheatLeadMinutes-*cfg.PreheatSpanMinutes < lastWorkEnd; anchor += windowMinutes {
		windowEnd := anchor - *cfg.PreheatLeadMinutes
		windowStart := windowEnd - *cfg.PreheatSpanMinutes
		_, eligible := periodIndexes[anchor]
		for _, work := range workPeriods {
			if windowStart < work.end && windowEnd > work.start {
				eligible = true
				break
			}
		}
		if _, skipped := skipTimes[anchor%minutesPerLocalDay]; eligible && !skipped {
			anchors = append(anchors, anchor)
		}
	}
	occurrences := make([]domain.PlannedOccurrence, 0, len(anchors)*len(accounts))

	for _, anchor := range anchors {
		periodIndex, legacy := periodIndexes[anchor]
		if !legacy {
			periodIndex = len(periodIndexes)
			periodIndexes[anchor] = periodIndex
		}
		windowEnd := anchor - *cfg.PreheatLeadMinutes
		windowStart := windowEnd - *cfg.PreheatSpanMinutes
		if windowStart < 0 || windowStart >= windowEnd {
			return nil, configError("derived preheat window must remain on the local date")
		}
		preheat := wallInterval{start: windowStart, end: windowEnd}
		allowedWall := subtractBlackouts(preheat, blackouts)

		allowed := make([]instantInterval, 0, len(allowedWall))
		for _, interval := range allowedWall {
			resolved, ok := resolveWallInterval(location, date, interval)
			if ok {
				allowed = append(allowed, resolved)
			}
		}

		windowStartInstant, windowEndInstant, windowOK := resolveWindowBoundaries(location, date, preheat)
		if !windowOK {
			// This can only occur for a wholly nonexistent local window. It is
			// still represented as a missed occurrence for every account.
			windowStartInstant = time.Time{}
			windowEndInstant = time.Time{}
		}

		localOccurrence := dateString + "/p" + strconv.Itoa(periodIndex)
		ranked := rankedAccounts(accounts, localOccurrence)
		if len(allowed) == 0 {
			for _, account := range ranked {
				occurrences = append(occurrences, missedOccurrence(dateString, periodIndex, account.key))
			}
			continue
		}

		total := intervalDuration(allowed)
		if total <= 0 {
			for _, account := range ranked {
				occurrences = append(occurrences, missedOccurrence(dateString, periodIndex, account.key))
			}
			continue
		}
		if !windowOK {
			// A valid allowed instant should also make the containing preheat
			// window resolvable. Keep the result safe if a future timezone rule
			// violates that assumption.
			windowStartInstant = allowed[0].start
			windowEndInstant = allowed[len(allowed)-1].end
		}

		for index, account := range ranked {
			offset := total * time.Duration(2*index+1) / time.Duration(2*len(ranked))
			plannedAt := instantAtOffset(allowed, offset)
			minute := plannedAt.Hour()*60 + plannedAt.Minute()
			// Filter AFTER staggering: clipping a window to work end would pull
			// later slots forward and issue requests before their intended time.
			if plannedAt.Format("2006-01-02") != dateString || minute >= lastWorkEnd ||
				(!legacy && !insideWork(minute)) {
				continue
			}
			occurrences = append(occurrences, domain.PlannedOccurrence{
				ID:          OccurrenceID(dateString, periodIndex, account.key),
				AccountKey:  account.key,
				LocalDate:   dateString,
				PeriodIndex: periodIndex,
				WindowStart: windowStartInstant,
				WindowEnd:   windowEndInstant,
				PlannedAt:   plannedAt,
			})
		}
	}

	return occurrences, nil
}

// IsWorkday reports whether date is selected by the configured weekly
// calendar. It is kept next to PlanDay so callers that render a daily plan
// use the same local-date weekday semantics.
func IsWorkday(cfg domain.Config, date time.Time) (bool, error) {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return false, configError("timezone must be a valid IANA location")
	}
	return configuredWeekday(cfg, date, location), nil
}

func OccurrenceID(date string, periodIndex int, accountKey string) string {
	return fmt.Sprintf("%s/p%d/%s", date, periodIndex, accountKey)
}

func rank(localOccurrence, accountKey string) [32]byte {
	return sha256.Sum256([]byte(localOccurrence + "\x00" + accountKey))
}

type rankedAccount struct {
	key  string
	rank [32]byte
}

func rankedAccounts(accounts []string, localOccurrence string) []rankedAccount {
	ranked := make([]rankedAccount, 0, len(accounts))
	for _, account := range accounts {
		ranked = append(ranked, rankedAccount{key: account, rank: rank(localOccurrence, account)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		comparison := bytes.Compare(ranked[i].rank[:], ranked[j].rank[:])
		if comparison != 0 {
			return comparison < 0
		}
		return ranked[i].key < ranked[j].key
	})
	return ranked
}

func intervalDuration(intervals []instantInterval) time.Duration {
	var total time.Duration
	for _, interval := range intervals {
		if interval.end.After(interval.start) {
			total += interval.end.Sub(interval.start)
		}
	}
	return total
}

func instantAtOffset(intervals []instantInterval, offset time.Duration) time.Time {
	for _, interval := range intervals {
		duration := interval.end.Sub(interval.start)
		if offset < duration {
			return interval.start.Add(offset)
		}
		offset -= duration
	}
	return intervals[len(intervals)-1].end
}

func missedOccurrence(date string, periodIndex int, accountKey string) domain.PlannedOccurrence {
	return domain.PlannedOccurrence{
		ID:           OccurrenceID(date, periodIndex, accountKey),
		AccountKey:   accountKey,
		LocalDate:    date,
		PeriodIndex:  periodIndex,
		Missed:       true,
		MissedReason: "no_allowed_time",
	}
}

func configuredWeekday(cfg domain.Config, date time.Time, location *time.Location) bool {
	year, month, day := date.Date()
	localNoon := time.Date(year, month, day, 12, 0, 0, 0, location)
	weekday := int(localNoon.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	for _, configured := range cfg.Weekdays {
		if configured == weekday {
			return true
		}
	}
	return false
}

func configError(message string) error {
	return &domain.Error{Code: domain.CodeConfigInvalid, Message: message, HTTPStatus: 400}
}
