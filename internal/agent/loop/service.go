package loop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/google/uuid"
)

type execution struct {
	cancel    context.CancelFunc
	done      chan struct{}
	requested bool
	session   string
	recovery  runRecoveryAction
}
type subscriber struct {
	cancel context.CancelFunc
}

const maxConcurrentRuns = 3

// Run 协调 Agent 的会话、模型、工具、持久化和生命周期。
type Run struct {
	runs           chat.RunRepository
	sessions       chat.SessionRepository
	execution      chat.ExecutionRepository
	contextBuilder chat.ContextBuilder
	compactor      chat.Compactor
	events         chat.EventRepository
	usage          chat.UsageRepository
	model          chat.ModelClient
	toolFactory    *tools.ToolFactory[chat.ModelCall]
	toolStore      chat.ToolRepository
	settings       func() ([]chat.ModelConfig, error)
	mu             sync.Mutex
	active         map[string]*execution
	slots          chan struct{}
	subscribers    map[string]*subscriber
	stopping       bool
	started        bool
	prepared       bool
	recovery       []recoveryRun
	subWorkers     sync.WaitGroup
	shutdownCtx    context.Context
	storageCtx     context.Context
	stopStorage    context.CancelFunc
}

// NewWithPorts 使用已装配的模型、工具和存储端口创建尚未准备恢复的 Loop。
func NewWithPorts(storage chat.Storage, model chat.ModelClient, settings func() ([]chat.ModelConfig, error), registry *tools.Registry) *Run {
	storageCtx, stopStorage := context.WithCancel(context.Background())
	service := &Run{
		runs:           storage.Runs,
		sessions:       storage.Sessions,
		execution:      storage.Execution,
		contextBuilder: chat.NewContextBuilder(storage.Execution, registry),
		events:         storage.Events,
		usage:          storage.Usage,
		model:          model,
		settings:       settings,
		active:         map[string]*execution{},
		slots:          make(chan struct{}, maxConcurrentRuns),
		subscribers:    map[string]*subscriber{},
		toolStore:      storage.Tools,
		storageCtx:     storageCtx,
		stopStorage:    stopStorage,
	}
	service.toolFactory = tools.NewToolFactory(registry, storage.Tools, tools.BatchControl{
		PersistenceContext: service.persistenceContext,
		StartContext:       service.storageCtx,
		StartGate:          &service.mu,
		RunLifecycle:       service.toolRunLifecycle,
		ReadSettings:       service.currentToolSettings,
	})
	return service
}

type recoveryRun struct {
	record   chat.Record
	previous *chat.ModelCall
	config   chat.ModelConfig
	action   runRecoveryAction
}

func (s *Run) prepareRecovery(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prepared {
		return nil
	}
	records, err := s.runs.Active(ctx)
	if err != nil {
		return err
	}
	var recovery []recoveryRun
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		call, err := s.execution.ReadCall(ctx, r.RunID)
		if err != nil {
			return err
		}
		session, err := s.sessions.Session(ctx, r.SessionID)
		hidden := errors.Is(err, chat.ErrSessionNotFound)
		if err != nil && !hidden {
			return err
		}
		cfg, _ := s.currentModel(r.Provider, r.Model)
		recovery = append(recovery, recoveryRun{
			record:   r,
			previous: call,
			config:   cfg,
			action:   recoverRun(r, session, hidden),
		})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.recovery, s.prepared = recovery, true
	return nil
}

// Activate 在宿主接纳后恢复占用并允许后台推进。
func (s *Run) Activate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.prepared || s.stopping {
		return chat.ErrStopping
	}
	if s.started {
		return nil
	}
	for _, item := range s.recovery {
		runCtx, cancel := context.WithCancel(context.Background())
		run := &execution{
			cancel:    cancel,
			done:      make(chan struct{}),
			session:   item.record.SessionID,
			requested: item.action == cancelHiddenRun || item.action == settleCancelledRun,
			recovery:  item.action,
		}
		if run.requested {
			cancel()
		}
		s.active[item.record.RunID] = run
		go s.execute(runCtx, run, item.record, item.config, item.previous)
	}
	s.recovery, s.started = nil, true
	return nil
}

