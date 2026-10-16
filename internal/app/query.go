package app

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// QueryScheduleState 在同一读取快照返回日程事实与待确认首分页。
func (a *App) QueryScheduleState(ctx context.Context, date string, now time.Time) (*model.ScheduleQueryResult, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if err := a.ensureScheduleService(); err != nil {
		return nil, err
	}
	if err := a.ensureNotificationService(); err != nil {
		return nil, err
	}
	result, err := a.scheduleService.QueryScheduleState(ctx, date, now)
	if err != nil {
		return nil, err
	}
	if a.Recovery != nil {
		result.Recovery, err = a.Recovery.Snapshot(ctx, a.notificationService, now)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// QueryOccurrences 分页读取事实与未来规则投影。
func (a *App) QueryOccurrences(ctx context.Context, date, cursor string, now time.Time) (model.OccurrencePage, error) {
	if err := a.ensureScheduleService(); err != nil {
		return model.OccurrencePage{}, err
	}
	return a.scheduleService.QueryOccurrences(ctx, date, cursor, now)
}

// QueryCalendar 查询月份日程标记。
func (a *App) QueryCalendar(ctx context.Context, month string, now time.Time) ([]string, error) {
	if err := a.ensureScheduleService(); err != nil {
		return nil, err
	}
	return a.scheduleService.QueryCalendar(ctx, month, now)
}
