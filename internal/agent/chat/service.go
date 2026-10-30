package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/google/uuid"
)

type execution struct {
	cancel    context.CancelFunc
	done      chan struct{}
	requested bool
	session   string
}
type subscriber struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Runtime 用真实存储和模型端口协调运行，内存只保存准入与订阅控制。
type Runtime struct {
	runs        RunRepository
	sessions    SessionRepository
	execution   ExecutionRepository
	events      EventRepository
	model       ModelClient
	tools       *tools.Registry
	toolStore   ToolRepository
	settings    func() ([]ModelConfig, error)
	mu          sync.Mutex
	active      map[string]*execution
	slots       chan struct{}
	subscribers map[string]*subscriber
	stopping    bool
	started     bool
	stopSignal  chan struct{}
}

// NewService 装配真实聊天用例，不播种任何会话。
func NewService(storage Storage, model ModelClient, settings func() ([]ModelConfig, error), registry *tools.Registry) *Runtime {
	service := &Runtime{
		runs:        storage.Runs,
		sessions:    storage.Sessions,
		execution:   storage.Execution,
		events:      storage.Events,
		model:       model,
		settings:    settings,
		active:      map[string]*execution{},
		slots:       make(chan struct{}, 3),
		subscribers: map[string]*subscriber{},
		stopSignal:  make(chan struct{}),
		toolStore:   storage.Tools,
		tools:       registry,
	}
	return service
}

// Start 在 ready 前恢复活跃占用，后台沿原 input 和同一逻辑调用恢复请求。
func (s *Runtime) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	records, err := s.runs.Active(ctx)
	if err != nil {
		return err
	}
	previousCalls := make(map[string]*ModelCall, len(records))
	hidden := make(map[string]bool)
	for i, r := range records {
		call, e := s.execution.ReadCall(ctx, r.RunID)
		if e != nil {
			return e
		}
		previousCalls[r.RunID] = call
		session, e := s.sessions.Session(ctx, r.SessionID)
		if errors.Is(e, ErrSessionNotFound) {
			hidden[r.RunID] = true
		} else if e != nil {
			return e
		} else if len(session.RunIDs) > 0 && session.RunIDs[len(session.RunIDs)-1] != r.RunID {
			records[i].Error = &RunError{
				Code:    "run_superseded",
				Message: "同一会话已有后续运行，旧运行仅做失败收尾",
			}
		}
	}
	for _, r := range records {
		previous := previousCalls[r.RunID]
		cfg := ModelConfig{}
		configs, _ := s.settings()
		for _, c := range configs {
			if c.Provider == r.Provider && c.Model == r.Model {
				cfg = c
				break
			}
		}
		runCtx, cancel := context.WithCancel(context.Background())
		run := &execution{
			cancel:  cancel,
			done:    make(chan struct{}),
			session: r.SessionID,
		}
		if hidden[r.RunID] || r.State == StateWaitForCancel {
			run.requested = true
			cancel()
		}
		s.active[r.RunID] = run
		go s.execute(runCtx, run, r, cfg, previous)
	}
	s.started = true
	return nil
}

// Stop 停止准入与模型请求，等待协调器保存已收到内容后释放依赖。
func (s *Runtime) Stop() {
	s.mu.Lock()
	if !s.stopping {
		close(s.stopSignal)
	}
	s.stopping = true
	var pending []chan struct{}
	for _, run := range s.active {
		run.cancel()
		pending = append(pending, run.done)
	}
	for _, sub := range s.subscribers {
		sub.cancel()
		pending = append(pending, sub.done)
	}
	s.mu.Unlock()
	for _, done := range pending {
		<-done
	}
}
func available(c ModelConfig) bool {
	return c.Key != "" && c.ContextWindow > c.MaxOutputTokens && c.MaxOutputTokens > 0
}

