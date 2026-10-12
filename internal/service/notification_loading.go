package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// NotificationSchedulerRepo 定义预加载与到期核验共用的窄事务端口。
type NotificationSchedulerRepo interface {
	PrepareNotifications(context.Context, model.NotificationWindow, model.PrepareNotificationWindow) (*model.NotificationBatch, error)
}

// PrepareNotificationCandidates 按版本展开半开窗口，合并相同日程的重合规则。
func PrepareNotificationCandidates(versions []model.ScheduleVersion, start, end time.Time) ([]model.NotificationCandidate, error) {
	if start.IsZero() || !end.After(start) {
		return nil, fmt.Errorf("invalid notification window")
	}
	var result []model.NotificationCandidate
	seen := make(map[string]bool)
	for _, version := range versions {
		s := version.Schedule
		if s.Single != nil && (s.Single.NotifyTime.Second() != 0 || s.Single.NotifyTime.Nanosecond() != 0) {
			continue
		}
		after := start.Add(-time.Nanosecond)
		if version.From.After(after) {
			after = version.From
		}
		for {
			at, ok, err := nextNotificationTime(s, after)
			if err != nil {
				return nil, fmt.Errorf("calculate schedule %d revision %d: %w", s.ID, s.Revision, err)
			}
			if !ok || !at.Before(end) || (version.Until != nil && at.After(*version.Until)) {
				break
			}
			key := fmt.Sprintf("%d/%d", s.ID, at.UnixNano())
			if !seen[key] {
				seen[key] = true
				result = append(result, model.NotificationCandidate{ScheduleID: s.ID, NotifyTime: at, Name: s.Name, Description: s.Description})
			}
			after = at
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].NotifyTime.Equal(result[j].NotifyTime) {
			return result[i].ScheduleID < result[j].ScheduleID
		}
		return result[i].NotifyTime.Before(result[j].NotifyTime)
	})
	return result, nil
}
