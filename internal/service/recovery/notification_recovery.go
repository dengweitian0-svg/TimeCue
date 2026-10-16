package recovery

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
)

// Repo 提供恢复边界、历史批次和只读统计。
type Repo interface {
	RecoveryStart(context.Context, time.Time) (time.Time, error)
	RecoverNotifications(context.Context, model.RecoveryWindow, model.PrepareRecoveryVersion) (*model.RecoveryBatch, error)
	CountRecoveryMembers(context.Context, []int, time.Time) (int, error)
	CountUnacknowledgedRecoveryMembers(context.Context, []int, time.Time) (int, error)
}

// DeliveryPort 仅暴露恢复轮次交接所需的投递操作。
type DeliveryPort interface {
	RecoveryPrepare(time.Time) ([]model.NotificationWindow, error)
	RecoveryRetireTask(string) (notification.RecoveryTask, bool)
	RecoveryCreateTask(string, time.Time, []int)
	RecoveryAddMembers(string, []*model.Notification) []int
	RecoveryCompleteTask(context.Context, string, time.Time) error
	RecoveryTaskFacts(string) []int
}

// Service 管理恢复窗口、轮次和历史去重位置。
type Service struct {
	repo        Repo
	options     notification.SchedulerOptions
	initialized bool
	pending     []model.NotificationWindow
	current     *round
	rounds      map[string]*round
	recovered   map[int]bool
}

type round struct {
	id       string
	taskID   string
	state    string
	through  time.Time
	windows  []model.NotificationWindow
	index    int
	position model.RecoveryPosition
	seen     map[int]bool
	facts    []int
}

// NewService 创建恢复服务。
func NewService(repo Repo, options notification.SchedulerOptions) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("recovery repo is nil")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Wait == nil {
		options.Wait = notification.WaitScheduler
	}
	return &Service{repo: repo, options: options, recovered: make(map[int]bool), rounds: make(map[string]*round)}, nil
}

// RunStep 组织一个恢复批次；调用方需在 App.operations 边界内调用。
func (s *Service) RunStep(ctx context.Context, now time.Time, schedulerWindows []model.NotificationWindow, deliveries DeliveryPort) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now = now.In(model.BeijingLocation())
	if err := s.prepare(ctx, now, schedulerWindows, deliveries); err != nil {
		return false, err
	}
	r := s.current
	if r == nil || r.state == "completed" || now.Before(r.through) {
		return false, nil
	}
	r.state = "running"
	if r.index < len(r.windows) {
		w := r.windows[r.index]
		batch, err := s.repo.RecoverNotifications(ctx, model.RecoveryWindow{Start: w.Start, End: w.End, AsOf: now, Position: r.position}, PrepareRecoveryCandidates)
		if err != nil {
			r.state = "failed"
			return false, err
		}
		newValues := make([]*model.Notification, 0, len(batch.Notifications))
		for _, n := range batch.Notifications {
			if n.Deleted || n.Status == model.NotifySent || s.recovered[n.ID] {
				continue
			}
			s.recovered[n.ID] = true
			r.seen[n.ID] = true
			newValues = append(newValues, n)
		}
		r.facts = append(r.facts, deliveries.RecoveryAddMembers(r.taskID, newValues)...)
		r.position = batch.Next
		if batch.Done {
			r.index++
			r.position = model.RecoveryPosition{}
		}
	}
	if r.index == len(r.windows) {
		if err := deliveries.RecoveryCompleteTask(ctx, r.taskID, now); err != nil {
			r.state = "failed"
			return true, err
		}
		r.state = "completed"
	}
	return true, nil
}

// Prepare 仅准备恢复轮次和投递任务，不提交历史批次。
func (s *Service) Prepare(ctx context.Context, now time.Time, schedulerWindows []model.NotificationWindow, deliveries DeliveryPort) error {
	return s.prepare(ctx, now, schedulerWindows, deliveries)
}

