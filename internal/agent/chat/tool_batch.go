package chat

import (
	"context"
	"encoding/json"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

func (cfg ModelConfig) toolSettings() tools.Settings {
	return tools.Settings{
		Concurrency:     cfg.ToolConcurrency,
		Timeout:         cfg.ToolTimeout,
		MaxRetry:        cfg.ToolMaxRetry,
		ResultMaxTokens: cfg.ToolResultMaxTokens,
	}
}

func (s *Runtime) currentToolSettings(provider, model string) (tools.Settings, bool) {
	latest, _ := s.currentModel(provider, model)
	// 模型密钥是否可用与同一连接的工具配置是否存在是两件事。
	return latest.toolSettings(), latest.Provider == provider && latest.Model == model
}

func (s *Runtime) toolRunLifecycle(ctx context.Context, runID string) tools.RunLifecycle {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.active[runID]
	return tools.RunLifecycle{
		Active:          run != nil,
		CancelRequested: run != nil && run.requested,
		ContextDone:     ctx.Err() != nil,
		ShuttingDown:    s.stopping,
	}
}

func (s *Runtime) processTools(ctx context.Context, call ModelCall, r Record, cfg ModelConfig) (ModelCall, *RunError) {
	batch := tools.Batch{
		RunID:       r.RunID,
		ModelCallID: call.ID,
		Provider:    r.Provider,
		Model:       r.Model,
	}
	if call.Output == nil || json.Unmarshal(call.Output.Message.ToolCalls, &batch.Requests) != nil {
		return call, toolError("model_invalid_format", "工具响应无效")
	}
	progress, pending, failure := s.toolFactory.RunBatch(ctx, batch, call, cfg.toolSettings())
	progress.PendingTools = append(progress.PendingTools, pending...)
	if failure != nil {
		return progress, persistenceFailure(progress, failure.Stage, failure.Err)
	}
	return progress, nil
}
