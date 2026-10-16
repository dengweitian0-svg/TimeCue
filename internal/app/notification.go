package app

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
)

// QueryUnacknowledged 查询全历史到期未确认记录。
func (a *App) QueryUnacknowledged(ctx context.Context, cursor string, now time.Time) (model.OccurrencePage, error) {
	if err := a.ensureNotificationService(); err != nil {
		return model.OccurrencePage{}, err
	}
	return a.notificationService.QueryUnacknowledged(ctx, cursor, now)
}

// QueryUnacknowledgedScope 按范围分页查询未确认记录。
func (a *App) QueryUnacknowledgedScope(ctx context.Context, scope, cursor string, now time.Time) (model.OccurrencePage, error) {
	if err := a.ensureNotificationService(); err != nil {
		return model.OccurrencePage{}, err
	}
	return a.notificationService.QueryUnacknowledgedScope(ctx, scope, cursor, now)
}

// GetNotification 返回包含历史父日程信息的提醒视图。
func (a *App) GetNotification(ctx context.Context, id int, now time.Time) (*model.OccurrenceView, error) {
	if err := a.ensureNotificationService(); err != nil {
		return nil, err
	}
	return a.notificationService.GetNotification(ctx, id, now)
}

// AcknowledgeNotifications 原子确认明确的提醒集合。
func (a *App) AcknowledgeNotifications(ctx context.Context, ids []int, now time.Time) ([]*model.Notification, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if err := a.ensureNotificationService(); err != nil {
		return nil, err
	}
	a.syncServices()
	return a.notificationService.AcknowledgeNotifications(ctx, ids, now)
}

// ClaimDelivery 接纳一组到期完整分钟任务。
func (a *App) ClaimDelivery(ctx context.Context, limit int) ([]notification.DeliveryClaim, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	defer a.syncServices()
	if err := a.notificationService.CheckClaimReady(ctx, limit); err != nil {
		return nil, err
	}
	if err := a.prepareRecoveryLocked(ctx, a.notificationService.Now().In(model.BeijingLocation())); err != nil {
		return nil, err
	}
	claims, err := a.notificationService.ClaimDelivery(ctx, limit)
	a.syncServices()
	return claims, err
}

// StartDelivery 冻结实际投递成员并确定展示内容。
func (a *App) StartDelivery(ctx context.Context, id string) (notification.DeliveryStart, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	defer a.syncServices()
	if err := a.notificationService.CheckStartReady(ctx, id); err != nil {
		return notification.DeliveryStart{}, err
	}
	if err := a.prepareRecoveryLocked(ctx, a.notificationService.Now().In(model.BeijingLocation())); err != nil {
		return notification.DeliveryStart{}, err
	}
	start, err := a.notificationService.StartDelivery(ctx, id)
	a.syncServices()
	return start, err
}

// CompleteDelivery 回填投递结果。
func (a *App) CompleteDelivery(ctx context.Context, id string, outcome model.NotifyStatus) (notification.DeliveryResult, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	defer a.syncServices()
	return a.notificationService.CompleteDelivery(ctx, id, outcome)
}

// DeliveryExecutorExitedAttempts 整理执行者明确提交的活动 attempt。
func (a *App) DeliveryExecutorExitedAttempts(ids []string) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	defer a.syncServices()
	a.notificationService.DeliveryExecutorExitedAttempts(ids)
}

// DeliveryExecutorExited 整理所有当前活动的执行 attempt。
func (a *App) DeliveryExecutorExited() {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	defer a.syncServices()
	a.notificationService.DeliveryExecutorExited()
}

// DeliverySnapshot 返回进程内投递状态副本。
func (a *App) DeliverySnapshot() []notification.DeliveryTaskView {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	defer a.syncServices()
	return a.notificationService.DeliverySnapshot()
}

// DeliveryAttemptMembers 返回一次尝试冻结的提醒 ID。
func (a *App) DeliveryAttemptMembers(id string) ([]int, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	return a.notificationService.DeliveryAttemptMembers(id)
}

// QueryDeliveryMembers 分页查询任务历史或尝试成员。
func (a *App) QueryDeliveryMembers(ctx context.Context, taskID, attemptID, cursor string, now time.Time) (model.OccurrencePage, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	page, err := a.notificationService.QueryDeliveryMembers(ctx, taskID, attemptID, cursor, now)
	if err != nil {
		return model.OccurrencePage{}, err
	}
	if a.Recovery != nil {
		page.Recovery, err = a.Recovery.SnapshotForTask(ctx, taskID, a.notificationService, now)
	}
	return page, err
}

// StopDelivery 阻止新的领取和开始。
func (a *App) StopDelivery() {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.syncServices()
	a.notificationService.StopDelivery()
}
