package model

import (
	"time"
)

type Rule struct {
	ScheduleID int
	Type       ScheduleType
	After      time.Time
}
