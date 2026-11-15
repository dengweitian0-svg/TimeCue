package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

// ContextBuilder 组装有效历史、新输入和已提交的助手及工具消息。
type ContextBuilder struct {
	history interface {
		History(context.Context, string) ([]Message, error)
	}
	tools *tools.Registry
}

const legacyScheduleInstructions = `你是 TimeCue 日程助手。通过真实工具查询或操作日程，只有工具的实际成功结果才能证明已完成，不能凭文字宣称已创建、已修改或已删除。
用户指令明确、信息完整且目标唯一时直接执行，无需重复确认。缺少名称、日期、时刻、周期规则，或修改/删除目标不唯一时先澄清，不猜测时间、目标或规则。修改或删除前通过查询取得唯一 id 和当前 revision，以 expectedRevision 提交；版本冲突或目标不存在须如实报告，不自动重试，不换成新版本强行覆盖。删除的后台执行身份由后端管理，禁止填写或猜测幂等键。
相对日期按每次实际解析时北京时间处理：将用户“明天上午九点”等规范为“明天 09:00”，保留今天/明天/后天/昨天原词交给工具解析，不根据运行或会话创建时间提前换算绝对日期。已取得的明确时间及实际结果按原事实使用。
批量意图拆为独立单条操作，逐条报告成功或失败，不回滚其他成功操作；有依赖的操作必须分模型轮次，先取得前一步真实结果，再生成下一步工具调用。创建和修改没有业务幂等键，不按名称或时间猜测去重，不承诺恰好执行一次；结果未确认时不能断言未生效。`

const scheduleInstructions = legacyScheduleInstructions + `
面向用户的回复只报告日程名称、时间、周期、启停状态、操作结果等业务信息。内部 ID、revision、version、expectedRevision、open 等原始字段只用于工具调用，不在回复中列出或作为附注；将启停状态写成“已启用”或“已停用”。`

func (b ContextBuilder) firstRequest(ctx context.Context, r Record, cfg ModelConfig) (ModelRequest, error) {
	history, err := b.history.History(ctx, r.SessionID)
	if err != nil {
		return ModelRequest{}, err
	}
	_, writes := b.tools.Lookup("create_schedule")
	_, deletes := b.tools.Lookup("delete_schedule")
	if writes || deletes {
		if len(history) > 0 && history[0].Role == "system" && history[0].Content == legacyScheduleInstructions {
			history = history[1:]
		}
		if len(history) == 0 || history[0].Role != "system" || history[0].Content != scheduleInstructions {
			history = append([]Message{{
				Role:    "system",
				Content: scheduleInstructions,
			}}, history...)
		}
	}
	return ModelRequest{
		Model: r.Model,
		Messages: append(cloneMessages(history), Message{
			Role:    "user",
			Content: r.Input,
		}),
		Stream:        true,
		MaxTokens:     cfg.MaxOutputTokens,
		StreamOptions: map[string]bool{"include_usage": true},
		Tools:         b.tools.Definitions(),
	}, nil
}

func (ContextBuilder) followupRequest(call ModelCall, continuation bool) ModelRequest {
	request := call.Input
	messages := call.CheckpointContext
	if call.CheckpointStage == "compact" {
		messages = call.Input.Messages
	}
	request.Messages = cloneMessages(messages)
	if call.CheckpointStage == "compact" && call.Output != nil {
		request.Messages = appendAssistant(request.Messages, call.Output.Message)
	}
	if continuation && !call.hasToolCalls() {
		request.Messages = append(request.Messages, Message{
			Role:    "user",
			Content: "继续未完成的输出",
		})
	}
	return request
}

func (ContextBuilder) withSummary(request ModelRequest, summary string) ModelRequest {
	messages := request.Messages
	systemCount := len(messages) - len(withoutLeadingSystem(messages))
	request.Messages = append(cloneMessages(messages[:systemCount]), Message{
		Role:    "user",
		Content: summary,
	}, messages[len(messages)-1])
	return request
}

func appendAssistant(messages []Message, message Message) []Message {
	if !hasAssistantContent(message) {
		return messages
	}
	if hasToolCalls(message.ToolCalls) {
		message.ToolCalls = slices.Clone(message.ToolCalls)
	} else {
		message.ToolCalls = nil
	}
	return append(messages, message)
}

func hasMessagePrefix(messages, prefix []Message) bool {
	if len(prefix) > len(messages) {
		return false
	}
	actual, actualErr := json.Marshal(messages[:len(prefix)])
	expected, expectedErr := json.Marshal(prefix)
	return actualErr == nil && expectedErr == nil && bytes.Equal(actual, expected)
}

func toolResultMessage(call ModelCall, tool ToolCall) Message {
	content := string(tool.Output)
	if tool.Error != nil {
		encoded, _ := json.Marshal(tool.Error)
		content = string(encoded)
	}
	if tool.State == "skipped" {
		content = "aborted"
	} else {
		content = call.toolFeedback(tool, content)
		content = trimToolContent(content, tool.ModelContentLimit)
	}
	return Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: tool.Request.ID,
	}
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
	return message.Content != "" || message.ReasoningContent != "" || hasToolCalls(message.ToolCalls)
}

func hasToolCalls(raw json.RawMessage) bool {
	// 旧模型输出可显式保存 null 或空数组，包括数组内部的 JSON 空白。
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return false
	}
	if body, ok := strings.CutPrefix(value, "["); ok {
		if items, ok := strings.CutSuffix(body, "]"); ok {
			return strings.TrimSpace(items) != ""
		}
	}
	return true
}

func (call ModelCall) hasToolCalls() bool {
	return call.Output != nil && hasToolCalls(call.Output.Message.ToolCalls)
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

// HistoryAfterCompact 生成摘要失败或取消后的有效历史，不纳入专用提示或部分摘要。
func (state CheckpointState) HistoryAfterCompact(base []Message, input string, status RunState) []Message {
	return (CheckpointState{Context: append(cloneMessages(base), Message{
		Role:    "user",
		Content: input,
	})}).History(status)
}

// WithContinuationBase 按实际请求的消息前缀恢复正文边界，不从事件重放或主键顺序猜测分组。
func (call ModelCall) WithContinuationBase(base ModelRequest) (ModelCall, bool) {
	messages := call.Input.Messages
	if call.CheckpointStage == "compact" {
		if call.Checkpoint().NeedsCompactBase("compact") {
			return call, false
		}
		messages = call.CheckpointContext
	} else if !call.Continuation {
		return call, false
	}
	if !hasMessagePrefix(messages, base.Messages) {
		return call, false
	}
	call.OutputPrefix = ""
	for _, message := range messages[len(base.Messages):] {
		if message.Role == "assistant" {
			call.OutputPrefix += message.Content
		}
	}
	return call, true
}
