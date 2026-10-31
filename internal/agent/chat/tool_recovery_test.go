package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// writeCrashGate 仅暂停真实持久化边界，业务和进度仍由正式实现提交。
type writeCrashGate struct {
	chat.ToolRepository
	stage   string
	ready   chan struct{}
	release chan struct{}
	once    sync.Once
	begins  int
}

func (g *writeCrashGate) pause(stage string) {
	if g.stage == stage {
		close(g.ready)
		<-g.release
	}
}

// AcceptResponse 在真实模型回复接纳后暂停。
func (g *writeCrashGate) AcceptResponse(ctx context.Context, call chat.ModelCall) error {
	err := g.ToolRepository.AcceptResponse(ctx, call)
	if err == nil {
		g.pause("accepted")
	}
	return err
}

// BeginTool 在登记事务前后暂停，包含已登记但尚未执行业务的窗口。
func (g *writeCrashGate) BeginTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, chat.ToolCall, error) {
	g.pause("before_begin")
	progress, saved, err := g.ToolRepository.BeginTool(ctx, call, tool)
	if err == nil {
		g.begins++
		g.pause("after_begin")
		if g.begins == 2 {
			g.pause("retry_started")
		}
	}
	return progress, saved, err
}

// FinishTool 在业务已提交后、结果事务前后暂停。
func (g *writeCrashGate) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	g.pause("before_result")
	progress, err := g.ToolRepository.FinishTool(ctx, call, tool)
	if err == nil {
		g.pause("after_result")
	}
	return progress, err
}

// WaitToolRetry 暂停已提交的真实退避，不改变期限或次数。
func (g *writeCrashGate) WaitToolRetry(ctx context.Context, tool chat.ToolCall) error {
	err := g.ToolRepository.WaitToolRetry(ctx, tool)
	if err == nil {
		g.pause("retry_wait")
	}
	return err
}

type committedWriteFailure struct {
	tools.Tool
	attempts atomic.Int32
}

// InvokableRun 只在首次真实业务提交后的响应边界注入暂时故障。
func (f *committedWriteFailure) InvokableRun(ctx context.Context, raw string) (string, error) {
	output, err := f.Tool.InvokableRun(ctx, raw)
	if err == nil && f.attempts.Add(1) == 1 {
		return "", &tools.Failure{
			Code:    "tool_temporary_error",
			Message: "业务提交后的响应故障",
		}
	}
	return output, err
}

// TestScheduleToolRecoveryAtSQLiteBoundaries 验证真实写入在七个中断位置的有限恢复和取消只读收尾。
func TestScheduleToolRecoveryAtSQLiteBoundaries(t *testing.T) {
	operations := []chat.ToolRequest{
		toolRequest("original", "create_schedule", ` {"name":"恢复创建","type":"single","notifyTime":"2099-01-02 09:00"} `),
		toolRequest("original", "update_schedule", ` {"id":1,"expectedRevision":1,"name":"恢复修改"} `),
		toolRequest("original", "delete_schedule", ` {"id":1,"expectedRevision":1} `),
	}
	for _, request := range operations {
		for _, stage := range []string{"accepted", "before_begin", "after_begin", "before_result", "after_result", "retry_wait", "retry_started"} {
			for _, budget := range []int{0, 1} {
				if budget == 0 && (stage == "retry_wait" || stage == "retry_started") {
					continue
				}
				for _, cancelling := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/retry_%d/cancel_%t", request.Function.Name, stage, budget, cancelling), func(t *testing.T) {
						testScheduleToolCrash(t, request, stage, budget, cancelling)
					})
				}
			}
		}
	}
}

