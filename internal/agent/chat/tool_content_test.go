package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// TestLargeToolContentKeepsFullOriginalEvents 验证模型头尾有界、UTF-8 完整且分页事件保留完整 JSON 数组。
func TestLargeToolContentKeepsFullOriginalEvents(t *testing.T) {
	requests := make(chan chat.ModelRequest, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if request.Messages[len(request.Messages)-1].Role == "user" {
			emitTools(w, []chat.ToolRequest{toolRequest("large", "list_schedules", `{"name":"大型","limit":50}`)})
		} else {
			emitText(w, "已查询完整原始结果")
		}
	}))
	defer upstream.Close()
	service, _, application := newToolFixture(t, upstream.URL)
	description := strings.Repeat("中🙂a", 9000) + "尾部事实"
	_, _, err := application.CreateSchedule(context.Background(), model.Schedule{
		Name:        "大型结果",
		Description: &description,
		Type:        model.SingleSchedule,
		Single:      &model.Single{NotifyTime: time.Now().Add(24 * time.Hour).Truncate(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := startToolRun(t, service, "large-content")
	if run.Status != chat.StatusSucceeded {
		t.Fatalf("run=%+v", run)
	}
	<-requests
	request := <-requests
	content := request.Messages[len(request.Messages)-1].Content
	if len(content) > 32768 || !utf8.ValidString(content) || !strings.Contains(content, "中间内容已省略，结果不完整") || json.Valid([]byte(content)) {
		t.Fatalf("模型投影未按阈值裁剪：bytes=%d valid=%v", len(content), utf8.ValidString(content))
	}
	var original json.RawMessage
	for after := int64(0); ; {
		page := eventPage(t, service, run.RunID, after, 1)
		for _, event := range page.Events {
			if event.Type == chat.EventToolResult {
				var result chat.ToolResultData
				if err := json.Unmarshal(event.Data, &result); err != nil || result.Error != nil {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				original = result.Output
			}
		}
		if !page.HasMore {
			break
		}
		after = page.NextAfterSeq
	}
	var values []struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(original, &values); err != nil || len(values) != 1 || values[0].Description != description {
		t.Fatalf("原始数组结果丢失：bytes=%d err=%v", len(original), err)
	}
	parts := strings.Split(content, "\n[中间内容已省略，结果不完整]\n")
	if len(parts) != 2 || !strings.HasPrefix(string(original), parts[0]) || !strings.HasSuffix(string(original), parts[1]) || len(parts[0])-len(parts[1]) > 3 || len(parts[1])-len(parts[0]) > 3 {
		t.Fatal("头尾不是原文的等分片段")
	}
}

// TestRuntimeCompactCannotBypassRoundLimit 验证摘要后的普通调用仍受既有轮次上限保护。
func TestRuntimeCompactCannotBypassRoundLimit(t *testing.T) {
	var normal atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if len(request.Tools) == 0 {
			emitText(w, "日程2已查到，后续仍待处理")
		} else if normal.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":2}`)})
		} else {
			emitText(w, "不应越过轮次上限")
		}
	}))
	defer upstream.Close()
	settings := func() ([]chat.ModelConfig, error) {
		configs, _ := compactSettings(upstream.URL, nil)()
		configs[0].MaxRounds = 1
		return configs, nil
	}
	service, _, _ := largeContentFixture(t, upstream.URL, settings, nil)
	run := startToolRun(t, service, "round-limit")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_round_limit_exceeded" || normal.Load() != 1 {
		t.Fatalf("run=%+v error=%+v normal=%d", run, run.Error, normal.Load())
	}
}

