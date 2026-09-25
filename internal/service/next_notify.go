package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// NextNotifyService 组合日程查询、规则计算与提醒写入。
type NextNotifyService struct {
	schedules     ScheduleRepo
	notifications NotifyRepo
}

var _ NextNotify = (*NextNotifyService)(nil)

// NewNextNotifyService 创建根据已持久化日程推进提醒的服务。
func NewNextNotifyService(schedules ScheduleRepo, notifications NotifyRepo) (*NextNotifyService, error) {
	if schedules == nil {
		return nil, fmt.Errorf("[service] schedule repo is nil")
	}
	if notifications == nil {
		return nil, fmt.Errorf("[service] notification repo is nil")
	}
	return &NextNotifyService{schedules: schedules, notifications: notifications}, nil
}

// Next 计算严格晚于 after 的下一次触发时间，并幂等创建提醒。
func (s *NextNotifyService) Next(ctx context.Context, scheduleID int, after time.Time) (*model.Notification, error) {
	if scheduleID < 0 {
		return nil, fmt.Errorf("[service] schedule ID must be positive, schedule_id=%d", scheduleID)
	}
	if after.IsZero() {
		return nil, fmt.Errorf("[service] after must not be zero, schedule_id=%d", scheduleID)
	}
	schedule, err := s.schedules.GetScheduleByID(ctx, scheduleID, model.QueryOption{})
	if err != nil {
		return nil, fmt.Errorf("[service] cannot read schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if schedule == nil {
		return nil, fmt.Errorf("[service] schedule repo returned nil, schedule_id=%d", scheduleID)
	}
	notifyAt, hasNext, err := nextNotificationTime(*schedule, after)
	if err != nil {
		return nil, fmt.Errorf("[service] cannot calculate next notification, schedule_id=%d: %w", scheduleID, err)
	}
	if !hasNext {
		return nil, nil
	}
	notification, err := s.notifications.CreateNotification(ctx, scheduleID, notifyAt)
	if err != nil {
		return nil, fmt.Errorf("[service] cannot create next notification, schedule_id=%d, notify_at=%q: %w", scheduleID, notifyAt.Format(time.RFC3339Nano), err)
	}
	return notification, nil
}

type cronKind uint8

const (
	cronDaily cronKind = iota
	cronWeekly
	cronMonthly
)

type cronRule struct {
	kind   cronKind
	minute int
	hour   int
	days   []int
}

// nextNotificationTime 仅计算时间；周期规则按固定 UTC+8 的北京时间解释。
func nextNotificationTime(schedule model.Schedule, after time.Time) (time.Time, bool, error) {
	if err := schedule.Validate(); err != nil {
		return time.Time{}, false, err
	}
	if after.IsZero() {
		return time.Time{}, false, fmt.Errorf("after must not be zero")
	}
	if schedule.Type == model.SingleSchedule {
		if schedule.Cancelled || schedule.Deleted || !schedule.Single.NotifyTime.After(after) {
			return time.Time{}, false, nil
		}
		return schedule.Single.NotifyTime.In(model.BeijingLocation()), true, nil
	}
	rules := make([]cronRule, 0, len(schedule.Period.Cron))
	for _, expression := range schedule.Period.Cron {
		rule, err := parseCronRule(expression)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("invalid cron expression %q: %w", expression, err)
		}
		rules = append(rules, rule)
	}
	if schedule.Cancelled || schedule.Deleted || !schedule.Period.Open {
		return time.Time{}, false, nil
	}
	var earliest time.Time
	for _, rule := range rules {
		candidate := rule.next(after.In(model.BeijingLocation()))
		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
	}
	return earliest, true, nil
}

// parseCronRule 只接受每天、每周列表或每月列表三种五字段形状。
func parseCronRule(expression string) (cronRule, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return cronRule{}, fmt.Errorf("expected five fields")
	}
	minute, err := parseCronNumber(fields[0], 0, 59)
	if err != nil {
		return cronRule{}, fmt.Errorf("invalid minute: %w", err)
	}
	hour, err := parseCronNumber(fields[1], 0, 23)
	if err != nil {
		return cronRule{}, fmt.Errorf("invalid hour: %w", err)
	}
	if fields[3] != "*" {
		return cronRule{}, fmt.Errorf("month must be *")
	}
	rule := cronRule{minute: minute, hour: hour}
	switch {
	case fields[2] == "*" && fields[4] == "*":
		rule.kind = cronDaily
	case fields[2] == "*":
		rule.kind = cronWeekly
		rule.days, err = parseCronList(fields[4], 1, 7)
	case fields[4] == "*":
		rule.kind = cronMonthly
		rule.days, err = parseCronList(fields[2], 1, 31)
	default:
		return cronRule{}, fmt.Errorf("day of month and day of week cannot both be set")
	}
	if err != nil {
		return cronRule{}, err
	}
	return rule, nil
}

// parseCronList 解析 cron 中的多日场景
func parseCronList(field string, min, max int) ([]int, error) {
	parts := strings.Split(field, ",")
	values := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		value, err := parseCronNumber(part, min, max)
		if err != nil {
			return nil, fmt.Errorf("invalid day list: %w", err)
		}
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	sort.Ints(values)
	return values, nil
}

func parseCronNumber(field string, min, max int) (int, error) {
	if field == "" {
		return 0, fmt.Errorf("empty number")
	}
	for i := 0; i < len(field); i++ {
		if field[i] < '0' || field[i] > '9' {
			return 0, fmt.Errorf("%q is not a decimal number", field)
		}
	}
	value, err := strconv.Atoi(field)
	if err != nil {
		return 0, fmt.Errorf("field change failed, err: %w", err)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("cron out of range, field: %q, val: %d", field, value)
	}
	return value, nil
}

func (r cronRule) next(after time.Time) time.Time {
	location := model.BeijingLocation()
	base := after.In(location)
	switch r.kind {
	case cronDaily:
		candidate := time.Date(base.Year(), base.Month(), base.Day(), r.hour, r.minute, 0, 0, location)
		if !candidate.After(base) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate
	case cronWeekly:
		day := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, location)
		for offset := 0; offset <= 7; offset++ {
			candidateDay := day.AddDate(0, 0, offset)
			weekday := int(candidateDay.Weekday())
			if weekday == 0 {
				weekday = 7
			}
			if containsDay(r.days, weekday) {
				candidate := candidateDay.Add(time.Duration(r.hour)*time.Hour + time.Duration(r.minute)*time.Minute)
				if candidate.After(base) {
					return candidate
				}
			}
		}
	case cronMonthly:
		for offset := 0; offset <= 1; offset++ {
			month := time.Date(base.Year(), base.Month()+time.Month(offset), 1, 0, 0, 0, 0, location)
			lastDay := time.Date(month.Year(), month.Month()+1, 0, 0, 0, 0, 0, location).Day()
			for _, requestedDay := range r.days {
				day := requestedDay
				if day > lastDay {
					day = lastDay
				}
				candidate := time.Date(month.Year(), month.Month(), day, r.hour, r.minute, 0, 0, location)
				if candidate.After(base) {
					return candidate
				}
			}
		}
	}
	return time.Time{}
}

func containsDay(days []int, day int) bool {
	for _, candidate := range days {
		if candidate == day {
			return true
		}
	}
	return false
}
