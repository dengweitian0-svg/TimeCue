package notification

import (
	"context"
	"errors"
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
	service := &Service{Delivery: d, Scheduler: s}
	ctx := context.Background()
	claims, err := service.ClaimDelivery(ctx, 20)
	if err != nil || len(claims) != 20 {
		t.Fatal("未按完整任务限制", len(claims), err)
	}
	for _, claim := range claims {
		if claim.NotifyTime.After(now) {
			t.Fatal("未来任务被领取")
		}
		if _, err := service.StartDelivery(ctx, claim.AttemptID); err != nil {
			t.Fatal(err)
		}
	}
	last, err := service.ClaimDelivery(ctx, 20)
	if err != nil || len(last) != 1 {
		t.Fatal("剩余任务遗失或未来任务泄漏", last, err)
	}
}

// TestRecoveryRetireRemovesExitedAttemptsOnlyForRetiredTask 验证退役只清理该恢复任务的旧 attempt。
func TestRecoveryRetireRemovesExitedAttemptsOnlyForRetiredTask(t *testing.T) {
	now := time.Date(2030, 10, 9, 10, 0, 0, 0, model.BeijingLocation())
	delivery, err := NewDeliveryService(deliveryTestRepo{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Delivery: delivery}
	service.RecoveryCreateTask("retired-task", now, []int{1})
	service.RecoveryCreateTask("surviving-task", now, []int{2})
	if err := service.RecoveryCompleteTask(context.Background(), "retired-task", now); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoveryCompleteTask(context.Background(), "surviving-task", now); err != nil {
		t.Fatal(err)
	}

	first, err := service.ClaimDelivery(context.Background(), 20)
	if err != nil || len(first) != 2 {
		t.Fatalf("首次claim失败: %#v %v", first, err)
	}
	var retiredInitial, survivorCurrent string
	for _, claim := range first {
		switch claim.DeliveryTaskID {
		case "retired-task":
			retiredInitial = claim.AttemptID
		case "surviving-task":
			survivorCurrent = claim.AttemptID
		}
	}
	if retiredInitial == "" || survivorCurrent == "" {
		t.Fatalf("缺少预期任务: %#v", first)
	}
	service.DeliveryExecutorExitedAttempts([]string{retiredInitial})

	second, err := service.ClaimDelivery(context.Background(), 20)
	if err != nil || len(second) != 2 {
		t.Fatalf("执行者退出后重复claim失败: %#v %v", second, err)
	}
	var retiredSecond string
	for _, claim := range second {
		if claim.DeliveryTaskID == "retired-task" && claim.AttemptID != retiredInitial {
			retiredSecond = claim.AttemptID
		}
	}
	if retiredSecond == "" {
		t.Fatalf("未重新领取退役前任务: %#v", second)
	}
	service.DeliveryExecutorExitedAttempts([]string{retiredSecond})

	if _, ok := service.RecoveryRetireTask("retired-task"); !ok {
		t.Fatal("未退役恢复任务")
	}
	for _, attemptID := range []string{retiredInitial, retiredSecond} {
		if _, err := service.StartDelivery(context.Background(), attemptID); !errors.Is(err, ErrAttemptNotFound) {
			t.Fatalf("退役后旧attempt仍可启动: %s: %v", attemptID, err)
		}
	}
	started, err := service.StartDelivery(context.Background(), survivorCurrent)
	if err != nil || !started.ShouldDisplay {
		t.Fatalf("其他任务的当前attempt受影响: %#v %v", started, err)
	}
}
