package chat

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/google/uuid"
)

// toolAttempt 只代表当前有效尝试；逻辑调用身份及预算仍保存在 ToolCall。
type toolAttempt struct {
	tool     ToolCall
	ctx      context.Context
	cancel   context.CancelFunc
	parallel bool
	token    uint64
}

type toolAttemptResult struct {
	tool       ToolCall
	token      uint64
	finishedAt time.Time
}

func (attempt *toolAttempt) accepts(result toolAttemptResult) bool {
	deadline, _ := attempt.ctx.Deadline()
	return attempt.token == result.token && !result.finishedAt.After(deadline)
}

func toolRetryable(failure *RunError) bool {
	return failure != nil && (failure.Code == "tool_timeout" || failure.Code == "tool_temporary_error")
}

func toolConfig(cfg ModelConfig) (concurrency int, timeout time.Duration, maxRetry int) {
	concurrency, timeout, maxRetry = 3, 30*time.Second, 3
	if cfg.ToolConcurrency > 0 {
		concurrency = cfg.ToolConcurrency
	}
	if cfg.ToolTimeout > 0 {
		timeout = cfg.ToolTimeout
	}
	if cfg.ToolMaxRetry != nil && *cfg.ToolMaxRetry >= 0 {
		maxRetry = *cfg.ToolMaxRetry
	}
	return
}

func (s *Runtime) preserveToolRetry(ctx context.Context, runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.active[runID]
	// 正常退出暂停逻辑调用；用户取消和会话删除仍终止后续重试。
	return run != nil && !run.requested && (ctx.Err() == nil || s.stopping)
}

