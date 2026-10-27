package chat

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Status 是对外展示的运行状态。
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// RunState 是内部状态；等待取消仍占用运行名额。
type RunState string

const (
	StateRunning RunState = "running"
	// StateRetryWait 仅用于逻辑模型调用，运行本身仍保持 running。
	StateRetryWait     RunState = "retry_wait"
	StateWaitForCancel RunState = "wait_for_cancel"
	StateSucceeded     RunState = "succeeded"
	StateFailed        RunState = "failed"
	StateCancelled     RunState = "cancelled"
)

// Terminal 判断内部运行是否已经结束。
func (s RunState) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

// Public 返回 HTTP 展示状态。
func (s RunState) Public() Status {
	if s == StateWaitForCancel {
		return StatusCancelled
	}
	return Status(s)
}

// EventType 是持久化事件类型。
type EventType string

const (
	EventStart    EventType = "start"
	EventThinking EventType = "thinking"
	EventText     EventType = "text"
	EventError    EventType = "error"
	EventEnd      EventType = "end"
)

var (
	ErrInvalidRequest          = errors.New("invalid request")
	ErrModelUnavailable        = errors.New("model configuration unavailable")
	ErrRunNotFound             = errors.New("run not found")
	ErrSessionNotFound         = errors.New("session not found")
	ErrSessionDeleted          = errors.New("session deleted")
	ErrSessionBusy             = errors.New("session already has a running run")
	ErrSessionConcurrencyLimit = errors.New("session concurrency limit reached")
	ErrIdempotencyConflict     = errors.New("idempotency key conflict")
	ErrRunTerminal             = errors.New("run is terminal")
	ErrStopping                = errors.New("chat service stopping")
)

// RunError 是模型或运行的公开错误，不包含上游密钥。
type RunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CreateRunInput 保留客户端本轮原始输入和模型选择。
type CreateRunInput struct {
	SessionID      string
	Input          string
	Provider       string
	Model          string
	IdempotencyKey string
}

// Receipt 是固定四字段创建回执。
type Receipt struct {
	RunID  string    `json:"run_id"`
	Status Status    `json:"status"`
	Output *string   `json:"output"`
	Error  *RunError `json:"error"`
}

