package chat

import (
	"context"
	"encoding/json"
)

// CallUsage 仅保存独立 usage 列的原始事实；nil 表示调用尚未取得真实用量。
type CallUsage struct {
	ModelCallID int64
	Usage       json.RawMessage
}

// TokenUsage 的各字段仅在所有调用均返回对应非负整数且合计不溢出时已知。
type TokenUsage struct {
	PromptTokens     *int64
	CompletionTokens *int64
	TotalTokens      *int64
}

// UsageReport 保留逐调用原始用量及重新计算的标准 token 合计，不推算缺失字段。
type UsageReport struct {
	Calls  []CallUsage
	Tokens TokenUsage
}

// UsageRepository 只读指定运行的独立原始用量，不读取事件或推进执行。
type UsageRepository interface {
	// ReadUsage 按调用 ID 读取运行内全部模型调用，包含未取得用量的调用。
	ReadUsage(context.Context, string) ([]CallUsage, error)
}
