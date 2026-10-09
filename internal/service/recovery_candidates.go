package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// PrepareRecoveryCandidates 按单个有效版本有界展开，不遍历空分钟。
func PrepareRecoveryCandidates(ctx context.Context, v model.ScheduleVersion, start, end, after time.Time, limit int) ([]model.NotificationCandidate, time.Time, bool, error) {
	if start.IsZero() || !end.After(start) || limit < 1 || limit > 500 {
		return nil, after, false, fmt.Errorf("invalid recovery candidate window")
	}
	s := v.Schedule
	if s.Single != nil && (s.Single.NotifyTime.Second() != 0 || s.Single.NotifyTime.Nanosecond() != 0) {
		return nil, after, true, nil
	}
	if floor := start.Add(-time.Nanosecond); floor.After(after) {
		after = floor
	}
	if v.From.After(after) {
		after = v.From
	}
	values := make([]model.NotificationCandidate, 0, limit)
	for len(values) < limit {
		if err := ctx.Err(); err != nil {
			return nil, after, false, err
		}
		at, ok, err := nextNotificationTime(s, after)
		if err != nil {
			return nil, after, false, fmt.Errorf("calculate recovery schedule %d revision %d: %w", s.ID, s.Revision, err)
		}
		if !ok || !at.Before(end) || (v.Until != nil && at.After(*v.Until)) {
			return values, after, true, nil
		}
		values = append(values, model.NotificationCandidate{ScheduleID: s.ID, NotifyTime: at, Name: s.Name, Description: s.Description})
		after = at
	}
	return values, after, false, nil
}
