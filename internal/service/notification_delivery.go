package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

var (
	ErrDeliveryInput    = errors.New("invalid delivery request")
	ErrAttemptNotFound  = errors.New("delivery attempt not found in this process")
	ErrDeliveryConflict = errors.New("delivery attempt conflicts with accepted state")
	ErrDeliveryStopping = errors.New("delivery executor is stopping")
)

// DeliveryRepo 只检查实际成员、许可重加载和原子保存结果，不保存运行身份。
type DeliveryRepo interface {
	ReadDeliveryMembers(context.Context, []int, time.Time, bool) ([]*model.Notification, error)
	SaveDeliveryResult(context.Context, []int, model.NotifyStatus) error
}

// DeliveryClaim 是执行者能读取的任务身份，不暴露内部候选集合。
type DeliveryClaim struct {
	DeliveryTaskID string    `json:"deliveryTaskId"`
	TaskType       string    `json:"taskType"`
	AttemptID      string    `json:"attemptId"`
	PlatformID     string    `json:"platformId"`
	NotifyTime     time.Time `json:"notifyTime"`
}

// DeliveryStart 缓存一次开始决定，重传不会重新计算文本或再次消费预算。
type DeliveryStart struct {
	DeliveryClaim
	ShouldDisplay bool   `json:"shouldDisplay"`
	MemberCount   int    `json:"memberCount"`
	Title         string `json:"title,omitempty"`
	Body          string `json:"body,omitempty"`
	AttemptNumber int    `json:"attemptNumber"`
}

// DeliveryResult 是本进程接受的实际平台结果，不代表用户确认。
type DeliveryResult struct {
	AttemptID   string             `json:"attemptId"`
	Outcome     model.NotifyStatus `json:"outcome"`
	MemberCount int                `json:"memberCount"`
}

// DeliveryTaskView 为 D3 提供副本，恢复流程不能通过视图修改预算或尝试。
type DeliveryTaskView struct {
	DeliveryTaskID     string
	Minute             time.Time
	FactIDs, MemberIDs []int
	Attempts           int
	Started            bool
	AttemptID          string
	RetryAt            time.Time
	Uncertain          bool
	Completed          bool
}

type deliveryTask struct {
	id             string
	minute         time.Time
	facts, members []int
	attempts       int
	active         *deliveryAttempt
	retryAt        time.Time
	done           bool
	ready          bool
	transferred    bool
	memberVersion  int
	recovery       *recoveryRound
}
type deliveryAttempt struct {
	task    *deliveryTask
	claim   DeliveryClaim
	start   *DeliveryStart
	members []int
	result  *DeliveryResult
	unknown bool
}

// DeliveryService 的状态仅由 App.operations 串行修改；此锁是业务操作边界，
// 与 Scheduler 内存锁分离，存储端口不得回调 App 或 Scheduler。
type DeliveryService struct {
	repo          DeliveryRepo
	now           func() time.Time
	tasks         map[int64]*deliveryTask
	attempts      map[string]*deliveryAttempt
	stopping      bool
	recoveryTasks map[string]*deliveryTask
	memberOwners  map[int]*deliveryTask
}

// NewDeliveryService 允许测试注入时钟，不启动循环、不创建数据库对象。
func NewDeliveryService(repo DeliveryRepo, now func() time.Time) (*DeliveryService, error) {
	if repo == nil {
		return nil, fmt.Errorf("delivery repo is nil")
	}
	if now == nil {
		now = time.Now
	}
	return &DeliveryService{repo: repo, now: now, tasks: make(map[int64]*deliveryTask), attempts: make(map[string]*deliveryAttempt), recoveryTasks: make(map[string]*deliveryTask), memberOwners: make(map[int]*deliveryTask)}, nil
}

func (a *App) delivery() (*DeliveryService, error) {
	if a.Delivery == nil {
		repo, ok := a.Notify.(DeliveryRepo)
		if !ok {
			return nil, fmt.Errorf("notification repo does not support delivery")
		}
		var err error
		a.Delivery, err = NewDeliveryService(repo, nil)
		if err != nil {
			return nil, err
		}
	}
	return a.Delivery, nil
}

