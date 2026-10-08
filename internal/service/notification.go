package service

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// NotifyRepo 定义提醒记录的基础持久化操作。
type NotifyRepo interface {
	// GetNotificationByID 读取一次提醒，用于历史详情。
	GetNotificationByID(context.Context, int) (*model.Notification, error)
	// AcknowledgeNotifications 原子确认明确提醒集合，保留首次时间与投递结果。
	AcknowledgeNotifications(context.Context, []int, time.Time) ([]*model.Notification, error)
	// CreateNotification 检查日程状态并幂等创建一条提醒。
	CreateNotification(ctx context.Context, scheduleID int, notifyAt time.Time) (*model.Notification, error)
	// GetNotificationByStartTime 查询时间范围内的待处理提醒记录。
	GetNotificationByStartTime(ctx context.Context, startTime time.Time, endTime time.Time) ([]*model.Notification, error)
	// DeleteNotificationByID 根据提醒 ID 软删除提醒。
	DeleteNotificationByID(ctx context.Context, notificationID int) error
}

// NextNotify 按日程 ID 计算严格晚于 after 的下一次触发，并幂等写入提醒。
type NextNotify interface {
	Next(ctx context.Context, scheduleID int, after time.Time) (*model.Notification, error)
}

// QueryUnacknowledged 分页读取全历史到期未确认记录，不检查父日程启停或投递成功。
func (a *App) QueryUnacknowledged(ctx context.Context, rawCursor string, now time.Time) (OccurrencePage, error) {
	cursor, err := queryCursor(rawCursor, "pending", now)
	if err != nil {
		return OccurrencePage{}, err
	}
	schedules, _, pending, total, err := a.Schedule.ReadScheduleState(ctx, cursor.AsOf, cursor.AsOf, cursor.AsOf, model.NotificationPosition{Time: cursor.Time, ID: cursor.ID}, 51)
	if err != nil {
		return OccurrencePage{}, err
	}
	return pendingPage(schedules, pending, total, cursor, 50), nil
}

// GetNotification 保留已删除父日程的历史只读内容。
func (a *App) GetNotification(ctx context.Context, id int, now time.Time) (*OccurrenceView, error) {
	n, err := a.Notify.GetNotificationByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.Deleted {
		return nil, model.ErrRecordNotFound
	}
	s, err := a.Schedule.GetScheduleByID(ctx, n.ScheduleID, model.WithScheduleIncludeDeleted(true))
	if err != nil {
		return nil, err
	}
	v := occurrence(s, n, n.NotifyTime, now)
	return &v, nil
}

// AcknowledgeNotifications 对明确的提醒集合原子确认，重复请求保留首次时间。
func (a *App) AcknowledgeNotifications(ctx context.Context, ids []int, now time.Time) ([]*model.Notification, error) {
	return a.Notify.AcknowledgeNotifications(ctx, ids, now)
}
