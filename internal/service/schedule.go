package service

import (
	"context"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// ScheduleRepo 定义日程持久化操作。
type ScheduleRepo interface {
	// CreateSchedule 创建日程并返回数据库生成的 ID 与时间戳。
	CreateSchedule(ctx context.Context, schedule model.Schedule) (*model.Schedule, error)
	// GetScheduleByID 要求单独传入日程 ID，QueryOption 中已设置的条件也参与过滤。
	GetScheduleByID(ctx context.Context, scheduleID int, query model.QueryOption) (*model.Schedule, error)
	// GetAllSchedule 的查询条件均可选，按已设置条件分页查询日程。
	GetAllSchedule(ctx context.Context, query model.QueryOption) ([]*model.Schedule, error)
	// DeleteScheduleByID 软删除日程并隐藏尚未到时的待投递提醒，保留历史记录。
	DeleteScheduleByID(ctx context.Context, scheduleID int) error
}
