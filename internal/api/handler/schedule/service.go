package schedule

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
)

// Service 是 HTTP 日程和提醒路由使用的 App 用例端口。
type Service interface {
	WriteSchedule(context.Context, model.Schedule, int, string) (*model.ScheduleWrite, error)
	GetSchedule(context.Context, int) (*model.Schedule, error)
	ListSchedules(context.Context, ...model.QueryOption) ([]*model.Schedule, error)
	DeleteScheduleWithResult(context.Context, int) (*model.ScheduleWrite, error)
	QueryScheduleState(context.Context, string, time.Time) (*model.ScheduleQueryResult, error)
	QueryOccurrences(context.Context, string, string, time.Time) (model.OccurrencePage, error)
	QueryCalendar(context.Context, string, time.Time) ([]string, error)
	GetNotification(context.Context, int, time.Time) (*model.OccurrenceView, error)
	QueryUnacknowledgedScope(context.Context, string, string, time.Time) (model.OccurrencePage, error)
	QueryDeliveryMembers(context.Context, string, string, string, time.Time) (model.OccurrencePage, error)
	AcknowledgeNotifications(context.Context, []int, time.Time) ([]*model.Notification, error)
	ClaimDelivery(context.Context, int) ([]notification.DeliveryClaim, error)
	StartDelivery(context.Context, string) (notification.DeliveryStart, error)
	CompleteDelivery(context.Context, string, model.NotifyStatus) (notification.DeliveryResult, error)
	DeliveryExecutorExitedAttempts([]string)
}
