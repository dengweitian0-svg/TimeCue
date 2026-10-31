package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const compactInstructions = `你正在执行一次上下文检查点压缩。

请为后续接手任务的另一个大语言模型生成一份交接摘要，使其能够继续当前任务。

摘要应包含：
1. 当前任务的进度，以及已经做出的关键决策。
2. 重要的上下文、约束条件和用户偏好。
3. 尚未完成的工作，以及明确的后续步骤。
4. 继续工作所必需的关键数据、示例和参考资料。

摘要应简洁、结构清晰，专注于让接手的模型能够顺利继续工作。`

func compactHistory(request ModelRequest) []Message {
	history := request.Messages[:len(request.Messages)-1]
	for len(history) > 0 && history[0].Role == "system" {
		history = history[1:]
	}
	return history
}

func needsCompact(request ModelRequest, cfg ModelConfig) bool {
	if len(request.Messages) == 0 || len(compactHistory(request)) == 0 {
		return false
	}
	threshold := cfg.CompactThreshold
	if threshold <= 0 {
		threshold = 0.8
	}
	encoded, _ := json.Marshal(request)
	return float64(EstimateTokens(encoded)) >= float64(cfg.ContextWindow-cfg.MaxOutputTokens)*threshold
}

func compactRequest(request ModelRequest) ModelRequest {
	history, _ := json.Marshal(compactHistory(request))
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

func (s *Runtime) advanceCompact(ctx context.Context, r *Record, cfg ModelConfig, call ModelCall) (ModelCall, *RunError) {
	for {
		if ctx.Err() != nil {
			return call, nil
		}
		if call.ID == 0 {
			id, err := s.execution.BeginCall(ctx, call)
			if err != nil {
				if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					return call, nil
				}
				return call, persistenceFailure()
			}
			call.ID = id
			call = call.WithCallStarted()
		}
		if call.State != StateSucceeded {
			var failure *RunError
			call, failure = s.complete(ctx, *r, call)
			if failure != nil || ctx.Err() != nil {
				return call, failure
			}
			if call.Output == nil || len(call.Output.Message.ToolCalls) > 0 || call.Output.FinishReason != "stop" && call.Output.FinishReason != "length" {
				return call, toolError("model_invalid_format", "摘要响应必须为正文或思考，不允许调用工具")
			}
		}
		latest, available := s.currentModel(r.Provider, r.Model)
		if latest.ContextWindow > latest.MaxOutputTokens && latest.MaxOutputTokens > 0 {
			cfg = latest
		}
		if call.Output.FinishReason == "length" {
			accepted := call.WithCompactResponse()
			if call.State != StateSucceeded {
				if err := s.toolStore.AcceptResponse(context.Background(), accepted); err != nil {
					return call, persistenceFailure()
				}
				accepted.PendingEvents = nil
			}
			call = accepted
			if !available {
				return call, toolError("model_config_unavailable", "原运行的模型配置或密钥不可用")
			}
			if failure := s.continuationBudgetError(ctx, r.RunID, cfg); failure != nil || ctx.Err() != nil {
				return call, failure
			}
			request := call.FollowupRequest(true)
			request.MaxTokens = cfg.MaxOutputTokens
			call = ModelCall{
				RunID:           r.RunID,
				SessionID:       r.SessionID,
				Input:           request,
				State:           StateRunning,
				MaxRetry:        cfg.MaxRetry,
				Continuation:    true,
				CheckpointStage: "compact",
			}
			continue
		}
		ordinary, err := s.newRequest(ctx, *r, cfg)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return call, nil
			}
			return call, persistenceFailure()
		}
		projected := call.WithCompactSucceeded(ordinary)
		if strings.TrimSpace(projected.CheckpointContext[len(projected.CheckpointContext)-2].Content) == "" {
			return call, toolError("model_invalid_format", "摘要未返回有效正文")
		}
		ordinary.Messages = projected.CheckpointContext
		failure := requestBudgetError(ordinary, cfg)
		var next *ModelCall
		if failure == nil {
			next = &ModelCall{
				RunID:     r.RunID,
				SessionID: r.SessionID,
				Input:     ordinary,
				State:     StateRunning,
				MaxRetry:  cfg.MaxRetry,
			}
		}
		committed, err := s.execution.CompleteCompact(ctx, projected, next)
		if err != nil {
			// 提交回执丢失时沿实际 checkpoint 收尾，不能重发已经登记的普通调用或丢弃有效摘要。
			if saved, readErr := s.execution.ReadCall(context.Background(), r.RunID); readErr == nil && saved != nil && (saved.ID != call.ID || saved.State == StateSucceeded) {
				call = *saved
			}
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return call, nil
			}
			return call, persistenceFailure()
		}
		return committed, failure
	}
}
