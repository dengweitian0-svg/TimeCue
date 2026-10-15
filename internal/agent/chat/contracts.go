package chat

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Status 表示一次对话运行的状态。
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// EventType 是流式事件的受控类型。
type EventType string

const (
	EventStart    EventType = "start"
	EventThinking EventType = "thinking"
	EventText     EventType = "text"
	EventTool     EventType = "tool"
	EventEnd      EventType = "end"
)

var (
	ErrInvalidRequest          = errors.New("invalid request")
	ErrRunNotFound             = errors.New("run not found")
	ErrSessionNotFound         = errors.New("session not found")
	ErrSessionDeleted          = errors.New("session deleted")
	ErrSessionBusy             = errors.New("session already has a running run")
	ErrSessionConcurrencyLimit = errors.New("session concurrency limit reached")
	ErrIdempotencyConflict     = errors.New("idempotency key conflict")
	ErrRunTerminal             = errors.New("run is terminal")
	ErrSSEConnectionExists     = errors.New("sse connection already exists")
	ErrEventBufferFull         = errors.New("event subscriber buffer is full")
)

// RunError 是 run 对外公开的稳定错误信息。
type RunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CreateRunInput 是创建 run 的应用层输入；各字段按调用方原样传递。
type CreateRunInput struct {
	SessionID      string
	Input          string
	Model          string
	IdempotencyKey string
}

// Run 是 HTTP API 与应用层之间共享的受控 run 视图。
type Run struct {
	RunID      string     `json:"run_id"`
	SessionID  string     `json:"session_id"`
	Model      string     `json:"model"`
	Input      string     `json:"input"`
	Status     Status     `json:"status"`
	Output     *string    `json:"output"`
	Error      *RunError  `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Event 保留下层提供的事件 ID 和 JSON 数据，不由 HTTP Handler 生成。
type Event struct {
	ID   int64           `json:"id"`
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

// StartData 表示模型开始输出的事件数据。
type StartData struct{}

// ThinkingData 表示思考过程增量。
type ThinkingData struct {
	Content string    `json:"content"`
	Error   *RunError `json:"error"`
}

// TextData 表示回复文本增量。
type TextData struct {
	Content string    `json:"content"`
	Error   *RunError `json:"error"`
}

// ToolData 保留工具调用事件协议；v1 Mock 不主动生成工具调用。
type ToolData struct {
	ID       string       `json:"id"`
	Function ToolFunction `json:"function"`
}

// ToolFunction 是工具调用的数据函数。
type ToolFunction struct {
	Name string   `json:"name"`
	Args ToolArgs `json:"args"`
}

// ToolArgs 描述受限工具参数。
type ToolArgs struct {
	Name string `json:"name"`
	Val  any    `json:"val"`
}

// EndData 表示 run 终止原因及下层错误。
type EndData struct {
	StopReason string    `json:"stop_reason"`
	Error      *RunError `json:"error"`
}

// Subscription 在返回前完成历史事件、实时事件及连接名额的原子衔接。
type Subscription struct {
	Events <-chan Event
	Close  func()
}

// Service 是聊天 HTTP API 所依赖的窄应用端口。
type Service interface {
	// CreateRun 创建一次对话运行并返回其 ID。
	CreateRun(context.Context, CreateRunInput) (string, error)
	// GetRun 查询 run 的受控状态视图。
	GetRun(context.Context, string) (Run, error)
	// CancelRun 请求取消 run，并返回当前状态。
	CancelRun(context.Context, string) (Status, error)
	// GetSession 按创建时间顺序返回 session 的 run ID。
	GetSession(context.Context, string) ([]string, error)
	// ListSessions 按最新 run 创建时间返回可见 session ID。
	ListSessions(context.Context) ([]string, error)
	// DeleteSession 隐藏 session 并请求异步清理其 run。
	DeleteSession(context.Context, string) error
	// Subscribe 从指定事件游标订阅 run 的历史及实时事件。
	Subscribe(context.Context, string, int64) (*Subscription, error)
}
