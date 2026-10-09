package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// SchedulerOptions 允许测试控制时钟与等待；错误回调不能改写业务结果。
type SchedulerOptions struct {
	Now     func() time.Time
	Wait    func(context.Context, time.Duration, <-chan struct{}) error
	OnError func(error)
}

// MinuteGroup 是完成到期核验后的受控分钟视图，成员尚未冻结为展示尝试。
type MinuteGroup struct {
	Minute    time.Time
	FactIDs   []int
	MemberIDs []int
	RetryIDs  []int // failed 仅作为候选交 D2，扫描自身不许可重加载。
}

// SchedulerSnapshot 提供 D2/D3 所需的只读运行视图，不持久化身份或预算。
type SchedulerSnapshot struct {
	Ready       []MinuteGroup
	Retry       []MinuteGroup // 仅候选，需 D2 预算许可后才可领取。
	WaitingIDs  []int
	Missing     []model.NotificationWindow
	ScanThrough time.Time
}

type waitingNotification struct {
	scheduleID int
	at         time.Time
}

// Scheduler 用单个循环执行扫描与到点核验，规则提交通过窄入口立即预加载。
type Scheduler struct {
	repo                              NotificationSchedulerRepo
	options                           SchedulerOptions
	wake                              chan struct{}
	work                              sync.Mutex
	mu                                sync.Mutex
	waiting                           map[int]waitingNotification
	groups                            map[int64]MinuteGroup
	memberSchedules                   map[int]int
	latestRevision                    map[int]int
	completed                         map[int64]bool
	lastObserved                      time.Time
	missing                           []model.NotificationWindow
	lastMinute, nextScan, scanThrough time.Time
}

// NewScheduler 组装窄存储端口和内存状态，不启动后台循环。
func NewScheduler(repo NotificationSchedulerRepo, options SchedulerOptions) (*Scheduler, error) {
	if repo == nil {
		return nil, fmt.Errorf("notification scheduler repo is nil")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Wait == nil {
		options.Wait = waitScheduler
	}
	return &Scheduler{repo: repo, options: options, wake: make(chan struct{}, 1), waiting: make(map[int]waitingNotification), groups: make(map[int64]MinuteGroup), memberSchedules: make(map[int]int), latestRevision: make(map[int]int), completed: make(map[int64]bool)}, nil
}

// Run 在取消后立即结束等待；入口负责在关闭数据库前等待此函数返回。
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := s.Step(ctx)
		delay := s.nextDelay()
		if err != nil {
			s.report(err)
			delay = time.Second
		}
		if err := s.options.Wait(ctx, delay, s.wake); err != nil {
			return err
		}
	}
}

