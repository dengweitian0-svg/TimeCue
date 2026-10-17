package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// NextNotifyService 根据日程规则创建下一条提醒。
type NextNotifyService struct {
	schedules     ScheduleReader
	notifications NotifyRepo
}

// NewNextNotifyService 创建提醒规则推进服务。
func NewNextNotifyService(schedules ScheduleReader, notifications NotifyRepo) (*NextNotifyService, error) {
	if schedules == nil || notifications == nil {
		return nil, fmt.Errorf("schedule and notification repos are required")
	}
	return &NextNotifyService{schedules: schedules, notifications: notifications}, nil
}

// Next 计算严格晚于 after 的下一次触发时间，并幂等创建提醒。
func (s *NextNotifyService) Next(ctx context.Context, scheduleID int, after time.Time) (*model.Notification, error) {
	if scheduleID < 0 || after.IsZero() {
		return nil, fmt.Errorf("invalid next notification input")
	}
	rule, err := s.schedules.GetScheduleByID(ctx, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("cannot read schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if rule == nil {
		return nil, fmt.Errorf("schedule repo returned nil, schedule_id=%d", scheduleID)
	}
	if rule.Revision > 1 && rule.UpdatedAt.After(after) {
		after = rule.UpdatedAt
	}
	at, ok, err := schedule.NextNotificationTime(*rule, after)
	if err != nil {
		return nil, fmt.Errorf("cannot calculate next notification, schedule_id=%d: %w", scheduleID, err)
	}
	if !ok {
		return nil, nil
	}
	value, err := s.notifications.CreateNotification(ctx, scheduleID, at)
	if err != nil {
		return nil, fmt.Errorf("cannot create next notification, schedule_id=%d, notify_at=%q: %w", scheduleID, at.Format(time.RFC3339Nano), err)
	}
	return value, nil
}
