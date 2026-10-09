package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

var (
	ErrTaskNotFound   = errors.New("delivery task not found in this process")
	ErrMembersChanged = errors.New("delivery task members changed; refresh first page")
)

// NotificationMembersRepo 读取受控成员的同一存储快照，不依赖内存任务实现。
type NotificationMembersRepo interface {
	ReadNotificationMembers(context.Context, []int, model.NotificationPosition, int) ([]*model.Schedule, []*model.Notification, int, error)
}

// QueryDeliveryMembers 查询任务历史或指定尝试的冻结集合，不触发执行或确认。
func (a *App) QueryDeliveryMembers(ctx context.Context, taskID, attemptID, rawCursor string, now time.Time) (OccurrencePage, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if taskID == "" {
		return OccurrencePage{}, ErrInvalidQuery
	}
	var task *deliveryTask
	if a.Delivery != nil {
		for _, value := range a.Delivery.allTasks() {
			if value.id == taskID && !value.transferred {
				task = value
				break
			}
		}
	}
	if task == nil {
		return OccurrencePage{}, ErrTaskNotFound
	}
	ids, version := task.facts, task.memberVersion
	if attemptID != "" {
		at := a.Delivery.attempts[attemptID]
		if at == nil || at.task != task {
			return OccurrencePage{}, ErrAttemptNotFound
		}
		if at.start == nil {
			return OccurrencePage{}, ErrDeliveryConflict
		}
		ids, version = at.members, 0
	}
	scope := fmt.Sprintf("task:%s/attempt:%s", taskID, attemptID)
	cursor, err := queryCursor(rawCursor, scope, now)
	if err != nil {
		return OccurrencePage{}, err
	}
	if rawCursor != "" && cursor.MemberVersion != version {
		return OccurrencePage{}, ErrMembersChanged
	}
	cursor.MemberVersion = version
	repo, ok := a.Notify.(NotificationMembersRepo)
	if !ok {
		return OccurrencePage{}, fmt.Errorf("notification repo does not support member queries")
	}
	rules, values, total, err := repo.ReadNotificationMembers(ctx, ids, model.NotificationPosition{Time: cursor.Time, ID: cursor.ID}, 51)
	if err != nil {
		return OccurrencePage{}, err
	}
	byID := make(map[int]*model.Schedule, len(rules))
	for _, s := range rules {
		byID[s.ID] = s
	}
	page := OccurrencePage{Items: []OccurrenceView{}, Total: total, AsOf: cursor.AsOf}
	for i, n := range values {
		if i == 50 {
			last := values[i-1]
			cursor.Time, cursor.ID = last.NotifyTime, last.ID
			page.NextCursor = encodeQueryCursor(cursor)
			break
		}
		page.Items = append(page.Items, occurrence(byID[n.ScheduleID], n, n.NotifyTime, cursor.AsOf))
	}
	if task.recovery != nil {
		page.Recovery, err = a.recoveryViewLocked(ctx, task.recovery, now)
	}
	return page, err
}
