package chat

import (
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
)

func persistenceLogContext(call ModelCall, stage string, err error, retry int) []any {
	return []any{
		"run_id", call.RunID,
		"session_id", call.SessionID,
		"model_call_id", call.ID,
		"stage", stage,
		"checkpoint_stage", call.CheckpointStage,
		"retry", retry,
		"error", err,
	}
}

func persistenceFailure(call ModelCall, stage string, err error) *RunError {
	logger.Warn("[chat] 持久化故障，停止推进并保存失败终态", persistenceLogContext(call, stage, err, 0)...)
	return &RunError{
		Code:    "persistence_failed",
		Message: "保存模型调用进度失败",
	}
}

func (s *Runtime) finish(call ModelCall, r Record) {
	call.NextRetryAt = nil
	if len(call.RetryHistory) > 0 {
		last := &call.RetryHistory[len(call.RetryHistory)-1]
		switch last.Result {
		case "retry_wait", "running", "interrupted":
			last.Result = string(r.State)
			last.Error = r.Error
		}
	}
	// 终态提交失败后只重试同一份事实，不重新调用模型或工具。
	var err error
	for retry := 0; retry <= 3; retry++ {
		if retry > 0 {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-timer.C:
			case <-s.storageCtx.Done():
				timer.Stop()
				logger.Warn("[chat] 退出期限已到，停止收尾重试并保留已持久化事实", persistenceLogContext(call, "finish", err, retry)...)
				return
			}
			logger.Warn("[chat] 重试保存终态", persistenceLogContext(call, "finish", err, retry)...)
		}
		if err = s.execution.Finish(s.persistenceContext(), call, r); err == nil {
			return
		}
		if retry == 0 {
			logger.Warn("[chat] 保存终态失败，保留占用等待重试", persistenceLogContext(call, "finish", err, retry)...)
		}
	}
	logger.Error("[chat] 终态保存重试耗尽，释放占用但不推送未保存终态", persistenceLogContext(call, "finish", err, 3)...)
	// 关闭现有订阅以释放轮询资源；数据库仍为实际状态，不补造结束事件。
	s.mu.Lock()
	if sub := s.subscribers[r.RunID]; sub != nil {
		sub.cancel()
	}
	s.mu.Unlock()
}
