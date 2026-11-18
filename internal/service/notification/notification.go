package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// NotifyRepo 定义提醒记录的基础持久化操作。
type NotifyRepo interface {
	// GetNotificationByID 读取一次提醒，用于历史详情。
	GetNotificationByID(context.Context, int) (*model.Notification, error)
	// AcknowledgeNotifications 原子确认明确提醒集合，保留首次时间与投递结果。
	AcknowledgeNotifications(context.Context, []int, time.Time) ([]*model.Notification, error)
	// CreateNotification 检查日程状态并幂等创建一条提醒。
	CreateNotification(ctx context.Context, scheduleID int, notifyAt time.Time) (*model.Notification, error)
	// GetNotificationByStartTime 查询时间范围内的待处理提醒记录。
	GetNotificationByStartTime(ctx context.Context, startTime time.Time, endTime time.Time) ([]*model.Notification, error)
	// DeleteNotificationByID 根据提醒 ID 软删除提醒。
	DeleteNotificationByID(ctx context.Context, notificationID int) error
}

// Repo 汇总提醒模块需要的持久化端口。
type Repo interface {
	NotifyRepo
	NotificationSchedulerRepo
	DeliveryRepo
	NotificationMembersRepo
}

// ScheduleReader 提供提醒展示和推进所需的日程只读端口。
type ScheduleReader interface {
	GetScheduleByID(context.Context, int, ...model.QueryOption) (*model.Schedule, error)
	ReadUnacknowledged(context.Context, model.UnacknowledgedRange, model.NotificationPosition, int) ([]*model.Schedule, []*model.Notification, int, error)
}

// Service 管理提醒确认、调度、投递和成员视图。
type Service struct {
	repo       Repo
	schedules  ScheduleReader
	NextNotify *NextNotifyService
	Scheduler  *Scheduler
	Delivery   *DeliveryService
}

// NewService 创建提醒服务及规则推进端口。
func NewService(schedules ScheduleReader, repo Repo) (*Service, error) {
	if schedules == nil || repo == nil {
		return nil, fmt.Errorf("schedule and notification repos are required")
	}
	next, err := NewNextNotifyService(schedules, repo)
	if err != nil {
		return nil, err
	}
	return &Service{repo: repo, schedules: schedules, NextNotify: next}, nil
}

// NextNotify 按日程 ID 计算严格晚于 after 的下一次触发，并幂等写入提醒。
type NextNotify interface {
	Next(ctx context.Context, scheduleID int, after time.Time) (*model.Notification, error)
}

// OccurrencePage 是提醒服务返回的分页视图。
type OccurrencePage = model.OccurrencePage

// OccurrenceView 是提醒服务返回的只读视图。
type OccurrenceView = model.OccurrenceView

var ErrInvalidQuery = schedule.ErrInvalidQuery

// QueryUnacknowledged 分页读取全历史到期未确认记录，不检查父日程启停或投递成功。
func (s *Service) QueryUnacknowledged(ctx context.Context, rawCursor string, now time.Time) (OccurrencePage, error) {
	return s.QueryUnacknowledgedScope(ctx, "", rawCursor, now)
}

// QueryUnacknowledgedScope 按原计划时间分类，续页绑定范围和北京时间日期。
func (s *Service) QueryUnacknowledgedScope(ctx context.Context, scope, rawCursor string, now time.Time) (OccurrencePage, error) {
	if scope != "" && scope != "today" && scope != "history" {
		return OccurrencePage{}, ErrInvalidQuery
	}
	now = now.In(model.BeijingLocation())
	purpose := "pending"
	if scope != "" {
		purpose += ":" + scope + ":" + now.Format("2006-01-02")
	}
	cursor, err := model.DecodeQueryCursor(rawCursor, purpose, now)
	if err != nil {
		return OccurrencePage{}, ErrInvalidQuery
	}
	rangeQuery := model.UnacknowledgedRange{AsOf: cursor.AsOf}
	if scope != "" {
		if cursor.AsOf.Format("2006-01-02") != now.Format("2006-01-02") {
			return OccurrencePage{}, ErrInvalidQuery
		}
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, model.BeijingLocation())
		if scope == "today" {
			rangeQuery.Start = midnight
		} else {
			rangeQuery.End = midnight
		}
	}
	schedules, pending, total, err := s.schedules.ReadUnacknowledged(ctx, rangeQuery, model.NotificationPosition{Time: cursor.Time, ID: cursor.ID}, 51)
	if err != nil {
		return OccurrencePage{}, err
	}
	return makePendingPage(schedules, pending, total, cursor, 50), nil
}

// GetNotification 保留已删除父日程的历史只读内容。
func (s *Service) GetNotification(ctx context.Context, id int, now time.Time) (*OccurrenceView, error) {
	n, err := s.repo.GetNotificationByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.Deleted {
		return nil, model.ErrRecordNotFound
	}
	rule, err := s.schedules.GetScheduleByID(ctx, n.ScheduleID, model.WithScheduleIncludeDeleted(true))
	if err != nil {
		return nil, err
	}
	v := makeOccurrence(rule, n, n.NotifyTime, now)
	return &v, nil
}

// AcknowledgeNotifications 对明确的提醒集合原子确认，重复请求保留首次时间。
func (s *Service) AcknowledgeNotifications(ctx context.Context, ids []int, now time.Time) ([]*model.Notification, error) {
	result, err := s.repo.AcknowledgeNotifications(ctx, ids, now)
	if err == nil && s.Scheduler != nil {
		s.Scheduler.NotificationsAcknowledged(result)
	}
	return result, err
}

func makePendingPage(rules []*model.Schedule, notifications []*model.Notification, total int, cursor model.QueryCursor, limit int) model.OccurrencePage {
	byID := make(map[int]*model.Schedule, len(rules))
	for _, rule := range rules {
		byID[rule.ID] = rule
	}
	page := model.OccurrencePage{Items: []model.OccurrenceView{}, Total: total, AsOf: cursor.AsOf}
	for i, n := range notifications {
		if i == limit {
			last := notifications[i-1]
			cursor.Time, cursor.ID = last.NotifyTime, last.ID
			page.NextCursor = model.EncodeQueryCursor(cursor)
			break
		}
		page.Items = append(page.Items, makeOccurrence(byID[n.ScheduleID], n, n.NotifyTime, cursor.AsOf))
	}
	return page
}

func makeOccurrence(rule *model.Schedule, n *model.Notification, at, asOf time.Time) model.OccurrenceView {
	return schedule.OccurrenceForDisplay(rule, n, at, asOf)
}
