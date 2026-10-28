package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestAssistantCreatesRealSchedule 验证模型工具创建正式日程、首条提醒并回填真实结果。
func TestAssistantCreatesRealSchedule(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			var request chat.ModelRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if len(request.Messages) < 2 || request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, "先澄清") || !strings.Contains(request.Messages[0].Content, "不自动重试") {
				t.Errorf("缺少真实操作约束提示：%s", mustJSON(request.Messages))
			}
			emitTools(w, []chat.ToolRequest{toolRequest("create", "create_schedule", `{"name":"助手会议","type":"single","notifyTime":"2099-01-02 09:00"}`)})
			return
		}
		var input chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(input.Messages[len(input.Messages)-1].Content), &value); err != nil || value["name"] != "助手会议" || value["revision"] != float64(1) {
			t.Errorf("真实创建结果：%v %v", value, err)
		}
		emitText(w, "创建成功")
	}))
	defer upstream.Close()
	service, application, _ := newWriteFixture(t, upstream.URL, nil)
	run := startToolRun(t, service, "write-create")
	if run.Status != chat.StatusSucceeded {
		t.Fatalf("运行：%+v", run)
	}
	values, err := application.ListSchedules(context.Background())
	if err != nil || len(values) != 1 || values[0].Name != "助手会议" || values[0].Revision != 1 {
		t.Fatalf("持久化日程：%+v %v", values, err)
	}
	page, err := application.QueryOccurrences(context.Background(), "2099-01-02", "", time.Now())
	if err != nil || len(page.Items) != 1 || page.Items[0].Source != "persisted" || page.Items[0].NotificationID == nil {
		t.Fatalf("正式首条提醒：%+v %v", page, err)
	}
}

