package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestRunUsageRecomputesPersistedFacts 验证真实用量逐次重算、范围隔离、重启保留及原始扩展字段不丢失。
func TestRunUsageRecomputesPersistedFacts(t *testing.T) {
	const raw = `{"prompt_tokens":11,"completion_tokens":7,"total_tokens":99,"prompt_cache_hit_tokens":3,"completion_tokens_details":{"reasoning_tokens":2}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "data: {\"id\":\"actual\",\"choices\":[{\"delta\":{\"content\":\"真实正文\"},\"finish_reason\":\"stop\"}],\"usage\":%s}\n\ndata: [DONE]\n\n", raw)
	}))
	defer upstream.Close()
	db, ports, path := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	ctx := context.Background()
	receipt, err := service.CreateRun(ctx, admissionInput("usage", "first"))
	if err != nil {
		t.Fatal(err)
	}
	run := awaitFinishedRun(t, service, receipt.RunID)
	other, err := service.CreateRun(ctx, admissionInput("other", "first"))
	if err != nil {
		t.Fatal(err)
	}
	awaitFinishedRun(t, service, other.RunID)
	before, err := service.GetRunEvents(ctx, run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.GetRunUsage(ctx, run.RunID)
	if err != nil || len(first.Calls) != 1 || string(first.Calls[0].Usage) != raw {
		t.Fatalf("真实原始用量丢失: %+v %v", first, err)
	}
	if first.Tokens.PromptTokens == nil || *first.Tokens.PromptTokens != 11 || first.Tokens.CompletionTokens == nil || *first.Tokens.CompletionTokens != 7 || first.Tokens.TotalTokens == nil || *first.Tokens.TotalTokens != 99 {
		t.Fatalf("用量被估算或超出请求范围: %+v", first.Tokens)
	}
	second, err := service.GetRunUsage(ctx, run.RunID)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("重复读取发生累计: %+v %v", second, err)
	}
	after, err := service.GetRunEvents(ctx, run.RunID, 0, 100)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("读取用量改变事件: %+v %v", after, err)
	}
	actual, err := service.GetRun(ctx, run.RunID)
	if err != nil || !reflect.DeepEqual(run, actual) {
		t.Fatalf("读取用量改变真实结果: %+v %v", actual, err)
	}
	service.Stop()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := store.NewSqlite()
	if err := reopened.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := startAdmissionService(t, store.NewChatStorage(reopened), completion.NewClient(nil), admissionSettings)
	third, err := restarted.GetRunUsage(ctx, run.RunID)
	if err != nil || !reflect.DeepEqual(first, third) {
		t.Fatalf("重启改变用量: %+v %v", third, err)
	}
}

// TestRunUsageMissingFailureAndCancellation 验证缺失保持未知，失败和取消保留已收到的真实用量。
func TestRunUsageMissingFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"missing", "null", "failed", "failed_missing", "cancelled", "cancelled_missing", "zero", "partial"} {
		t.Run(mode, func(t *testing.T) {
			raw := `{"prompt_tokens":11,"completion_tokens":7,"total_tokens":99,"vendor_detail":{"cache":3}}`
			switch mode {
			case "missing", "failed_missing", "cancelled_missing":
				raw = ""
			case "null":
				raw = "null"
			case "zero":
				raw = `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`
			case "partial":
				raw = `{"total_tokens":9}`
			}
			cancelled := strings.HasPrefix(mode, "cancelled")
			failed := strings.HasPrefix(mode, "failed")
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				usage := ""
				if raw != "" {
					usage = `,"usage":` + raw
				}
				for i := 0; i < 5; i++ {
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"原文\"}}]%s}\n\n", usage)
				}
				if cancelled {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				} else if !failed {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer upstream.Close()
			_, ports, _ := admissionDatabase(t)
			service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
			ctx := context.Background()
			receipt, err := service.CreateRun(ctx, admissionInput("usage", mode))
			if err != nil {
				t.Fatal(err)
			}
			if cancelled {
				deadline := time.Now().Add(10 * time.Second)
				for {
					page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 100)
					if err != nil {
						t.Fatal(err)
					}
					if len(page.Events) >= 6 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("部分正文未持久化")
					}
					time.Sleep(time.Millisecond)
				}
				// 只统计已提交事实，不从正文事件或适配器内存推测尚未保存的用量。
				pending, err := service.GetRunUsage(ctx, receipt.RunID)
				if err != nil || len(pending.Calls) != 1 || pending.Calls[0].Usage != nil || pending.Tokens.TotalTokens != nil {
					t.Fatalf("未提交用量被冒充已知: %+v %v", pending, err)
				}
				if _, err := service.CancelRun(ctx, receipt.RunID); err != nil {
					t.Fatal(err)
				}
			}
			run := awaitFinishedRun(t, service, receipt.RunID)
			want := chat.StatusSucceeded
			if cancelled {
				want = chat.StatusCancelled
			} else if failed {
				want = chat.StatusFailed
			}
			if run.Status != want || run.Output == nil || *run.Output != strings.Repeat("原文", 5) {
				t.Fatalf("原始输出或终态丢失: %+v", run)
			}
			usage, err := service.GetRunUsage(ctx, receipt.RunID)
			if err != nil || len(usage.Calls) != 1 {
				t.Fatalf("调用缺失: %+v %v", usage, err)
			}
			if raw == "" || raw == "null" {
				if usage.Calls[0].Usage != nil || usage.Tokens.PromptTokens != nil || usage.Tokens.CompletionTokens != nil || usage.Tokens.TotalTokens != nil {
					t.Fatalf("未知用量被补齐: %+v", usage)
				}
			} else {
				if string(usage.Calls[0].Usage) != raw || usage.Tokens.TotalTokens == nil {
					t.Fatalf("真实用量丢失: %+v", usage)
				}
				expected := int64(99)
				if mode == "zero" {
					expected = 0
				} else if mode == "partial" {
					expected = 9
					if usage.Tokens.PromptTokens != nil || usage.Tokens.CompletionTokens != nil {
						t.Fatalf("缺失字段被推算: %+v", usage.Tokens)
					}
				}
				if *usage.Tokens.TotalTokens != expected {
					t.Fatalf("实际总量被改写: %+v", usage.Tokens)
				}
			}
			service.Stop()
			restarted := startAdmissionService(t, ports, completion.NewClient(nil), admissionSettings)
			again, err := restarted.GetRunUsage(ctx, receipt.RunID)
			if err != nil || !reflect.DeepEqual(usage, again) {
				t.Fatalf("重启改变用量: %+v %v", again, err)
			}
		})
	}
}

// TestRunUsageIncludesCompactAndContinuations 验证摘要及两类续写共用同一入口，部分未知不能作为完整合计。
func TestRunUsageIncludesCompactAndContinuations(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			var calls atomic.Int32
			raw := []string{
				`{"prompt_tokens":11,"completion_tokens":7,"total_tokens":99}`,
				`{"prompt_tokens":2,"completion_tokens":3,"total_tokens":8}`,
				`{"prompt_tokens":3,"completion_tokens":4,"total_tokens":9}`,
				`{"prompt_tokens":5,"completion_tokens":6,"total_tokens":12}`,
			}
			if missing {
				raw[1] = "null"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := int(calls.Add(1)) - 2
				if index < 0 {
					emitText(w, strings.Repeat("old-history-", 1600))
					return
				}
				if index >= len(raw) {
					t.Error("用量查询推进了模型")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				reason, text := "stop", "结束"
				if index%2 == 0 {
					reason, text = "length", "第一段"
				}
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":%q}],\"usage\":%s}\n\ndata: [DONE]\n\n", text, reason, raw[index])
			}))
			defer upstream.Close()
			_, ports, _ := admissionDatabase(t)
			service := startAdmissionService(t, ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
				configs, err := compactSettings(upstream.URL, nil)()
				configs[0].MaxContinue = 2
				return configs, err
			})
			ctx := context.Background()
			awaitFinishedRun(t, service, createCompactRun(t, service, "history").RunID)
			run := awaitFinishedRun(t, service, createCompactRun(t, service, "with-compact").RunID)
			if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "第一段结束" {
				t.Fatalf("摘要或续写没有完成: %+v", run)
			}
			usage, err := service.GetRunUsage(ctx, run.RunID)
			if err != nil || len(usage.Calls) != 4 {
				t.Fatalf("没有覆盖全部独立调用: %+v %v", usage, err)
			}
			for i, call := range usage.Calls {
				if i > 0 && call.ModelCallID <= usage.Calls[i-1].ModelCallID {
					t.Fatal("独立调用身份丢失")
				}
				want := raw[i]
				if want == "null" {
					want = ""
				}
				if string(call.Usage) != want {
					t.Fatalf("调用 %d 原始用量被改写: %s", i, call.Usage)
				}
			}
			if missing {
				if usage.Tokens.PromptTokens != nil || usage.Tokens.CompletionTokens != nil || usage.Tokens.TotalTokens != nil {
					t.Fatalf("不完整统计冒充完整合计: %+v", usage.Tokens)
				}
			} else if usage.Tokens.PromptTokens == nil || *usage.Tokens.PromptTokens != 21 || usage.Tokens.CompletionTokens == nil || *usage.Tokens.CompletionTokens != 20 || usage.Tokens.TotalTokens == nil || *usage.Tokens.TotalTokens != 128 {
				t.Fatalf("摘要或续写被漏计或重复累计: %+v", usage.Tokens)
			}
			again, err := service.GetRunUsage(ctx, run.RunID)
			if err != nil || !reflect.DeepEqual(usage, again) || calls.Load() != 5 {
				t.Fatalf("重复读取推进或累计: %+v %v calls=%d", again, err, calls.Load())
			}
		})
	}
}

// TestRunUsageRespectsVisibilityAndNoCalls 验证未知运行、软删除及未调用模型的预算拒绝不被补成零用量。
func TestRunUsageRespectsVisibilityAndNoCalls(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), admissionSettings)
	ctx := context.Background()
	if _, err := service.GetRunUsage(ctx, "missing"); !errors.Is(err, chat.ErrRunNotFound) {
		t.Fatalf("未知运行没有拒绝: %v", err)
	}
	input := admissionInput("usage", "budget")
	input.Input = strings.Repeat("x", 200000)
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	awaitFinishedRun(t, service, receipt.RunID)
	usage, err := service.GetRunUsage(ctx, receipt.RunID)
	if err != nil || len(usage.Calls) != 0 || usage.Tokens.TotalTokens != nil {
		t.Fatalf("无调用被估算补齐: %+v %v", usage, err)
	}
	if err := service.DeleteSession(ctx, input.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetRunUsage(ctx, receipt.RunID); !errors.Is(err, chat.ErrRunNotFound) {
		t.Fatalf("已删除会话用量仍可见: %v", err)
	}
}

// TestRunUsageUnusableCountersStayUnknown 验证供应商异常字段或溢出不产生伪造合计，原始对象仍保留。
func TestRunUsageUnusableCountersStayUnknown(t *testing.T) {
	for _, raw := range []string{
		`{"total_tokens":-1}`,
		`{"total_tokens":null}`,
		`{"total_tokens":"9"}`,
		`{"total_tokens":1.5}`,
		`{"total_tokens":9223372036854775807}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reason := "length"
				if calls.Add(1) > 1 {
					reason = "stop"
				}
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"正文\"},\"finish_reason\":%q}],\"usage\":%s}\n\ndata: [DONE]\n\n", reason, raw)
			}))
			defer upstream.Close()
			service := newFixture(t, upstream.URL)
			ctx := context.Background()
			receipt, err := service.CreateRun(ctx, admissionInput("usage", "counter"))
			if err != nil {
				t.Fatal(err)
			}
			awaitFinishedRun(t, service, receipt.RunID)
			usage, err := service.GetRunUsage(ctx, receipt.RunID)
			if err != nil || len(usage.Calls) != 2 || usage.Tokens.TotalTokens != nil {
				t.Fatalf("非法字段或溢出被当作已知: %+v %v", usage, err)
			}
			for _, call := range usage.Calls {
				if !json.Valid(call.Usage) || string(call.Usage) != raw {
					t.Fatalf("原始异常字段被改写: %s", call.Usage)
				}
			}
		})
	}
}
