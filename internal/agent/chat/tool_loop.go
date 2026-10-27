package chat

import (
	"context"
	"encoding/json"
	"time"
)

func hasModelOutput(output ModelOutput) bool {
	return output.Message.Content != "" || output.Message.ReasoningContent != "" || len(output.Message.ToolCalls) > 0
}

func (s *Runtime) advance(ctx context.Context, r *Record, cfg ModelConfig, call ModelCall) (ModelCall, *RunError) {
	for r.Error == nil && ctx.Err() == nil {
		if call.State == StateSucceeded && (call.CheckpointStage == "model_call_end" || call.CheckpointStage == "tool_start") {
			var failure *RunError
			call, failure = s.processTools(ctx, call)
			if failure != nil || ctx.Err() != nil {
				return call, failure
			}
		}
		if call.State == StateSucceeded && call.CheckpointStage == "tool_end" {
			request := call.Input
			request.Messages = call.CheckpointContext
			request.MaxTokens = cfg.MaxOutputTokens
			call = ModelCall{
				RunID:     r.RunID,
				SessionID: r.SessionID,
				Input:     request,
				State:     StateRunning,
				MaxRetry:  cfg.MaxRetry,
			}
		}
		if call.ID == 0 {
			var err error
			if call.Input.Model == "" {
				call.Input, err = s.newRequest(ctx, *r, cfg)
			}
			if err != nil {
				return call, persistenceFailure()
			}
			if failure := requestBudgetError(call.Input, cfg); failure != nil {
				return ModelCall{
					RunID:     r.RunID,
					SessionID: r.SessionID,
				}, failure
			}
			count, err := s.execution.OrdinaryCallCount(ctx, r.RunID)
			if err != nil {
				return call, persistenceFailure()
			}
			limit := cfg.MaxRounds
			if limit <= 0 {
				limit = 20
			}
			if count >= limit {
				return ModelCall{
					RunID:     r.RunID,
					SessionID: r.SessionID,
				}, &RunError{
					Code:    "model_round_limit_exceeded",
					Message: "已达到普通模型轮数上限，任务仍需继续",
				}
			}
			call.ID, err = s.execution.BeginCall(ctx, call)
			if err != nil {
				if ctx.Err() != nil {
					return call, nil
				}
				return call, persistenceFailure()
			}
		}
		var failure *RunError
		call, failure = s.complete(ctx, *r, call)
		r.Output = nil
		if call.Output != nil && call.Output.Message.Content != "" {
			content := call.Output.Message.Content
			r.Output = &content
		}
		if failure != nil || ctx.Err() != nil {
			return call, failure
		}
		if call.Output == nil || len(call.Output.Message.ToolCalls) == 0 {
			return call, nil
		}
		if s.tools == nil || s.toolStore == nil {
			return call, &RunError{
				Code:    "model_invalid_format",
				Message: "工具执行端口未装配",
			}
		}
		call.State = StateSucceeded
		call.CheckpointStage = "model_call_end"
		call.CheckpointContext = append(append([]Message{}, call.Input.Messages...), call.Output.Message)
		call.CompletedTools = []string{}
		call.LastTool = ""
		if err := s.toolStore.AcceptResponse(context.Background(), call); err != nil {
			call.State = StateRunning
			call.CheckpointStage = ""
			return call, persistenceFailure()
		}
		call.PendingEvents = nil
	}
	return call, r.Error
}

func toolError(code, message string) *RunError {
	return &RunError{
		Code:    code,
		Message: message,
	}
}

