package chat

import (
	"bytes"
	"encoding/json"
	"slices"
)

// CheckpointState 是版本 3 的三字段磁盘格式，不持有独立于 ModelCall 的可推进状态。
type CheckpointState struct {
	Context   []Message `json:"context"`
	Completed []string  `json:"completed_tool"`
	Last      string    `json:"last_tool"`
}

func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	result := make([]Message, len(messages))
	copy(result, messages)
	for i := range result {
		result[i].ToolCalls = append(json.RawMessage(nil), messages[i].ToolCalls...)
	}
	return result
}

func hasAssistantContent(message Message) bool {
	return message.Content != "" || message.ReasoningContent != "" ||
		len(message.ToolCalls) > 0 && string(message.ToolCalls) != "null" && string(message.ToolCalls) != "[]"
}

// History 沿完整快照保留消息次序，并在旧轮全部消息之后追加独立中断说明。
func (state CheckpointState) History(status RunState) []Message {
	messages := make([]Message, 0, len(state.Context)+1)
	for _, message := range cloneMessages(state.Context) {
		// 兼容旧快照的全空 assistant，保留仅思考回复及原工具配对。
		if message.Role != "assistant" || hasAssistantContent(message) {
			messages = append(messages, message)
		}
	}
	if status != StateSucceeded {
		messages = append(messages, Message{
			Role:    "user",
			Content: "上一轮回复未完成；其操作表述不代表业务已经生效。",
		})
	}
	return messages
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
	return call.WithCheckpoint("model_call_start", CheckpointState{Context: call.Input.Messages})
}

// FollowupRequest 沿已接纳的完整配对快照生成后续请求，文字续写保留独立 user 边界。
func (call ModelCall) FollowupRequest(continuation bool) ModelRequest {
	request := call.Input
	request.Messages = cloneMessages(call.CheckpointContext)
	if continuation && len(call.Output.Message.ToolCalls) == 0 {
		request.Messages = append(request.Messages, Message{
			Role:    "user",
			Content: "继续未完成的输出",
		})
	}
	return request
}

// PresentedOutput 只拼接当前普通回复及其续写正文，不修改实际模型输出。
func (call ModelCall) PresentedOutput() *string {
	content := call.OutputPrefix
	if call.Output != nil {
		content += call.Output.Message.Content
	}
	if content == "" {
		return nil
	}
	return &content
}

// WithContinuationBase 按实际请求的消息前缀恢复正文边界，不从事件重放或主键顺序猜测分组。
func (call ModelCall) WithContinuationBase(base ModelRequest) (ModelCall, bool) {
	if !call.Continuation || len(base.Messages) > len(call.Input.Messages) {
		return call, false
	}
	actual, _ := json.Marshal(call.Input.Messages[:len(base.Messages)])
	start, _ := json.Marshal(base.Messages)
	if !bytes.Equal(actual, start) {
		return call, false
	}
	call.OutputPrefix = ""
	for _, message := range call.Input.Messages[len(base.Messages):] {
		if message.Role == "assistant" {
			call.OutputPrefix += message.Content
		}
	}
	return call, true
}

// WithResponseAccepted 生成完整成功回复的待提交进度，提交成功后才可进入工具流程。
func (call ModelCall) WithResponseAccepted() ModelCall {
	call.State = StateSucceeded
	call.Error = nil
	// 从实际请求构造一次回复快照，不累计上次调用的工具事实。
	call = call.WithCallStarted()
	return call.WithCallEnded()
}

// WithCallEnded 生成调用结束快照，未接纳的工具片段只保留可用正文和思考。
func (call ModelCall) WithCallEnded() ModelCall {
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
		if hasAssistantContent(message) {
			call.CheckpointContext = append(call.CheckpointContext, cloneMessages([]Message{message})[0])
		}
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
	content := string(tool.Output)
	if tool.Error != nil {
		encoded, _ := json.Marshal(tool.Error)
		content = string(encoded)
	}
	if tool.State == "skipped" {
		content = "aborted"
	} else {
		content = call.toolFeedback(tool, content)
	}
	call.CheckpointContext = append(call.CheckpointContext, Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: tool.Request.ID,
	})
	call.CompletedTools = append(call.CompletedTools, tool.Request.ID)
	call.LastTool = "tool_result: " + tool.Request.Function.Name + "-" + tool.Request.ID
	if call.toolBatchComplete() {
		call.CheckpointStage = "tool_end"
	}
	return call
}

func (call ModelCall) toolBatchComplete() bool {
	var requests []ToolRequest
	if call.Output == nil || json.Unmarshal(call.Output.Message.ToolCalls, &requests) != nil || len(requests) == 0 {
		return false
	}
	for _, request := range requests {
		if !call.ToolCompleted(request.ID) {
			return false
		}
	}
	return true
}

// WithResultUnconfirmed 保留原调用身份，为无法确认的处理生成统一失败事实。
func (tool ToolCall) WithResultUnconfirmed() ToolCall {
	tool.State = "failed"
	tool.Error = toolError("tool_result_unconfirmed", "中断前工具结果未能确认，未重新执行")
	return tool
}

// WithRunFinished 沿事务内已提交事实收拢异常批次，实际结果优先于取消或未确认补齐。
func (call ModelCall) WithRunFinished(status RunState, stored []ToolCall) (ModelCall, []ToolCall, error) {
	call = call.WithCallEnded()
	if call.State != StateSucceeded || call.Output == nil || len(call.Output.Message.ToolCalls) == 0 || status == StateSucceeded {
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
				MaxRetry:    3,
			}
		}
		if !exists || tool.State == "running" || tool.State == "retry_wait" {
			tool = tool.WithResultUnconfirmed()
			if status == StateCancelled && (!exists || known[request.ID].State == "retry_wait") {
				tool.State = "skipped"
				tool.Output = nil
				tool.Error = toolError("tool_skipped", "运行已取消，未启动或未恢复此工具")
			}
		}
		call = call.WithToolResult(tool)
		results = append(results, tool)
	}
	return call, results, nil
}
