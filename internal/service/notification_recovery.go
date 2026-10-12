package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// RecoveryRepo 只暴露历史批次及只读统计，不保存恢复运行身份。
type RecoveryRepo interface {
	RecoveryStart(context.Context, time.Time) (time.Time, error)
	RecoverNotifications(context.Context, model.RecoveryWindow, model.PrepareRecoveryVersion) (*model.RecoveryBatch, error)
	CountRecoveryMembers(context.Context, []int, time.Time) (int, error)
	CountUnacknowledgedRecoveryMembers(context.Context, []int, time.Time) (int, error)
}

// RecoveryView 区分历史整理与真实投递，未知总数固定为 null。
type RecoveryView struct {
	RecoveryID          string    `json:"recoveryId"`
	State               string    `json:"state"`
	Through             time.Time `json:"through"`
	MaterializedCount   int       `json:"materializedCount"`
	MissedTotal         *int      `json:"missedTotal"`
	DisplayableCount    int       `json:"displayableCount"`
	UnacknowledgedCount int       `json:"unacknowledgedCount"`
	DeliveryTaskID      string    `json:"deliveryTaskId"`
}

type recoveryRound struct {
	id       string
	state    string
	through  time.Time
	windows  []model.NotificationWindow
	index    int
	position model.RecoveryPosition
	seen     map[int]bool
	task     *deliveryTask
}

// RecoveryService 仅由所属 App.operations 访问，批间让出操作边界。
type RecoveryService struct {
	repo        RecoveryRepo
	options     SchedulerOptions
	initialized bool
	pending     []model.NotificationWindow
	current     *recoveryRound
	recovered   map[int]bool
}