// GetModels 返回全部本地配置选项，不向上游验证密钥。
func (s *Runtime) GetModels(context.Context) ([]ModelOption, error) {
	configs, err := s.settings()
	if err != nil {
		return nil, ErrModelUnavailable
	}
	models := []ModelOption{}
	for _, cfg := range configs {
		models = append(models, ModelOption{
			Provider:  cfg.Provider,
			Model:     cfg.Model,
			Available: available(cfg),
		})
	}
	return models, nil
}
func receipt(r Record) Receipt {
	result := Receipt{
		RunID:  r.RunID,
		Status: r.State.Public(),
	}
	if r.State != StateRunning {
		result.Output = r.Output
		result.Error = r.Error
	}
	return result
}

// CreateRun 优先重放持久化幂等回执，再校验配置并原子预留名额。
func (s *Runtime) CreateRun(ctx context.Context, input CreateRunInput) (Receipt, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.Input) == "" ||
		strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.Model) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return Receipt{}, ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || !s.started {
		return Receipt{}, ErrStopping
	}
	old, err := s.runs.FindReplay(ctx, input.SessionID, input.IdempotencyKey)
	if err == nil {
		if old.Input != input.Input {
			return Receipt{}, ErrIdempotencyConflict
		}
		return receipt(old), nil
	}
	if !errors.Is(err, ErrRunNotFound) {
		return Receipt{}, err
	}
	configs, err := s.settings()
	if err != nil {
		return Receipt{}, ErrModelUnavailable
	}
	var cfg ModelConfig
	found := false
	for _, c := range configs {
		if c.Provider == input.Provider && c.Model == input.Model {
			cfg = c
			found = true
			break
		}
	}
	if !found {
		return Receipt{}, ErrInvalidRequest
	}
	if !available(cfg) {
		return Receipt{}, ErrModelUnavailable
	}
	for _, run := range s.active {
		if run.session == input.SessionID {
			return Receipt{}, ErrSessionBusy
		}
	}
	if len(s.active) >= 3 {
		return Receipt{}, ErrSessionConcurrencyLimit
	}
	r := Record{
		Run: Run{
			RunID:     uuid.NewString(),
			SessionID: input.SessionID,
			Provider:  input.Provider,
			Model:     input.Model,
			Input:     input.Input,
			Status:    StatusRunning,
			CreatedAt: time.Now(),
		},
		State:          StateRunning,
		IdempotencyKey: input.IdempotencyKey,
	}
	request, err := s.newRequest(ctx, r, cfg)
	if err != nil {
		return Receipt{}, err
	}
	if failure := requestBudgetError(request, cfg); failure != nil {
		r.State, r.Error = StateFailed, failure
		now := time.Now()
		r.FinishedAt = &now
	}
	if err = s.runs.Create(ctx, r); err != nil {
		return Receipt{}, err
	}
	result := receipt(r)
	result.created = true
	if r.State.Terminal() {
		return result, nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	run := &execution{
		cancel:  cancel,
		done:    make(chan struct{}),
		session: r.SessionID,
	}
	s.active[r.RunID] = run
	go s.execute(runCtx, run, r, cfg, &ModelCall{
		RunID:     r.RunID,
		SessionID: r.SessionID,
		Input:     request,
		State:     StateRunning,
		MaxRetry:  cfg.MaxRetry,
	})
	return result, nil
}

