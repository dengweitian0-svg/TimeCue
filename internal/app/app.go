package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/service/recovery"
	"github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// App 统筹服务用例、跨模块操作边界与运行生命周期。
type App struct {
	Schedule            schedule.Repo
	Notify              notification.Repo
	scheduleService     *schedule.Service
	notificationService *notification.Service
	Config              *config.Manager
	Scheduler           *notification.Scheduler
	Delivery            *notification.DeliveryService
	Recovery            *recovery.Service
	ChatService         chat.Service
	operations          sync.Mutex
	serviceMu           sync.Mutex
}

// NewApp 通过持久化端口组装日程与提醒服务。
func NewApp(manager *config.Manager, schedules schedule.Repo, notifications notification.Repo) (*App, error) {
	if manager == nil {
		return nil, fmt.Errorf("[app] config manager is nil")
	}
	if schedules == nil {
		return nil, fmt.Errorf("[app] schedule repo is nil")
	}
	if notifications == nil {
		return nil, fmt.Errorf("[app] notification repo is nil")
	}
	scheduleService, err := schedule.NewService(schedules)
	if err != nil {
		return nil, err
	}
	notificationService, err := notification.NewService(schedules, notifications)
	if err != nil {
		return nil, err
	}
	return &App{Schedule: schedules, Notify: notifications, scheduleService: scheduleService, notificationService: notificationService, Config: manager}, nil
}

// ErrNotPeriodSchedule 表示提醒所属日程不是可推进的周期日程。
var ErrNotPeriodSchedule = schedule.ErrNotPeriodSchedule

// ErrInvalidSchedule 表示日程规则无效。
var ErrInvalidSchedule = schedule.ErrInvalidSchedule

// ErrScheduleNotFound 表示日程不存在或已删除。
var ErrScheduleNotFound = schedule.ErrScheduleNotFound

func (a *App) syncServices() {
	a.serviceMu.Lock()
	defer a.serviceMu.Unlock()
	a.initializeServicesLocked()
	if a.Scheduler != nil {
		a.notificationService.Scheduler = a.Scheduler
	} else {
		a.Scheduler = a.notificationService.Scheduler
	}
	if a.Delivery != nil {
		a.notificationService.Delivery = a.Delivery
	} else {
		a.Delivery = a.notificationService.Delivery
	}
}

func (a *App) initializeServicesLocked() {
	if a.scheduleService == nil && a.Schedule != nil {
		a.scheduleService, _ = schedule.NewService(a.Schedule)
	}
	if a.notificationService == nil {
		if a.Schedule != nil && a.Notify != nil {
			a.notificationService, _ = notification.NewService(a.Schedule, a.Notify)
		} else {
			a.notificationService = &notification.Service{}
		}
	}
}

func (a *App) ensureScheduleService() error {
	a.serviceMu.Lock()
	defer a.serviceMu.Unlock()
	a.initializeServicesLocked()
	if a.scheduleService == nil {
		return fmt.Errorf("[app] schedule repo is required")
	}
	return nil
}

func (a *App) ensureNotificationService() error {
	a.serviceMu.Lock()
	defer a.serviceMu.Unlock()
	a.initializeServicesLocked()
	if a.notificationService == nil {
		return fmt.Errorf("[app] notification service is unavailable")
	}
	return nil
}

// AdvanceNotification 只允许周期日程以已处理提醒的计划时间推进下一条提醒。
func (a *App) AdvanceNotification(ctx context.Context, n model.Notification) (*model.Notification, error) {
	if err := a.ensureNotificationService(); err != nil {
		return nil, err
	}
	rule, err := a.Schedule.GetScheduleByID(ctx, n.ScheduleID)
	if err != nil {
		return nil, fmt.Errorf("[app] cannot read schedule for notification, notification_id=%d, schedule_id=%d: %w", n.ID, n.ScheduleID, err)
	}
	if rule == nil {
		return nil, fmt.Errorf("[app] schedule repo returned nil for notification, notification_id=%d, schedule_id=%d", n.ID, n.ScheduleID)
	}
	if rule.Type != model.PeriodSchedule {
		return nil, fmt.Errorf("[app] cannot advance notification, notification_id=%d, schedule_id=%d, type=%q: %w", n.ID, n.ScheduleID, rule.Type, ErrNotPeriodSchedule)
	}
	return a.notificationService.NextNotify.Next(ctx, n.ScheduleID, n.NotifyTime)
}
