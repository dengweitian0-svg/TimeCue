package chat

import (
	"encoding/json"
	"strings"
)

// partialJSONContext 只闭合已有容器和字符串；不完整字段或字面值整体省略，不推测业务值。
func partialJSONContext(raw string) json.RawMessage {
	// 扫描和候选校验共用 1 MiB 字节预算，避免上下文辅助补全长时间占用结果事务。
	remaining := 1 << 20
	if len(raw) > remaining {
		return nil
	}
	remaining -= len(raw)
	type closing struct {
		value  byte
		parent *closing
		depth  int
	}
	type boundary struct {
		end   int
		quote bool
		stack *closing
	}
	var candidates []boundary
	var stack *closing
	inString, escaped := false, false
	remember := func(end int, quote bool) {
		candidates = append(candidates, boundary{
			end:   end,
			quote: quote,
			stack: stack,
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
			value := byte('}')
			if b == '[' {
				value = ']'
			}
			depth := 1
			if stack != nil {
				depth += stack.depth
			}
			// 与 encoding/json 的最大嵌套深度一致，超限片段不尝试补全。
			if depth > 10000 {
				return nil
			}
			stack = &closing{
				value:  value,
				parent: stack,
				depth:  depth,
			}
			remember(i+1, false)
		case '}', ']':
			if stack == nil || stack.value != b {
				return nil
			}
			stack = stack.parent
			remember(i+1, false)
		case ',':
			remember(i, false)
		case ' ', '\n', '\r', '\t':
			// 字符串外连续空白只有首个位置形成不同的语法边界。
			if i == 0 || !strings.ContainsRune(" \t\r\n", rune(raw[i-1])) {
				remember(i, false)
			}
		}
	}
	// 不补齐未完成的转义或数字/布尔字面值，json.Valid 只接受已有完整 token。
	if !escaped {
		remember(len(raw), inString)
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		depth := 0
		if candidate.stack != nil {
			depth = candidate.stack.depth
		}
		length := candidate.end + depth
		if candidate.quote {
			length++
		}
		if length > remaining {
			return nil
		}
		remaining -= length
		// 扫描时共享不可变栈，仅在验证候选时一次分配，避免逐层复制闭合串。
		value := make(json.RawMessage, 0, length)
		value = append(value, raw[:candidate.end]...)
		if candidate.quote {
			value = append(value, '"')
		}
		for node := candidate.stack; node != nil; node = node.parent {
			value = append(value, node.value)
		}
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
