package chat

import (
	"context"
	"encoding/json"
)

const scheduleInstructions = `你是 TimeCue 日程助手。通过真实工具查询或操作日程，只有工具的实际成功结果才能证明已完成，不能凭文字宣称已创建、已修改或已删除。
用户指令明确、信息完整且目标唯一时直接执行，无需重复确认。缺少名称、日期、时刻、周期规则，或修改/删除目标不唯一时先澄清，不猜测时间、目标或规则。修改或删除前通过查询取得唯一 id 和当前 revision，以 expectedRevision 提交；版本冲突或目标不存在须如实报告，不自动重试，不换成新版本强行覆盖。删除的后台执行身份由后端管理，禁止填写或猜测幂等键。
相对日期按每次实际解析时北京时间处理：将用户“明天上午九点”等规范为“明天 09:00”，保留今天/明天/后天/昨天原词交给工具解析，不根据运行或会话创建时间提前换算绝对日期。已取得的明确时间及实际结果按原事实使用。
批量意图拆为独立单条操作，逐条报告成功或失败，不回滚其他成功操作；有依赖的操作必须分模型轮次，先取得前一步真实结果，再生成下一步工具调用。创建和修改没有业务幂等键，不按名称或时间猜测去重，不承诺恰好执行一次；结果未确认时不能断言未生效。`

// EstimateTokens 统一以 UTF-8 字节数除以四并向上取整估算 token，不作为实际 usage。
func EstimateTokens(encoded []byte) int {
	return (len(encoded) + 3) / 4
}

func (s *Runtime) newRequest(ctx context.Context, r Record, cfg ModelConfig) (ModelRequest, error) {
	history, err := s.execution.History(ctx, r.SessionID)
	if err != nil {
		return ModelRequest{}, err
	}
	_, writes := s.tools.Lookup("create_schedule")
	_, deletes := s.tools.Lookup("delete_schedule")
	if writes || deletes {
		if len(history) == 0 || history[0].Role != "system" || history[0].Content != scheduleInstructions {
			history = append([]Message{{
				Role:    "system",
				Content: scheduleInstructions,
			}}, history...)
		}
	}
	return ModelRequest{
		Model: r.Model,
		Messages: append(history, Message{
			Role:    "user",
			Content: r.Input,
		}),
		Stream:        true,
		MaxTokens:     cfg.MaxOutputTokens,
		StreamOptions: map[string]bool{"include_usage": true},
		Tools:         s.tools.Definitions(),
	}, nil
}

func requestBudgetError(input ModelRequest, cfg ModelConfig) *RunError {
	encoded, _ := json.Marshal(input)
	if EstimateTokens(encoded) > cfg.ContextWindow-cfg.MaxOutputTokens {
		return &RunError{
			Code:    "context_budget_exceeded",
			Message: "输入超出模型上下文预算",
		}
	}
	return nil
}
