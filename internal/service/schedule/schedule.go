package schedule

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// Repo 定义日程持久化操作。
type Repo interface {
	// ReadUnacknowledged 在同一事务读取范围总数、提醒页及父日程。
	ReadUnacknowledged(context.Context, model.UnacknowledgedRange, model.NotificationPosition, int) ([]*model.Schedule, []*model.Notification, int, error)
	ApplyScheduleChange(context.Context, model.ScheduleChange, model.PrepareScheduleChange) (*model.ScheduleWrite, error)
	// ApplyScheduleDelete 在同一事务检查版本、软删除、停止提醒并保存回执。
	ApplyScheduleDelete(context.Context, model.ScheduleDelete) (*model.ScheduleWrite, error)
	// ReadScheduleReceipt 只读核对请求摘要，未命中返回 nil，不补做业务。
	ReadScheduleReceipt(context.Context, string, string) (*model.ScheduleWrite, error)
	// ReadScheduleState 在同一读取事务获取日程规则与提醒事实，保持响应一致。
	ReadScheduleState(context.Context, time.Time, time.Time, time.Time, model.NotificationPosition, int) ([]*model.Schedule, []*model.Notification, []*model.Notification, int, error)
	// CreateSchedule 创建日程并返回数据库生成的 ID 与时间戳。
	CreateSchedule(ctx context.Context, schedule model.Schedule) (*model.Schedule, error)
	// CreateScheduleWithNotification 在同一事务中创建日程及可选的首条提醒。
	CreateScheduleWithNotification(ctx context.Context, schedule model.Schedule, notifyAt *time.Time) (*model.Schedule, *model.Notification, error)
	// GetScheduleByID 要求单独传入日程 ID，附加选项也参与过滤。
	GetScheduleByID(ctx context.Context, scheduleID int, options ...model.QueryOption) (*model.Schedule, error)
	// GetAllSchedule 的查询条件均可选，按已设置条件分页查询日程。
	GetAllSchedule(ctx context.Context, options ...model.QueryOption) ([]*model.Schedule, error)
	// DeleteScheduleByID 软删除日程并隐藏尚未到时的待投递提醒，保留历史记录。
	DeleteScheduleByID(ctx context.Context, scheduleID int) (*model.ScheduleWrite, error)
}

// Service 执行日程查询与日历投影。
type Service struct{ repo Repo }

// NewService 创建日程服务。
func NewService(repo Repo) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("schedule repo is nil")
	}
	return &Service{repo: repo}, nil
}

// ErrInvalidQuery 表示日期、月份或游标不符合查询契约。
var ErrInvalidQuery = errors.New("invalid schedule or notification query")

// ErrNotPeriodSchedule 表示提醒所属日程不是可推进的周期日程。
var ErrNotPeriodSchedule = errors.New("schedule is not periodic")

// ErrInvalidSchedule 表示日程规则或正式变更请求无效。
var ErrInvalidSchedule = errors.New("invalid schedule")

// ErrScheduleNotFound 表示日程不存在或已删除。
var ErrScheduleNotFound = errors.New("schedule not found")

type queryPosition = model.QueryCursor

// OccurrenceView 是日程服务返回的只读触发视图。
type OccurrenceView = model.OccurrenceView

// OccurrencePage 是日程服务返回的分页视图。
type OccurrencePage = model.OccurrencePage

// ScheduleQueryResult 是日程服务返回的组合快照视图。
type ScheduleQueryResult = model.ScheduleQueryResult

func encodeQueryCursor(value queryPosition) string {
	return model.EncodeQueryCursor(value)
}

func queryCursor(raw, scope string, now time.Time) (queryPosition, error) {
	value, err := model.DecodeQueryCursor(raw, scope, now)
	if err != nil {
		return value, ErrInvalidQuery
	}
	return value, nil
}

func scheduleRange(raw string, month bool, now time.Time) (time.Time, time.Time, error) {
	layout := "2006-01-02"
	if month {
		layout = "2006-01"
	}
	if raw == "" {
		raw = now.In(model.BeijingLocation()).Format(layout)
	}
	start, err := time.ParseInLocation(layout, raw, model.BeijingLocation())
	if err != nil || start.Format(layout) != raw || start.Year() < 1970 || start.Year() > 2200 {
		return time.Time{}, time.Time{}, ErrInvalidQuery
	}
	end := start.AddDate(0, 0, 1)
	if month {
		end = start.AddDate(0, 1, 0)
	}
	return start, end, nil
}