func (s *Service) prepare(ctx context.Context, now time.Time, schedulerWindows []model.NotificationWindow, deliveries DeliveryPort) error {
	minute := now.Truncate(time.Minute)
	if !s.initialized {
		start, err := s.repo.RecoveryStart(ctx, minute)
		if err != nil {
			return err
		}
		if !start.IsZero() && start.Before(minute) {
			s.pending = append(s.pending, model.NotificationWindow{Start: start, End: minute})
		}
		s.initialized = true
	}
	sealed, err := deliveries.RecoveryPrepare(now)
	if err != nil {
		return err
	}
	s.pending = append(s.pending, sealed...)
	s.pending = append(s.pending, schedulerWindows...)
	if len(s.pending) > 0 && (s.current == nil || s.current.state == "completed") {
		current := &round{id: rand.Text(), taskID: rand.Text(), state: "running", windows: mergeRecoveryWindows(s.pending, minute), seen: make(map[int]bool)}
		if len(current.windows) == 0 {
			s.pending = nil
			return nil
		}
		current.through = current.windows[len(current.windows)-1].End
		if s.current != nil {
			if old, ok := deliveries.RecoveryRetireTask(s.current.taskID); ok {
				for id := range s.current.seen {
					current.seen[id] = true
					s.recovered[id] = true
				}
				for _, id := range old.FactIDs {
					current.seen[id] = true
					s.recovered[id] = true
				}
				current.facts = append(current.facts, old.FactIDs...)
				if old.Minute.After(current.through) {
					current.through = old.Minute
				}
				delete(s.rounds, s.current.taskID)
			}
		}
		sort.Ints(current.facts)
		deliveries.RecoveryCreateTask(current.taskID, current.through, current.facts)
		s.current = current
		s.rounds[current.taskID] = current
		s.pending = nil
	}
	return nil
}

// Snapshot 返回当前恢复轮次的只读进度与统计。
func (s *Service) Snapshot(ctx context.Context, deliveries DeliveryPort, now time.Time) (*model.RecoveryView, error) {
	r := s.current
	if r == nil {
		return nil, nil
	}
	return s.snapshotRound(ctx, r, deliveries, now)
}

func (s *Service) snapshotRound(ctx context.Context, r *round, deliveries DeliveryPort, now time.Time) (*model.RecoveryView, error) {
	facts := deliveries.RecoveryTaskFacts(r.taskID)
	displayable, err := s.repo.CountRecoveryMembers(ctx, facts, now)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(r.seen))
	for id := range r.seen {
		ids = append(ids, id)
	}
	unack, err := s.repo.CountUnacknowledgedRecoveryMembers(ctx, ids, now)
	if err != nil {
		return nil, err
	}
	view := &model.RecoveryView{RecoveryID: r.id, State: r.state, Through: r.through, MaterializedCount: len(r.seen), DisplayableCount: displayable, UnacknowledgedCount: unack, DeliveryTaskID: r.taskID}
	if r.state == "completed" {
		total := len(r.seen)
		view.MissedTotal = &total
	}
	return view, nil
}

// SnapshotForTask 返回指定恢复任务的进度，包含已经开始并保留身份的旧轮次。
func (s *Service) SnapshotForTask(ctx context.Context, taskID string, deliveries DeliveryPort, now time.Time) (*model.RecoveryView, error) {
	r := s.rounds[taskID]
	if r == nil {
		return nil, nil
	}
	return s.snapshotRound(ctx, r, deliveries, now)
}

// Wait 间隔等待恢复下一批或上下文取消。
func (s *Service) Wait(ctx context.Context, d time.Duration) error {
	return s.options.Wait(ctx, d, nil)
}

// ReportError 报告后台恢复失败。
func (s *Service) ReportError(err error) {
	if s.options.OnError != nil {
		s.options.OnError(err)
	}
}

// Now 返回恢复服务配置的当前时间。
func (s *Service) Now() time.Time { return s.options.Now() }

func mergeRecoveryWindows(windows []model.NotificationWindow, through time.Time) []model.NotificationWindow {
	sort.Slice(windows, func(i, j int) bool { return windows[i].Start.Before(windows[j].Start) })
	merged := make([]model.NotificationWindow, 0, len(windows))
	for _, w := range windows {
		if start := w.Start.Truncate(time.Minute); !start.Equal(w.Start) {
			w.Start = start.Add(time.Minute)
		}
		if end := w.End.Truncate(time.Minute); !end.Equal(w.End) {
			w.End = end.Add(time.Minute)
		}
		if w.End.After(through) {
			w.End = through
		}
		if !w.End.After(w.Start) {
			continue
		}
		if len(merged) == 0 || w.Start.After(merged[len(merged)-1].End) {
			merged = append(merged, w)
		} else if w.End.After(merged[len(merged)-1].End) {
			merged[len(merged)-1].End = w.End
		}
	}
	return merged
}

// MergeRecoveryWindows 合并并截断恢复窗口，只保留整分钟触发范围。
func MergeRecoveryWindows(windows []model.NotificationWindow, through time.Time) []model.NotificationWindow {
	return mergeRecoveryWindows(windows, through)
}
