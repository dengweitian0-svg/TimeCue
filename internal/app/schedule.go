package app

import (
	"context"
	"errors"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// CreateSchedule 校验规则，并原子创建日程和首条提醒。
func (a *App) CreateSchedule(ctx context.Context, value model.Schedule) (*model.Schedule, *model.Notification, error) {
	result, err := a.WriteSchedule(ctx, value, 0, "")
	if err != nil {
		return nil, nil, err
	}
	return result.Schedule, result.Notification, nil
}

// ListSchedules 查询未删除日程。
func (a *App) ListSchedules(ctx context.Context, options ...model.QueryOption) ([]*model.Schedule, error) {
	return a.Schedule.GetAllSchedule(ctx, options...)
}

// GetSchedule 查询一条未删除日程。
func (a *App) GetSchedule(ctx context.Context, id int) (*model.Schedule, error) {
	value, err := a.Schedule.GetScheduleByID(ctx, id)
	if errors.Is(err, model.ErrRecordNotFound) {
		return nil, ErrScheduleNotFound
	}
	return value, err
}

// WriteSchedule 执行日程正式写入并通知内存调度器。
func (a *App) WriteSchedule(ctx context.Context, value model.Schedule, expectedRevision int, key string) (*model.ScheduleWrite, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if err := a.ensureScheduleService(); err != nil {
		return nil, err
	}
	result, err := a.scheduleService.WriteSchedule(ctx, value, expectedRevision, key)
	if err == nil && a.Scheduler != nil {
		a.Scheduler.ScheduleChanged(ctx, result)
	}
	return result, err
}

// DeleteSchedule 软删除一条日程。
func (a *App) DeleteSchedule(ctx context.Context, id int) error {
	_, err := a.DeleteScheduleWithResult(ctx, id)
	return err
}

// DeleteScheduleWithResult 返回正式删除事务的状态变化。
func (a *App) DeleteScheduleWithResult(ctx context.Context, id int) (*model.ScheduleWrite, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if a.Schedule == nil {
		return nil, ErrScheduleNotFound
	}
	result, err := a.Schedule.DeleteScheduleByID(ctx, id)
	if errors.Is(err, model.ErrRecordNotFound) {
		return nil, ErrScheduleNotFound
	}
	if err == nil && a.Scheduler != nil {
		a.Scheduler.ScheduleChanged(ctx, result)
	}
	return result, err
}

// DeleteScheduleVersion 通过正式操作锁提交带版本及回执的删除，并同步内存调度器。
func (a *App) DeleteScheduleVersion(ctx context.Context, id, expectedRevision int, key string) (*model.ScheduleWrite, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if err := a.ensureScheduleService(); err != nil {
		return nil, err
	}
	result, err := a.scheduleService.DeleteScheduleVersion(ctx, id, expectedRevision, key)
	if err == nil && a.Scheduler != nil {
		a.Scheduler.ScheduleChanged(ctx, result)
	}
	return result, err
}

// ReadScheduleDeleteReceipt 只读核对原删除回执，不调用写入入口或内存调度器。
func (a *App) ReadScheduleDeleteReceipt(ctx context.Context, id, expectedRevision int, key string) (*model.ScheduleWrite, error) {
	if err := a.ensureScheduleService(); err != nil {
		return nil, err
	}
	return a.scheduleService.ReadScheduleDeleteReceipt(ctx, id, expectedRevision, key)
}
