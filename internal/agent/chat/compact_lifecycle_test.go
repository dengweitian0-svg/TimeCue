package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
)

// TestCompactSuccessSurvivesKeyRemoval 验证完整摘要返回时密钥被删除，成功摘要仍生效且可供后续复用。
func TestCompactSuccessSurvivesKeyRemoval(t *testing.T) {
	var missing, expanded atomic.Bool
	requests := make(chan chat.ModelRequest, 5)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if request.Messages[0].Role == "system" {
			missing.Store(true)
			emitText(w, "已完整生成的有效摘要")
		} else if len(request.Messages) == 1 {
			emitText(w, strings.Repeat("old-history-", 1600))
		} else {
			emitText(w, "后续成功")
		}
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
		cfg, err := compactSettings(upstream.URL, &expanded)()
		if missing.Load() {
			cfg[0].Key = ""
		}
		return cfg, err
	})
	awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
	<-requests
	run := awaitFinishedRun(t, service, createCompactRun(t, service, "second").RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_config_unavailable" {
		t.Fatalf("run=%+v", run)
	}
	<-requests
	missing.Store(false)
	expanded.Store(true)
	awaitFinishedRun(t, service, createCompactRun(t, service, "third").RunID)
	next := <-requests
	if next.Messages[0].Content != "已完整生成的有效摘要" || strings.Contains(mustJSON(next), "old-history-") {
		t.Fatal("密钥删除导致成功摘要丢失")
	}
}

type compactCancelFault struct {
	chat.ExecutionRepository
	seam           string
	storageFailure bool
	armed          atomic.Bool
	ready          atomic.Bool
	entered        chan struct{}
	release        chan struct{}
}

func (f *compactCancelFault) fail(ctx context.Context) error {
	close(f.entered)
	<-f.release
	if f.storageFailure {
		return errors.New("独立的存储故障")
	}
	return fmt.Errorf("存储边界的取消：%w", ctx.Err())
}

// BeginCall 在真实登记入口注入取消或独立故障，不伪造聊天产品行为。
func (f *compactCancelFault) BeginCall(ctx context.Context, call chat.ModelCall) (int64, error) {
	if f.seam == "begin" && call.CheckpointStage == "compact" && f.armed.CompareAndSwap(true, false) {
		return 0, f.fail(ctx)
	}
	return f.ExecutionRepository.BeginCall(ctx, call)
}

// History 在摘要已完整返回后的原历史读取边界注入故障。
func (f *compactCancelFault) History(ctx context.Context, session string) ([]chat.Message, error) {
	if f.seam == "history" && f.ready.Load() && f.armed.CompareAndSwap(true, false) {
		return nil, f.fail(ctx)
	}
	return f.ExecutionRepository.History(ctx, session)
}

// CompleteCompact 在切换前注入故障，确认取消不会掩盖独立存储错误。
func (f *compactCancelFault) CompleteCompact(ctx context.Context, call chat.ModelCall, next *chat.ModelCall, terminal *chat.Record) (chat.ModelCall, error) {
	if f.seam == "switch" && f.armed.CompareAndSwap(true, false) {
		return call, f.fail(ctx)
	}
	return f.ExecutionRepository.CompleteCompact(ctx, call, next, terminal)
}

// TestCompactCancellationPreservesStorageFailure 验证三个持久化边界只将真正的 context 错误归为生命周期中断。
func TestCompactCancellationPreservesStorageFailure(t *testing.T) {
	for _, seam := range []string{"begin", "history", "switch"} {
		for _, storageFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", seam, storageFailure), func(t *testing.T) {
				_, ports, _ := admissionDatabase(t)
				fault := &compactCancelFault{
					ExecutionRepository: ports.Execution,
					seam:                seam,
					storageFailure:      storageFailure,
					entered:             make(chan struct{}),
					release:             make(chan struct{}),
				}
				ports.Execution = fault
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request chat.ModelRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					if request.Messages[0].Role == "system" {
						fault.ready.Store(true)
						emitText(w, "完整摘要")
					} else {
						emitText(w, strings.Repeat("old-history-", 1600))
					}
				}))
				defer upstream.Close()
				service := startAdmissionService(t, ports, completion.NewClient(nil), compactSettings(upstream.URL, nil))
				awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
				fault.armed.Store(true)
				receipt := createCompactRun(t, service, "second")
				select {
				case <-fault.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("故障边界没有进入")
				}
				if _, err := service.CancelRun(context.Background(), receipt.RunID); err != nil {
					t.Fatal(err)
				}
				close(fault.release)
				run := awaitFinishedRun(t, service, receipt.RunID)
				if storageFailure {
					if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" {
						t.Fatalf("run=%+v", run)
					}
				} else if run.Status != chat.StatusCancelled || run.Error != nil {
					t.Fatalf("run=%+v", run)
				}
			})
		}
	}
}