func occurrence(schedule *model.Schedule, n *model.Notification, at time.Time, asOf time.Time) OccurrenceView {
	value := OccurrenceView{
		OccurrenceKey: fmt.Sprintf("%d:%d", schedule.ID, at.UnixNano()),
		ScheduleID:    schedule.ID,
		Name:          schedule.Name,
		Description:   schedule.Description,
		NotifyTime:    at.In(model.BeijingLocation()),
		Source:        "projected",
		Editable:      !schedule.Deleted && !schedule.Cancelled && (schedule.Type == model.PeriodSchedule || at.After(asOf)),
	}
	if n != nil {
		if n.SnapshotTaken && n.NameSnapshot != nil {
			value.Name = *n.NameSnapshot
			value.Description = n.DescriptionSnapshot
		}
		value.Source = "persisted"
		value.NotificationID = &n.ID
		value.DeliveryStatus = &n.Status
		value.AcknowledgedAt = n.AcknowledgedAt
	}
	return value
}

func projectOccurrences(ctx context.Context, rules []*model.Schedule, notifications []*model.Notification, start, end, asOf time.Time) ([]OccurrenceView, error) {
	schedules := map[int]*model.Schedule{}
	for _, s := range rules {
		schedules[s.ID] = s
	}
	values := map[string]OccurrenceView{}
	for _, n := range notifications {
		if s := schedules[n.ScheduleID]; s != nil {
			// 父日程的启停状态不抹去历史；未来事实仍遵循当前有效规则。
			if n.NotifyTime.After(asOf) && (s.Deleted || s.Cancelled || (s.Period != nil && !s.Period.Open) || (s.Single != nil && (s.Single.NotifyTime.Second() != 0 || s.Single.NotifyTime.Nanosecond() != 0))) {
				continue
			}
			v := occurrence(s, n, n.NotifyTime, asOf)
			values[v.OccurrenceKey] = v
		}
	}
	for _, s := range rules {
		// 旧版秒级单次只保留持久化事实，不作为新版本的有效未来规则展开。
		if s.Single != nil && (s.Single.NotifyTime.Second() != 0 || s.Single.NotifyTime.Nanosecond() != 0) {
			continue
		}
		after := start.Add(-time.Nanosecond)
		if asOf.After(after) {
			after = asOf
		}
		if s.UpdatedAt.After(after) {
			after = s.UpdatedAt
		}
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			at, ok, err := nextNotificationTime(*s, after)
			if err != nil {
				return nil, err
			}
			if !ok || !at.Before(end) {
				break
			}
			v := occurrence(s, nil, at, asOf)
			if _, exists := values[v.OccurrenceKey]; !exists {
				values[v.OccurrenceKey] = v
			}
			after = at
		}
	}
	result := make([]OccurrenceView, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].NotifyTime.Equal(result[j].NotifyTime) {
			return result[i].ScheduleID < result[j].ScheduleID
		}
		return result[i].NotifyTime.Before(result[j].NotifyTime)
	})
	return result, nil
}

func occurrencePage(values []OccurrenceView, cursor queryPosition, limit int) OccurrencePage {
	page := OccurrencePage{Items: []OccurrenceView{}, Total: len(values), AsOf: cursor.AsOf}
	for _, v := range values {
		if cursor.ID > 0 && (v.NotifyTime.Before(cursor.Time) || (v.NotifyTime.Equal(cursor.Time) && v.ScheduleID <= cursor.ID)) {
			continue
		}
		if len(page.Items) == limit {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = encodeQueryCursor(queryPosition{Scope: cursor.Scope, AsOf: cursor.AsOf, Time: last.NotifyTime, ID: last.ScheduleID})
			break
		}
		page.Items = append(page.Items, v)
	}
	return page
}

