package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
)

type deadlinePause struct {
	chat.ExecutionRepository
	timedOut chan struct{}
}

// TestRecoveryConfigurationGateOnlyForRequests 验证缺失配置、密钥或配置读取失败时只阻断需要重发的模型请求。
func TestRecoveryConfigurationGateOnlyForRequests(t *testing.T) {
	for _, phase := range []string{"no_checkpoint", "missing_output", "partial", "retry_wait", "cancel"} {
		for _, missing := range []string{"config", "key", "settings"} {
			t.Run(phase+"/"+missing, func(t *testing.T) {
				_, ports, _ := admissionDatabase(t)
				record := recoveryRecord(phase)
				var call chat.ModelCall
				if phase == "no_checkpoint" {
					if err := ports.Runs.Create(context.Background(), record); err != nil {
						t.Fatal(err)
					}
				} else {
					call = recoveryCall(t, ports, phase)
					if phase == "partial" || phase == "cancel" {
						call.Output = &chat.ModelOutput{Message: chat.Message{
							Role:    "assistant",
							Content: "已经保存的部分回复",
						}}
					} else if phase == "retry_wait" {
						deadline := time.Now().Add(time.Hour)
						call.State = chat.StateRetryWait
						call.MaxRetry, call.RetryCount, call.NextRetryAt = 1, 1, &deadline
					}
					if err := ports.Execution.Pause(context.Background(), call); err != nil {
						t.Fatal(err)
					}
					if phase == "cancel" {
						if _, err := ports.Runs.RequestCancel(context.Background(), record.RunID); err != nil {
							t.Fatal(err)
						}
					}
				}
				settings := func() ([]chat.ModelConfig, error) {
					switch missing {
					case "config":
						return nil, nil
					case "settings":
						return nil, errors.New("配置不可读取")
					default:
						cfg, err := admissionSettings()
						cfg[0].Key = ""
						return cfg, err
					}
				}
				m := &admissionModel{}
				s := startAdmissionService(t, ports, m, settings)
				run := awaitFinishedRun(t, s, record.RunID)
				if phase == "cancel" {
					if run.Status != chat.StatusCancelled || run.Error != nil {
						t.Fatalf("配置阻断取消收尾: %+v", run)
					}
				} else if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_config_unavailable" {
					t.Fatalf("未正确报告原模型不可用: run=%+v error=%+v", run, run.Error)
				}
				if m.starts.Load() != 0 || (phase == "partial" || phase == "cancel") && (run.Output == nil || *run.Output != "已经保存的部分回复") {
					t.Fatalf("请求了替代模型或丢失原回复: %+v requests=%d", run, m.starts.Load())
				}
				if phase == "retry_wait" {
					saved, err := ports.Execution.ReadCall(context.Background(), record.RunID)
					if err != nil || saved.ID != call.ID || saved.RetryCount != 1 || saved.MaxRetry != 1 {
						t.Fatalf("配置门禁改变重试身份或预算: %+v %v", saved, err)
					}
				}
			})
		}
	}
}

type interruptCompactSwitch struct {
	chat.ExecutionRepository
	entered chan struct{}
}

// CompleteCompact 在响应完整返回后等待正常退出，不提交摘要切换。
func (f *interruptCompactSwitch) CompleteCompact(ctx context.Context, call chat.ModelCall, next *chat.ModelCall, terminal *chat.Record) (chat.ModelCall, error) {
	close(f.entered)
	<-ctx.Done()
	return call, ctx.Err()
}

// TestRecoveryCompletesSavedCompactWithoutConfiguration 验证完整摘要在配置移除后仍生效，普通请求独立失败。
func TestRecoveryCompletesSavedCompactWithoutConfiguration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(request.Messages[0].Content, "上下文检查点压缩") {
			emitText(w, "已完整生成的摘要")
		} else {
			emitText(w, strings.Repeat("old-history-", 1600))
		}
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	fault := &interruptCompactSwitch{
		ExecutionRepository: ports.Execution,
		entered:             make(chan struct{}),
	}
	storage := ports
	storage.Execution = fault
	s := startAdmissionService(t, storage, completion.NewClient(nil), compactSettings(upstream.URL, nil))
	awaitFinishedRun(t, s, createCompactRun(t, s, "first").RunID)
	id := createCompactRun(t, s, "second").RunID
	select {
	case <-fault.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("摘要没有完整返回")
	}
	s.Stop()
	m := &admissionModel{}
	s = startAdmissionService(t, ports, m, func() ([]chat.ModelConfig, error) { return nil, nil })
	run := awaitFinishedRun(t, s, id)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_config_unavailable" || m.starts.Load() != 0 {
		t.Fatalf("配置移除未正确收尾: run=%+v error=%+v requests=%d", run, run.Error, m.starts.Load())
	}
	history, err := ports.Execution.History(context.Background(), run.SessionID)
	if err != nil || len(history) != 3 || history[0].Content != "已完整生成的摘要" || strings.Contains(mustJSON(history), "old-history-") {
		t.Fatalf("完整摘要丢失或原文重复: %+v err=%v", history, err)
	}
}

