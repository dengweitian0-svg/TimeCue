package tools

import (
	"context"
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
)

func (f *ToolFactory[P]) readReceipt(ctx context.Context, tool ToolCall) (ToolCall, bool, error) {
	output, found, err := f.registry.readResult(WithExecutionKey(ctx, tool.IdempotencyKey), tool.Request.Function.Name, tool.Request.Function.Arguments)
	if found {
		tool.State, tool.Output, tool.Error, tool.NextRetryAt = "succeeded", output, nil, nil
	}
	return tool, found, err
}

// CancellationResults 恢复取消时只核对既有回执，在途未确认项按原身份结算。
func (f *ToolFactory[P]) CancellationResults(ctx context.Context, batch Batch, progress P, cfg Settings) ([]ToolCall, error) {
	stored, err := f.store.ReadTools(ctx, batch.ModelCallID)
	if err != nil {
		return nil, err
	}
	var results []ToolCall
	for _, tool := range stored {
		if !tool.unsettled() || progress.ToolCompleted(tool.Request.ID) {
			continue
		}
		confirmed, found, err := f.readReceipt(ctx, tool)
		if err != nil {
			logger.Warn("[tools] 取消恢复未确认业务回执", "run_id", batch.RunID, "tool_call_id", tool.Request.ID, "error", err)
		}
		if found {
			tool = confirmed
		} else if tool.State == "running" {
			tool = tool.WithResultUnconfirmed()
		} else {
			// 退避项由终态事务补齐为跳过。
			continue
		}
		tool.ModelContentLimit = cfg.ResultMaxTokens
		results = append(results, tool)
	}
	return results, nil
}

func (tool ToolCall) unsettled() bool {
	return tool.State == "running" || tool.State == "retry_wait"
}

func (tool ToolCall) retryWait() time.Duration {
	if tool.State != "retry_wait" || tool.NextRetryAt == nil {
		return 0
	}
	return time.Until(*tool.NextRetryAt)
}

func (tool ToolCall) canRetry() bool {
	return tool.RetryCount < tool.MaxRetry && tool.Error != nil &&
		(tool.Error.Code == "tool_timeout" || tool.Error.Code == "tool_temporary_error")
}
