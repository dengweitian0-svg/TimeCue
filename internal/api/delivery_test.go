package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestDeliveryHTTPBoundary 验证严格输入、失效身份、空队列及日志事件。
func TestDeliveryHTTPBoundary(t *testing.T) {
	router, db := testRouter(t)
	for _, body := range []string{`null`, `[]`, `{"limit":null}`, `{"limit":0}`, `{"limit":21}`, `{"limit":1.5}`, `{"notificationIds":[1]}`, `{} {}`} {
		requireStatus(t, request(router, http.MethodPost, "/api/v1/notifications/claim", body, "application/json"), 400)
	}
	for _, body := range []string{`{}`, `{"limit":1}`, `{"limit":20}`} {
		response := request(router, http.MethodPost, "/api/v1/notifications/claim", body, "application/json")
		requireStatus(t, response, 200)
		if strings.TrimSpace(response.Body.String()) != `{"data":[]}` {
			t.Fatal("空任务必须返回数组", response.Body.String())
		}
	}
	for _, path := range []string{"start", "results"} {
		body := `{}`
		if path == "results" {
			body = `{"outcome":"sent"}`
		}
		requireStatus(t, request(router, http.MethodPost, "/api/v1/notifications/delivery-attempts/old-process/"+path, body, "application/json"), 404)
		requireStatus(t, request(router, http.MethodPost, "/api/v1/notifications/delivery-attempts/old-process/"+path, `{"memberIds":[1]}`, "application/json"), 400)
	}
	requireStatus(t, request(router, http.MethodPost, "/api/v1/notifications/claim", `{}`, "text/plain"), 415)
	requireStatus(t, request(router, http.MethodPost, "/api/v1/notifications/claim", strings.Repeat(" ", 1<<20)+`{}`, "application/json"), 413)
	manager, _ := config.NewManager()
	schedules, _ := store.NewScheduleStore(db)
	notify, _ := store.NewNotificationStore(db)
	app, err := service.NewApp(manager, schedules, notify)
	if err != nil {
		t.Fatal(err)
	}
	app.StopDelivery()
	stopping := NewRouter(app, db.CheckHealth)
	requireStatus(t, request(stopping, http.MethodPost, "/api/v1/notifications/claim", `{}`, "application/json"), 503)
	if err := db.CheckHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestDeliveryHTTPFlow 验证真实路由的开始、回填、重复结果和日志隐私。
func TestDeliveryHTTPFlow(t *testing.T) {
	_, db := testRouter(t)
	manager, _ := config.NewManager()
	schedules, _ := store.NewScheduleStore(db)
	notify, _ := store.NewNotificationStore(db)
	app, err := service.NewApp(manager, schedules, notify)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(2 * time.Minute).Truncate(time.Minute)
	app.Scheduler, err = service.NewScheduler(notify, service.SchedulerOptions{Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	app.Delivery, err = service.NewDeliveryService(notify, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	runtime := service.NewRuntimeInfo("delivery-test-run")
	router := NewRouter(app, db.CheckHealth, runtime)
	for _, body := range []string{`{"instanceId":"delivery-test-run"}`, `{"instanceId":"delivery-test-run","attemptIds":null}`, `{"instanceId":"delivery-test-run","attemptIds":"bad"}`, `{"instanceId":"delivery-test-run","attemptIds":[""]}`} {
		requireStatus(t, request(router, http.MethodPost, "/api/v1/notifications/executor-exit", body, "application/json"), 400)
	}
	emptyExit := request(router, http.MethodPost, "/api/v1/notifications/executor-exit", `{"instanceId":"delivery-test-run","attemptIds":[]}`, "application/json")
	requireStatus(t, emptyExit, 200)
	for i := 0; i < 2; i++ {
		body := fmt.Sprintf(`{"name":"private-response-%d","type":"single","single":{"notifyTime":%q}}`, i, at.In(time.FixedZone("北京时间", 8*60*60)).Format(time.RFC3339))
		requireStatus(t, request(router, http.MethodPost, "/api/v1/scheduler/schedules", body, "application/json"), 201)
	}
	if err := app.Scheduler.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	claimResponse := request(router, http.MethodPost, "/api/v1/notifications/claim", `{}`, "application/json")
	requireStatus(t, claimResponse, 200)
	var claims struct {
		Data []service.DeliveryClaim `json:"data"`
	}
	if err := json.Unmarshal(claimResponse.Body.Bytes(), &claims); err != nil || len(claims.Data) != 1 {
		t.Fatal("HTTP 未领取完整分组", err)
	}
	path := "/api/v1/notifications/delivery-attempts/" + claims.Data[0].AttemptID
	readLogs := captureHTTPLogs(t)
	startedAt := time.Now()
	start := request(router, http.MethodPost, path+"/start", `{}`, "application/json")
	requireStatus(t, start, 200)
	var startData struct {
		Data service.DeliveryStart `json:"data"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &startData); err != nil || startData.Data.MemberCount != 2 || startData.Data.AttemptNumber != 1 {
		t.Fatal("HTTP 开始结果错误", err)
	}
	record := requireHTTPRecord(t, readLogs(), http.MethodPost, path+"/start", "INFO", "delivery attempt started", 200, time.Since(startedAt))
	if record["count"] != float64(2) || record["attempt_number"] != float64(1) {
		t.Fatal("投递日志缺少执行事实", record)
	}
	exitPath := "/api/v1/notifications/executor-exit"
	exitBody := fmt.Sprintf(`{"instanceId":"old-run","attemptIds":[%q]}`, claims.Data[0].AttemptID)
	requireStatus(t, request(router, http.MethodPost, exitPath, exitBody, "application/json"), 404)
	if snapshot := app.DeliverySnapshot(); len(snapshot) != 1 || snapshot[0].AttemptID != claims.Data[0].AttemptID || !snapshot[0].Started {
		t.Fatal("旧运行身份改变了活动投递", snapshot)
	}
	exitBody = fmt.Sprintf(`{"instanceId":"delivery-test-run","attemptIds":[%q]}`, claims.Data[0].AttemptID)
	exitResponse := request(router, http.MethodPost, exitPath, exitBody, "application/json")
	requireStatus(t, exitResponse, 200)
	if strings.TrimSpace(exitResponse.Body.String()) != `{"data":{"accepted":true}}` {
		t.Fatal("执行者退出响应错误", exitResponse.Body.String())
	}
	if snapshot := app.DeliverySnapshot(); len(snapshot) != 1 || snapshot[0].Attempts != 1 || !snapshot[0].Uncertain {
		t.Fatal("退出应保留展示预算并标记结果未知", snapshot)
	}
	at = at.Add(5 * time.Second)
	newClaims, err := app.ClaimDelivery(context.Background(), 20)
	if err != nil || len(newClaims) != 1 || newClaims[0].AttemptID == claims.Data[0].AttemptID {
		t.Fatal("未知结果等待后应创建新 attempt", newClaims, err)
	}
	lateExit := fmt.Sprintf(`{"instanceId":"delivery-test-run","attemptIds":[%q,%q,"unknown"]}`, claims.Data[0].AttemptID, claims.Data[0].AttemptID)
	requireStatus(t, request(router, http.MethodPost, exitPath, lateExit, "application/json"), 200)
	if snapshot := app.DeliverySnapshot(); len(snapshot) != 1 || snapshot[0].AttemptID != newClaims[0].AttemptID || snapshot[0].Attempts != 1 {
		t.Fatal("迟到退出不能整理新 attempt 或重置预算", snapshot)
	}
	requireStatus(t, request(router, http.MethodPost, path+"/results", `{"outcome":"sent"}`, "application/json"), 200)
	requireStatus(t, request(router, http.MethodPost, path+"/results", `{"outcome":"sent"}`, "application/json"), 200)
	requireStatus(t, request(router, http.MethodPost, path+"/results", `{"outcome":"failed"}`, "application/json"), 409)
	remaining := request(router, http.MethodPost, "/api/v1/notifications/claim", `{}`, "application/json")
	requireStatus(t, remaining, 200)
	if strings.TrimSpace(remaining.Body.String()) != `{"data":[]}` {
		t.Fatal("成功后重复领取")
	}
}
