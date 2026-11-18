package loop

import (
	"context"
	"errors"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

type runRecoveryAction uint8

const (
	resumeRun runRecoveryAction = iota
	finishSupersededRun
	cancelHiddenRun
	settleCancelledRun
)

func recoverRun(run chat.Record, session chat.Session, hidden bool) runRecoveryAction {
	switch {
	case run.State == chat.StateWaitForCancel:
		return settleCancelledRun
	case hidden:
		return cancelHiddenRun
	case len(session.RunIDs) > 0 && session.RunIDs[len(session.RunIDs)-1] != run.RunID:
		return finishSupersededRun
	default:
		return resumeRun
	}
}

type modelRecoveryAction uint8

const (
	sendModelRequest modelRecoveryAction = iota
	reuseModelReply
	waitForModelRetry
)

func recoverModel(call chat.ModelCall) (modelRecoveryAction, *time.Time) {
	// 完整回复可能在正常退出时保存，复用前仍须经过同一响应校验。
	if call.State == chat.StateRunning && call.Error == nil && call.Output != nil && call.Output.FinishReason != "" {
		return reuseModelReply, nil
	}
	if call.State == chat.StateRetryWait {
		return waitForModelRetry, call.NextRetryAt
	}
	return sendModelRequest, nil
}

func waitUntil(ctx context.Context, at *time.Time) bool {
	if at == nil || !time.Now().Before(*at) {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(time.Until(*at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func nextModelRetry(call chat.ModelCall, failure *chat.RunError, interruptedOrPartial bool) (time.Time, bool) {
	if interruptedOrPartial || call.RetryCount >= call.MaxRetry || !retryable(failure) {
		return time.Time{}, false
	}
	return agent.RetryAt(time.Now(), call.RetryCount+1), true
}
func retryable(failure *chat.RunError) bool {
	if failure == nil {
		return false
	}
	switch failure.Code {
	case "model_timeout", "model_network_error", "model_rate_limited", "model_server_error", "model_server_busy":
		return true
	default:
		return false
	}
}

func shouldPause(action runRecoveryAction, stopping, cancelled bool, failure *chat.RunError) bool {
	return action == resumeRun && stopping && !cancelled && (failure == nil || failure.Code != "persistence_failed")
}

const (
	finishRetryLimit = 3
	finishRetryDelay = 5 * time.Second
)

func (s *Run) runInterrupted(ctx context.Context, run *chat.Record, err error) bool {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return true
	}
	if !errors.Is(err, context.Canceled) {
		return false
	}
	// SQLite 可能已提交取消而运行 context 尚未收到信号。
	saved, readErr := s.runs.ReadRun(s.persistenceContext(), run.RunID, false)
	if readErr == nil && saved.State == chat.StateWaitForCancel {
		run.State = saved.State
		return true
	}
	return false
}
