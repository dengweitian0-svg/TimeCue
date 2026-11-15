package chat

import (
	"encoding/json"
	"slices"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

// CheckpointState 是版本 3 的三字段磁盘格式，不持有独立于 ModelCall 的可推进状态。
type CheckpointState struct {
	Context   []Message `json:"context"`
	Completed []string  `json:"completed_tool"`
	Last      string    `json:"last_tool"`
}

// Checkpoint 返回隔离的可保存快照，以 ModelCall 的进度字段为唯一事实来源。
func (call ModelCall) Checkpoint() CheckpointState {
	return CheckpointState{
		Context:   cloneMessages(call.CheckpointContext),
		Completed: append([]string{}, call.CompletedTools...),
		Last:      call.LastTool,
	}
}

// WithCheckpoint 回读同一调用的已提交进度，保留其完成集合和最近工具事实。
func (call ModelCall) WithCheckpoint(stage string, state CheckpointState) ModelCall {
	call.CheckpointStage = stage
	call.CheckpointContext = cloneMessages(state.Context)
	call.CompletedTools = append([]string{}, state.Completed...)
	call.LastTool = state.Last
	return call
}

// WithCallStarted 为新调用建立请求快照并清空本次工具事实，不用于恢复同一调用。
func (call ModelCall) WithCallStarted() ModelCall {
	stage := "model_call_start"
	if call.CheckpointStage == "compact" {
		stage = "compact"
		if len(call.CheckpointContext) > 0 && !call.Checkpoint().NeedsCompactBase(stage) {
			// 运行中摘要尚未生效时保留工具完整配对，失败与退出不会丢失本 run 事实。
			return call.WithCheckpoint(stage, call.Checkpoint())
		}
	}
	return call.WithCheckpoint(stage, CheckpointState{Context: call.Input.Messages})
}

// PresentedOutput 只拼接当前普通回复及其续写正文，不修改实际模型输出。
func (call ModelCall) PresentedOutput() *string {
	if call.CheckpointStage == "compact" {
		if call.OutputPrefix == "" {
			return nil
		}
		return &call.OutputPrefix
	}
	content := call.OutputPrefix
	if call.Output != nil {
		content += call.Output.Message.Content
	}
	if content == "" {
		return nil
	}
	return &content
}

// WithResponseAccepted 生成完整成功回复的待提交进度，提交成功后才可进入工具流程。
func (call ModelCall) WithResponseAccepted() ModelCall {
	if call.CheckpointStage == "compact" {
		return call.WithCompactResponse()
	}
	call.State = StateSucceeded
	call.Error = nil
	// 从实际请求构造一次回复快照，不累计上次调用的工具事实。
	call = call.WithCallStarted()
	return call.WithCallEnded()
}

// WithCallEnded 生成调用结束快照，未接纳的工具片段只保留可用正文和思考。
func (call ModelCall) WithCallEnded() ModelCall {
	if call.CheckpointStage == "compact" {
		return call.WithCheckpoint("compact", call.Checkpoint())
	}
	if call.CheckpointStage == "tool_start" || call.CheckpointStage == "tool_end" ||
		call.CheckpointStage == "model_call_end" && call.State == StateSucceeded {
		return call.WithCheckpoint(call.CheckpointStage, call.Checkpoint())
	}
	call = call.WithCallStarted()
	call.CheckpointStage = "model_call_end"
	if call.Output != nil {
		message := call.Output.Message
		if call.Error != nil || call.State != StateSucceeded {
			message.ToolCalls = nil
		}
		call.CheckpointContext = appendAssistant(call.CheckpointContext, message)
	}
	return call
}

// WithToolStarted 在原进度上生成单项开始快照，不改变此前保存的消息或完成集合。
func (call ModelCall) WithToolStarted(tool ToolCall) ModelCall {
	call = call.WithCheckpoint("tool_start", call.Checkpoint())
	call.LastTool = "tool_use: " + tool.Request.Function.Name + "-" + tool.Request.ID
	return call
}

// ToolCompleted 判断当前回复的原 ID 是否已有配对结果，不表示业务成功。
func (call ModelCall) ToolCompleted(id string) bool {
	return slices.Contains(call.CompletedTools, id)
}

// WithToolResult 合入单项配对结果，仅在全部原调用均有结果时表达批次完成事实。
func (call ModelCall) WithToolResult(tool ToolCall) ModelCall {
	if call.ToolCompleted(tool.Request.ID) {
		return call.WithCheckpoint(call.CheckpointStage, call.Checkpoint())
	}
	call = call.WithCheckpoint("tool_start", call.Checkpoint())
	call.CheckpointContext = append(call.CheckpointContext, toolResultMessage(call, tool))
	call.CompletedTools = append(call.CompletedTools, tool.Request.ID)
	call.LastTool = "tool_result: " + tool.Request.Function.Name + "-" + tool.Request.ID
	if call.toolBatchComplete() {
		call.CheckpointStage = "tool_end"
	}
	return call
}

func (call ModelCall) toolBatchComplete() bool {
	var requests []ToolRequest
	if call.Output == nil || json.Unmarshal(call.Output.Message.ToolCalls, &requests) != nil {
		return false
	}
	return (tools.Batch{Requests: requests}).Complete(call)
}

// WithRunFinished 沿事务内已提交事实收拢异常批次，实际结果优先于取消或未确认补齐。
func (call ModelCall) WithRunFinished(status RunState, stored []ToolCall) (ModelCall, []ToolCall, error) {
	call = call.WithCallEnded()
	if call.State != StateSucceeded || !call.hasToolCalls() || status == StateSucceeded {
		return call, nil, nil
	}
	var requests []ToolRequest
	if err := json.Unmarshal(call.Output.Message.ToolCalls, &requests); err != nil {
		return call, nil, err
	}
	known := make(map[string]ToolCall, len(stored))
	for _, tool := range stored {
		known[tool.Request.ID] = tool
	}
	results := []ToolCall{}
	for _, tool := range call.PendingTools {
		if call.ToolCompleted(tool.Request.ID) {
			continue
		}
		if saved, ok := known[tool.Request.ID]; ok && saved.State != "running" && saved.State != "retry_wait" {
			tool = saved
		}
		call = call.WithToolResult(tool)
		results = append(results, tool)
	}
	for _, request := range requests {
		if call.ToolCompleted(request.ID) {
			continue
		}
		tool, exists := known[request.ID]
		if !exists {
			tool = ToolCall{
				ModelCallID: call.ID,
				Request:     request,
				MaxRetry:    tools.DefaultMaxRetry,
			}
		}
		tool = tool.WithTerminalResult(exists, status == StateCancelled)
		call = call.WithToolResult(tool)
		results = append(results, tool)
	}
	return call, results, nil
}

// WithCompactResponse 接纳摘要片段，保持专用请求的消息边界和 compact 阶段。
func (call ModelCall) WithCompactResponse() ModelCall {
	call.State, call.Error = StateSucceeded, nil
	call = call.WithCallStarted()
	if !call.Checkpoint().NeedsCompactBase("compact") {
		return call
	}
	if call.Output != nil {
		call.CheckpointContext = appendAssistant(call.CheckpointContext, call.Output.Message)
	}
	return call
}

// NeedsCompactBase 表示尚未生效的摘要不能作为有效历史，须沿原快照保留历史与真实输入。
func (state CheckpointState) NeedsCompactBase(stage string) bool {
	return stage == "compact" && len(state.Context) > 0 && state.Context[0].Role == "system" && state.Context[0].Content == compactInstructions
}