// ClaimDelivery 接纳 D1 的完整分钟副本；领取丢失后返回原未开始身份。
func (a *App) ClaimDelivery(ctx context.Context, limit int) ([]DeliveryClaim, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if limit < 1 || limit > 20 {
		return nil, ErrDeliveryInput
	}
	d, err := a.delivery()
	if err != nil {
		return nil, err
	}
	if d.stopping {
		return nil, ErrDeliveryStopping
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := d.now().In(model.BeijingLocation())
	if err := a.prepareRecoveryLocked(ctx, now); err != nil {
		return nil, err
	}
	if a.Scheduler != nil {
		snapshot := a.Scheduler.Snapshot()
		for _, g := range append(snapshot.Ready, snapshot.Retry...) {
			if g.Minute.After(now) {
				continue
			}
			key := g.Minute.UnixNano()
			if d.tasks[key] == nil {
				candidates := append(append([]int(nil), g.MemberIDs...), g.RetryIDs...)
				unowned := make([]int, 0, len(candidates))
				for _, id := range candidates {
					if d.memberOwners[id] == nil {
						unowned = append(unowned, id)
					}
				}
				// 新运行没有旧预算，D2 在接纳边界正式许可当前分钟 failed 重加载。
				members, err := d.repo.ReadDeliveryMembers(ctx, unowned, now, true)
				if err != nil {
					return nil, err
				}
				d.tasks[key] = &deliveryTask{id: rand.Text(), minute: g.Minute, facts: append([]int(nil), g.FactIDs...), members: deliveryMemberIDs(members), done: len(members) == 0, ready: true}
				for _, id := range g.FactIDs {
					if d.memberOwners[id] == nil {
						d.memberOwners[id] = d.tasks[key]
					}
				}
			}
		}
	}
	ordered := make([]*deliveryTask, 0, len(d.tasks))
	for _, task := range d.tasks {
		ordered = append(ordered, task)
	}
	for _, task := range d.recoveryTasks {
		ordered = append(ordered, task)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].minute.Equal(ordered[j].minute) {
			return ordered[i].id < ordered[j].id
		}
		return ordered[i].minute.Before(ordered[j].minute)
	})
	claims := make([]DeliveryClaim, 0, limit)
	for _, task := range ordered {
		if len(claims) == limit {
			break
		}
		if task.done || task.transferred || (task.recovery != nil && !task.ready) || task.attempts >= 4 || now.Before(task.retryAt) || task.minute.After(now) {
			continue
		}
		if task.active != nil {
			if task.active.start == nil {
				claims = append(claims, task.active.claim)
			}
			continue
		}
		if !task.retryAt.IsZero() {
			members, err := d.repo.ReadDeliveryMembers(ctx, task.members, now, true)
			if err != nil {
				return nil, err
			}
			task.members = deliveryMemberIDs(members)
			if len(task.members) == 0 {
				task.done = true
				continue
			}
		}
		id := rand.Text()
		kind := "minute"
		if task.recovery != nil {
			kind = "recovery"
		}
		attempt := &deliveryAttempt{task: task, claim: DeliveryClaim{DeliveryTaskID: task.id, TaskType: kind, AttemptID: id, PlatformID: id, NotifyTime: task.minute}}
		task.active = attempt
		d.attempts[id] = attempt
		claims = append(claims, attempt.claim)
	}
	return claims, nil
}

// StartDelivery 与规则操作及确认共用边界，校验后立即冻结实际展示集合。
func (a *App) StartDelivery(ctx context.Context, id string) (DeliveryStart, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	d, err := a.delivery()
	if err != nil {
		return DeliveryStart{}, err
	}
	if d.stopping {
		return DeliveryStart{}, ErrDeliveryStopping
	}
	if err := a.prepareRecoveryLocked(ctx, d.now()); err != nil {
		return DeliveryStart{}, err
	}
	at := d.attempts[id]
	if at == nil {
		return DeliveryStart{}, ErrAttemptNotFound
	}
	if at.start != nil {
		return *at.start, nil
	}
	if err := ctx.Err(); err != nil {
		return DeliveryStart{}, err
	}
	members, err := d.repo.ReadDeliveryMembers(ctx, at.task.members, d.now(), false)
	if err != nil {
		return DeliveryStart{}, err
	}
	start := DeliveryStart{DeliveryClaim: at.claim, MemberCount: len(members), ShouldDisplay: len(members) > 0, AttemptNumber: at.task.attempts}
	if len(members) > 0 {
		at.task.attempts++
		start.AttemptNumber = at.task.attempts
		start.Title = "日程提醒"
		if at.task.recovery != nil {
			start.Body = fmt.Sprintf("有 %d 项错过的日程需要处理", len(members))
		} else if len(members) == 1 {
			start.Body = fmt.Sprintf("%s %s", *members[0].NameSnapshot, members[0].NotifyTime.In(model.BeijingLocation()).Format("2006-01-02 15:04"))
		} else {
			start.Body = fmt.Sprintf("有 %d 项日程需要处理", len(members))
		}
	} else {
		at.task.done = true
	}
	at.members = deliveryMemberIDs(members)
	at.task.members = append([]int(nil), at.members...)
	at.start = &start
	return start, nil
}