// Step 使用墙上时间推进当前分钟并按 30 秒节奏扫描，可用于确定性测试。
func (s *Scheduler) Step(ctx context.Context) error {
	s.work.Lock()
	defer s.work.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := s.options.Now().In(model.BeijingLocation())
	minute := now.Truncate(time.Minute)
	s.mu.Lock()
	lastMinute := s.lastMinute
	completed := s.completed[minute.UnixNano()]
	if now.Before(s.lastObserved) {
		s.nextScan = time.Time{}
	}
	scanDue := s.nextScan.IsZero() || !now.Before(s.nextScan) || now.Before(s.lastObserved)
	s.mu.Unlock()
	if !completed {
		batch, err := s.repo.PrepareNotifications(ctx, model.NotificationWindow{Start: minute, End: minute.Add(time.Minute), AsOf: now, FreezeDue: true}, PrepareNotificationCandidates)
		if err != nil {
			return fmt.Errorf("prepare due minute %s: %w", minute.Format(time.RFC3339), err)
		}
		s.mu.Lock()
		if !lastMinute.IsZero() && minute.After(lastMinute.Add(time.Minute)) {
			s.addMissing(lastMinute.Add(time.Minute), minute)
		}
		for id, n := range s.waiting {
			if n.at.Before(minute) {
				s.addMissing(n.at, minute)
				delete(s.waiting, id)
			}
		}
		facts := make([]int, 0, len(batch.Notifications))
		retryIDs := make([]int, 0)
		for _, n := range batch.Notifications {
			if n.Deleted || n.NotifyTime.Second() != 0 || n.NotifyTime.Nanosecond() != 0 {
				continue
			}
			facts = append(facts, n.ID)
			if n.Status == model.NotifyFailed && !n.DeliveryStopped && n.AcknowledgedAt == nil && n.SnapshotTaken {
				retryIDs = append(retryIDs, n.ID)
			}
			delete(s.waiting, n.ID)
			s.memberSchedules[n.ID] = n.ScheduleID
		}
		if len(batch.EligibleIDs) > 0 || len(retryIDs) > 0 {
			s.groups[minute.UnixNano()] = MinuteGroup{Minute: minute, FactIDs: facts, MemberIDs: append([]int(nil), batch.EligibleIDs...), RetryIDs: retryIDs}
		}
		s.lastMinute = minute
		s.completed[minute.UnixNano()] = true
		s.mu.Unlock()
	}
	s.mu.Lock()
	if completed && !lastMinute.IsZero() && minute.After(lastMinute.Add(time.Minute)) {
		s.addMissing(lastMinute.Add(time.Minute), minute)
	}
	s.lastMinute, s.lastObserved = minute, now
	s.mu.Unlock()
	if scanDue {
		window := model.NotificationWindow{Start: now.Add(-30 * time.Second), End: now.Add(30 * time.Second), AsOf: now}
		batch, err := s.repo.PrepareNotifications(ctx, window, PrepareNotificationCandidates)
		if err != nil {
			return fmt.Errorf("scan notification window: %w", err)
		}
		s.mu.Lock()
		if !s.scanThrough.IsZero() && window.Start.After(s.scanThrough) {
			s.addMissing(s.scanThrough, window.Start)
		}
		if window.End.After(s.scanThrough) {
			s.scanThrough = window.End
		}
		s.loadBatch(window, batch)
		s.nextScan = now.Add(30 * time.Second)
		s.mu.Unlock()
	}
	return nil
}

// ScheduleChanged 在规则提交后刷新近期首提醒；排队失败不能撤销已提交规则。
func (s *Scheduler) ScheduleChanged(ctx context.Context, result *model.ScheduleWrite) {
	if result == nil || result.Schedule == nil {
		return
	}
	s.work.Lock()
	defer s.work.Unlock()
	now := s.options.Now().In(model.BeijingLocation())
	if !result.Replayed {
		stopped := result.Schedule.Deleted || result.Schedule.Cancelled || (result.Schedule.Period != nil && !result.Schedule.Period.Open)
		s.mu.Lock()
		// 提交后的回调可能乱序，旧版本不能撤销新版本已经加载的任务。
		if result.Schedule.Revision < s.latestRevision[result.Schedule.ID] {
			s.mu.Unlock()
			return
		}
		s.latestRevision[result.Schedule.ID] = result.Schedule.Revision
		for id, n := range s.waiting {
			if n.scheduleID == result.Schedule.ID && (stopped || n.at.After(now)) {
				delete(s.waiting, id)
			}
		}
		if stopped {
			for key, group := range s.groups {
				members := make([]int, 0, len(group.MemberIDs))
				for _, id := range group.MemberIDs {
					if s.memberSchedules[id] != result.Schedule.ID {
						members = append(members, id)
					}
				}
				group.MemberIDs = members
				s.groups[key] = group
			}
		}
		s.mu.Unlock()
	}
	if n := result.Notification; n != nil && n.NotifyTime.Sub(now) <= time.Minute {
		window := model.NotificationWindow{Start: n.NotifyTime, End: n.NotifyTime.Add(time.Nanosecond), AsOf: now, ScheduleID: result.Schedule.ID}
		batch, err := s.repo.PrepareNotifications(ctx, window, PrepareNotificationCandidates)
		if err != nil {
			s.report(fmt.Errorf("load committed schedule %d: %w", result.Schedule.ID, err))
		} else {
			s.mu.Lock()
			s.loadBatch(window, batch)
			if n.NotifyTime.Before(now.Truncate(time.Minute)) && len(batch.EligibleIDs) > 0 {
				s.addMissing(n.NotifyTime, now.Truncate(time.Minute))
			}
			s.mu.Unlock()
		}
	}
	s.signal()
}

