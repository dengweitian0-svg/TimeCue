package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/google/uuid"
)

// BatchProgress 提供已提交的工具配对事实，工具模块不依赖模型快照的具体类型。
type BatchProgress interface {
	// ToolCompleted 判断原调用 ID 是否已配对。
	ToolCompleted(string) bool
}

// BatchRepository 沿用现有存储事务登记尝试、退避和结果。
type BatchRepository[P BatchProgress] interface {
	// ReadTools 读取当前模型调用的已登记工具，不跨回复匹配同值 ID。
	ReadTools(context.Context, int64) ([]ToolCall, error)
	// BeginTool 原子登记开始、公开事件及进度，返回已提交快照及原身份工具。
	BeginTool(context.Context, P, ToolCall) (P, ToolCall, error)
	// WaitToolRetry 保存实际失败和退避期限，不提交配对。
	WaitToolRetry(context.Context, ToolCall) error
	// FinishTool 原子合并结果、公开事件及配对进度，返回已提交快照。
	FinishTool(context.Context, P, ToolCall) (P, error)
}

// Batch 绑定一条已接纳回复的工具请求及原调用身份。
type Batch struct {
	RunID       string
	ModelCallID int64
	Provider    string
	Model       string
	Requests    []ToolRequest
}

// Complete 判断每个原调用是否都有已提交的配对结果。
func (batch Batch) Complete(progress BatchProgress) bool {
	if len(batch.Requests) == 0 {
		return false
	}
	for _, request := range batch.Requests {
		if !progress.ToolCompleted(request.ID) {
			return false
		}
	}
	return true
}

// BatchControl 提供运行生命周期的信号及与取消共用的事务门禁。
type BatchControl struct {
	PersistenceContext func() context.Context
	StartContext       context.Context
	StartGate          sync.Locker
	RunLifecycle       func(context.Context, string) RunLifecycle
	ReadSettings       func(provider, model string) (Settings, bool)
}

// RunLifecycle 描述一次运行的活动、用户取消和宿主退出状态。
type RunLifecycle struct {
	Active          bool
	CancelRequested bool
	ContextDone     bool
	ShuttingDown    bool
}

// Settings 保存同一模型连接的工具执行配置。
type Settings struct {
	Concurrency     int
	Timeout         time.Duration
	MaxRetry        *int
	ResultMaxTokens int
}

// DefaultMaxRetry 是未单独配置的逻辑工具调用预算。
const DefaultMaxRetry = 3

func (cfg Settings) limits() (concurrency int, timeout time.Duration, maxRetry int) {
	concurrency, timeout, maxRetry = 3, 30*time.Second, DefaultMaxRetry
	if cfg.Concurrency > 0 {
		concurrency = cfg.Concurrency
	}
	if cfg.Timeout > 0 {
		timeout = cfg.Timeout
	}
	if cfg.MaxRetry != nil && *cfg.MaxRetry >= 0 {
		maxRetry = *cfg.MaxRetry
	}
	return
}

// BatchFailure 保留出错的事务阶段和原因，供运行按统一的持久化故障规则收尾。
type BatchFailure struct {
	Stage string
	Err   error
}

func batchFailure(stage string, err error) *BatchFailure {
	return &BatchFailure{
		Stage: stage,
		Err:   err,
	}
}

// ToolFactory 持有工具注册和执行策略，跨批次跟踪尚未实际退出的工具。
type ToolFactory[P BatchProgress] struct {
	registry *Registry
	store    BatchRepository[P]
	control  BatchControl
	workers  sync.WaitGroup
}

// NewToolFactory 使用启动时验证过的工具目录及既有存储事务装配批次执行。
func NewToolFactory[P BatchProgress](registry *Registry, store BatchRepository[P], control BatchControl) *ToolFactory[P] {
	return &ToolFactory[P]{
		registry: registry,
		store:    store,
		control:  control,
	}
}

// Configured 判断工具批次执行所需的注册表和存储事务是否均已装配。
func (f *ToolFactory[P]) Configured() bool {
	return f.registry != nil && f.store != nil
}

func (f *ToolFactory[P]) canRetry(ctx context.Context, runID string) bool {
	run := f.control.RunLifecycle(ctx, runID)
	// 正常退出保留未耗尽的预算，用户取消和会话删除终止后续重试。
	return run.Active && !run.CancelRequested && (!run.ContextDone || run.ShuttingDown)
}

// Wait 等待已撤销的业务实际返回，供宿主关闭日程服务和数据库。
func (f *ToolFactory[P]) Wait() { f.workers.Wait() }

// toolAttempt 只代表当前有效尝试；逻辑调用身份及预算仍保存在 ToolCall。
type toolAttempt struct {
	tool     ToolCall
	ctx      context.Context
	cancel   context.CancelFunc
	parallel bool
	token    uint64
}

type toolAttemptResult struct {
	tool       ToolCall
	token      uint64
	finishedAt time.Time
}