func (s *Runtime) execute(ctx context.Context, run *execution, r Record, cfg ModelConfig, previous *ModelCall) {
	defer func() { s.mu.Lock(); delete(s.active, r.RunID); close(run.done); s.mu.Unlock(); run.cancel() }()
	// 持久化故障耗尽后可能遗留超过三个未完成会话，恢复推进同样受全局名额约束。
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		// 排队取消或退出只保存已有事实，不等待正在执行的模型释放名额。
	}
	call := ModelCall{
		RunID:     r.RunID,
		SessionID: r.SessionID,
		State:     StateRunning,
		MaxRetry:  cfg.MaxRetry,
	}
	var err error
	if previous != nil {
		call = *previous
		r.Output = call.PresentedOutput()
	}
	// 取消恢复只保存已有事实，不重新登记或发送模型请求。
	if r.State == StateWaitForCancel {
		r.State = StateCancelled
		r.Error = nil
		if !call.State.Terminal() {
			call.State = StateCancelled
			call.Error = nil
		}
		now := time.Now()
		r.FinishedAt = &now
		call = s.readCancellationResults(call)
		s.finish(call, r)
		return
	}
	if r.Error == nil && !available(cfg) {
		r.Error = &RunError{
			Code:    "model_config_unavailable",
			Message: "原运行的模型配置或密钥不可用",
		}
	}
	call, r.Error = s.advance(ctx, &r, cfg, call)
	s.mu.Lock()
	if s.stopping && !run.requested && (r.Error == nil || r.Error.Code != "persistence_failed") {
		s.mu.Unlock()
		if call.ID != 0 {
			if err = s.execution.Pause(context.Background(), call); err != nil {
				logger.Error("[chat] 保存退出进度失败", "run_id", r.RunID, "error", err)
				r.State = StateFailed
				r.Error = &RunError{
					Code:    "persistence_failed",
					Message: "保存退出进度失败",
				}
				call.State = r.State
				call.Error = r.Error
				now := time.Now()
				r.FinishedAt = &now
				s.finish(call, r)
			}
		}
		return
	}
	r.State = StateSucceeded
	if r.Error != nil && r.Error.Code == "persistence_failed" {
		r.State = StateFailed
	} else if run.requested {
		r.State = StateCancelled
		r.Error = nil
	} else if r.Error != nil {
		r.State = StateFailed
	}
	s.mu.Unlock()
	if !call.State.Terminal() {
		call.State = r.State
		call.Error = r.Error
	}
	call.NextRetryAt = nil
	now := time.Now()
	r.FinishedAt = &now
	s.finish(call, r)
}

func (s *Runtime) readCancellationResults(call ModelCall) ModelCall {
	if call.ID == 0 || s.toolStore == nil {
		return call
	}
	stored, err := s.toolStore.ReadTools(context.Background(), call.ID)
	if err != nil {
		logger.Error("[chat] 取消恢复读取工具失败", "run_id", call.RunID, "model_call_id", call.ID, "error", err)
		return call
	}
	for _, tool := range stored {
		if tool.State != "running" || call.ToolCompleted(tool.Request.ID) {
			continue
		}
		output, found, err := s.tools.ReadResult(tools.WithExecutionKey(context.Background(), tool.IdempotencyKey), tool.Request.Function.Name, tool.Request.Function.Arguments)
		if err != nil {
			logger.Warn("[chat] 取消恢复未确认业务回执", "run_id", call.RunID, "tool_call_id", tool.Request.ID, "error", err)
		}
		tool = tool.WithResultUnconfirmed()
		if found {
			tool.State, tool.Output, tool.Error = "succeeded", output, nil
		}
		call.PendingTools = append(call.PendingTools, tool)
	}
	return call
}

func (s *Runtime) finish(call ModelCall, r Record) {
	call.NextRetryAt = nil
	if len(call.RetryHistory) > 0 {
		last := &call.RetryHistory[len(call.RetryHistory)-1]
		switch last.Result {
		case "retry_wait", "running", "interrupted":
			last.Result = string(r.State)
			last.Error = r.Error
		}
	}
	// 终态提交失败后只重试收尾，不重新调用模型。
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-timer.C:
			case <-s.stopSignal:
				timer.Stop()
				logger.Error("[chat] 退出中停止收尾重试，保留已持久化事实", "run_id", r.RunID)
				return
			}
			logger.Warn("[chat] 重试保存终态", "run_id", r.RunID, "retry", attempt)
		}
		if err = s.execution.Finish(context.Background(), call, r); err == nil {
			return
		}
		logger.Error("[chat] 保存终态失败", "run_id", r.RunID, "attempt", attempt+1, "error", err)
	}
	logger.Error("[chat] 终态保存重试耗尽，释放占用但不推送未保存终态", "run_id", r.RunID, "error", err)
}

