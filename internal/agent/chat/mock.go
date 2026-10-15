package chat

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/google/uuid"
)

// MaxSessionCount 与设计文档中的并发 session 上限一致。
const MaxSessionCount = 3

const (
	mockModel      = "deepseek-flash"
	eventBufferCap = 4096
)

// MockOptions 仅用于控制临时内存 Mock 的演示和测试行为。
type MockOptions struct {
	AutoRun   bool
	StepDelay time.Duration
}

type mockSession struct {
	deleted bool
	runIDs  []string
}

type mockRun struct {
	run             Run
	events          []Event
	subscriber      *mockSubscriber
	cancelRequested bool
	cancelWake      chan struct{}
	checkpoint      string
}

type mockSubscriber struct {
	events chan Event
	done   chan struct{}
}

type idempotencyRecord struct {
	input CreateRunInput
	runID string
}

// Mock 是 API 联调阶段使用的临时内存实现，后续接入真实应用层时应删除或替换。
type Mock struct {
	mu         sync.Mutex
	sessions   map[string]*mockSession
	runs       map[string]*mockRun
	idempotent map[string]idempotencyRecord
	now        func() time.Time
	options    MockOptions
}

// NewMock 创建会自动输出固定事件序列的临时 Mock。
func NewMock() *Mock {
	return NewMockWithOptions(MockOptions{AutoRun: true, StepDelay: 80 * time.Millisecond})
}

// NewMockWithOptions 创建可控的临时 Mock；AutoRun=false 时由测试或调用方发布事件。
func NewMockWithOptions(options MockOptions) *Mock {
	if options.StepDelay <= 0 {
		options.StepDelay = 80 * time.Millisecond
	}
	return &Mock{
		sessions:   make(map[string]*mockSession),
		runs:       make(map[string]*mockRun),
		idempotent: make(map[string]idempotencyRecord),
		now:        time.Now,
		options:    options,
	}
}

// CreateRun 创建内存 run，模拟幂等、session 忙碌和并发上限；不连接数据库或模型。
func (m *Mock) CreateRun(ctx context.Context, input CreateRunInput) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	if session := m.sessions[input.SessionID]; session != nil && session.deleted {
		m.mu.Unlock()
		return "", ErrSessionDeleted
	}
	if previous, exists := m.idempotent[input.IdempotencyKey]; exists {
		if previous.input != input {
			m.mu.Unlock()
			return "", ErrIdempotencyConflict
		}
		m.mu.Unlock()
		return previous.runID, nil
	}
	if session := m.sessions[input.SessionID]; session != nil {
		for _, runID := range session.runIDs {
			if m.runs[runID].run.Status == StatusRunning {
				m.mu.Unlock()
				return "", ErrSessionBusy
			}
		}
	}
	if m.activeSessionCountLocked() >= MaxSessionCount {
		m.mu.Unlock()
		return "", ErrSessionConcurrencyLimit
	}
	runID := uuid.NewString()
	createdAt := m.now().In(model.BeijingLocation())
	if m.sessions[input.SessionID] == nil {
		m.sessions[input.SessionID] = &mockSession{}
	}
	m.sessions[input.SessionID].runIDs = append(m.sessions[input.SessionID].runIDs, runID)
	m.runs[runID] = &mockRun{
		run: Run{
			RunID: runID, SessionID: input.SessionID, Model: input.Model, Input: input.Input,
			Status: StatusRunning, CreatedAt: createdAt,
		},
		cancelWake: make(chan struct{}, 1),
	}
	m.idempotent[input.IdempotencyKey] = idempotencyRecord{input: input, runID: runID}
	m.mu.Unlock()

	if m.options.AutoRun {
		go m.runDemo(runID)
	}
	return runID, nil
}

// GetRun 返回可见 run 的受控视图。
func (m *Mock) GetRun(ctx context.Context, runID string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, err := m.visibleRunLocked(runID)
	if err != nil {
		return Run{}, err
	}
	return cloneRun(run.run), nil
}

// CancelRun 请求取消；运行中状态只在 Mock 确认终态后改变。
func (m *Mock) CancelRun(ctx context.Context, runID string) (Status, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, err := m.visibleRunLocked(runID)
	if err != nil {
		return "", err
	}
	if run.run.Status != StatusRunning {
		return run.run.Status, nil
	}
	run.cancelRequested = true
	select {
	case run.cancelWake <- struct{}{}:
	default:
	}
	return StatusRunning, nil
}

