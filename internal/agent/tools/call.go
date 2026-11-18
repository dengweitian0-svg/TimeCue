package tools

import (
	"encoding/json"
	"time"
)

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

// ToolState 是工具调用持久化使用的状态。
type ToolState string

const (
	StateRunning   ToolState = "running"
	StateRetryWait ToolState = "retry_wait"
	StateSucceeded ToolState = "succeeded"
	StateFailed    ToolState = "failed"
	StateSkipped   ToolState = "skipped"
)

// Terminal 判断工具调用是否已结算。
func (state ToolState) Terminal() bool {
	return state == StateSucceeded || state == StateFailed || state == StateSkipped
}

// ToolCall 保存工具执行身份、原始结果及版本 3 已有的重试事实。
type ToolCall struct {
	// ModelContentLimit 仅用于本次结果投影；已提交文本直接沿 checkpoint 复用。
	ModelContentLimit int
	ID                int64
	IdempotencyKey    string
	ModelCallID       int64
	Request           ToolRequest
	Output            json.RawMessage
	Error             *Failure
	State             ToolState
	MaxRetry          int
	RetryCount        int
	NextRetryAt       *time.Time
}

// WithResultUnconfirmed 保留原调用身份，为无法确认的处理生成统一失败事实。
func (tool ToolCall) WithResultUnconfirmed() ToolCall {
	tool.State = StateFailed
	tool.Error = &Failure{
		Code:    "tool_result_unconfirmed",
		Message: "中断前工具结果未能确认，未重新执行",
	}
	return tool
}

// WithTerminalResult 保留已结算结果，并按运行取消和已登记事实补齐未完成项。
func (tool ToolCall) WithTerminalResult(registered, cancelled bool) ToolCall {
	if registered && !tool.unsettled() {
		return tool
	}
	if !cancelled || registered && tool.State == StateRunning {
		return tool.WithResultUnconfirmed()
	}
	tool.State = StateSkipped
	tool.Output = nil
	tool.Error = &Failure{
		Code:    "tool_skipped",
		Message: "运行已取消，未启动或未恢复此工具",
	}
	return tool
}