// GetRun 查询可见运行的公开概况。
func (s *Runtime) GetRun(ctx context.Context, id string) (Run, error) {
	r, err := s.runs.ReadRun(ctx, id, true)
	return r.Run, err
}

// GetRunEvents 查询可见运行的原始分页事件。
func (s *Runtime) GetRunEvents(ctx context.Context, id string, after int64, limit int) (EventPage, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return EventPage{}, ErrInvalidRequest
	}
	if _, err := s.runs.ReadRun(ctx, id, true); err != nil {
		return EventPage{}, err
	}
	return s.events.Events(ctx, id, after, limit)
}

// GetSession 查询可见会话。
func (s *Runtime) GetSession(ctx context.Context, id string) (Session, error) {
	return s.sessions.Session(ctx, id)
}

// ListSessions 查询可见会话列表。
func (s *Runtime) ListSessions(ctx context.Context) ([]string, error) {
	return s.sessions.Sessions(ctx)
}

// CancelRun 先持久化取消等待再停止后续推进，回执不表示工具已完成收尾。
func (s *Runtime) CancelRun(ctx context.Context, id string) (Status, error) {
	s.mu.Lock()
	r, err := s.runs.ReadRun(ctx, id, true)
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	run := s.active[id]
	if r.State.Terminal() || run == nil {
		s.mu.Unlock()
		return r.State.Public(), nil
	}
	state, err := s.runs.RequestCancel(ctx, id)
	if err != nil {
		logger.Error("[chat] 保存取消等待失败", "run_id", id, "stage", "wait_for_cancel", "error", err)
	}
	if err == nil && !state.Terminal() {
		run.requested = true
		run.cancel()
	}
	s.mu.Unlock()
	return state.Public(), err
}

// DeleteSession 先同步隐藏会话，再请求取消自有运行，不删除执行事实。
func (s *Runtime) DeleteSession(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sessions.Hide(ctx, id); err != nil {
		return err
	}
	for _, run := range s.active {
		if run.session == id {
			run.requested = true
			run.cancel()
		}
	}
	return nil
}

// Subscribe 从持久化游标接续，替换旧连接；断开不取消运行。
func (s *Runtime) Subscribe(ctx context.Context, id string, after int64) (*Subscription, error) {
	if after < 0 {
		return nil, ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.runs.ReadRun(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if after > 0 {
		// run 内序号连续；游标必须指向已经持久化的事件，校验通过后才替换旧订阅。
		page, err := s.events.Events(ctx, id, after-1, 1)
		if err != nil {
			return nil, err
		}
		if len(page.Events) == 0 || page.Events[0].ID != after {
			return nil, ErrInvalidRequest
		}
	}
	if r.State.Terminal() {
		return nil, ErrRunTerminal
	}
	if old := s.subscribers[id]; old != nil {
		old.cancel()
	}
	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscriber{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.subscribers[id] = sub
	events := make(chan Event)
	go func() {
		defer close(sub.done)
		defer close(events)
		defer cancel()
		defer func() {
			s.mu.Lock()
			if s.subscribers[id] == sub {
				delete(s.subscribers, id)
			}
			s.mu.Unlock()
		}()
		timer := time.NewTicker(20 * time.Millisecond)
		defer timer.Stop()
		for {
			page, err := s.events.Events(subCtx, id, after, 100)
			if err != nil {
				return
			}
			for _, e := range page.Events {
				select {
				case events <- e:
					after = e.ID
				case <-subCtx.Done():
					return
				}
				if e.Type == EventEnd {
					return
				}
			}
			if page.HasMore {
				continue
			}
			select {
			case <-subCtx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	return &Subscription{
		Events: events,
		Close:  cancel,
	}, nil
}