func (s *Run) shutdown(ctx context.Context) {
	// 工具登记可能持有运行锁等待存储，先挂接期限才能解除该等待。
	stopDeadline := context.AfterFunc(ctx, s.stopStorage)
	defer stopDeadline()
	s.mu.Lock()
	if !s.stopping {
		s.shutdownCtx = ctx
	}
	s.stopping = true
	s.recovery = nil
	var pending []chan struct{}
	for _, run := range s.active {
		run.cancel()
		pending = append(pending, run.done)
	}
	for _, sub := range s.subscribers {
		sub.cancel()
	}
	s.mu.Unlock()
	for _, done := range pending {
		<-done
	}
	s.stopStorage()
	s.subWorkers.Wait()
}

func (s *Run) persistenceContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdownCtx != nil {
		return s.shutdownCtx
	}
	return s.storageCtx
}
func available(c chat.ModelConfig) bool {
	return c.Key != "" && c.ContextWindow > c.MaxOutputTokens && c.MaxOutputTokens > 0
}

// GetModels 返回全部本地配置选项，不向上游验证密钥。
func (s *Run) GetModels(context.Context) ([]chat.ModelOption, error) {
	configs, err := s.settings()
	if err != nil {
		return nil, chat.ErrModelUnavailable
	}
	models := []chat.ModelOption{}
	for _, cfg := range configs {
		models = append(models, chat.ModelOption{
			Provider:       cfg.Provider,
			Model:          cfg.Model,
			Available:      available(cfg),
			ConfigRevision: cfg.ConfigRevision,
		})
	}
	return models, nil
}

// CreateRun 优先重放持久化幂等回执，再校验配置并原子预留名额。
func (s *Run) CreateRun(ctx context.Context, input chat.CreateRunInput) (chat.Receipt, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.Input) == "" ||
		strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.Model) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return chat.Receipt{}, chat.ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || !s.started {
		return chat.Receipt{}, chat.ErrStopping
	}
	old, err := s.runs.FindReplay(ctx, input.SessionID, input.IdempotencyKey)
	if err == nil {
		if old.Input != input.Input {
			return chat.Receipt{}, chat.ErrIdempotencyConflict
		}
		return old.Receipt(false), nil
	}
	if !errors.Is(err, chat.ErrRunNotFound) {
		return chat.Receipt{}, err
	}
	configs, err := s.settings()
	if err != nil {
		return chat.Receipt{}, chat.ErrModelUnavailable
	}
	var cfg chat.ModelConfig
	found := false
	for _, c := range configs {
		if c.Provider == input.Provider && c.Model == input.Model {
			cfg = c
			found = true
			break
		}
	}
	if !found {
		return chat.Receipt{}, chat.ErrInvalidRequest
	}
	if !available(cfg) {
		return chat.Receipt{}, chat.ErrModelUnavailable
	}
	for _, run := range s.active {
		if run.session == input.SessionID {
			return chat.Receipt{}, chat.ErrSessionBusy
		}
	}
	if len(s.active) >= maxConcurrentRuns {
		return chat.Receipt{}, chat.ErrSessionConcurrencyLimit
	}
	r := chat.Record{
		Run: chat.Run{
			RunID:     uuid.NewString(),
			SessionID: input.SessionID,
			Provider:  input.Provider,
			Model:     input.Model,
			Input:     input.Input,
			Status:    chat.StatusRunning,
			CreatedAt: time.Now(),
		},
		State:          chat.StateRunning,
		IdempotencyKey: input.IdempotencyKey,
	}
	request, err := s.contextBuilder.FirstRequest(ctx, r, cfg)
	if err != nil {
		return chat.Receipt{}, err
	}
	call, failure := s.prepareInitialCall(r, request, cfg)
	if failure == nil && call.CheckpointStage != "compact" {
		failure = requestBudgetError(call.InputBody, cfg)
	}
	if failure != nil {
		r.State, r.Error = chat.StateFailed, failure
		now := time.Now()
		r.FinishedAt = &now
	}
	if err = s.runs.Create(ctx, r); err != nil {
		return chat.Receipt{}, err
	}
	result := r.Receipt(true)
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
	go s.execute(runCtx, run, r, cfg, &call)
	return result, nil
}