// GetSession 返回 session 内按创建时间升序排列的可见 run ID。
func (m *Mock) GetSession(ctx context.Context, sessionID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[sessionID]
	if session == nil || session.deleted {
		return nil, ErrSessionNotFound
	}
	ids := append([]string(nil), session.runIDs...)
	sort.SliceStable(ids, func(i, j int) bool {
		return m.runs[ids[i]].run.CreatedAt.Before(m.runs[ids[j]].run.CreatedAt)
	})
	return ids, nil
}

// ListSessions 返回有 run 的未删除 session，按最新 run 创建时间降序排列。
func (m *Mock) ListSessions(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type item struct {
		id      string
		created time.Time
	}
	items := make([]item, 0, len(m.sessions))
	for id, session := range m.sessions {
		if session.deleted || len(session.runIDs) == 0 {
			continue
		}
		var latest time.Time
		for _, runID := range session.runIDs {
			created := m.runs[runID].run.CreatedAt
			if created.After(latest) {
				latest = created
			}
		}
		items = append(items, item{id: id, created: latest})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].created.Equal(items[j].created) {
			return items[i].id < items[j].id
		}
		return items[i].created.After(items[j].created)
	})
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}
	return ids, nil
}

// DeleteSession 立即隐藏 session，并异步请求其活跃 run 取消。
func (m *Mock) DeleteSession(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	session := m.sessions[sessionID]
	if session == nil {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if session.deleted {
		m.mu.Unlock()
		return nil
	}
	session.deleted = true
	for _, runID := range session.runIDs {
		run := m.runs[runID]
		if run.run.Status == StatusRunning {
			run.cancelRequested = true
			select {
			case run.cancelWake <- struct{}{}:
			default:
			}
		}
	}
	m.mu.Unlock()
	return nil
}

// Subscribe 校验游标并原子地衔接历史回放与后续事件。
func (m *Mock) Subscribe(ctx context.Context, runID string, after int64) (*Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if after < 0 {
		return nil, ErrInvalidRequest
	}
	m.mu.Lock()
	run, err := m.visibleRunLocked(runID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	maxID := int64(len(run.events))
	if after > maxID {
		m.mu.Unlock()
		return nil, ErrInvalidRequest
	}
	if run.run.Status != StatusRunning {
		m.mu.Unlock()
		return nil, ErrRunTerminal
	}
	if run.subscriber != nil {
		m.mu.Unlock()
		return nil, ErrSSEConnectionExists
	}
	capacity := len(run.events) + eventBufferCap
	if capacity < eventBufferCap {
		capacity = eventBufferCap
	}
	subscriber := &mockSubscriber{events: make(chan Event, capacity), done: make(chan struct{})}
	for _, event := range run.events {
		if event.ID > after {
			subscriber.events <- cloneEvent(event)
		}
	}
	run.subscriber = subscriber
	m.mu.Unlock()

	var once sync.Once
	closeSubscription := func() {
		once.Do(func() {
			m.mu.Lock()
			if current := m.runs[runID]; current != nil && current.subscriber == subscriber {
				current.subscriber = nil
				close(subscriber.events)
				close(subscriber.done)
			}
			m.mu.Unlock()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			closeSubscription()
		case <-subscriber.done:
		}
	}()
	return &Subscription{Events: subscriber.events, Close: closeSubscription}, nil
}

// PublishEvent 由测试或临时装配控制事件源；事件 ID 由 Mock 按 run 顺序分配。
func (m *Mock) PublishEvent(runID string, eventType EventType, data json.RawMessage) (Event, error) {
	if !validEventType(eventType) || !json.Valid(data) {
		return Event{}, ErrInvalidRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, err := m.mutableRunLocked(runID)
	if err != nil {
		return Event{}, err
	}
	if run.run.Status != StatusRunning {
		return Event{}, ErrRunTerminal
	}
	event := Event{ID: int64(len(run.events) + 1), Type: eventType, Data: append(json.RawMessage(nil), data...)}
	if run.subscriber != nil {
		select {
		case run.subscriber.events <- cloneEvent(event):
		default:
			return Event{}, ErrEventBufferFull
		}
	}
	run.events = append(run.events, event)
	return cloneEvent(event), nil
}

// ConfirmRun 由可控测试事件源确认终态，并将下层提供的 end 数据转发给现有订阅。
func (m *Mock) ConfirmRun(runID string, status Status, output *string, runErr *RunError, endData json.RawMessage) error {
	if status != StatusSucceeded && status != StatusFailed && status != StatusCancelled {
		return ErrInvalidRequest
	}
	if len(endData) > 0 && !json.Valid(endData) {
		return ErrInvalidRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, err := m.mutableRunLocked(runID)
	if err != nil {
		return err
	}
	if run.run.Status != StatusRunning {
		return ErrRunTerminal
	}
	if len(endData) > 0 {
		event := Event{ID: int64(len(run.events) + 1), Type: EventEnd, Data: append(json.RawMessage(nil), endData...)}
		if run.subscriber != nil {
			select {
			case run.subscriber.events <- cloneEvent(event):
			default:
				return ErrEventBufferFull
			}
		}
		run.events = append(run.events, event)
	}
	now := m.now().In(model.BeijingLocation())
	run.run.Status = status
	run.run.Output = cloneString(output)
	run.run.Error = cloneRunError(runErr)
	run.run.FinishedAt = &now
	if run.subscriber != nil {
		close(run.subscriber.events)
		close(run.subscriber.done)
		run.subscriber = nil
	}
	return nil
}

func (m *Mock) activeSessionCountLocked() int {
	count := 0
	for _, session := range m.sessions {
		for _, runID := range session.runIDs {
			if m.runs[runID].run.Status == StatusRunning {
				count++
				break
			}
		}
	}
	return count
}

func (m *Mock) visibleRunLocked(runID string) (*mockRun, error) {
	run := m.runs[runID]
	if run == nil {
		return nil, ErrRunNotFound
	}
	session := m.sessions[run.run.SessionID]
	if session == nil || session.deleted {
		return nil, ErrRunNotFound
	}
	return run, nil
}

// mutableRunLocked 允许异步执行器在 session 删除后完成已有 run 收尾。
func (m *Mock) mutableRunLocked(runID string) (*mockRun, error) {
	run := m.runs[runID]
	if run == nil {
		return nil, ErrRunNotFound
	}
	return run, nil
}

func (m *Mock) runDemo(runID string) {
	if _, err := m.PublishEvent(runID, EventStart, json.RawMessage(`{}`)); err != nil {
		return
	}
	if !m.demoWait(runID) {
		return
	}
	if _, err := m.PublishEvent(runID, EventThinking, mustJSON(map[string]any{"content": "正在整理本轮问题。", "error": nil})); err != nil {
		return
	}
	if !m.demoWait(runID) {
		return
	}
	parts := []string{"这是一条固定的 Mock 回复，", "用于展示逐条事件输出。"}
	var output strings.Builder
	for _, part := range parts {
		output.WriteString(part)
		m.mu.Lock()
		if run := m.runs[runID]; run != nil {
			run.checkpoint = output.String()
		}
		m.mu.Unlock()
		if _, err := m.PublishEvent(runID, EventText, mustJSON(map[string]any{"content": part, "error": nil})); err != nil {
			return
		}
		if !m.demoWait(runID) {
			return
		}
	}
	_ = m.ConfirmRun(runID, StatusSucceeded, stringPointer(output.String()), nil, mustJSON(map[string]any{"stop_reason": "stop", "error": nil}))
}

// demoWait 将异步延迟限制在固定间隔，并在取消请求后于事件边界确认停止。
func (m *Mock) demoWait(runID string) bool {
	timer := time.NewTimer(m.options.StepDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-m.cancelSignal(runID):
		m.finishDemoCancelled(runID)
		return false
	}
	m.mu.Lock()
	run := m.runs[runID]
	cancelled := run == nil || run.cancelRequested
	m.mu.Unlock()
	if cancelled {
		m.finishDemoCancelled(runID)
		return false
	}
	return true
}

func (m *Mock) cancelSignal(runID string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run := m.runs[runID]; run != nil {
		return run.cancelWake
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}

func (m *Mock) finishDemoCancelled(runID string) {
	m.mu.Lock()
	run := m.runs[runID]
	var checkpoint string
	if run != nil {
		checkpoint = run.checkpoint
	}
	m.mu.Unlock()
	_ = m.ConfirmRun(runID, StatusCancelled, stringPointer(checkpoint), nil, mustJSON(map[string]any{"stop_reason": "cancelled", "error": nil}))
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventStart, EventThinking, EventText, EventTool, EventEnd:
		return true
	default:
		return false
	}
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func cloneRun(run Run) Run {
	run.Output = cloneString(run.Output)
	run.Error = cloneRunError(run.Error)
	if run.FinishedAt != nil {
		finished := *run.FinishedAt
		run.FinishedAt = &finished
	}
	return run
}

func cloneEvent(event Event) Event {
	event.Data = append(json.RawMessage(nil), event.Data...)
	return event
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneRunError(runErr *RunError) *RunError {
	if runErr == nil {
		return nil
	}
	copy := *runErr
	return &copy
}

func stringPointer(value string) *string { return &value }

var _ Service = (*Mock)(nil)