// Run 是固定十字段运行概况。
type Run struct {
	RunID      string     `json:"run_id"`
	SessionID  string     `json:"session_id"`
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	Input      string     `json:"input"`
	Status     Status     `json:"status"`
	Output     *string    `json:"output"`
	Error      *RunError  `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Record 将持久化内部状态与对外视图区分。
type Record struct {
	Run
	State          RunState
	IdempotencyKey string
}

// Session 是可见会话及按创建顺序排列的运行 ID。
type Session struct {
	SessionID   string   `json:"session_id"`
	SessionName string   `json:"session_name"`
	RunIDs      []string `json:"run_ids"`
}

// Event 的序号由持久化事务分配，SSE 与分页共用。
type Event struct {
	ID   int64           `json:"seq"`
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

// EventPage 是事件游标分页。
type EventPage struct {
	Events       []Event `json:"events"`
	NextAfterSeq int64   `json:"next_after_seq"`
	HasMore      bool    `json:"has_more"`
}

// StartData 表示运行开始。
type StartData struct{}

// ThinkingData 保存思考增量。
type ThinkingData struct {
	Content string `json:"content"`
}

// TextData 保存正文增量。
type TextData struct {
	Content string `json:"content"`
}

// EndData 只表达最终状态。
type EndData struct {
	Status Status `json:"status"`
}

// Subscription 的关闭只影响订阅，不影响运行。
type Subscription struct {
	Events <-chan Event
	Close  func()
}

// ModelOption 是模型列表的公开条目。
type ModelOption struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Available bool   `json:"available"`
}

// ModelConfig 是一次请求使用的私有配置，密钥从不进入持久化输入。
type ModelConfig struct {
	Provider        string
	Model           string
	BaseURL         string
	Key             string
	ContextWindow   int
	MaxOutputTokens int
	MaxRetry        int
	FirstTimeout    time.Duration
	IdleTimeout     time.Duration
}

// Message 保存实际模型消息，包括已收到的思考内容。
type Message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// ModelRequest 是实际提交的 Chat Completions 请求。
type ModelRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	Stream        bool            `json:"stream"`
	MaxTokens     int             `json:"max_tokens"`
	StreamOptions map[string]bool `json:"stream_options"`
}

// ModelOutput 保留结构化部分响应、完成原因和原始 usage。
type ModelOutput struct {
	ID           string          `json:"id"`
	Message      Message         `json:"message"`
	FinishReason string          `json:"finish_reason"`
	Usage        json.RawMessage `json:"usage"`
}

// ModelClient 在上游边界提供可取消的增量请求。
type ModelClient interface {
	// Complete 通过上游端口读取真实模型响应及有效增量。
	Complete(context.Context, ModelConfig, ModelRequest, func(EventType, string) error) (ModelOutput, *RunError)
}

// ModelCall 保存调用开始或结束时需要原子提交的实际事实。
type ModelCall struct {
	ID           int64
	RunID        string
	SessionID    string
	Input        ModelRequest
	Output       *ModelOutput
	State        RunState
	Error        *RunError
	MaxRetry     int
	RetryCount   int
	NextRetryAt  *time.Time
	RetryHistory []ModelRetry
	// PendingEvents 与调用快照一起提交，序号只在事务成功后对外可见。
	PendingEvents []Event
}

// ModelRetry 保存同一逻辑调用内一次已计划重试的时间、结果及故障原因。
type ModelRetry struct {
	Retry  int       `json:"retry"`
	At     time.Time `json:"at"`
	Result string    `json:"result"`
	Cause  *RunError `json:"cause"`
	Error  *RunError `json:"error"`
}

// RunRepository 管理运行准入、幂等回读与运行查询。
type RunRepository interface {
	// FindReplay 读取原请求回执并检查父会话是否删除。
	FindReplay(context.Context, string, string) (Record, error)
	// Create 原子创建会话、运行与开始事件。
	Create(context.Context, Record) error
	// ReadRun 读取运行内部状态，可要求父会话可见。
	ReadRun(context.Context, string, bool) (Record, error)
	// Active 扫描尚未结束的运行，供启动恢复登记。
	Active(context.Context) ([]Record, error)
}

// SessionRepository 管理会话查询及软删除可见性。
type SessionRepository interface {
	// Session 查询会话名称和按顺序排列的运行 ID。
	Session(context.Context, string) (Session, error)
	// Sessions 按最近运行顺序读取可见会话 ID。
	Sessions(context.Context) ([]string, error)
	// Hide 同步软删除父会话，不删除执行事实。
	Hide(context.Context, string) error
}

// ExecutionRepository 保存模型执行事实及其原子进度。
type ExecutionRepository interface {
	// History 从实际输入与模型输出组装有效消息。
	History(context.Context, string) ([]Message, error)
	// BeginCall 原子保存请求 input、running 和 checkpoint。
	BeginCall(context.Context, ModelCall) (int64, error)
	// ReadCall 读取当前逻辑调用的原请求与部分结果。
	ReadCall(context.Context, string) (*ModelCall, error)
	// Pause 保存正常退出时的部分响应，不推进终态。
	Pause(context.Context, ModelCall) error
	// SaveEvents 原子保存部分模型输出及待提交的原始事件批次。
	SaveEvents(context.Context, ModelCall) error
	// Finish 原子提交模型结果、usage、checkpoint、运行终态及尾部事件。
	Finish(context.Context, ModelCall, Record) error
}

// EventRepository 只读查询持久化事件，不承担运行推进。
type EventRepository interface {
	// Events 按游标读取完整事件并返回下一页信息。
	Events(context.Context, string, int64, int) (EventPage, error)
}

// Storage 分别装配四个职责独立的持久化端口，不要求同一对象实现全部能力。
type Storage struct {
	Runs      RunRepository
	Sessions  SessionRepository
	Execution ExecutionRepository
	Events    EventRepository
}

// ModelService 提供本地模型配置选项，不承担运行与会话用例。
type ModelService interface {
	// GetModels 读取提供方、模型及当前本地可用性。
	GetModels(context.Context) ([]ModelOption, error)
}

// RunService 管理单次对话运行、取消与事件消费。
type RunService interface {
	// CreateRun 创建或重放一次对话运行。
	CreateRun(context.Context, CreateRunInput) (Receipt, error)
	// GetRun 查询运行的固定公开概况。
	GetRun(context.Context, string) (Run, error)
	// GetRunEvents 按游标分页读取原始持久化事件。
	GetRunEvents(context.Context, string, int64, int) (EventPage, error)
	// CancelRun 受理协作式取消并返回当前公开状态。
	CancelRun(context.Context, string) (Status, error)
	// Subscribe 订阅运行事件，关闭订阅不取消运行。
	Subscribe(context.Context, string, int64) (*Subscription, error)
}

// SessionService 管理会话查询和软删除。
type SessionService interface {
	// GetSession 查询可见会话及有序运行 ID。
	GetSession(context.Context, string) (Session, error)
	// ListSessions 按最近运行顺序返回可见会话 ID。
	ListSessions(context.Context) ([]string, error)
	// DeleteSession 隐藏会话并请求停止后续推进。
	DeleteSession(context.Context, string) error
}

// Services 分别注入模型、运行与会话用例，消费方只依赖对应接口。
type Services struct {
	Model   ModelService
	Run     RunService
	Session SessionService
}

// Lifecycle 由 App 管理启动恢复与退出，不暴露给 HTTP Handler。
type Lifecycle interface {
	// Start 在 ready 前恢复占用并启动后台推进。
	Start(context.Context) error
	// Stop 停止准入和上游请求，保存已取得进度。
	Stop()
}
