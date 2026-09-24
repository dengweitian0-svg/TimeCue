package service

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// NotifyRepo 定义提醒记录的基础持久化操作。
type NotifyRepo interface {
	// CreateNotification 检查日程状态并幂等创建一条提醒。
	CreateNotification(ctx context.Context, scheduleID int, notifyAt time.Time) (*model.Notification, error)
	// GetNotificationByStartTime 查询时间范围内的待处理提醒记录。
	GetNotificationByStartTime(ctx context.Context, startTime time.Time, endTime time.Time) ([]model.Notification, error)
	// DeleteNotificationByID 根据提醒 ID 软删除提醒。
	DeleteNotificationByID(ctx context.Context, notificationID int) error
}

// NextNotify 提供根据 Rule 计算下一次提醒的统一接口
// 应该由 model.Schedule 和 model.Notification 实现
type NextNotify interface {
	Next(ctx context.Context, rule model.Rule) model.Notification
}