// TestToolContentProjectionSurvivesConfigChange 验证结果事务已保存投影，退出后配置调小不重新裁剪或查询。
func TestToolContentProjectionSurvivesConfigChange(t *testing.T) {
	requests := make(chan chat.ModelRequest, 4)
	var count atomic.Int32
	var tokens atomic.Int32
	tokens.Store(8192)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if count.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":2}`)})
		} else {
			emitText(w, "恢复完成")
		}
	}))
	defer upstream.Close()
	settings := func() ([]chat.ModelConfig, error) {
		configs, _ := toolSettings(upstream.URL)()
		configs[0].ToolResultMaxTokens = int(tokens.Load())
		return configs, nil
	}
	var gate *toolCommitGate
	service, ports, application := largeContentFixture(t, upstream.URL, settings, func(ports chat.Storage) chat.Storage {
		gate = &toolCommitGate{
			ToolRepository: ports.Tools,
			afterResults:   1,
			committed:      make(chan struct{}),
			release:        make(chan struct{}),
		}
		ports.Tools = gate
		return ports
	})
	defer gate.once.Do(func() { close(gate.release) })
	receipt, err := service.CreateRun(context.Background(), admissionInput("saved-projection", "first"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("没有提交工具结果")
	}
	saved, err := ports.Execution.ReadCall(context.Background(), receipt.RunID)
	if err != nil || saved == nil || saved.CheckpointStage != "tool_end" {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	content := saved.CheckpointContext[len(saved.CheckpointContext)-1].Content
	if len(content) > 32768 || len(content) < 32000 {
		t.Fatalf("投影没有随结果保存：bytes=%d", len(content))
	}
	sub, err := service.Subscribe(context.Background(), receipt.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	for {
		select {
		case event := <-sub.Events:
			if event.Type != chat.EventToolResult {
				continue
			}
			var result chat.ToolResultData
			if err := json.Unmarshal(event.Data, &result); err != nil || len(result.Output) < 60000 || !json.Valid(result.Output) {
				t.Fatalf("SSE 未保留完整原始结果：%v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("SSE 没有取得已提交结果")
		}
		break
	}
	stopped := make(chan struct{})
	go func() { service.Stop(); close(stopped) }()
	// 等到停止准入后再释放事务回执，避免原实例先登记下一请求。
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := service.CreateRun(context.Background(), admissionInput("saved-projection", "probe"))
		if err == chat.ErrStopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务未停止准入")
		}
		time.Sleep(time.Millisecond)
	}
	gate.once.Do(func() { close(gate.release) })
	<-stopped
	ports.Tools = gate.ToolRepository
	tokens.Store(64)
	service = chat.NewService(ports, completion.NewClient(nil), settings, queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	if run := awaitFinishedRun(t, service, receipt.RunID); run.Status != chat.StatusSucceeded {
		t.Fatalf("run=%+v", run)
	}
	<-requests
	resumed := <-requests
	if got := resumed.Messages[len(resumed.Messages)-1].Content; got != content || count.Load() != 2 {
		t.Fatalf("配置变化重新裁剪或查询：bytes=%d requests=%d", len(got), count.Load())
	}
}

// TestRuntimeCompactsReuseOldSummaryWithoutExtraBudget 验证创建前与多次运行中摘要不占普通轮次或另设次数预算。
func TestRuntimeCompactsReuseOldSummaryWithoutExtraBudget(t *testing.T) {
	requests := make(chan chat.ModelRequest, 12)
	var normal, summaries atomic.Int32
	var expanded atomic.Bool
	expanded.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if len(request.Tools) == 0 {
			summaries.Add(1)
			emitText(w, "旧有效摘要")
			return
		}
		switch normal.Add(1) {
		case 1:
			emitText(w, strings.Repeat("已覆盖的原始历史", 2000))
		case 2, 3:
			emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":2}`)})
		default:
			emitText(w, "多次摘要后正常完成")
		}
	}))
	defer upstream.Close()
	settings := func() ([]chat.ModelConfig, error) {
		configs, _ := compactSettings(upstream.URL, &expanded)()
		configs[0].MaxRounds = 3
		return configs, nil
	}
	service, ports, _ := largeContentFixture(t, upstream.URL, settings, nil)
	first := admissionInput("multiple-compacts", "old")
	receipt, err := service.CreateRun(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	awaitFinishedRun(t, service, receipt.RunID)
	<-requests
	expanded.Store(false)
	input := admissionInput(first.SessionID, "current")
	// 真实输入与旧摘要同值，必须按消息边界排除，不能按内容过滤。
	input.Input = "旧有效摘要"
	receipt, err = service.CreateRun(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	run := awaitFinishedRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "多次摘要后正常完成" || summaries.Load() != 3 || normal.Load() != 4 {
		t.Fatalf("run=%+v error=%+v summaries=%d normal=%d", run, run.Error, summaries.Load(), normal.Load())
	}
	for index := 0; index < 6; index++ {
		request := <-requests
		if len(request.Tools) != 0 {
			if chat.EstimateTokens([]byte(mustJSON(request))) > 5500 {
				t.Fatal("普通请求超预算仍被发送")
			}
			continue
		}
		if index == 0 {
			continue
		}
		var history []chat.Message
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &history); err != nil || len(history) != 3 || history[0].Content != "旧有效摘要" || history[1].Role != "assistant" || history[2].Role != "tool" || strings.Contains(mustJSON(history), "已覆盖的原始历史") {
			t.Fatalf("再次摘要丢失旧摘要、重复真实输入或补回原文：%s %v", mustJSON(history), err)
		}
	}
	count, err := ports.Execution.OrdinaryCallCount(context.Background(), receipt.RunID)
	if err != nil || count != 3 {
		t.Fatalf("摘要占普通轮次：count=%d err=%v", count, err)
	}
}

// TestRuntimeCompactResumesSameRequest 验证摘要及摘要续写退出恢复沿原请求、完整工具投影和共享续写预算继续。
func TestRuntimeCompactResumesSameRequest(t *testing.T) {
	for _, phase := range []string{"summary", "continuation", "failure"} {
		t.Run(phase, func(t *testing.T) {
			requests := make(chan chat.ModelRequest, 10)
			blocked := make(chan struct{})
			var ready sync.Once
			var restored atomic.Bool
			var normal, summary atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chat.ModelRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- request
				if len(request.Tools) != 0 {
					if normal.Add(1) == 1 {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"正在核对\"}}]}\n\n")
						emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":2}`)})
					} else {
						emitText(w, "恢复后的普通正文")
					}
					return
				}
				attempt := summary.Add(1)
				if restored.Load() {
					if phase == "failure" {
						w.WriteHeader(401)
					} else {
						emitText(w, "摘要后段")
					}
				} else if phase == "continuation" && attempt == 1 {
					emitLengthDelta(w, "content", "摘要前段", "length")
				} else {
					ready.Do(func() { close(blocked) })
					<-r.Context().Done()
				}
			}))
			defer upstream.Close()
			settings := compactSettings(upstream.URL, nil)
			service, ports, application := largeContentFixture(t, upstream.URL, settings, nil)
			receipt, err := service.CreateRun(context.Background(), admissionInput("compact-restart", "first"))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-blocked:
			case <-time.After(5 * time.Second):
				t.Fatal("未进入摘要恢复边界")
			}
			service.Stop()
			<-requests
			interrupted := <-requests
			if phase == "continuation" {
				interrupted = <-requests
			}
			restored.Store(true)
			service = chat.NewService(ports, completion.NewClient(nil), settings, queryRegistry(t, application))
			if err := service.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer service.Stop()
			run := awaitFinishedRun(t, service, receipt.RunID)
			if phase == "failure" {
				if run.Status != chat.StatusFailed || run.Output == nil || *run.Output != "正在核对" || run.Error == nil || run.Error.Code != "model_auth_failed" {
					t.Fatalf("摘要恢复失败丢失普通正文：run=%+v error=%+v", run, run.Error)
				}
			} else if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "恢复后的普通正文" {
				t.Fatalf("run=%+v error=%+v", run, run.Error)
			}
			resumed := <-requests
			if mustJSON(resumed) != mustJSON(interrupted) {
				t.Fatal("恢复改变了已保存摘要请求")
			}
			if phase == "failure" {
				return
			}
			ordinary := <-requests
			want := "摘要后段"
			if phase == "continuation" {
				want = "摘要前段摘要后段"
			}
			if ordinary.Messages[0].Content != want || ordinary.Messages[1].Content != admissionInput("compact-restart", "first").Input {
				t.Fatalf("恢复摘要投影错误：%s", mustJSON(ordinary))
			}
			compacts := 0
			for _, event := range eventPage(t, service, receipt.RunID, 0, 100).Events {
				if event.Type == chat.EventCompact {
					compacts++
				}
			}
			if compacts != 1 {
				t.Fatalf("恢复重复摘要：%d", compacts)
			}
		})
	}
}