// Pause 在存储边界等待共享退出期限，未传入期限时明确报告故障。
func (p *deadlinePause) Pause(ctx context.Context, call chat.ModelCall) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("退出进度保存没有期限")
	}
	<-ctx.Done()
	close(p.timedOut)
	return ctx.Err()
}

// TestShutdownBoundsProgressPersistence 验证退出保存到期时留下可恢复进度，不伪造失败或结束事件。
func TestShutdownBoundsProgressPersistence(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	paused := &deadlinePause{
		ExecutionRepository: ports.Execution,
		timedOut:            make(chan struct{}),
	}
	ports.Execution = paused
	m := &admissionModel{
		started: make(chan struct{}, 1),
	}
	s := startAdmissionService(t, ports, m, admissionSettings)
	receipt, err := s.CreateRun(context.Background(), admissionInput("shutdown", "first"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.started:
	case <-time.After(3 * time.Second):
		t.Fatal("模型请求未开始")
	}
	s.Stop()
	select {
	case <-paused.timedOut:
	default:
		t.Fatal("未在有界退出窗口内尝试保存进度")
	}
	run, err := s.GetRun(context.Background(), receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning || run.FinishedAt != nil || run.Error != nil {
		t.Fatalf("保存到期伪造终态: %+v err=%v", run, err)
	}
	page := eventPage(t, s, receipt.RunID, 0, 100)
	if len(page.Events) != 1 || page.Events[0].Type != chat.EventStart {
		t.Fatalf("保存到期产生结束事件: %+v", page)
	}
}

// TestRecoveryReusesAcceptedReplyWithoutModelConfig 验证只需结束运行的完整回复不受模型配置门禁影响。
func TestRecoveryReusesAcceptedReplyWithoutModelConfig(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	call := recoveryCall(t, ports, "accepted")
	call.Output = &chat.ModelOutput{
		Message: chat.Message{
			Role:    "assistant",
			Content: "已提交的完整回复",
		},
		FinishReason: "stop",
	}
	if err := ports.Tools.AcceptResponse(context.Background(), call.WithResponseAccepted()); err != nil {
		t.Fatal(err)
	}
	m := &admissionModel{}
	s := startAdmissionService(t, ports, m, func() ([]chat.ModelConfig, error) { return nil, nil })
	run := awaitFinishedRun(t, s, call.RunID)
	if run.Status != chat.StatusSucceeded || run.Error != nil || run.Output == nil || *run.Output != "已提交的完整回复" || m.starts.Load() != 0 {
		t.Fatalf("完整结果被重执或配置阻断: run=%+v error=%+v requests=%d", run, run.Error, m.starts.Load())
	}
}

// TestRecoveryReusesCompletePausedResponse 验证退出已保存完整响应时不再请求模型或重复追加消息。
func TestRecoveryReusesCompletePausedResponse(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	call := recoveryCall(t, ports, "complete-paused")
	call.Output = &chat.ModelOutput{
		ID: "complete-response",
		Message: chat.Message{
			Role:             "assistant",
			Content:          "退出前完整返回",
			ReasoningContent: "原思考",
		},
		FinishReason: "stop",
	}
	if err := ports.Execution.Pause(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	m := &admissionModel{}
	s := startAdmissionService(t, ports, m, func() ([]chat.ModelConfig, error) { return nil, nil })
	run := awaitFinishedRun(t, s, call.RunID)
	if run.Status != chat.StatusSucceeded || run.Error != nil || run.Output == nil || *run.Output != "退出前完整返回" || m.starts.Load() != 0 {
		t.Fatalf("完整响应未复用: run=%+v error=%+v requests=%d", run, run.Error, m.starts.Load())
	}
	history, err := ports.Execution.History(context.Background(), call.SessionID)
	if err != nil || len(history) != 2 || history[1].Content != "退出前完整返回" || history[1].ReasoningContent != "原思考" {
		t.Fatalf("恢复重复或丢失消息: %+v err=%v", history, err)
	}
}

func recoveryCall(t *testing.T, ports chat.Storage, id string) chat.ModelCall {
	t.Helper()
	ctx := context.Background()
	if err := ports.Runs.Create(ctx, recoveryRecord(id)); err != nil {
		t.Fatal(err)
	}
	call := chat.ModelCall{
		RunID:     id,
		SessionID: id,
		Input: chat.ModelRequest{
			Model: "model",
			Messages: []chat.Message{{
				Role:    "user",
				Content: "原始输入",
			}},
			MaxTokens: 8192,
		},
		State:    chat.StateRunning,
		MaxRetry: 1,
	}
	var err error
	call.ID, err = ports.Execution.BeginCall(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	return call.WithCallStarted()
}

func recoveryRecord(id string) chat.Record {
	return chat.Record{
		Run: chat.Run{
			RunID:     id,
			SessionID: id,
			Provider:  "test",
			Model:     "model",
			Input:     "原始输入",
			CreatedAt: time.Now(),
		},
		State:          chat.StateRunning,
		IdempotencyKey: "key",
	}
}