// CompleteDelivery 先提交整组事实，再保存运行回执；旧失败不得覆盖新尝试阶段。
func (a *App) CompleteDelivery(ctx context.Context, id string, outcome model.NotifyStatus) (DeliveryResult, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if outcome != model.NotifySent && outcome != model.NotifyFailed {
		return DeliveryResult{}, ErrDeliveryInput
	}
	d, err := a.delivery()
	if err != nil {
		return DeliveryResult{}, err
	}
	at := d.attempts[id]
	if at == nil {
		return DeliveryResult{}, ErrAttemptNotFound
	}
	if at.start == nil || !at.start.ShouldDisplay {
		return DeliveryResult{}, ErrDeliveryConflict
	}
	if at.result != nil {
		if at.result.Outcome != outcome {
			return DeliveryResult{}, ErrDeliveryConflict
		}
		return *at.result, nil
	}
	// 明确退出后的旧失败只结算旧身份；可信成功仍回流已冻结的成员。
	if outcome == model.NotifySent || at.task.active == at {
		if err := d.repo.SaveDeliveryResult(ctx, at.members, outcome); err != nil {
			return DeliveryResult{}, err
		}
	}
	result := DeliveryResult{AttemptID: id, Outcome: outcome, MemberCount: len(at.members)}
	at.result = &result
	if outcome == model.NotifySent {
		at.task.done = true
		if active := at.task.active; active != nil && active != at && active.start == nil {
			active.start = &DeliveryStart{DeliveryClaim: active.claim, AttemptNumber: at.task.attempts}
		}
	} else if at.task.active == at {
		at.task.active = nil
		at.task.retryAt = d.now().Add(deliveryRetryDelay(at.task.attempts))
	}
	return result, nil
}

func deliveryRetryDelay(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 5 * time.Second
	case 2:
		return 30 * time.Second
	default:
		return 120 * time.Second
	}
}
func deliveryMemberIDs(values []*model.Notification) []int {
	ids := make([]int, 0, len(values))
	for _, n := range values {
		ids = append(ids, n.ID)
	}
	return ids
}

// DeliveryExecutorExited 兼容服务内明确退出调用；未知结果不伪造 failed。
func (a *App) DeliveryExecutorExited() {
	a.deliveryExecutorExited(nil)
}

// DeliveryExecutorExitedAttempts 仅整理退出执行者持有的 attempt，迟到事件不会影响新领取。
func (a *App) DeliveryExecutorExitedAttempts(attemptIDs []string) {
	ids := make(map[string]struct{}, len(attemptIDs))
	for _, id := range attemptIDs {
		ids[id] = struct{}{}
	}
	a.deliveryExecutorExited(ids)
}

func (a *App) deliveryExecutorExited(attemptIDs map[string]struct{}) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if a.Delivery == nil {
		return
	}
	d := a.Delivery
	for _, task := range d.allTasks() {
		at := task.active
		if task.done || at == nil || at.unknown || attemptIDs != nil && !containsAttempt(attemptIDs, at.claim.AttemptID) {
			continue
		}
		if at.start == nil {
			at.start = &DeliveryStart{DeliveryClaim: at.claim, AttemptNumber: task.attempts}
		} else if at.start.ShouldDisplay && at.result == nil {
			at.unknown = true
			task.retryAt = d.now().Add(deliveryRetryDelay(task.attempts))
		}
		task.active = nil
	}
}

func containsAttempt(values map[string]struct{}, id string) bool {
	_, ok := values[id]
	return ok
}

// StopDelivery 阻止新领取及开始，仍允许已接收的结果回填。
func (a *App) StopDelivery() {
	a.operations.Lock()
	defer a.operations.Unlock()
	if d, err := a.delivery(); err == nil {
		d.stopping = true
	}
}

// DeliverySnapshot 只复制运行视图，不访问数据库。
func (a *App) DeliverySnapshot() []DeliveryTaskView {
	a.operations.Lock()
	defer a.operations.Unlock()
	views := make([]DeliveryTaskView, 0)
	if a.Delivery == nil {
		return views
	}
	for _, task := range a.Delivery.allTasks() {
		if task.transferred {
			continue
		}
		v := DeliveryTaskView{DeliveryTaskID: task.id, Minute: task.minute, FactIDs: append([]int(nil), task.facts...), MemberIDs: append([]int(nil), task.members...), Attempts: task.attempts, RetryAt: task.retryAt, Completed: task.done}
		if at := task.active; at != nil {
			v.AttemptID = at.claim.AttemptID
			v.Started = at.start != nil && at.start.ShouldDisplay
		}
		for _, at := range a.Delivery.attempts {
			if at.task == task && at.unknown && at.result == nil {
				v.Uncertain = true
				v.Started = true
			}
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Minute.Before(views[j].Minute) })
	return views
}

func (d *DeliveryService) allTasks() []*deliveryTask {
	values := make([]*deliveryTask, 0, len(d.tasks)+len(d.recoveryTasks))
	for _, task := range d.tasks {
		values = append(values, task)
	}
	for _, task := range d.recoveryTasks {
		values = append(values, task)
	}
	return values
}

// DeliveryAttemptMembers 为 D3 的尝试成员查询返回独立副本，不依赖当前候选。
func (a *App) DeliveryAttemptMembers(id string) ([]int, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	if a.Delivery == nil || a.Delivery.attempts[id] == nil {
		return nil, ErrAttemptNotFound
	}
	at := a.Delivery.attempts[id]
	if at.start == nil {
		return nil, ErrDeliveryConflict
	}
	return append([]int(nil), at.members...), nil
}
