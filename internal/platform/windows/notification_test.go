//go:build windows

package windows

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// fakeNotificationRuntime 记录 Wails 调用并允许测试控制在途提交。
type fakeNotificationRuntime struct {
	mu          sync.Mutex
	initialized int
	options     []wailsruntime.NotificationOptions
	callback    func(wailsruntime.NotificationResult)
	entered     chan struct{}
	release     chan struct{}
}

// InitializeNotifications 记录通知初始化调用。
func (f *fakeNotificationRuntime) InitializeNotifications(context.Context) error {
	f.mu.Lock()
	f.initialized++
	f.mu.Unlock()
	return nil
}

// SendNotification 记录选项，并按测试配置阻塞在途提交。
func (f *fakeNotificationRuntime) SendNotification(_ context.Context, options wailsruntime.NotificationOptions) error {
	f.mu.Lock()
	f.options = append(f.options, options)
	entered, release := f.entered, f.release
	f.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if release != nil {
		<-release
	}
	return nil
}

// OnNotificationResponse 保存 Wails 通知响应回调。
func (f *fakeNotificationRuntime) OnNotificationResponse(_ context.Context, callback func(wailsruntime.NotificationResult)) {
	f.mu.Lock()
	f.callback = callback
	f.mu.Unlock()
}

// callbackSnapshot 读取已注册响应回调。
func (f *fakeNotificationRuntime) callbackSnapshot() func(wailsruntime.NotificationResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callback
}

// fakeActivationPublisher 记录通知激活回流。
type fakeActivationPublisher struct {
	mu          sync.Mutex
	activations []platform.Activation
}

// Publish 保存一条激活事实。
func (p *fakeActivationPublisher) Publish(activation platform.Activation) error {
	p.mu.Lock()
	p.activations = append(p.activations, activation)
	p.mu.Unlock()
	return nil
}

// snapshot 返回已发布激活的副本。
func (p *fakeActivationPublisher) snapshot() []platform.Activation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]platform.Activation(nil), p.activations...)
}

// TestNotificationDataPreservesIdentity 验证通知数据只含稳定身份且 ID 使用十进制字符串。
func TestNotificationDataPreservesIdentity(t *testing.T) {
	identity := domain.OccurrenceIdentity{ScheduleID: 9007199254740991, OriginalDate: "2026-09-16"}
	data, err := notificationDataForIdentity(identity)
	if err != nil {
		t.Fatalf("notificationDataForIdentity() error = %v", err)
	}
	want := map[string]interface{}{"schedule_id": "9007199254740991", "original_date": "2026-09-16"}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("notification data = %#v, want %#v", data, want)
	}
	parsed, err := parseNotificationIdentity(data)
	if err != nil || parsed != identity {
		t.Fatalf("parseNotificationIdentity() = %+v, %v; want %+v", parsed, err, identity)
	}
}

// TestWailsNotificationCenterSubmitsEachReminder 验证每个发生独立提交且不聚合身份数据。
func TestWailsNotificationCenterSubmitsEachReminder(t *testing.T) {
	runtime := &fakeNotificationRuntime{}
	center, err := NewWailsNotificationCenter(WailsNotificationCenterOptions{Runtime: runtime, Cleaner: NotificationHistoryCleanerFunc(func(context.Context) error { return nil })})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := center.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		if err := center.Submit(ctx, app.ReminderNotification{Identity: domain.OccurrenceIdentity{ScheduleID: domain.ScheduleID(id), OriginalDate: "2026-09-16"}, Title: "title", Notes: "notes"}); err != nil {
			t.Fatal(err)
		}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.options) != 2 {
		t.Fatalf("SendNotification calls = %d, want 2", len(runtime.options))
	}
	for _, options := range runtime.options {
		if len(options.Data) != 2 {
			t.Fatalf("notification data = %#v, want exactly two fields", options.Data)
		}
	}
}

// TestWailsNotificationCenterResponseConversion 验证 UserInfo 严格转换为 Toast 激活事实。
func TestWailsNotificationCenterResponseConversion(t *testing.T) {
	runtime := &fakeNotificationRuntime{}
	publisher := &fakeActivationPublisher{}
	center, err := NewWailsNotificationCenter(WailsNotificationCenterOptions{Runtime: runtime, Cleaner: NotificationHistoryCleanerFunc(func(context.Context) error { return nil }), ActivationPublisher: publisher})
	if err != nil {
		t.Fatal(err)
	}
	if err := center.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime.callbackSnapshot()(wailsruntime.NotificationResult{Response: wailsruntime.NotificationResponse{UserInfo: map[string]interface{}{"schedule_id": "7", "original_date": "2026-09-16"}}})
	want := platform.Activation{Kind: platform.ActivationKindToast, Target: &domain.OccurrenceIdentity{ScheduleID: 7, OriginalDate: "2026-09-16"}}
	if got := publisher.snapshot(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("published activations = %+v, want %+v", got, want)
	}
}

// TestWailsNotificationCenterWaitsInflight 验证清理先停止新提交，再等待在途提交结算。
func TestWailsNotificationCenterWaitsInflight(t *testing.T) {
	runtime := &fakeNotificationRuntime{entered: make(chan struct{}), release: make(chan struct{})}
	cleaned := make(chan struct{})
	center, err := NewWailsNotificationCenter(WailsNotificationCenterOptions{Runtime: runtime, Cleaner: NotificationHistoryCleanerFunc(func(context.Context) error { close(cleaned); return nil })})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := center.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	submitted := make(chan error, 1)
	go func() {
		submitted <- center.Submit(ctx, app.ReminderNotification{Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-09-16"}, Title: "title"})
	}()
	<-runtime.entered
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- center.Cleanup(ctx) }()
	select {
	case <-cleaned:
		t.Fatal("cleanup ran before in-flight submission completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(runtime.release)
	if err := <-submitted; err != nil {
		t.Fatal(err)
	}
	if err := <-cleanupDone; err != nil {
		t.Fatal(err)
	}
	if err := center.Submit(ctx, app.ReminderNotification{Identity: domain.OccurrenceIdentity{ScheduleID: 2, OriginalDate: "2026-09-16"}}); err == nil {
		t.Fatal("Submit() error = nil after Cleanup")
	}
}

// TestWailsNotificationCenterCleanupRetryAndClose 验证清理失败可重试且 Close 幂等。
func TestWailsNotificationCenterCleanupRetryAndClose(t *testing.T) {
	runtime := &fakeNotificationRuntime{}
	attempts := 0
	center, err := NewWailsNotificationCenter(WailsNotificationCenterOptions{Runtime: runtime, Cleaner: NotificationHistoryCleanerFunc(func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary history failure")
		}
		return nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := center.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := center.Cleanup(context.Background()); err == nil {
		t.Fatal("Cleanup() error = nil on first cleaner failure")
	}
	if err := center.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := center.Close(); err != nil {
		t.Fatal(err)
	}
	if err := center.Close(); err != nil {
		t.Fatal(err)
	}
}