func largeContentFixture(t *testing.T, base string, settings func() ([]chat.ModelConfig, error), wrap func(chat.Storage) chat.Storage) (*chat.Runtime, chat.Storage, *app.App) {
	t.Helper()
	service, ports, application := newToolFixture(t, base)
	service.Stop()
	description := strings.Repeat("实际查询事实🙂", 6000) + "完整尾部"
	if _, _, err := application.CreateSchedule(context.Background(), model.Schedule{
		Name:        "大型日程",
		Description: &description,
		Type:        model.SingleSchedule,
		Single:      &model.Single{NotifyTime: time.Now().Add(24 * time.Hour).Truncate(time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	if wrap != nil {
		ports = wrap(ports)
	}
	service = chat.NewService(ports, completion.NewClient(nil), settings, queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return service, ports, application
}

// TestToolContentUsesCurrentConfiguredLimit 验证后续新结果使用最新配置且既有历史投影不随之变化。
func TestToolContentUsesCurrentConfiguredLimit(t *testing.T) {
	requests := make(chan chat.ModelRequest, 8)
	var tokens atomic.Int32
	tokens.Store(64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if request.Messages[len(request.Messages)-1].Role == "user" {
			emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":2}`)})
		} else {
			emitText(w, "已核对")
		}
	}))
	defer upstream.Close()
	settings := func() ([]chat.ModelConfig, error) {
		configs, _ := toolSettings(upstream.URL)()
		configs[0].ToolResultMaxTokens = int(tokens.Load())
		return configs, nil
	}
	service, _, _ := largeContentFixture(t, upstream.URL, settings, nil)
	var previous string
	for _, limit := range []int{64, 256} {
		tokens.Store(int32(limit))
		input := admissionInput("config-limit", fmt.Sprint(limit))
		receipt, err := service.CreateRun(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if run := awaitFinishedRun(t, service, receipt.RunID); run.Status != chat.StatusSucceeded {
			t.Fatalf("run=%+v", run)
		}
		<-requests
		request := <-requests
		content := request.Messages[len(request.Messages)-1].Content
		if len(content) > limit*4 || len(content) < limit*4-8 || !utf8.ValidString(content) || !strings.Contains(content, "结果不完整") {
			t.Fatalf("配置没有用于新结果：limit=%d bytes=%d", limit, len(content))
		}
		if previous != "" && request.Messages[2].Content != previous {
			t.Fatal("新阈值改写了历史投影")
		}
		previous = content
	}
}

// TestRuntimeCompactFailureKeepsCurrentToolFacts 验证失败、取消、切换故障及摘要仍超预算时保留原始结果和有效历史。
func TestRuntimeCompactFailureKeepsCurrentToolFacts(t *testing.T) {
	for _, mode := range []string{"auth", "empty", "cancel", "oversize", "rollback", "lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			requests := make(chan chat.ModelRequest, 12)
			started := make(chan struct{}, 1)
			var normal, summaries atomic.Int32
			var expanded atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chat.ModelRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- request
				if len(request.Tools) != 0 {
					if normal.Add(1) == 1 {
						emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":2}`)})
					} else {
						emitText(w, "后续正常答复")
					}
					return
				}
				summaries.Add(1)
				switch mode {
				case "auth":
					w.WriteHeader(401)
				case "empty":
					emitText(w, "")
				case "cancel":
					started <- struct{}{}
					<-r.Context().Done()
				case "oversize":
					emitText(w, strings.Repeat("仍很大的有效摘要", 1500))
				default:
					emitText(w, "工具2实际查询完成，修改仍未执行")
				}
			}))
			defer upstream.Close()
			settings := compactSettings(upstream.URL, &expanded)
			service, _, _ := largeContentFixture(t, upstream.URL, settings, func(ports chat.Storage) chat.Storage {
				if mode == "rollback" || mode == "lost-ack" {
					ports.Execution = &compactSwitchFault{
						ExecutionRepository: ports.Execution,
						mode:                mode,
					}
				}
				return ports
			})
			input := admissionInput("runtime-failure", "first")
			input.Input = "本轮真实查询意图"
			receipt, err := service.CreateRun(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("未进入运行中摘要")
				}
				if _, err := service.CancelRun(context.Background(), receipt.RunID); err != nil {
					t.Fatal(err)
				}
			}
			run := awaitFinishedRun(t, service, receipt.RunID)
			code := map[string]string{"auth": "model_auth_failed", "empty": "model_invalid_format", "oversize": "context_budget_exceeded", "rollback": "persistence_failed", "lost-ack": "persistence_failed"}[mode]
			if mode == "cancel" && run.Status != chat.StatusCancelled || mode != "cancel" && (run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != code) || run.Output != nil || normal.Load() != 1 || summaries.Load() != 1 {
				t.Fatalf("run=%+v error=%+v normal=%d summaries=%d", run, run.Error, normal.Load(), summaries.Load())
			}
			<-requests
			summary := <-requests
			if strings.Contains(summary.Messages[1].Content, input.Input) || !strings.Contains(summary.Messages[1].Content, "tool_call_id") {
				t.Fatal("摘要没有独立压缩工具事实")
			}
			page := eventPage(t, service, run.RunID, 0, 100)
			found := false
			for _, event := range page.Events {
				if event.Type == chat.EventToolResult {
					var result chat.ToolResultData
					if err := json.Unmarshal(event.Data, &result); err != nil || len(result.Output) < 32768 || result.Error != nil || !json.Valid(result.Output) {
						t.Fatalf("原始结果丢失：%v", err)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("摘要失败丢失已完成工具事件")
			}
			expanded.Store(true)
			nextInput := admissionInput(input.SessionID, "next")
			nextReceipt, err := service.CreateRun(context.Background(), nextInput)
			if err != nil {
				t.Fatal(err)
			}
			if next := awaitFinishedRun(t, service, nextReceipt.RunID); next.Status != chat.StatusSucceeded {
				t.Fatalf("next=%+v", next)
			}
			next := <-requests
			activated := mode == "oversize" || mode == "lost-ack"
			if activated {
				if strings.Contains(mustJSON(next), "tool_call_id") || !strings.Contains(next.Messages[0].Content, "摘要") && mode == "oversize" {
					t.Fatal("成功摘要未保存或补回覆盖原文")
				}
			} else if len(next.Messages) != 5 || next.Messages[0].Content != input.Input || next.Messages[1].Role != "assistant" || next.Messages[2].Role != "tool" || !strings.Contains(next.Messages[2].Content, "结果不完整") || !strings.Contains(next.Messages[3].Content, "上一轮回复未完成") {
				t.Fatalf("失败摘要丢失当前事实或破坏原顺序：%s", mustJSON(next.Messages))
			}
		})
	}
}