func (attempt *toolAttempt) accepts(result toolAttemptResult) bool {
	deadline, _ := attempt.ctx.Deadline()
	return attempt.token == result.token && !result.finishedAt.After(deadline)
}

func toolRetryable(failure *Failure) bool {
	return failure != nil && (failure.Code == "tool_timeout" || failure.Code == "tool_temporary_error")
}

// RunBatch 调度已接纳的批次并返回实际提交的配对进度。事务故障时一并交回已取得的结果。
func (f *ToolFactory[P]) RunBatch(ctx context.Context, batch Batch, call P, cfg Settings) (result P, pending []ToolCall, failure *BatchFailure) {
	// 读取配置失败时保留最近一次有效设置；已登记的尝试保留原预算及期限。
	limit, timeout, maxRetry := cfg.limits()
	contentLimit := cfg.ResultMaxTokens
	refreshConfig := func() {
		if current, ok := f.control.ReadSettings(batch.Provider, batch.Model); ok {
			limit, timeout, maxRetry = current.limits()
			contentLimit = current.ResultMaxTokens
		}
	}
	stored, err := f.store.ReadTools(f.control.PersistenceContext(), batch.ModelCallID)
	if err != nil {
		return call, pending, batchFailure("read_tools", err)
	}
	known := map[string]ToolCall{}
	for _, tool := range stored {
		known[tool.Request.ID] = tool
	}
	active := map[string]*toolAttempt{}
	// 回调只提交尝试事实；单协调器核验 token 后才有权结算。
	results := make(chan toolAttemptResult, len(batch.Requests))
	closed := make(chan struct{})
	var token uint64
	defer func() {
		close(closed)
		// 先撤销全部有效尝试，再只收集已返回事实；不等待忽略取消的业务。
		for _, attempt := range active {
			attempt.cancel()
		}
		if failure == nil {
			return
		}
		for {
			select {
			case returned := <-results:
				if attempt := active[returned.tool.Request.ID]; attempt != nil && attempt.accepts(returned) {
					delete(active, returned.tool.Request.ID)
					returned.tool.ModelContentLimit = contentLimit
					pending = append(pending, returned.tool)
				}
			default:
				return
			}
		}
	}()
	settle := func(tool ToolCall) *BatchFailure {
		refreshConfig()
		tool.ModelContentLimit = contentLimit
		if toolRetryable(tool.Error) && tool.RetryCount < tool.MaxRetry && f.canRetry(ctx, batch.RunID) {
			next := time.Now().Add(agent.RetryDelay(tool.RetryCount + 1))
			if latest := time.Unix(0, 1<<63-1); next.After(latest) {
				next = latest
			}
			tool.State, tool.NextRetryAt = "retry_wait", &next
			if err := f.store.WaitToolRetry(f.control.PersistenceContext(), tool); err != nil {
				// 保存退避失败也保留本次实际错误，不将已取得的结果改为未确认。
				tool.State, tool.NextRetryAt = "failed", nil
				pending = append(pending, tool)
				return batchFailure("tool_retry_wait", err)
			}
			known[tool.Request.ID] = tool
			return nil
		}
		progress, err := f.store.FinishTool(f.control.PersistenceContext(), call, tool)
		if err != nil {
			pending = append(pending, tool)
			return batchFailure("tool_result", err)
		}
		call = progress
		known[tool.Request.ID] = tool
		return nil
	}
	// 恢复先核对原身份的只读回执；命中是已有事实，不是一次新的业务尝试。
	// retry_wait 也可能来自业务提交后返回的暂时故障，核对不受退避或剩余预算阻挡。
	for _, tool := range stored {
		if call.ToolCompleted(tool.Request.ID) || tool.State != "running" && tool.State != "retry_wait" {
			continue
		}
		confirmed, found, err := f.readReceipt(f.control.PersistenceContext(), tool)
		if err != nil {
			logger.Warn("[tools] 恢复未确认业务回执，沿原身份和剩余预算处理", "run_id", batch.RunID, "tool_call_id", tool.Request.ID, "error", err)
		}
		if found {
			if failure := settle(confirmed); failure != nil {
				return call, pending, failure
			}
		}
	}
	acceptResult := func(returned toolAttemptResult) *BatchFailure {
		attempt := active[returned.tool.Request.ID]
		if attempt == nil || !attempt.accepts(returned) {
			return nil
		}
		attempt.cancel()
		delete(active, returned.tool.Request.ID)
		return settle(returned.tool)
	}
	// 周期唤醒读取热配置；精确超时及退避期限用最近期限定时器唤醒。
	for {
		// 宿主退出预算到期后保留未完成工具；用户取消仍等待原结果或原期限。
		if f.control.PersistenceContext().Err() != nil {
			return call, pending, nil
		}
		// 已在期限内返回的事实优先接纳，短事务等待不能将其改判超时。
		for draining := true; draining; {
			select {
			case returned := <-results:
				if failure := acceptResult(returned); failure != nil {
					return call, pending, failure
				}
			default:
				draining = false
			}
		}
		expired := false
		for id, attempt := range active {
			if attempt.ctx.Err() == nil {
				continue
			}
			attempt.cancel()
			delete(active, id)
			tool := attempt.tool
			tool.State = "failed"
			tool.Error = &Failure{
				Code:    "tool_timeout",
				Message: "工具执行超时，取消信号不代表业务副作用已回滚",
			}
			if failure := settle(tool); failure != nil {
				return call, pending, failure
			}
			// 保存可能阻塞，先重新接纳期间及时返回的事实，再裁定下一个超时。
			expired = true
			break
		}
		if expired {
			continue
		}
		serial := false
		for _, attempt := range active {
			serial = serial || !attempt.parallel
		}
		for _, request := range batch.Requests {
			refreshConfig()
			if ctx.Err() != nil || serial || len(active) >= limit {
				break
			}
			if call.ToolCompleted(request.ID) || active[request.ID] != nil {
				continue
			}
			tool, exists := known[request.ID]
			if exists && tool.State == "retry_wait" && tool.NextRetryAt != nil && time.Now().Before(*tool.NextRetryAt) {
				continue
			}
			entry, registered := f.registry.lookup(request.Function.Name)
			parallel := !registered || entry.info.Parallel
			if !parallel && len(active) > 0 {
				continue
			}
			if !exists {
				tool = ToolCall{
					ModelCallID: batch.ModelCallID,
					Request:     request,
					State:       "running",
					MaxRetry:    maxRetry,
				}
				if registered && entry.info.Dangerous {
					tool.IdempotencyKey = "tool:" + request.ID + ":" + uuid.NewString()
				}
			} else {
				if tool.State != "running" && tool.State != "retry_wait" {
					return call, pending, batchFailure("tool_state", fmt.Errorf("工具 %s 的已提交状态 %s 缺少配对进度", tool.Request.ID, tool.State))
				}
				if tool.RetryCount >= tool.MaxRetry {
					if tool.State == "running" {
						tool = tool.WithResultUnconfirmed()
					} else {
						tool.State = "failed"
					}
					if failure := settle(tool); failure != nil {
						return call, pending, failure
					}
					continue
				}
				tool.RetryCount++
			}
			tool.State, tool.Error, tool.Output, tool.NextRetryAt = "running", nil, nil, nil
			// 与取消受理共用启动门，取消提交后不能再登记有效尝试。
			f.control.StartGate.Lock()
			if ctx.Err() != nil {
				f.control.StartGate.Unlock()
				break
			}
			started, registeredTool, err := f.store.BeginTool(f.control.StartContext, call, tool)
			if err != nil {
				f.control.StartGate.Unlock()
				return call, pending, batchFailure("tool_start", err)
			}
			call, tool = started, registeredTool
			known[request.ID] = tool
			attemptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			token++
			attempt := &toolAttempt{
				tool:     tool,
				ctx:      attemptCtx,
				cancel:   cancel,
				parallel: parallel,
				token:    token,
			}
			active[request.ID] = attempt
			f.workers.Add(1)
			go func(attempt *toolAttempt) {
				defer f.workers.Done()
				tool := attempt.tool
				output, failure := f.registry.Execute(WithExecutionKey(attempt.ctx, tool.IdempotencyKey), tool.Request.Function.Name, tool.Request.Function.Arguments)
				tool.Output, tool.State = output, "succeeded"
				if failure != nil {
					tool.State = "failed"
					tool.Error = failure
					if failure.Code == "tool_argument_parse_error" {
						logger.Warn("[tools] 工具参数不完整或 JSON 非法，未执行业务", "run_id", batch.RunID, "model_call_id", tool.ModelCallID, "tool_call_id", tool.Request.ID)
					}
				}
				select {
				case results <- toolAttemptResult{
					tool:       tool,
					token:      attempt.token,
					finishedAt: time.Now(),
				}:
				case <-closed:
				}
			}(attempt)
			f.control.StartGate.Unlock()
			serial = !parallel
		}
		if len(active) == 0 && (ctx.Err() != nil || batch.Complete(call)) {
			return call, pending, nil
		}
		wait := 100 * time.Millisecond
		for _, attempt := range active {
			deadline, _ := attempt.ctx.Deadline()
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
		}
		if ctx.Err() == nil {
			for id, tool := range known {
				if active[id] == nil && !call.ToolCompleted(id) && tool.State == "retry_wait" && tool.NextRetryAt != nil {
					if remaining := time.Until(*tool.NextRetryAt); remaining > 0 && remaining < wait {
						wait = remaining
					}
				}
			}
		}
		timer := time.NewTimer(wait)
		// 取消只阻止后续启动，已开始尝试等待实际结果或原期限。
		wake := ctx.Done()
		if ctx.Err() != nil {
			wake = nil
		}
		select {
		case returned := <-results:
			timer.Stop()
			if failure := acceptResult(returned); failure != nil {
				return call, pending, failure
			}
		case <-timer.C:
		case <-wake:
			timer.Stop()
		}
	}
}
