package service

import (
	"context"
	"testing"
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

// TestDeliveryClaimTaskLimitAndFuture 验证按任务限制领取，不限制成员且不提前开放。
func TestDeliveryClaimTaskLimitAndFuture(t *testing.T) {
	now := time.Date(2030, 10, 9, 10, 0, 0, 0, model.BeijingLocation())
	d, _ := NewDeliveryService(deliveryTestRepo{}, func() time.Time { return now })
	s := &Scheduler{groups: make(map[int64]MinuteGroup)}
	for i := 0; i < 22; i++ {
		at := now.Add(time.Duration(i-20) * time.Minute)
		s.groups[at.UnixNano()] = MinuteGroup{Minute: at, FactIDs: []int{i + 1}, MemberIDs: []int{i + 1}}
	}
	app := &App{Delivery: d, Scheduler: s}
	ctx := context.Background()
	claims, err := app.ClaimDelivery(ctx, 20)
	if err != nil || len(claims) != 20 {
		t.Fatal("未按完整任务限制", len(claims), err)
	}
	for _, claim := range claims {
		if claim.NotifyTime.After(now) {
			t.Fatal("未来任务被领取")
		}
		if _, err := app.StartDelivery(ctx, claim.AttemptID); err != nil {
			t.Fatal(err)
		}
	}
	last, err := app.ClaimDelivery(ctx, 20)
	if err != nil || len(last) != 1 {
		t.Fatal("剩余任务遗失或未来任务泄漏", last, err)
	}
}
