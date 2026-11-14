package app

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

type deliveryTestRepo struct{}

func (deliveryTestRepo) ReadDeliveryMembers(_ context.Context, ids []int, at time.Time, _ bool) ([]*model.Notification, error) {
	name := "测试成员"
	values := make([]*model.Notification, 0, len(ids))
	for _, id := range ids {
		values = append(values, &model.Notification{ID: id, Status: model.NotifyLoaded, NotifyTime: at, SnapshotTaken: true, NameSnapshot: &name})
	}
	return values, nil
}
func (deliveryTestRepo) SaveDeliveryResult(context.Context, []int, model.NotifyStatus) error {
	return nil
}

type dueSchedulerRepo struct{ at time.Time }

func (r dueSchedulerRepo) PrepareNotifications(_ context.Context, w model.NotificationWindow, _ model.PrepareNotificationWindow) (*model.NotificationBatch, error) {
	if !w.FreezeDue {
		return &model.NotificationBatch{}, nil
	}
	name := "测试成员"
	n := &model.Notification{ID: 1, ScheduleID: 1, Status: model.NotifyLoaded, NotifyTime: r.at, SnapshotTaken: true, NameSnapshot: &name}
	return &model.NotificationBatch{Notifications: []*model.Notification{n}, EligibleIDs: []int{1}}, nil
}
