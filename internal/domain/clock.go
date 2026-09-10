package domain

import "time"

type Timer interface {
	Stop() bool
}

type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}

func utcTimes(values map[string]time.Time) map[string]time.Time {
	if values == nil {
		return nil
	}
	normalized := make(map[string]time.Time, len(values))
	for key, value := range values {
		normalized[key] = value.UTC()
	}
	return normalized
}
