package domain

import "time"

type Timer interface {
	Stop() bool
}

type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}