func (s *Run) execute(ctx context.Context, run *execution, r chat.Record, cfg chat.ModelConfig, previous *chat.ModelCall) {
	defer func() { s.mu.Lock(); delete(s.active, r.RunID); close(run.done); s.mu.Unlock(); run.cancel() }()
	// 持久化故障耗尽后可能遗留超过三个未完成会话，恢复推进同样受全局名额约束。
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		// 排队取消或退出只保存已有事实，不等待正在执行的模型释放名额。
	}
	call := newModelCall(r.Run, cfg.MaxRetry)
	var err error
	if previous != nil {
		call = *previous
		r.Output = call.PresentedOutput()
	}
	switch run.recovery {
	case finishSupersededRun:
		r.Error = toolError("run_superseded", "同一会话已有后续运行，旧运行仅做失败收尾")
	case cancelHiddenRun, settleCancelledRun:
		r.Error = nil
		call, r.Error = s.readCancellationResults(call, cfg)
	case resumeRun:
		call, r.Error = s.advance(ctx, &r, cfg, call)
	}
	s.mu.Lock()
	cancelled := run.requested || r.State == chat.StateWaitForCancel
	if shouldPause(run.recovery, s.stopping, cancelled, r.Error) {
		s.mu.Unlock()
		if call.ID != 0 {
			pauseCtx := s.persistenceContext()
			if err = s.execution.Pause(pauseCtx, call); err != nil {
				if pauseCtx.Err() != nil && errors.Is(err, pauseCtx.Err()) {
					logger.Warn("[chat] 退出保存期限已到，保留已提交进度", "run_id", r.RunID, "error", err)
					return
				}
				r.State = chat.StateFailed
				r.Error = persistenceFailure(call, "pause", err)
				now := time.Now()
				r.FinishedAt = &now
				s.finish(call, r)
			}
		}
		return
	}
	r.State = chat.StateSucceeded
	if r.Error != nil && r.Error.Code == "persistence_failed" {
		r.State = chat.StateFailed
	} else if cancelled {
		r.State = chat.StateCancelled
		r.Error = nil
	} else if r.Error != nil {
		r.State = chat.StateFailed
	}
	s.mu.Unlock()
	now := time.Now()
	r.FinishedAt = &now
	s.finish(call, r)
}

func (s *Run) readCancellationResults(call chat.ModelCall, cfg chat.ModelConfig) (chat.ModelCall, *chat.RunError) {
	if call.ID == 0 || s.toolStore == nil || call.State != chat.StateSucceeded || !call.HasToolCalls() {
		return call, nil
	}
	batch := tools.Batch{
		RunID:       call.RunID,
		ModelCallID: call.ID,
	}
	results, err := s.toolFactory.CancellationResults(s.persistenceContext(), batch, call, toolSettings(cfg))
	if err != nil {
		return call, persistenceFailure(call, "cancel_tools", err)
	}
	call.PendingTools = append(call.PendingTools, results...)
	return call, nil
}

// GetRun 查询可见运行的公开概况。
func (s *Run) GetRun(ctx context.Context, id string) (chat.Run, error) {
	r, err := s.runs.ReadRun(ctx, id, true)
	return r.Run, err
}

// GetRunEvents 查询可见运行的原始分页事件。
func (s *Run) GetRunEvents(ctx context.Context, id string, after int64, limit int) (chat.EventPage, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return chat.EventPage{}, chat.ErrInvalidRequest
	}
	if _, err := s.runs.ReadRun(ctx, id, true); err != nil {
		return chat.EventPage{}, err
	}
	return s.events.Events(ctx, id, after, limit)
}

// GetSession 查询可见会话。
func (s *Run) GetSession(ctx context.Context, id string) (chat.Session, error) {
	return s.sessions.Session(ctx, id)
}

// ListSessions 查询可见会话列表。
func (s *Run) ListSessions(ctx context.Context) ([]string, error) {
	return s.sessions.Sessions(ctx)
}

// CancelRun 先持久化取消等待再停止后续推进，回执不表示工具已完成收尾。
func (s *Run) CancelRun(ctx context.Context, id string) (chat.Status, error) {
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
func (s *Run) DeleteSession(ctx context.Context, id string) error {
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
func (s *Run) Subscribe(ctx context.Context, id string, after int64) (*chat.Subscription, error) {
	if after < 0 {
		return nil, chat.ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil, chat.ErrStopping
	}
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
			return nil, chat.ErrInvalidRequest
		}
	}
	if r.State.Terminal() {
		return nil, chat.ErrRunTerminal
	}
	if old := s.subscribers[id]; old != nil {
		old.cancel()
	}
	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscriber{cancel: cancel}
	s.subscribers[id] = sub
	events := make(chan chat.Event)
	s.subWorkers.Add(1)
	go func() {
		defer s.subWorkers.Done()
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
				if e.Type == chat.EventEnd {
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
	return &chat.Subscription{
		Events: events,
		Close:  cancel,
	}, nil
}
