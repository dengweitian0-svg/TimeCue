package chat

import (
	"encoding/json"
	"strings"
)

// Compactor 提取待压缩历史、构造无工具请求并接纳模型摘要。
type Compactor struct{}

const compactInstructions = `你正在执行一次上下文检查点压缩。

请为后续接手任务的另一个大语言模型生成一份交接摘要，使其能够继续当前任务。

摘要应包含：
1. 当前任务的进度，以及已经做出的关键决策。
2. 重要的上下文、约束条件和用户偏好。
3. 尚未完成的工作，以及明确的后续步骤。
4. 继续工作所必需的关键数据、示例和参考资料。

摘要应简洁、结构清晰，专注于让接手的模型能够顺利继续工作。`

// BeforeFirstCall 判断已准备的首个请求是否达到摘要阈值。
func (c Compactor) BeforeFirstCall(prepared PreparedRequest, cfg ModelConfig) (ModelRequest, bool) {
	messages := prepared.Input.Messages
	if len(messages) == 0 {
		return ModelRequest{}, false
	}
	history := withoutLeadingSystem(messages[:len(messages)-1])
	threshold := cfg.CompactThreshold
	if threshold <= 0 {
		threshold = 0.8
	}
	if len(history) > 0 && float64(EstimateTokens([]byte(prepared.Body))) >= float64(cfg.ContextWindow-cfg.MaxOutputTokens)*threshold {
		return c.request(prepared.Input, history), true
	}
	return ModelRequest{}, false
}

// AfterTools 从已配齐工具的请求中提取待压缩历史。
func (c Compactor) AfterTools(request, base ModelRequest) (ModelRequest, *RunError) {
	// 普通上下文的首个已登记请求止于本轮真实输入。按完整前缀定位，
	// 以保留同名旧输入和此前已生效的摘要。
	index := len(base.Messages) - 1
	if index < 0 || base.Messages[index].Role != "user" || !hasMessagePrefix(request.Messages, base.Messages) {
		return ModelRequest{}, &RunError{
			Code:    "model_invalid_format",
			Message: "无法从普通请求前缀定位本轮输入",
		}
	}
	history := cloneMessages(request.Messages)
	history = append(history[:index], history[index+1:]...)
	return c.request(request, withoutLeadingSystem(history)), nil
}

func (Compactor) request(request ModelRequest, messages []Message) ModelRequest {
	history, _ := json.Marshal(messages)
	request.Messages = []Message{
		{
			Role:    "system",
			Content: compactInstructions,
		},
		{
			Role:    "user",
			Content: string(history),
		},
	}
	request.Tools = nil
	return request
}

func withoutLeadingSystem(messages []Message) []Message {
	for len(messages) > 0 && messages[0].Role == "system" {
		messages = messages[1:]
	}
	return messages
}

// ValidResponse 检查摘要回复是否可以接纳。
func (Compactor) ValidResponse(output *ModelOutput) bool {
	return output != nil && !hasToolCalls(output.Message.ToolCalls) &&
		(output.FinishReason == "stop" || output.FinishReason == "length")
}

// Summary 拼接已接纳的摘要正文与续写。
func (Compactor) Summary(call ModelCall) string {
	var content strings.Builder
	// 跳过专用系统提示和待压缩历史，只拼接已接纳的摘要正文。
	for _, message := range call.Input.Messages[min(2, len(call.Input.Messages)):] {
		if message.Role == "assistant" {
			content.WriteString(message.Content)
		}
	}
	if call.Output != nil {
		content.WriteString(call.Output.Message.Content)
	}
	summary := content.String()
	if strings.TrimSpace(summary) == "" {
		return ""
	}
	return summary
}