// dateOccurrencePage 在分页前统计当天启用日程，确认状态和同规则触发次数不改变日程数。
func dateOccurrencePage(values []OccurrenceView, rules []*model.Schedule, cursor queryPosition, limit int) OccurrencePage {
	open := make(map[int]bool, len(rules))
	for _, rule := range rules {
		open[rule.ID] = !rule.Deleted && !rule.Cancelled && rule.Validate() == nil &&
			(rule.Type == model.SingleSchedule || (rule.Period != nil && rule.Period.Open))
	}
	ids := make(map[int]struct{})
	for _, value := range values {
		if open[value.ScheduleID] {
			ids[value.ScheduleID] = struct{}{}
		}
	}
	count := len(ids)
	page := occurrencePage(values, cursor, limit)
	page.OpenScheduleCount = &count
	return page
}

func pendingPage(rules []*model.Schedule, notifications []*model.Notification, total int, cursor queryPosition, limit int) OccurrencePage {
	schedules := map[int]*model.Schedule{}
	for _, s := range rules {
		schedules[s.ID] = s
	}
	page := OccurrencePage{Items: []OccurrenceView{}, Total: total, AsOf: cursor.AsOf}
	for i, n := range notifications {
		if i == limit {
			last := notifications[i-1]
			page.NextCursor = encodeQueryCursor(queryPosition{Scope: cursor.Scope, AsOf: cursor.AsOf, Time: last.NotifyTime, ID: last.ID})
			break
		}
		page.Items = append(page.Items, occurrence(schedules[n.ScheduleID], n, n.NotifyTime, cursor.AsOf))
	}
	return page
}

// QueryScheduleState 组合两张表的同一读取快照，返回日期触发与待确认首分页。
func (s *Service) QueryScheduleState(ctx context.Context, date string, now time.Time) (*ScheduleQueryResult, error) {
	start, end, err := scheduleRange(date, false, now)
	if err != nil {
		return nil, err
	}
	now = now.In(model.BeijingLocation())
	schedules, notifications, pending, total, err := s.repo.ReadScheduleState(ctx, start, end, now, model.NotificationPosition{}, 51)
	if err != nil {
		return nil, err
	}
	values, err := projectOccurrences(ctx, schedules, notifications, start, end, now)
	if err != nil {
		return nil, err
	}
	return &ScheduleQueryResult{
		Date:           start.Format("2006-01-02"),
		AsOf:           now,
		Occurrences:    dateOccurrencePage(values, schedules, queryPosition{Scope: "date:" + start.Format("2006-01-02"), AsOf: now}, 50),
		Unacknowledged: pendingPage(schedules, pending, total, queryPosition{Scope: "pending", AsOf: now}, 50),
	}, nil
}

// QueryOccurrences 对事实和未来投影分页，游标绑定日期及读取时刻。
func (s *Service) QueryOccurrences(ctx context.Context, date, rawCursor string, now time.Time) (OccurrencePage, error) {
	start, end, err := scheduleRange(date, false, now)
	if err != nil {
		return OccurrencePage{}, err
	}
	cursor, err := queryCursor(rawCursor, "date:"+start.Format("2006-01-02"), now)
	if err != nil {
		return OccurrencePage{}, err
	}
	schedules, notifications, _, _, err := s.repo.ReadScheduleState(ctx, start, end, cursor.AsOf, model.NotificationPosition{}, 0)
	if err != nil {
		return OccurrencePage{}, err
	}
	values, err := projectOccurrences(ctx, schedules, notifications, start, end, cursor.AsOf)
	if err != nil {
		return OccurrencePage{}, err
	}
	return dateOccurrencePage(values, schedules, cursor, 50), nil
}

// QueryCalendar 一次读取月份事实和规则，返回日期标记。
func (s *Service) QueryCalendar(ctx context.Context, month string, now time.Time) ([]string, error) {
	start, end, err := scheduleRange(month, true, now)
	if err != nil {
		return nil, err
	}
	schedules, notifications, _, _, err := s.repo.ReadScheduleState(ctx, start, end, now, model.NotificationPosition{}, 0)
	if err != nil {
		return nil, err
	}
	values, err := projectOccurrences(ctx, schedules, notifications, start, end, now)
	if err != nil {
		return nil, err
	}
	dates := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		date := value.NotifyTime.Format("2006-01-02")
		if !seen[date] {
			seen[date] = true
			dates = append(dates, date)
		}
	}
	return dates, nil
}