// TestAssistantUpdatesRuleAndReminder 验证依赖创建结果的修改分轮执行，保留未改字段并替换正式提醒。
func TestAssistantUpdatesRuleAndReminder(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch count.Add(1) {
		case 1:
			emitTools(w, []chat.ToolRequest{toolRequest("create", "create_schedule", `{"name":"原会议","type":"single","description":"保留说明","notifyTime":"2099-01-02 09:00"}`)})
		case 2:
			results := requestToolResults(t, r)
			var created struct {
				ID       int `json:"id"`
				Revision int `json:"revision"`
			}
			if err := json.Unmarshal(results["create"], &created); err != nil || created.ID < 1 || created.Revision != 1 {
				t.Errorf("创建回执：%+v %v", created, err)
			}
			emitTools(w, []chat.ToolRequest{toolRequest("update", "update_schedule", fmt.Sprintf(`{"id":%d,"expectedRevision":%d,"name":"新会议","notifyTime":"2099-01-03T10:30:00+08:00"}`, created.ID, created.Revision))})
		default:
			results := requestToolResults(t, r)
			var changed struct {
				Name        string `json:"name"`
				Revision    int    `json:"revision"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(results["update"], &changed); err != nil || changed.Name != "新会议" || changed.Revision != 2 || changed.Description != "保留说明" {
				t.Errorf("真实修改结果：%+v %v", changed, err)
			}
			emitText(w, "已按真实结果修改")
		}
	}))
	defer upstream.Close()
	service, application, _ := newWriteFixture(t, upstream.URL, nil)
	run := startToolRun(t, service, "write-update")
	if run.Status != chat.StatusSucceeded || count.Load() != 3 {
		t.Fatalf("依赖操作：%+v 请求=%d", run, count.Load())
	}
	values, err := application.ListSchedules(context.Background())
	if err != nil || len(values) != 1 || values[0].Revision != 2 || values[0].Name != "新会议" {
		t.Fatalf("正式规则：%+v %v", values, err)
	}
	old, err := application.QueryOccurrences(context.Background(), "2099-01-02", "", time.Now())
	if err != nil || len(old.Items) != 0 {
		t.Fatalf("旧提醒仍可见：%+v %v", old, err)
	}
	next, err := application.QueryOccurrences(context.Background(), "2099-01-03", "", time.Now())
	if err != nil || len(next.Items) != 1 || next.Items[0].Name != "新会议" || next.Items[0].NotifyTime.Format("15:04") != "10:30" || next.Items[0].Source != "persisted" {
		t.Fatalf("新提醒：%+v %v", next, err)
	}
}

// TestAssistantBatchKeepsIndependentResults 验证无键创建不猜测去重，冲突和错误不重试、不回滚其他业务。
func TestAssistantBatchKeepsIndependentResults(t *testing.T) {
	requests := []chat.ToolRequest{
		toolRequest("create-a", "create_schedule", `{"name":"同名安排","type":"single","notifyTime":"2099-02-01 09:00"}`),
		toolRequest("create-b", "create_schedule", `{"name":"同名安排","type":"single","notifyTime":"2099-02-01 09:00"}`),
		toolRequest("missing", "create_schedule", `{"name":"缺时刻","type":"single","notifyTime":"明天"}`),
		toolRequest("missing-time", "create_schedule", `{"name":"缺时间","type":"single"}`),
		toolRequest("seconds", "create_schedule", `{"name":"秒级","type":"single","notifyTime":"2099-02-01T09:00:01+08:00"}`),
		toolRequest("invalid-cron", "create_schedule", `{"name":"非法规则","type":"period","cron":["bad"],"open":true}`),
		toolRequest("bad-array", "create_schedule", `{"name":"错误数组","type":"period","cron":[1],"open":true}`),
		toolRequest("bad-bool", "create_schedule", `{"name":"错误开关","type":"period","cron":["0 9 * * *"],"open":"true"}`),
		toolRequest("no-revision", "update_schedule", `{"id":1,"name":"无版本"}`),
		toolRequest("valid-update", "update_schedule", `{"id":1,"expectedRevision":1,"name":"已更新"}`),
		toolRequest("stale", "update_schedule", `{"id":1,"expectedRevision":1,"name":"不应覆盖"}`),
		toolRequest("not-found", "update_schedule", `{"id":9999,"expectedRevision":1,"name":"不存在"}`),
	}
	expected := map[string]string{
		"missing": "tool_argument_validation_error", "missing-time": "tool_argument_validation_error",
		"seconds": "invalid_schedule", "invalid-cron": "invalid_schedule",
		"bad-array": "tool_argument_validation_error", "bad-bool": "tool_argument_validation_error",
		"no-revision": "tool_argument_validation_error", "stale": "revision_conflict", "not-found": "schedule_not_found",
	}
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			emitTools(w, requests)
			return
		}
		results := requestToolResults(t, r)
		for id, code := range expected {
			var failure chat.RunError
			if err := json.Unmarshal(results[id], &failure); err != nil || failure.Code != code {
				t.Errorf("%s 错误：%s %v，预期 %s", id, results[id], err, code)
			}
		}
		emitText(w, "分别报告成功与失败")
	}))
	defer upstream.Close()
	service, application, _ := newWriteFixture(t, upstream.URL, nil)
	if _, _, err := application.CreateSchedule(context.Background(), model.Schedule{
		Name:   "已有规则",
		Type:   model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Date(2099, 1, 1, 9, 0, 0, 0, model.BeijingLocation())},
	}); err != nil {
		t.Fatal(err)
	}
	run := startToolRun(t, service, "write-batch")
	if run.Status != chat.StatusSucceeded || count.Load() != 2 {
		t.Fatalf("批量运行：%+v 请求=%d", run, count.Load())
	}
	values, err := application.ListSchedules(context.Background())
	if err != nil || len(values) != 3 {
		t.Fatalf("同名同时刻应独立创建：%+v %v", values, err)
	}
	current, err := application.GetSchedule(context.Background(), 1)
	if err != nil || current.Name != "已更新" || current.Revision != 2 {
		t.Fatalf("冲突未保护现有规则：%+v %v", current, err)
	}
	// 通过公开事件核验未自动重试。
	page, err := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	starts, results := 0, 0
	for _, event := range page.Events {
		if event.Type == chat.EventToolStart {
			starts++
		}
		if event.Type == chat.EventToolResult {
			results++
		}
		if event.Type == chat.EventError {
			t.Fatalf("工具失败不能失败运行：%s", event.Data)
		}
	}
	if starts != len(requests) || results != len(requests) {
		t.Fatalf("工具事实重复或缺失：开始=%d 结果=%d", starts, results)
	}
}

// TestAssistantRelativeTimeUsesEachParseAcrossMidnight 验证同一运行中的两次解析使用各自北京时间，绝对时间不再解释。
func TestAssistantRelativeTimeUsesEachParseAcrossMidnight(t *testing.T) {
	var parses, requests atomic.Int32
	clock := func() time.Time {
		if parses.Add(1) == 1 {
			return time.Date(2099, 12, 31, 15, 59, 0, 0, time.UTC)
		}
		return time.Date(2099, 12, 31, 16, 1, 0, 0, time.UTC)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{
				toolRequest("before", "create_schedule", `{"name":"午夜前解析","type":"single","notifyTime":"明天 09:00"}`),
				toolRequest("after", "create_schedule", `{"name":"午夜后解析","type":"single","notifyTime":"明天 09:00"}`),
				toolRequest("absolute", "create_schedule", `{"name":"明确时间","type":"single","notifyTime":"2100-01-01T09:00:00+08:00"}`),
			})
			return
		}
		emitText(w, "已使用各次解析的北京时间")
	}))
	defer upstream.Close()
	service, application, _ := newWriteFixture(t, upstream.URL, clock)
	input := "  明天九点提醒我\n不要改写原文  "
	receipt, err := service.CreateRun(context.Background(), chat.CreateRunInput{
		SessionID:      "midnight",
		Input:          input,
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "midnight",
	})
	if err != nil {
		t.Fatal(err)
	}
	run := awaitRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || run.Input != input || parses.Load() != 2 {
		t.Fatalf("原文或解析次数：%+v 解析=%d", run, parses.Load())
	}
	values, err := application.ListSchedules(context.Background())
	if err != nil || len(values) != 3 {
		t.Fatalf("时间结果：%+v %v", values, err)
	}
	expected := map[string]string{"午夜前解析": "2100-01-01 09:00", "午夜后解析": "2100-01-02 09:00", "明确时间": "2100-01-01 09:00"}
	for _, value := range values {
		if value.Single.NotifyTime.In(model.BeijingLocation()).Format("2006-01-02 15:04") != expected[value.Name] {
			t.Errorf("%s 解析错误：%s", value.Name, value.Single.NotifyTime)
		}
	}
}

// TestAssistantPeriodUsesFormalReminderRules 验证周期创建和暂停复用正式提醒联动，未提交字段保持原规则。
func TestAssistantPeriodUsesFormalReminderRules(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch count.Add(1) {
		case 1:
			emitTools(w, []chat.ToolRequest{toolRequest("period", "create_schedule", `{"name":"每日安排","type":"period","cron":["0 9 * * *"],"open":true}`)})
		case 2:
			results := requestToolResults(t, r)
			var result struct {
				ID       int `json:"id"`
				Revision int `json:"revision"`
				Mutation struct {
					NotificationID *int `json:"notification_id"`
				} `json:"mutation"`
			}
			if err := json.Unmarshal(results["period"], &result); err != nil || result.ID < 1 || result.Mutation.NotificationID == nil {
				t.Errorf("周期首提醒：%s %v", results["period"], err)
			}
			emitTools(w, []chat.ToolRequest{toolRequest("pause", "update_schedule", fmt.Sprintf(`{"id":%d,"expectedRevision":%d,"open":false}`, result.ID, result.Revision))})
		default:
			emitText(w, "周期已暂停")
		}
	}))
	defer upstream.Close()
	service, application, _ := newWriteFixture(t, upstream.URL, nil)
	run := startToolRun(t, service, "period")
	if run.Status != chat.StatusSucceeded {
		t.Fatalf("周期运行：%+v", run)
	}
	value, err := application.GetSchedule(context.Background(), 1)
	if err != nil || value.Period == nil || value.Period.Open || value.Revision != 2 || len(value.Period.Cron) != 1 || value.Period.Cron[0] != "0 9 * * *" {
		t.Fatalf("周期规则：%+v %v", value, err)
	}
	page, err := application.QueryOccurrences(context.Background(), time.Now().In(model.BeijingLocation()).AddDate(0, 0, 1).Format("2006-01-02"), "", time.Now())
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("暂停后提醒仍可见：%+v %v", page, err)
	}
}

func requestToolResults(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	var input chat.ModelRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		t.Error(err)
		return nil
	}
	results := map[string]json.RawMessage{}
	for _, message := range input.Messages {
		if message.Role == "tool" {
			results[message.ToolCallID] = json.RawMessage(message.Content)
		}
	}
	return results
}

// TestAssistantWriteKeysRemainSQLNull 验证版本 3 的写入工具身份不生成幂等键，正式业务也不创建回执。
func TestAssistantWriteKeysRemainSQLNull(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{
				toolRequest("create", "create_schedule", `{"name":"无键","type":"single","notifyTime":"2099-01-02 09:00"}`),
				toolRequest("update", "update_schedule", `{"id":1,"expectedRevision":1,"name":"无键更新"}`),
			})
			return
		}
		emitText(w, "完成")
	}))
	defer upstream.Close()
	db, storage, path := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	if _, _, err := application.CreateSchedule(context.Background(), model.Schedule{
		Name:   "已有规则",
		Type:   model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Date(2099, 1, 1, 9, 0, 0, 0, model.BeijingLocation())},
	}); err != nil {
		t.Fatal(err)
	}
	definitions := append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, nil)...)
	registry, err := tools.NewRegistry(context.Background(), definitions...)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"create_schedule", "update_schedule"} {
		info, ok := registry.Lookup(name)
		if !ok || info.Dangerous || info.Parallel {
			t.Fatalf("写入元数据：%+v", info)
		}
	}
	service := chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), registry)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	run := startToolRun(t, service, "null-keys")
	if run.Status != chat.StatusSucceeded {
		t.Fatalf("运行：%+v", run)
	}
	connection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	var total, keys int
	if err := connection.QueryRow(`SELECT count(*),count(idempotency_key) FROM ai_tool_calls`).Scan(&total, &keys); err != nil || total != 2 || keys != 0 {
		t.Fatalf("工具 SQL NULL：%d/%d %v", keys, total, err)
	}
	if err := connection.QueryRow(`SELECT count(*) FROM schedule_write_receipts`).Scan(&total); err != nil || total != 0 {
		t.Fatalf("业务空 key：%d %v", total, err)
	}
}

// TestAssistantRestoresSavedTimeWithoutReexecution 验证已提交的相对时间和创建结果在午夜后恢复时直接复用。
func TestAssistantRestoresSavedTimeWithoutReexecution(t *testing.T) {
	ctx := context.Background()
	database, ports, path := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(database)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	var parses, requests atomic.Int32
	clock := func() time.Time {
		parses.Add(1)
		return time.Date(2099, 12, 31, 23, 59, 0, 0, model.BeijingLocation())
	}
	definitions := append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, clock)...)
	registry, err := tools.NewRegistry(ctx, definitions...)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("saved", "create_schedule", `{"name":"保存的时间","type":"single","notifyTime":"明天 09:00"}`)})
			return
		}
		results := requestToolResults(t, r)
		if !strings.Contains(string(results["saved"]), "2100-01-01T09:00:00+08:00") {
			t.Errorf("保存的真实结果改变：%s", results["saved"])
		}
		emitText(w, "复用已保存结果")
	}))
	t.Cleanup(upstream.Close)
	gate := &toolCommitGate{
		ToolRepository: ports.Tools,
		afterResults:   1,
		committed:      make(chan struct{}),
		release:        make(chan struct{}),
	}
	gated := ports
	gated.Tools = gate
	service := chat.NewService(gated, completion.NewClient(nil), toolSettings(upstream.URL), registry)
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	t.Cleanup(func() { gate.once.Do(func() { close(gate.release) }) })
	receipt, err := service.CreateRun(ctx, admissionInput("saved-time", "saved-time"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("未到达真实结果提交边界")
	}
	backup := filepath.Join(t.TempDir(), "write-crash.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(backup, "'", "''")+`'`)
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	gate.once.Do(func() { close(gate.release) })
	if run := awaitRun(t, service, receipt.RunID); run.Status != chat.StatusSucceeded {
		t.Fatalf("原运行：%+v", run)
	}
	service.Stop()
	restoredDB := store.NewSqlite()
	if err := restoredDB.Connect(ctx, backup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restoredDB.Close() })
	restoredSchedules, err := store.NewScheduleStore(restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	restoredApp := &app.App{Schedule: restoredSchedules}
	definitions = append(tools.ScheduleTools(restoredApp), tools.ScheduleWriteTools(restoredApp, func() time.Time {
		parses.Add(1)
		return time.Date(2100, 1, 1, 0, 1, 0, 0, model.BeijingLocation())
	})...)
	registry, err = tools.NewRegistry(ctx, definitions...)
	if err != nil {
		t.Fatal(err)
	}
	restarted := chat.NewService(store.NewChatStorage(restoredDB), completion.NewClient(nil), toolSettings(upstream.URL), registry)
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Stop)
	if run := awaitRun(t, restarted, receipt.RunID); run.Status != chat.StatusSucceeded || parses.Load() != 1 || requests.Load() != 3 {
		t.Fatalf("恢复重复解析或执行：%+v 解析=%d 请求=%d", run, parses.Load(), requests.Load())
	}
	values, err := restoredApp.ListSchedules(ctx)
	if err != nil || len(values) != 1 || values[0].Single.NotifyTime.Format("2006-01-02 15:04") != "2100-01-01 09:00" {
		t.Fatalf("恢复改写日程：%+v %v", values, err)
	}
}

func newWriteFixture(t *testing.T, base string, now func() time.Time) (*chat.Runtime, *app.App, chat.Storage) {
	t.Helper()
	db, storage, _ := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	definitions := append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, now)...)
	registry, err := tools.NewRegistry(context.Background(), definitions...)
	if err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(storage, completion.NewClient(nil), toolSettings(base), registry)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return service, application, storage
}
