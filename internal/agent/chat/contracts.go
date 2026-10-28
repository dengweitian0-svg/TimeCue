package chat

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
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
	EventStart      EventType = "start"
	EventThinking   EventType = "thinking"
	EventText       EventType = "text"
	EventError      EventType = "error"
	EventEnd        EventType = "end"
	EventToolStart  EventType = "tool_start"
	EventToolResult EventType = "tool_result"
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
	RunID   string    `json:"run_id"`
	Status  Status    `json:"status"`
	Output  *string   `json:"output"`
	Error   *RunError `json:"error"`
	created bool
}

// NewlyCreated 区分本次真正创建与幂等重放，不进入公开四字段 JSON。
func (r Receipt) NewlyCreated() bool { return r.created }

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
	MaxRounds       int
	FirstTimeout    time.Duration
	IdleTimeout     time.Duration
}

// Message 保存实际模型消息，包括已收到的思考内容。
type Message struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
}

// ModelRequest 是实际提交的 Chat Completions 请求。
type ModelRequest struct {
	Model         string             `json:"model"`
	Messages      []Message          `json:"messages"`
	Stream        bool               `json:"stream"`
	MaxTokens     int                `json:"max_tokens"`
	StreamOptions map[string]bool    `json:"stream_options"`
	Tools         []tools.Definition `json:"tools,omitempty"`
}

// ToolRequest 保存一次模型回复内完整组装的工具请求及参数原文。
type ToolRequest struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction 保留模型提供的名称和 arguments 字符串，不改写参数。
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall 保存工具执行身份、原始结果及版本 3 已有的重试事实。
type ToolCall struct {
	ID          int64
	ModelCallID int64
	Request     ToolRequest
	Output      json.RawMessage
	Error       *RunError
	State       string
	MaxRetry    int
	RetryCount  int
}

// ToolStartData 保存公开工具开始事件，一个开始记录包含实际处理的调用。
type ToolStartData struct {
	Function []ToolStartFunction `json:"function"`
}

// ToolStartFunction 保存原调用身份及解析对象投影，不对外暴露非法参数片段。
type ToolStartFunction struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Argument json.RawMessage `json:"argument"`
}

// ToolResultData 保存原调用的原始 JSON 结果和独立工具错误。
type ToolResultData struct {
	ToolCallID string          `json:"tool_call_id"`
	Output     json.RawMessage `json:"output"`
	Error      *RunError       `json:"error"`
}

// ToolRepository 将模型接纳、工具开始与逐个结果分别原子提交。
type ToolRepository interface {
	// AcceptResponse 保存完整模型结果、尾批及 model_call_end，提交后才启动工具。
	AcceptResponse(context.Context, ModelCall) error
	// ReadTools 读取当前模型调用已登记的工具事实，不跨回复复用同值 ID。
	ReadTools(context.Context, int64) ([]ToolCall, error)
	// BeginTool 原子登记开始事实，返回已提交完整进度与原身份工具；失败保留传入快照。
	BeginTool(context.Context, ModelCall, ToolCall) (ModelCall, ToolCall, error)
	// FinishTool 沿事务内已提交事实合并结果，返回已提交完整进度；失败保留传入快照。
	FinishTool(context.Context, ModelCall, ToolCall) (ModelCall, error)
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
	// 进度字段是单一事实来源，由 progress.go 的完整操作生成，协调器仅采用成功提交的快照。
	CheckpointStage   string
	CheckpointContext []Message
	CompletedTools    []string
	LastTool          string
	// PendingEvents 与调用快照一起提交，序号只在事务成功后对外可见。
	PendingEvents []Event
	// PendingTools 在事务失败后保留已取得的实际结果，随终态事务重试保存，不重新执行业务。
	PendingTools []ToolCall
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
	// OrdinaryCallCount 统计当前运行中非续写且不关联 compact 的逻辑模型调用。
	OrdinaryCallCount(context.Context, string) (int, error)
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

// Storage 分别装配运行、会话、模型执行、工具事务及事件查询端口。
type Storage struct {
	Runs      RunRepository
	Sessions  SessionRepository
	Execution ExecutionRepository
	Events    EventRepository
	Tools     ToolRepository
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
