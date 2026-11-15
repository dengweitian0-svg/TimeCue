package loop

import (
	"context"
	"encoding/json"
	"math"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

// GetRunUsage 为内部调用按需重算可见运行的真实用量，不加入 HTTP 契约。
func (s *Run) GetRunUsage(ctx context.Context, id string) (chat.UsageReport, error) {
	if _, err := s.runs.ReadRun(ctx, id, true); err != nil {
		return chat.UsageReport{}, err
	}
	calls, err := s.usage.ReadUsage(ctx, id)
	if err != nil {
		return chat.UsageReport{}, err
	}
	values := make([]map[string]json.RawMessage, len(calls))
	for i, call := range calls {
		// 非对象及非法计数字段只影响合计是否已知，原始 JSON 始终保留。
		_ = json.Unmarshal(call.Usage, &values[i])
	}
	return chat.UsageReport{
		Calls: calls,
		Tokens: chat.TokenUsage{
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
