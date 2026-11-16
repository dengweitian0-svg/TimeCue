package loop_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
)

// TestCompactFinalRequestBudget 验证新摘要调用在 UTF-8 正文预算边界登记和发送同一份字节。
func TestCompactFinalRequestBudget(t *testing.T) {
	for _, phase := range []string{"create", "recover", "continue"} {
		t.Run(phase, func(t *testing.T) {
			for _, over := range []bool{false, true} {
				name := "exact"
				if over {
					name = "over"
				}
				t.Run(name, func(t *testing.T) {
					testCompactFinalRequestBudget(t, phase, over)
				})
			}
		})
	}
}

func testCompactFinalRequestBudget(t *testing.T, phase string, over bool) {
	t.Helper()
	ctx := context.Background()
	_, ports, path := admissionDatabase(t)
	history := strings.Repeat("中🙂", 100) + "引号\"、反斜杠\\和换行\nxx"
	if over {
		history += "x"
	}
	const partial = "尚未完成的摘要🙂"
	requests := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request chat.ModelRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			return
		}
		requests <- string(body)
		switch {
		case len(request.Messages) == 1:
			emitText(w, history)
		case request.Messages[0].Role != "system":
			emitText(w, "普通答复")
		case phase == "continue" && len(request.Messages) == 2:
			emitLengthDelta(w, "content", partial, "length")
		default:
			emitText(w, "有效摘要")
		}
	}))
	t.Cleanup(upstream.Close)

	var budget atomic.Int32
	budget.Store(2000)
	settings := func() ([]chat.ModelConfig, error) {
		configs, err := compactSettings(upstream.URL, nil)()
		configs[0].ContextWindow = configs[0].MaxOutputTokens + int(budget.Load())
		return configs, err
	}
	client := &extendedRequestClient{Client: completion.NewClient(nil)}
	service := startAdmissionService(t, ports, client, settings)
	initial := awaitFinishedRun(t, service, createCompactRun(t, service, "initial").RunID)
	if initial.Status != chat.StatusSucceeded || len(requests) != 1 {
		t.Fatalf("初始化历史失败：run=%+v requests=%d", initial, len(requests))
	}
	<-requests

	// 首个摘要正文恰好 1568 字节，续写正文恰好 1680 字节。
	limit := int32(392)
	if phase == "continue" {
		limit = 420
	}
	budget.Store(limit)
	input := admissionInput("compact", "budget")
	input.Input = strings.Repeat("请求", 140)
	var runID string
	if phase == "recover" {
		service.Stop()
		runID = "pending-summary-budget"
		if err := ports.Runs.Create(ctx, chat.Record{
			Run: chat.Run{
				RunID:     runID,
				SessionID: input.SessionID,
				Provider:  input.Provider,
				Model:     input.Model,
				Input:     input.Input,
				CreatedAt: time.Now(),
			},
			State:          chat.StateRunning,
			IdempotencyKey: input.IdempotencyKey,
		}); err != nil {
			t.Fatal(err)
		}
		service = startAdmissionService(t, ports, client, settings)
	} else {
		receipt, err := service.CreateRun(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		runID = receipt.RunID
	}
	run := awaitFinishedRun(t, service, runID)
	service.Stop()

	inspection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	rows, err := inspection.QueryContext(ctx, "SELECT input FROM ai_model_calls WHERE run_id=? ORDER BY id", runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sizes []int
	for rows.Next() {
		var saved string
		if err := rows.Scan(&saved); err != nil {
			t.Fatal(err)
		}
		select {
		case sent := <-requests:
			if saved != sent {
				t.Fatal("登记与发送正文不一致")
			}
		default:
			t.Fatal("已登记的调用未发送")
		}
		sizes = append(sizes, len(saved))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantCalls := 2
	if phase == "continue" {
		wantCalls++
	}
	if over {
		wantCalls -= 2
	}
	if len(sizes) != wantCalls || len(requests) != 0 {
		t.Fatalf("预算 %d 时，登记正文长度=%v，额外上游请求=%d，期望调用数=%d", limit, sizes, len(requests), wantCalls)
	}
	if over {
		if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "context_budget_exceeded" || run.Output != nil {
			t.Fatalf("超预算摘要未按预期收尾：run=%+v error=%+v", run, run.Error)
		}
		effective, err := ports.Execution.History(ctx, input.SessionID)
		if err != nil || len(effective) < 2 || effective[1].Content != history || strings.Contains(mustJSON(effective), partial) {
			t.Fatalf("原有效历史未保留：%s %v", mustJSON(effective), err)
		}
		return
	}
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "普通答复" {
		t.Fatalf("预算内摘要未生效：run=%+v error=%+v", run, run.Error)
	}
	if sizes[wantCalls-2] != int(limit)*4 {
		t.Fatalf("摘要正文未达到字节边界：%v，预算=%d", sizes, limit)
	}
	if len(client.prepared[1]) <= int(limit)*4 {
		t.Fatal("夹具中的首次普通请求应超预算")
	}
}
