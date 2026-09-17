package windows

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// ActivationPublisher 是通知激活回流所需的最小发布端口。
type ActivationPublisher interface {
	Publish(platform.Activation) error
}

// NotificationHistoryCleaner 清理当前应用的系统通知历史；失败必须可重试。
type NotificationHistoryCleaner interface {
	Clear(context.Context) error
}

// NotificationHistoryCleanerFunc 将函数适配为通知历史清理端口，便于测试和替换实现。
type NotificationHistoryCleanerFunc func(context.Context) error

// Clear 执行注入的通知历史清理函数。
func (f NotificationHistoryCleanerFunc) Clear(ctx context.Context) error {
	if f == nil {
		return nil
	}
	return f(ctx)
}

// notificationDataKeys 是 Wails payload 中允许回流的唯一两个稳定身份字段。
var notificationDataKeys = map[string]struct{}{
	"schedule_id":   {},
	"original_date": {},
}

// notificationDataForIdentity 生成不会丢失 int64 精度的通知身份载荷。
func notificationDataForIdentity(identity domain.OccurrenceIdentity) (map[string]interface{}, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"schedule_id":   strconv.FormatInt(int64(identity.ScheduleID), 10),
		"original_date": identity.OriginalDate,
	}, nil
}

// parseNotificationIdentity 严格解析通知回流身份，拒绝额外字段和非规范类型。
func parseNotificationIdentity(data map[string]interface{}) (domain.OccurrenceIdentity, error) {
	if len(data) != len(notificationDataKeys) {
		return domain.OccurrenceIdentity{}, errors.New("notification data must contain only schedule_id and original_date")
	}
	for key := range data {
		if _, ok := notificationDataKeys[key]; !ok {
			return domain.OccurrenceIdentity{}, fmt.Errorf("notification data contains unsupported field %q", key)
		}
	}
	scheduleID, ok := data["schedule_id"].(string)
	if !ok {
		return domain.OccurrenceIdentity{}, errors.New("notification schedule_id must be a decimal string")
	}
	parsedID, err := parseScheduleID(scheduleID)
	if err != nil {
		return domain.OccurrenceIdentity{}, err
	}
	originalDate, ok := data["original_date"].(string)
	if !ok {
		return domain.OccurrenceIdentity{}, errors.New("notification original_date must be a string")
	}
	if err := validateOriginalDate(originalDate); err != nil {
		return domain.OccurrenceIdentity{}, err
	}
	return domain.OccurrenceIdentity{ScheduleID: domain.ScheduleID(parsedID), OriginalDate: originalDate}, nil
}

// notificationOptionsForReminder 将一条发生转换为一条无确认语义的通知选项。
func notificationOptionsForReminder(notification app.ReminderNotification) (notificationOptions, error) {
	data, err := notificationDataForIdentity(notification.Identity)
	if err != nil {
		return notificationOptions{}, fmt.Errorf("convert reminder notification identity: %w", err)
	}
	return notificationOptions{
		ID:    scheduleNotificationID(notification.Identity),
		Title: notification.Title,
		Body:  notification.Notes,
		Data:  data,
	}, nil
}

// scheduleNotificationID 生成稳定且不依赖显示文本的 Wails 通知 ID。
func scheduleNotificationID(identity domain.OccurrenceIdentity) string {
	return "timecue/" + strconv.FormatInt(int64(identity.ScheduleID), 10) + "/" + identity.OriginalDate
}

// notificationOptions 复刻 Wails v2.15 NotificationOptions 的最小公共形状，供非 Windows stub 编译。
type notificationOptions struct {
	ID    string
	Title string
	Body  string
	Data  map[string]interface{}
}

// notificationResponseActivation 将稳定身份包装成 Toast 激活事实。
func notificationResponseActivation(data map[string]interface{}) (platform.Activation, error) {
	identity, err := parseNotificationIdentity(data)
	if err != nil {
		return platform.Activation{}, fmt.Errorf("parse notification response: %w", err)
	}
	return platform.Activation{Kind: platform.ActivationKindToast, Target: &identity}, nil
}

// responseErrorState 保存异步 Wails 回调无法直接返回的转换或发布错误。
type responseErrorState struct {
	mu  sync.Mutex
	err error
}

// set 设置最近一次异步响应错误，供宿主边界记录而不伪装成清理失败。
func (s *responseErrorState) set(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// get 返回最近一次异步响应错误。
func (s *responseErrorState) get() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