// NotificationsAcknowledged 仅使队列成员失效，不改写真实投递结果。
func (s *Scheduler) NotificationsAcknowledged(notifications []*model.Notification) {
	s.work.Lock()
	defer s.work.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make(map[int]bool, len(notifications))
	for _, n := range notifications {
		ids[n.ID] = true
		delete(s.waiting, n.ID)
	}
	for key, group := range s.groups {
		members := make([]int, 0, len(group.MemberIDs))
		for _, id := range group.MemberIDs {
			if !ids[id] {
				members = append(members, id)
			}
		}
		group.MemberIDs = members
		s.groups[key] = group
	}
	s.signal()
}

// Snapshot 返回切片副本，调用方不能通过视图修改内部状态。
func (s *Scheduler) Snapshot() SchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := SchedulerSnapshot{ScanThrough: s.scanThrough, Missing: append([]model.NotificationWindow(nil), s.missing...)}
	for id := range s.waiting {
		value.WaitingIDs = append(value.WaitingIDs, id)
	}
	sort.Ints(value.WaitingIDs)
	for _, g := range s.groups {
		if len(g.MemberIDs) == 0 && len(g.RetryIDs) == 0 {
			continue
		}
		g.FactIDs, g.MemberIDs = append([]int(nil), g.FactIDs...), append([]int(nil), g.MemberIDs...)
		g.RetryIDs = append([]int(nil), g.RetryIDs...)
		if len(g.MemberIDs) > 0 {
			value.Ready = append(value.Ready, g)
		}
		if len(g.RetryIDs) > 0 {
			value.Retry = append(value.Retry, g)
		}
	}
	sort.Slice(value.Ready, func(i, j int) bool { return value.Ready[i].Minute.Before(value.Ready[j].Minute) })
	sort.Slice(value.Retry, func(i, j int) bool { return value.Retry[i].Minute.Before(value.Retry[j].Minute) })
	return value
}

// 以下函数在持有 mu 时调用，不访问数据库。
func (s *Scheduler) loadBatch(window model.NotificationWindow, batch *model.NotificationBatch) {
	for id, n := range s.waiting {
		if !n.at.Before(window.Start) && n.at.Before(window.End) && (window.ScheduleID == 0 || n.scheduleID == window.ScheduleID) {
			delete(s.waiting, id)
		}
	}
	eligible := make(map[int]bool, len(batch.EligibleIDs))
	for _, id := range batch.EligibleIDs {
		eligible[id] = true
	}
	for _, n := range batch.Notifications {
		if eligible[n.ID] && !s.completed[n.NotifyTime.Truncate(time.Minute).UnixNano()] && !n.NotifyTime.Before(window.AsOf.Truncate(time.Minute)) {
			s.waiting[n.ID] = waitingNotification{scheduleID: n.ScheduleID, at: n.NotifyTime}
		}
	}
}

func (s *Scheduler) addMissing(start, end time.Time) {
	if !end.After(start) {
		return
	}
	s.missing = append(s.missing, model.NotificationWindow{Start: start, End: end})
	sort.Slice(s.missing, func(i, j int) bool { return s.missing[i].Start.Before(s.missing[j].Start) })
	merged := s.missing[:0]
	for _, window := range s.missing {
		if len(merged) == 0 || window.Start.After(merged[len(merged)-1].End) {
			merged = append(merged, window)
		} else if window.End.After(merged[len(merged)-1].End) {
			merged[len(merged)-1].End = window.End
		}
	}
	s.missing = merged
}

func (s *Scheduler) nextDelay() time.Duration {
	now := s.options.Now()
	next := now.Truncate(time.Minute).Add(time.Minute)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.nextScan.IsZero() && s.nextScan.Before(next) {
		next = s.nextScan
	}
	for _, n := range s.waiting {
		if n.at.Before(next) {
			next = n.at
		}
	}
	if delay := next.Sub(now); delay > 0 {
		return delay
	}
	return time.Second
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Scheduler) report(err error) {
	if s.options.OnError != nil {
		s.options.OnError(err)
	}
}
func waitScheduler(ctx context.Context, delay time.Duration, wake <-chan struct{}) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-wake:
		return nil
	}
}
