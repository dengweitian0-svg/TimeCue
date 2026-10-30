package chat

import (
	"encoding/json"
)

// partialJSONContext 只闭合已有容器和字符串；不完整字段或字面值整体省略，不推测业务值。
func partialJSONContext(raw string) json.RawMessage {
	type boundary struct {
		end  int
		tail string
	}
	var candidates []boundary
	var stack []byte
	inString, escaped := false, false
	remember := func(end int, quote bool) {
		tail := ""
		if quote {
			tail = `"`
		}
		for i := len(stack) - 1; i >= 0; i-- {
			tail += string(stack[i])
		}
		candidates = append(candidates, boundary{
			end:  end,
			tail: tail,
		})
	}
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
				remember(i+1, false)
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			closing := byte('}')
			if b == '[' {
				closing = ']'
			}
			stack = append(stack, closing)
			remember(i+1, false)
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != b {
				return nil
			}
			stack = stack[:len(stack)-1]
			remember(i+1, false)
		case ',', ' ', '\n', '\r', '\t':
			remember(i, false)
		}
	}
	// 不补齐未完成的转义或数字/布尔字面值，json.Valid 只接受已有完整 token。
	if !escaped {
		remember(len(raw), inString)
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		value := json.RawMessage(raw[:candidate.end] + candidate.tail)
		if json.Valid(value) {
			return value
		}
	}
	return nil
}

func (call ModelCall) toolFeedback(tool ToolCall, content string) string {
	if call.Output == nil || call.Output.FinishReason != "length" || tool.Error == nil || tool.Error.Code != "tool_argument_parse_error" {
		return content
	}
	feedback, _ := json.Marshal(struct {
		*RunError
		Context json.RawMessage `json:"context_arguments"`
		Notice  string          `json:"notice"`
	}{
		RunError: tool.Error,
		Context:  partialJSONContext(tool.Request.Function.Arguments),
		Notice:   "该工具参数不完整，未执行业务操作，请重新输出完整调用；格式补全仅供理解已接收内容，省略未完成字段，不代表可执行参数，不得拼接两次 arguments。",
	})
	return string(feedback)
}