func testScheduleToolCrash(t *testing.T, request chat.ToolRequest, stage string, budget int, cancelling bool) {
	t.Helper()
	ctx := context.Background()
	_, ports, application, path := deleteFixture(t)
	var models atomic.Int32
	inputs := make(chan chat.ModelRequest, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if models.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{request})
			return
		}
		var input chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		inputs <- input
		emitText(w, "按真实结果完成")
	}))
	t.Cleanup(upstream.Close)
	settings := func() ([]chat.ModelConfig, error) {
		configs, err := toolSettings(upstream.URL)()
		configs[0].ToolMaxRetry = &budget
		return configs, err
	}
	gate := &writeCrashGate{
		ToolRepository: ports.Tools,
		stage:          stage,
		ready:          make(chan struct{}),
		release:        make(chan struct{}),
	}
	gated := ports
	gated.Tools = gate
	registry := deleteRegistry(t, application)
	if stage == "retry_wait" || stage == "retry_started" {
		definitions := append(tools.ScheduleWriteTools(application, nil), tools.ScheduleDeleteTools(application)...)
		for _, definition := range definitions {
			info, err := definition.Info(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if info.Name == request.Function.Name {
				registry, err = tools.NewRegistry(ctx, &committedWriteFailure{Tool: definition})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	service := chat.NewService(gated, completion.NewClient(nil), settings, registry)
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	resume := func() { gate.once.Do(func() { close(gate.release) }) }
	t.Cleanup(resume)
	receipt, err := service.CreateRun(ctx, admissionInput("write-crash", "original-key"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("未到达真实中断窗口")
	}
	before, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || before == nil {
		t.Fatalf("缺少已接纳调用: %+v %v", before, err)
	}
	original, err := ports.Tools.ReadTools(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	backup := snapshotToolDatabase(t, path)
	originalStatus, originalModels := chat.StatusSucceeded, int32(2)
	if stage == "retry_wait" {
		// 快照已固定；取消原实例避免它等待退避，恢复实例仍须遵守快照中的原期限。
		if _, err := service.CancelRun(ctx, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		originalStatus, originalModels = chat.StatusCancelled, 1
	}
	resume()
	if run := awaitFinishedRun(t, service, receipt.RunID); run.Status != originalStatus {
		t.Fatalf("原实例未完成: %+v", run)
	}
	service.Stop()
	if originalModels == 2 {
		<-inputs
	}

	restoredDB := store.NewSqlite()
	if err := restoredDB.Connect(ctx, backup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restoredDB.Close() })
	restored := store.NewChatStorage(restoredDB)
	schedules, err := store.NewScheduleStore(restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	restoredApp := &app.App{Schedule: schedules}
	if cancelling {
		// 在崩溃快照保存取消意图，模拟提交成功而本地停止信号尚未送达。
		if state, err := restored.Runs.RequestCancel(ctx, receipt.RunID); err != nil || state != chat.StateWaitForCancel {
			t.Fatalf("取消意图未提交: %s %v", state, err)
		}
	}
	// 配置变更只能影响尚未登记的调用，既有调用仍受原始 0/1 预算约束。
	recoverySettings := func() ([]chat.ModelConfig, error) {
		configs, err := settings()
		larger := 9
		configs[0].ToolMaxRetry = &larger
		if cancelling {
			return nil, nil
		}
		return configs, err
	}
	restarted := chat.NewService(restored, completion.NewClient(nil), recoverySettings, deleteRegistry(t, restoredApp))
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Stop)
	run := awaitFinishedRunWithin(t, restarted, receipt.RunID, 30*time.Second)
	wantStatus := chat.StatusSucceeded
	if cancelling {
		wantStatus = chat.StatusCancelled
	}
	if run.Status != wantStatus || run.Error != nil {
		t.Fatalf("恢复运行失败: %+v", run)
	}
	stored, err := restored.Tools.ReadTools(ctx, before.ID)
	if err != nil || len(stored) != 1 || stored[0].Request != request {
		t.Fatalf("恢复创建了额外记录或改写原参数: %+v %v", stored, err)
	}
	tool := stored[0]
	registered := stage != "accepted" && stage != "before_begin"
	committed := stage == "before_result" || stage == "after_result" || stage == "retry_wait" || stage == "retry_started"
	saved := stage == "after_result"
	used := 0
	if stage == "retry_started" {
		used = 1
	}
	canReplay := !cancelling && !saved && (!registered || budget > used)
	wantRetry := used
	if registered && canReplay && !(committed && request.Function.Name == "delete_schedule") {
		wantRetry++
	}
	if tool.RetryCount != wantRetry || tool.NextRetryAt != nil {
		t.Fatalf("恢复次数或最终期限错误: %+v", tool)
	}
	if registered {
		if len(original) != 1 || tool.ID != original[0].ID || tool.MaxRetry != budget || tool.IdempotencyKey != original[0].IdempotencyKey {
			t.Fatalf("原身份或固定预算丢失: %+v -> %+v", original, tool)
		}
		if saved && !reflect.DeepEqual(tool, original[0]) {
			t.Fatalf("已保存结果被改写: %+v -> %+v", original[0], tool)
		}
	}
	if request.Function.Name == "delete_schedule" && (!cancelling || registered) {
		assertDeleteKey(t, tool)
	} else if tool.IdempotencyKey != "" {
		t.Fatalf("无键工具或未启动取消生成了执行键: %+v", tool)
	}
	wantError := ""
	switch {
	case saved || committed && request.Function.Name == "delete_schedule":
	case cancelling && (!registered || stage == "retry_wait"):
		wantError = "tool_skipped"
	case cancelling || registered && budget <= used:
		wantError = "tool_result_unconfirmed"
	case committed && request.Function.Name == "update_schedule":
		wantError = "revision_conflict"
	}
	if wantError == "" {
		if tool.State != "succeeded" || tool.Error != nil {
			t.Fatalf("实际结果未复用或执行: %+v", tool)
		}
	} else if tool.Error == nil || tool.Error.Code != wantError {
		t.Fatalf("未如实反馈 %s: %+v", wantError, tool)
	}
	values, err := restoredApp.ListSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantCount := 2
	switch request.Function.Name {
	case "create_schedule":
		if committed {
			wantCount++
		}
		if canReplay {
			wantCount++
		}
	case "update_schedule":
		current, err := restoredApp.GetSchedule(ctx, 1)
		wantRevision := 1
		if committed || canReplay {
			wantRevision = 2
		}
		if err != nil || current.Revision != wantRevision {
			t.Fatalf("真实版本被重复修改或取消后补写: %+v %v", current, err)
		}
	case "delete_schedule":
		if committed || canReplay {
			wantCount--
		}
		if registered {
			business, err := restoredApp.ReadScheduleDeleteReceipt(ctx, 1, 1, tool.IdempotencyKey)
			if err != nil || (business != nil) != (committed || canReplay) || business != nil && business.Schedule.Revision != 2 {
				t.Fatalf("真实删除回执错误: %+v %v", business, err)
			}
		}
	}
	if stage == "retry_wait" && canReplay && request.Function.Name != "delete_schedule" {
		if len(original) != 1 || original[0].NextRetryAt == nil || time.Now().Before(*original[0].NextRetryAt) {
			t.Fatal("恢复跳过原退避期限")
		}
	}
	if len(values) != wantCount {
		t.Fatalf("真实日程数量错误，取消补做或无键创建被猜测去重: %d != %d", len(values), wantCount)
	}
	assertAttemptEvents(t, restarted, receipt.RunID, 1)
	page, err := restarted.GetRunEvents(ctx, receipt.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	ends := 0
	for _, event := range page.Events {
		if event.Type == chat.EventEnd {
			ends++
		}
	}
	if ends != 1 {
		t.Fatalf("终态不唯一: %s", mustJSON(page))
	}
	if cancelling {
		if models.Load() != originalModels {
			t.Fatal("取消恢复重新调用了模型")
		}
		if _, err := restarted.CancelRun(ctx, receipt.RunID); err != nil {
			t.Fatal(err)
		}
	} else {
		if models.Load() != originalModels+1 {
			t.Fatal("恢复重发了已接纳的模型回复")
		}
		followup := <-inputs
		results := cloneToolResultMessages(followup.Messages)
		if len(results) != 1 || results[0].ToolCallID != request.ID || len(followup.Messages) < len(before.CheckpointContext) || !reflect.DeepEqual(followup.Messages[:len(before.CheckpointContext)], before.CheckpointContext) {
			t.Fatalf("原上下文或工具结果重复追加: %s", mustJSON(followup))
		}
	}
	// 终态再次启动不得产生事件或重新推进业务。
	restarted.Stop()
	final := chat.NewService(restored, completion.NewClient(nil), settings, deleteRegistry(t, restoredApp))
	if err := final.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(final.Stop)
	again, err := final.GetRunEvents(ctx, receipt.RunID, 0, 100)
	if err != nil || !reflect.DeepEqual(again, page) {
		t.Fatalf("重复恢复改变原历史: %+v %v", again, err)
	}
	// 公共入口能够在同一会话再次创建，证明取消收尾未遗留占用。
	next, err := final.CreateRun(ctx, admissionInput("write-crash", "next-key"))
	if err != nil {
		t.Fatal(err)
	}
	if nextRun := awaitFinishedRun(t, final, next.RunID); nextRun.Status != chat.StatusSucceeded {
		t.Fatalf("终态未释放同会话占用: %+v", nextRun)
	}
}

func snapshotToolDatabase(t *testing.T, path string) string {
	t.Helper()
	backup := filepath.Join(t.TempDir(), "tool-crash.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(context.Background(), `VACUUM INTO '`+strings.ReplaceAll(backup, "'", "''")+`'`); err != nil {
		t.Fatal(err)
	}
	return backup
}