// TestRuntimeCompactContinuesWithPairedFacts 验证工具超预算在同一运行摘要，保留系统与真实输入并覆盖后续历史。
func TestRuntimeCompactContinuesWithPairedFacts(t *testing.T) {
	requests := make(chan chat.ModelRequest, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if len(request.Tools) == 0 {
			emitText(w, "日程2已查询成功；尾部事实；日期仍待澄清。")
		} else if len(request.Messages) == 2 {
			emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":2}`), toolRequest("missing", "get_schedule", `{"id":9999}`)})
		} else {
			emitText(w, "已核对真实结果")
		}
	}))
	defer upstream.Close()
	service, application, ports := newWriteFixture(t, upstream.URL, nil)
	description := strings.Repeat("运行中工具事实🙂", 5000) + "尾部事实"
	_, _, err := application.CreateSchedule(context.Background(), model.Schedule{
		Name:   "小型日程",
		Type:   model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Now().Add(24 * time.Hour).Truncate(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = application.CreateSchedule(context.Background(), model.Schedule{
		Name:        "大型日程",
		Description: &description,
		Type:        model.SingleSchedule,
		Single:      &model.Single{NotifyTime: time.Now().Add(24 * time.Hour).Truncate(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.Stop()
	settings := func() ([]chat.ModelConfig, error) {
		configs, _ := compactSettings(upstream.URL, nil)()
		configs[0].MaxRounds = 2
		return configs, nil
	}
	registry, err := tools.NewRegistry(context.Background(), append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, nil)...)...)
	if err != nil {
		t.Fatal(err)
	}
	service = chat.NewService(ports, completion.NewClient(nil), settings, registry)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run := startToolRun(t, service, "runtime-compact")
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "已核对真实结果" {
		t.Fatalf("run=%+v error=%+v", run, run.Error)
	}
	first := <-requests
	summary := <-requests
	var history []chat.Message
	if err := json.Unmarshal([]byte(summary.Messages[1].Content), &history); err != nil || len(history) != 3 || history[0].Role != "assistant" || history[1].Role != "tool" || history[2].Role != "tool" {
		t.Fatalf("摘要没有完整工具配对：%s %v", mustJSON(history), err)
	}
	if strings.Contains(summary.Messages[1].Content, run.Input) || !strings.Contains(summary.Messages[1].Content, "schedule_not_found") || !strings.Contains(summary.Messages[1].Content, "结果不完整") || len(summary.Tools) != 0 {
		t.Fatal("摘要输入未保留工具事实或混入真实输入")
	}
	ordinary := <-requests
	if len(ordinary.Messages) != 3 || ordinary.Messages[0].Content != first.Messages[0].Content || ordinary.Messages[1].Content != "日程2已查询成功；尾部事实；日期仍待澄清。" || ordinary.Messages[2].Content != run.Input || chat.EstimateTokens([]byte(mustJSON(ordinary))) > 5500 {
		t.Fatalf("摘要后请求错误：%s", mustJSON(ordinary))
	}
	page := eventPage(t, service, run.RunID, 0, 100)
	compactCount := 0
	for _, event := range page.Events {
		if event.Type == chat.EventCompact {
			compactCount++
		}
	}
	if compactCount != 1 {
		t.Fatalf("compact=%d", compactCount)
	}
	input := admissionInput("runtime-compact", "next")
	receipt, err := service.CreateRun(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if next := awaitFinishedRun(t, service, receipt.RunID); next.Status != chat.StatusSucceeded {
		t.Fatalf("next=%+v", next)
	}
	next := <-requests
	if strings.Contains(mustJSON(next), "运行中工具事实") || strings.Contains(mustJSON(next), "tool_call_id") || !strings.Contains(mustJSON(next), "日期仍待澄清") {
		t.Fatal("后续 run 补回已覆盖原文或丢失有效摘要")
	}
}