func (s *Runtime) processTools(ctx context.Context, call ModelCall, r Record, cfg ModelConfig) (result ModelCall, failure *RunError) {
	// 模型密钥读取失败不改变仍有效的工具权限；从运行已有配置起保留最近有效参数。
	limit, timeout, maxRetry := toolConfig(cfg)
	refreshConfig := func() {
		latest, _ := s.currentModel(r.Provider, r.Model)
		if latest.Provider == r.Provider && latest.Model == r.Model {
			limit, timeout, maxRetry = toolConfig(latest)
		}
	}
	var requests []ToolRequest
	if call.Output == nil || json.Unmarshal(call.Output.Message.ToolCalls, &requests) != nil {
		return call, toolError("model_invalid_format", "工具响应无效")
	}
	stored, err := s.toolStore.ReadTools(context.Background(), call.ID)
	if err != nil {
		return call, persistenceFailure()
	}
	known := map[string]ToolCall{}
	for _, tool := range stored {
		known[tool.Request.ID] = tool
	}
	active := map[string]*toolAttempt{}
	// 回调只提交尝试事实；单协调器核验 token 后才有权结算。
	results := make(chan toolAttemptResult, len(requests))
	closed := make(chan struct{})
	var token uint64
	defer func() {
		close(closed)
		defer func() {
			for _, attempt := range active {
				attempt.cancel()
			}
		}()
		if failure == nil {
			return
		}
		for {
			select {
			case returned := <-results:
				if attempt := active[returned.tool.Request.ID]; attempt != nil && attempt.accepts(returned) {
					result.PendingTools = append(result.PendingTools, returned.tool)
				}
			default:
				return
			}
		}
	}()
	settle := func(tool ToolCall) *RunError {
		if toolRetryable(tool.Error) && tool.RetryCount < tool.MaxRetry && s.preserveToolRetry(ctx, r.RunID) {
			next := time.Now().Add(retryDelay(tool.RetryCount + 1))
			if latest := time.Unix(0, 1<<63-1); next.After(latest) {
				next = latest
			}
			tool.State, tool.NextRetryAt = "retry_wait", &next
			if err := s.toolStore.WaitToolRetry(context.Background(), tool); err != nil {
				// 中间故障不能冒充最终结果；失败收尾按未确认规则补齐。
				return persistenceFailure()
			}
			known[tool.Request.ID] = tool
			return nil
		}
		progress, err := s.toolStore.FinishTool(context.Background(), call, tool)
		if err != nil {
			call.PendingTools = append(call.PendingTools, tool)
			return persistenceFailure()
		}
		call = progress
		known[tool.Request.ID] = tool
		return nil
	}
	acceptResult := func(returned toolAttemptResult) *RunError {
		attempt := active[returned.tool.Request.ID]
		if attempt == nil || !attempt.accepts(returned) {
			return nil
		}
		attempt.cancel()
		delete(active, returned.tool.Request.ID)
		return settle(returned.tool)
	}
	// 周期唤醒读取热配置；精确超时及退避期限用最近期限定时器唤醒。
	for {
		// 已在期限内返回的事实优先接纳，短事务等待不能将其改判超时。
		for draining := true; draining; {
			select {
			case returned := <-results:
				if failure := acceptResult(returned); failure != nil {
					return call, failure
				}
			default:
				draining = false
			}
		}
		expired := false
		for id, attempt := range active {
			if attempt.ctx.Err() == nil {
				continue
			}
			attempt.cancel()
			delete(active, id)
			tool := attempt.tool
			tool.State = "failed"
			tool.Error = toolError("tool_timeout", "工具执行超时，取消信号不代表业务副作用已回滚")
			if failure := settle(tool); failure != nil {
				return call, failure
			}
			// 保存可能阻塞，先重新接纳期间及时返回的事实，再裁定下一个超时。
			expired = true
			break
		}
		if expired {
			continue
		}
		serial := false
		for _, attempt := range active {
			serial = serial || !attempt.parallel
		}
		for _, request := range requests {
			refreshConfig()
			if ctx.Err() != nil || serial || len(active) >= limit {
				break
			}
			if call.ToolCompleted(request.ID) || active[request.ID] != nil {
				continue
			}
			tool, exists := known[request.ID]
			if exists && tool.State == "retry_wait" && tool.NextRetryAt != nil && time.Now().Before(*tool.NextRetryAt) {
				continue
			}
			entry, registered := s.tools.Lookup(request.Function.Name)
			parallel := !registered || entry.Parallel
			if !parallel && len(active) > 0 {
				continue
			}
			if !exists {
				tool = ToolCall{
					ModelCallID: call.ID,
					Request:     request,
					State:       "running",
					MaxRetry:    maxRetry,
				}
				if registered && entry.Dangerous {
					tool.IdempotencyKey = "tool:" + request.ID + ":" + uuid.NewString()
				}
			} else {
				if tool.State != "running" && tool.State != "retry_wait" {
					return call, persistenceFailure()
				}
				if tool.RetryCount >= tool.MaxRetry {
					if tool.State == "running" {
						tool = tool.WithResultUnconfirmed()
					} else {
						tool.State = "failed"
					}
					if failure := settle(tool); failure != nil {
						return call, failure
					}
					continue
				}
				tool.RetryCount++
			}
			tool.State, tool.Error, tool.Output, tool.NextRetryAt = "running", nil, nil, nil
			// 与取消受理共用启动门，取消提交后不能再登记有效尝试。
			s.mu.Lock()
			if ctx.Err() != nil {
				s.mu.Unlock()
				break
			}
			started, registeredTool, err := s.toolStore.BeginTool(context.Background(), call, tool)
			if err != nil {
				s.mu.Unlock()
				return call, persistenceFailure()
			}
			call, tool = started, registeredTool
			known[request.ID] = tool
			attemptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			token++
			attempt := &toolAttempt{
				tool:     tool,
				ctx:      attemptCtx,
				cancel:   cancel,
				parallel: parallel,
				token:    token,
			}
			active[request.ID] = attempt
			go func(attempt *toolAttempt) {
				tool := attempt.tool
				output, failure := s.tools.Execute(tools.WithExecutionKey(attempt.ctx, tool.IdempotencyKey), tool.Request.Function.Name, tool.Request.Function.Arguments)
				tool.Output, tool.State = output, "succeeded"
				if failure != nil {
					tool.State = "failed"
					tool.Error = toolError(failure.Code, failure.Message)
					if failure.Code == "tool_argument_parse_error" {
						logger.Warn("[chat] 工具参数不完整或 JSON 非法，未执行业务", "run_id", r.RunID, "model_call_id", tool.ModelCallID, "tool_call_id", tool.Request.ID)
					}
				}
				select {
				case results <- toolAttemptResult{
					tool:       tool,
					token:      attempt.token,
					finishedAt: time.Now(),
				}:
				case <-closed:
				}
			}(attempt)
			s.mu.Unlock()
			serial = !parallel
		}
		if len(active) == 0 && (ctx.Err() != nil || call.toolBatchComplete()) {
			return call, nil
		}
		wait := 100 * time.Millisecond
		for _, attempt := range active {
			deadline, _ := attempt.ctx.Deadline()
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
		}
		if ctx.Err() == nil {
			for id, tool := range known {
				if active[id] == nil && !call.ToolCompleted(id) && tool.State == "retry_wait" && tool.NextRetryAt != nil {
					if remaining := time.Until(*tool.NextRetryAt); remaining > 0 && remaining < wait {
						wait = remaining
					}
				}
			}
		}
		timer := time.NewTimer(wait)
		// 取消只阻止后续启动，已开始尝试等待实际结果或原期限。
		wake := ctx.Done()
		if ctx.Err() != nil {
			wake = nil
		}
		select {
		case returned := <-results:
			timer.Stop()
			if failure := acceptResult(returned); failure != nil {
				return call, failure
			}
		case <-timer.C:
		case <-wake:
			timer.Stop()
		}
	}
}
