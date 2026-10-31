package chat

import (
	"context"
	"encoding/json"
	"math"
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

// GetRunUsage 为内部调用按需重算可见运行的真实用量，不加入 HTTP 契约。
func (s *Runtime) GetRunUsage(ctx context.Context, id string) (UsageReport, error) {
	if _, err := s.runs.ReadRun(ctx, id, true); err != nil {
		return UsageReport{}, err
	}
	calls, err := s.usage.ReadUsage(ctx, id)
	if err != nil {
		return UsageReport{}, err
	}
	values := make([]map[string]json.RawMessage, len(calls))
	for i, call := range calls {
		// 非对象及非法计数字段只影响合计是否已知，原始 JSON 始终保留。
		_ = json.Unmarshal(call.Usage, &values[i])
	}
	return UsageReport{
		Calls: calls,
		Tokens: TokenUsage{
			PromptTokens:     sumUsage(values, "prompt_tokens"),
			CompletionTokens: sumUsage(values, "completion_tokens"),
			TotalTokens:      sumUsage(values, "total_tokens"),
		},
	}, nil
}

func sumUsage(values []map[string]json.RawMessage, field string) *int64 {
	if len(values) == 0 {
		return nil
	}
	var total int64
	for _, value := range values {
		raw := value[field]
		var tokens *int64
		if json.Unmarshal(raw, &tokens) != nil || tokens == nil || *tokens < 0 || total > math.MaxInt64-*tokens {
			return nil
		}
		total += *tokens
	}
	return &total
}
