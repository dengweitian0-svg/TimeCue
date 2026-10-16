package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/dengweitian0-svg/TimeCue/internal/api"
	app "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/service/recovery"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestRecoveryHTTPQueries 验证新增筛选、恢复摘要和旧查询的兼容边界。
func TestRecoveryHTTPQueries(t *testing.T) {
	db, conn := newRecoveryAPIDatabase(t)
	notify, _ := store.NewNotificationStore(db)
	schedules, _ := store.NewScheduleStore(db)
	app := &app.App{Schedule: schedules, Notify: notify}
	now := time.Now().Truncate(time.Minute).In(model.BeijingLocation())
	app.Delivery, _ = notification.NewDeliveryService(notify, func() time.Time { return now })
	app.Recovery, _ = recovery.NewService(notify, notification.SchedulerOptions{Now: func() time.Time { return now }})
	// 使用正式创建，再通过可靠版本边界模拟长期离线；不增加生产测试接口。
	rule, err := schedules.CreateSchedule(context.Background(), model.Schedule{Name: "历史私密内容", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{fmt.Sprintf("%d %d * * *", now.Minute(), now.Hour())}}})
	if err != nil {
		t.Fatal(err)
	}
	from := now.AddDate(0, 0, -3).Add(-time.Minute)
	if _, err := conn.Exec(`UPDATE schedule_versions SET effective_from = ? WHERE schedule_id = ?`, from.UnixNano(), rule.ID); err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(app, db.CheckHealth)
	for _, query := range []string{"", "state=sent", "deliveryTaskId=", "attemptId=x", "state=unacknowledged&deliveryTaskId=x", "deliveryTaskId=x&deliveryTaskId=y", "deliveryTaskId=x&cursor=garbage", "unknown=x"} {
		expected := 400
		if query == "deliveryTaskId=x&cursor=garbage" {
			expected = 404
		}
		requireStatus(t, request(router, http.MethodGet, "/api/v1/notifications?"+query, "", ""), expected)
	}
	requireStatus(t, request(router, http.MethodGet, "/api/v1/notifications?state=unacknowledged&cursor=", "", ""), 200)
	for i := 0; i < 10; i++ {
		if _, err := app.RecoverStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	view, err := app.RecoverySnapshot(context.Background(), now)
	if err != nil || view == nil || view.MissedTotal == nil || *view.MissedTotal != 3 {
		t.Fatal("恢复夹具不正确", view, err)
	}
	snapshot := request(router, http.MethodGet, "/api/v1/scheduler/schedules?view=occurrences&include=unacknowledged", "", "")
	requireStatus(t, snapshot, 200)
	var combined struct {
		Data model.ScheduleQueryResult `json:"data"`
	}
	if err := json.Unmarshal(snapshot.Body.Bytes(), &combined); err != nil || combined.Data.Recovery == nil || combined.Data.Recovery.State != "completed" {
		t.Fatal("组合快照未返回恢复进度", snapshot.Body.String(), err)
	}
	claimResponse := request(router, http.MethodPost, "/api/v1/notifications/claim", `{}`, "application/json")
	requireStatus(t, claimResponse, 200)
	var claims struct {
		Data []notification.DeliveryClaim `json:"data"`
	}
	if err := json.Unmarshal(claimResponse.Body.Bytes(), &claims); err != nil || len(claims.Data) != 1 || claims.Data[0].TaskType != "recovery" {
		t.Fatal("未提供恢复类型", claimResponse.Body.String(), err)
	}
	claim := claims.Data[0]
	base := "/api/v1/notifications?deliveryTaskId=" + claim.DeliveryTaskID
	response := request(router, http.MethodGet, base, "", "")
	requireStatus(t, response, 200)
	var page struct {
		Data model.OccurrencePage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Data.Total != 3 || len(page.Data.Items) != 3 {
		t.Fatal("成员查询错误", response.Body.String(), err)
	}
	requireStatus(t, request(router, http.MethodGet, base+"&attemptId="+claim.AttemptID, "", ""), 409)
	requireStatus(t, request(router, http.MethodGet, base+"&attemptId=old-process", "", ""), 404)
	requireStatus(t, request(router, http.MethodGet, base+"&cursor=garbage", "", ""), 400)
	path := "/api/v1/notifications/delivery-attempts/" + claim.AttemptID
	requireStatus(t, request(router, http.MethodPost, path+"/start", `{}`, "application/json"), 200)
	requireStatus(t, request(router, http.MethodGet, base+"&attemptId="+claim.AttemptID, "", ""), 200)
	requireStatus(t, request(router, http.MethodPost, path+"/results", `{"outcome":"sent"}`, "application/json"), 200)
	response = request(router, http.MethodGet, base+"&attemptId="+claim.AttemptID, "", "")
	requireStatus(t, response, 200)
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || *page.Data.Items[0].DeliveryStatus != model.NotifySent || page.Data.Items[0].AcknowledgedAt != nil {
		t.Fatal("真实结果与确认混淆", response.Body.String(), err)
	}
	if page.Data.Recovery == nil || page.Data.Recovery.UnacknowledgedCount != 3 {
		t.Fatal("平台成功被当作历史确认", response.Body.String())
	}
	ids := make([]int, 0, len(page.Data.Items))
	for _, item := range page.Data.Items {
		ids = append(ids, *item.NotificationID)
	}
	body, _ := json.Marshal(map[string]any{"notificationIds": ids})
	requireStatus(t, request(router, http.MethodPost, "/api/v1/notifications/acknowledgements", string(body), "application/json"), 200)
	snapshot = request(router, http.MethodGet, "/api/v1/scheduler/schedules?view=occurrences&include=unacknowledged", "", "")
	if err := json.Unmarshal(snapshot.Body.Bytes(), &combined); err != nil || combined.Data.Recovery.UnacknowledgedCount != 0 || *combined.Data.Recovery.MissedTotal != 3 {
		t.Fatal("确认后历史待处理计数未归零或历史总数改变", snapshot.Body.String(), err)
	}
	response = request(router, http.MethodGet, base, "", "")
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Data.Total != 3 || page.Data.Recovery.UnacknowledgedCount != 0 {
		t.Fatal("确认后历史分页错误", response.Body.String(), err)
	}
}

func newRecoveryAPIDatabase(t *testing.T) (*store.Sqlite, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recovery-api.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for _, name := range []string{"001_initial.up.sql", "002_schedule_changes.up.sql"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
	}
	db := store.NewSqlite()
	if err := db.Connect(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, conn
}
