package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// ErrNotPeriodSchedule 表示提醒所属日程不是可推进的周期日程。
var ErrNotPeriodSchedule = errors.New("schedule is not periodic")

// ErrInvalidSchedule 表示创建请求中的日程规则无效。
var ErrInvalidSchedule = errors.New("invalid schedule")

// ErrScheduleNotFound 表示日程不存在或已删除。
var ErrScheduleNotFound = errors.New("schedule not found")

// App 统筹所有服务
type App struct {
	Schedule   ScheduleRepo
	Notify     NotifyRepo
	NextNotify NextNotify
	Config     *config.Manager
}

// NewApp 使用依赖注入的配置与 Repo 接口组装应用服务。
func NewApp(manager *config.Manager, schedule ScheduleRepo, notify NotifyRepo) (*App, error) {
	if manager == nil {
		return nil, fmt.Errorf("[app] config manager is nil")
	}
	if schedule == nil {
		return nil, fmt.Errorf("[app] schedule repo is nil")
	}
	if notify == nil {
		return nil, fmt.Errorf("[app] notification repo is nil")
	}
	nextNotify, err := NewNextNotifyService(schedule, notify)
	if err != nil {
		return nil, err
	}
	return &App{Schedule: schedule, Notify: notify, NextNotify: nextNotify, Config: manager}, nil
}

// CreateSchedule 校验规则，并原子创建日程和首条提醒。
func (a *App) CreateSchedule(ctx context.Context, schedule model.Schedule) (*model.Schedule, *model.Notification, error) {
	after := time.Now().In(model.BeijingLocation())
	notifyAt, hasNext, err := nextNotificationTime(schedule, after)
	if err != nil {
		return nil, nil, fmt.Errorf("[app] invalid schedule rule: %w: %v", ErrInvalidSchedule, err)
	}
	if schedule.Type == model.SingleSchedule && !hasNext {
		return nil, nil, fmt.Errorf("[app] single schedule time must be after current time: %w", ErrInvalidSchedule)
	}
	if !hasNext {
		return a.Schedule.CreateScheduleWithNotification(ctx, schedule, nil)
	}
	return a.Schedule.CreateScheduleWithNotification(ctx, schedule, &notifyAt)
}

// ListSchedules 查询未删除日程。
func (a *App) ListSchedules(ctx context.Context, options ...model.QueryOption) ([]*model.Schedule, error) {
	return a.Schedule.GetAllSchedule(ctx, options...)
}

// GetSchedule 查询一条未删除日程。
func (a *App) GetSchedule(ctx context.Context, id int) (*model.Schedule, error) {
	schedule, err := a.Schedule.GetScheduleByID(ctx, id)
	if errors.Is(err, model.ErrRecordNotFound) {
		return nil, ErrScheduleNotFound
	}
	return schedule, err
}

// DeleteSchedule 软删除一条日程。
func (a *App) DeleteSchedule(ctx context.Context, id int) error {
	err := a.Schedule.DeleteScheduleByID(ctx, id)
	if errors.Is(err, model.ErrRecordNotFound) {
		return ErrScheduleNotFound
	}
	return err
}

// AdvanceNotification 只允许周期日程以已处理提醒的计划时间推进下一条提醒。
func (a *App) AdvanceNotification(ctx context.Context, notification model.Notification) (*model.Notification, error) {
	schedule, err := a.Schedule.GetScheduleByID(ctx, notification.ScheduleID)
	if err != nil {
		return nil, fmt.Errorf("[app] cannot read schedule for notification, notification_id=%d, schedule_id=%d: %w", notification.ID, notification.ScheduleID, err)
	}
	if schedule == nil {
		return nil, fmt.Errorf("[app] schedule repo returned nil for notification, notification_id=%d, schedule_id=%d", notification.ID, notification.ScheduleID)
	}
	if schedule.Type != model.PeriodSchedule {
		return nil, fmt.Errorf("[app] cannot advance notification, notification_id=%d, schedule_id=%d, type=%q: %w", notification.ID, notification.ScheduleID, schedule.Type, ErrNotPeriodSchedule)
	}
	return a.NextNotify.Next(ctx, notification.ScheduleID, notification.NotifyTime)
}