// NewRecoveryService 不启动工作；App.Run 负责取消与等待。
func NewRecoveryService(repo RecoveryRepo, options SchedulerOptions) (*RecoveryService, error) {
	if repo == nil {
		return nil, fmt.Errorf("recovery repo is nil")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Wait == nil {
		options.Wait = waitScheduler
	}
	return &RecoveryService{repo: repo, options: options, recovered: make(map[int]bool)}, nil
}

// RunRecovery 每次只提交一个批次，失败留在原位置，不阻塞实时调度。
func (a *App) RunRecovery(ctx context.Context) error {
	if a.Recovery == nil {
		return fmt.Errorf("recovery is not configured")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		worked, err := a.RecoverStep(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := time.Second
		if worked && err == nil {
			delay = time.Millisecond
		}
		if err != nil && a.Recovery.options.OnError != nil {
			a.Recovery.options.OnError(err)
		}
		if err := a.Recovery.options.Wait(ctx, delay, nil); err != nil {
			return err
		}
	}
}

// RecoverStep 提交一个批次后公布成员和位置，支持确定性恢复与竞争测试。
func (a *App) RecoverStep(ctx context.Context) (bool, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if a.Recovery == nil {
		return false, fmt.Errorf("recovery is not configured")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := a.Recovery.options.Now().In(model.BeijingLocation())
	if err := a.prepareRecoveryLocked(ctx, now); err != nil {
		return false, err
	}
	r := a.Recovery.current
	if r == nil || r.state == "completed" || now.Before(r.through) {
		return false, nil
	}
	r.state = "running"
	if r.index < len(r.windows) {
		w := r.windows[r.index]
		batch, err := a.Recovery.repo.RecoverNotifications(ctx, model.RecoveryWindow{Start: w.Start, End: w.End, AsOf: now, Position: r.position}, PrepareRecoveryCandidates)
		if err != nil {
			r.state = "failed"
			return false, err
		}
		for _, n := range batch.Notifications {
			if n.Deleted || n.Status == model.NotifySent || a.Recovery.recovered[n.ID] {
				continue
			}
			a.Recovery.recovered[n.ID] = true
			r.seen[n.ID] = true
			if owner := a.Delivery.memberOwners[n.ID]; owner != nil && owner != r.task {
				continue
			}
			a.Delivery.memberOwners[n.ID] = r.task
			r.task.facts = append(r.task.facts, n.ID)
			r.task.memberVersion++
		}
		r.position = batch.Next
		if batch.Done {
			r.index++
			r.position = model.RecoveryPosition{}
		}
	}
	if r.index == len(r.windows) {
		// 只有 D2 的正式接纳才许可本运行的 failed 重加载。
		members, err := a.Delivery.repo.ReadDeliveryMembers(ctx, r.task.facts, now, true)
		if err != nil {
			r.state = "failed"
			return true, err
		}
		r.task.members = deliveryMemberIDs(members)
		r.task.ready = true
		r.task.done = len(members) == 0
		r.state = "completed"
	}
	return true, nil
}

// prepareRecoveryLocked 与 claim/start 共用守卫，先封存旧分钟，再进行历史整理。
func (a *App) prepareRecoveryLocked(ctx context.Context, now time.Time) error {
	r := a.Recovery
	if r == nil {
		return nil
	}
	d, err := a.delivery()
	if err != nil {
		return err
	}
	minute := now.Truncate(time.Minute)
	if !r.initialized {
		start, err := r.repo.RecoveryStart(ctx, minute)
		if err != nil {
			return err
		}
		if !start.IsZero() && start.Before(minute) {
			r.pending = append(r.pending, model.NotificationWindow{Start: start, End: minute})
		}
		r.initialized = true
	}
	if a.Scheduler != nil {
		r.pending = append(r.pending, a.Scheduler.TakeRecoveryWindows(minute)...)
	}
	for _, task := range d.tasks {
		if task.transferred {
			continue
		}
		if task.attempts > 0 || !task.minute.Before(minute) {
			for _, id := range task.facts {
				if d.memberOwners[id] == nil {
					d.memberOwners[id] = task
				}
			}
			continue
		}
		r.pending = append(r.pending, model.NotificationWindow{Start: task.minute, End: task.minute.Add(time.Minute)})
		task.transferred, task.done = true, true
		if at := task.active; at != nil {
			delete(d.attempts, at.claim.AttemptID)
			task.active = nil
		}
		for _, id := range task.facts {
			if d.memberOwners[id] == task {
				delete(d.memberOwners, id)
			}
		}
	}
	r.pending = mergeRecoveryWindows(r.pending, minute)
	if len(r.pending) > 0 && (r.current == nil || r.current.state == "completed") {
		current := &recoveryRound{id: rand.Text(), state: "running", windows: r.pending, seen: make(map[int]bool)}
		current.through = current.windows[len(current.windows)-1].End
		current.task = &deliveryTask{id: rand.Text(), minute: current.through, recovery: current}
		// 连续遗漏发生时，尚未有效开始的旧汇总并入新轮，不能累积多条待展示通知。
		for oldID, old := range d.recoveryTasks {
			if old.transferred || old.attempts > 0 {
				continue
			}
			if old.recovery.through.After(current.through) {
				current.through = old.recovery.through
				current.task.minute = current.through
			}
			for id := range old.recovery.seen {
				current.seen[id] = true
			}
			for _, id := range old.facts {
				if owner := d.memberOwners[id]; owner == nil || owner == old {
					d.memberOwners[id] = current.task
					current.task.facts = append(current.task.facts, id)
					current.task.memberVersion++
				}
			}
			for id, at := range d.attempts {
				if at.task == old {
					delete(d.attempts, id)
				}
			}
			old.transferred, old.done, old.active = true, true, nil
			delete(d.recoveryTasks, oldID)
		}
		d.recoveryTasks[current.task.id] = current.task
		r.current, r.pending = current, nil
	}
	return nil
}

func mergeRecoveryWindows(windows []model.NotificationWindow, through time.Time) []model.NotificationWindow {
	sort.Slice(windows, func(i, j int) bool { return windows[i].Start.Before(windows[j].Start) })
	merged := make([]model.NotificationWindow, 0, len(windows))
	for _, w := range windows {
		// 窗口只负责整分钟触发点；空的亚分钟间隙不创建新恢复轮次。
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

func (a *App) recoveryViewLocked(ctx context.Context, r *recoveryRound, now time.Time) (*RecoveryView, error) {
	if r == nil {
		return nil, nil
	}
	count, err := a.Recovery.repo.CountRecoveryMembers(ctx, r.task.facts, now)
	if err != nil {
		return nil, err
	}
	// 房间待处理提示按本轮完整历史中的确认事实统计，与展示资格独立。
	ids := make([]int, 0, len(r.seen))
	for id := range r.seen {
		ids = append(ids, id)
	}
	unacknowledged, err := a.Recovery.repo.CountUnacknowledgedRecoveryMembers(ctx, ids, now)
	if err != nil {
		return nil, err
	}
	view := &RecoveryView{RecoveryID: r.id, State: r.state, Through: r.through, MaterializedCount: len(r.seen), DisplayableCount: count, UnacknowledgedCount: unacknowledged, DeliveryTaskID: r.task.id}
	if r.state == "completed" {
		total := len(r.seen)
		view.MissedTotal = &total
	}
	return view, nil
}

// RecoverySnapshot 返回受控进度，不触发补齐或投递。
func (a *App) RecoverySnapshot(ctx context.Context, now time.Time) (*RecoveryView, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if a.Recovery == nil {
		return nil, nil
	}
	return a.recoveryViewLocked(ctx, a.Recovery.current, now)
}