func (s *Runtime) processTools(ctx context.Context, call ModelCall) (ModelCall, *RunError) {
	var requests []ToolRequest
	if call.Output == nil || json.Unmarshal(call.Output.Message.ToolCalls, &requests) != nil {
		return call, toolError("model_invalid_format", "工具响应无效")
	}
	stored, err := s.toolStore.ReadTools(context.Background(), call.ID)
	if err != nil {
		return call, persistenceFailure()
	}
	known := map[string]ToolCall{}
	completed := map[string]bool{}
	for _, tool := range stored {
		known[tool.Request.ID] = tool
	}
	for _, id := range call.CompletedTools {
		completed[id] = true
	}
	// 查询工具可并行，开始及结果进度始终由当前协调器串行提交。
	results := make(chan ToolCall, 3)
	inFlight, next := 0, 0
	for next < len(requests) || inFlight > 0 {
		for next < len(requests) && inFlight < cap(results) {
			request := requests[next]
			if completed[request.ID] {
				next++
				continue
			}
			if ctx.Err() != nil {
				break
			}
			tool, exists := known[request.ID]
			if !exists {
				tool = ToolCall{
					ModelCallID: call.ID,
					Request:     request,
					State:       "running",
					MaxRetry:    3,
				}
			} else if tool.State != "running" {
				// 最终结果和 completed_tool 在同一事务提交，不允许孤立的终态记录再次执行。
				return call, persistenceFailure()
			} else if tool.RetryCount >= tool.MaxRetry {
				tool.State = "failed"
				tool.Error = toolError("tool_result_unconfirmed", "中断前工具结果未能确认，未重新执行")
				call = toolProgress(call, tool, len(requests))
				if err := s.toolStore.FinishTool(context.Background(), call, tool); err != nil {
					return call, persistenceFailure()
				}
				next++
				continue
			} else {
				tool.RetryCount++
			}
			entry, registered := s.tools.Lookup(request.Function.Name)
			parallel := !registered || entry.Parallel
			if !parallel && inFlight > 0 {
				break
			}
			beforeStart := call
			call.CheckpointStage = "tool_start"
			call.LastTool = "tool_use: " + request.Function.Name + "-" + request.ID
			tool.ID, err = s.toolStore.BeginTool(context.Background(), call, tool)
			if err != nil {
				return beforeStart, persistenceFailure()
			}
			inFlight++
			next++
			go func(tool ToolCall) {
				// 已开始的只读查询取得结果或达到原超时，取消仅阻止后续启动。
				attempt, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				output, failure := s.tools.Execute(attempt, tool.Request.Function.Name, tool.Request.Function.Arguments)
				tool.Output, tool.State = output, "succeeded"
				if failure != nil {
					tool.State = "failed"
					tool.Error = toolError(failure.Code, failure.Message)
				}
				results <- tool
			}(tool)
			if !parallel {
				break
			}
		}
		if inFlight == 0 {
			break
		}
		tool := <-results
		inFlight--
		call = toolProgress(call, tool, len(requests))
		if err := s.toolStore.FinishTool(context.Background(), call, tool); err != nil {
			call.PendingTool = &tool
			return call, persistenceFailure()
		}
		completed[tool.Request.ID] = true
	}
	// 退出保存未完成进度；用户取消则补齐原 ID 的 aborted 结果。
	s.mu.Lock()
	requested := s.active[call.RunID] != nil && s.active[call.RunID].requested
	s.mu.Unlock()
	if requested {
		for ; next < len(requests); next++ {
			request := requests[next]
			if completed[request.ID] {
				continue
			}
			tool := known[request.ID]
			tool.Request = request
			tool.State = "skipped"
			tool.Error = toolError("tool_skipped", "运行已取消，未启动此工具")
			call = toolProgress(call, tool, len(requests))
			if err := s.toolStore.FinishTool(context.Background(), call, tool); err != nil {
				return call, persistenceFailure()
			}
		}
	}
	return call, nil
}

func toolProgress(call ModelCall, tool ToolCall, total int) ModelCall {
	content := string(tool.Output)
	if tool.Error != nil {
		encoded, _ := json.Marshal(tool.Error)
		content = string(encoded)
	}
	if tool.State == "skipped" {
		content = "aborted"
	}
	call.CheckpointContext = append(call.CheckpointContext, Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: tool.Request.ID,
	})
	call.CompletedTools = append(call.CompletedTools, tool.Request.ID)
	call.LastTool = "tool_result: " + tool.Request.Function.Name + "-" + tool.Request.ID
	call.CheckpointStage = "tool_start"
	if len(call.CompletedTools) == total {
		call.CheckpointStage = "tool_end"
	}
	return call
}
