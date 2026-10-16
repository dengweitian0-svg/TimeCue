package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	api "github.com/dengweitian0-svg/TimeCue/internal/api"
	app "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestUnacknowledgedScopeHTTP 验证新参数严格校验、旧查询兼容及范围游标隔离。
func TestUnacknowledgedScopeHTTP(t *testing.T) {
	db, conn := newRecoveryAPIDatabase(t)
	schedules, _ := store.NewScheduleStore(db)
	notify, _ := store.NewNotificationStore(db)
	app := &app.App{Schedule: schedules, Notify: notify}
	router := api.NewRouter(app, db.CheckHealth)
	now := time.Now().In(model.BeijingLocation())
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, model.BeijingLocation())
	for i := 1; i <= 102; i++ {
		at := start
		if i <= 51 {
			at = start.Add(-time.Second)
		}
		if _, err := conn.Exec(`INSERT INTO schedules(id,name,type,single_notify_time,created_at,updated_at,revision) VALUES (?,'HTTP范围','single',?,?,?,1)`, i, at.UnixNano(), at.Add(-time.Hour).UnixNano(), at.Add(-time.Hour).UnixNano()); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO notifications(id,schedule_id,notify_time,status) VALUES (?,?,?,'failed')`, i, i, at.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	base := "/api/v1/notifications?state=unacknowledged"
	for _, suffix := range []string{"&scope=", "&scope=all", "&scope=today&scope=history", "&scope=today&state=unacknowledged", "&scope=today&deliveryTaskId=x", "&scope=today&deliveryTaskId=", "&scope=history&attemptId=x", "&scope=today&extra=x", "&scope=history&cursor=bad"} {
		response := request(router, http.MethodGet, base+suffix, "", "")
		requireStatus(t, response, 400)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != "invalid_request" {
			t.Fatal(response.Body.String(), err)
		}
	}
	requireStatus(t, request(router, http.MethodGet, base+"&extra=x&cursor=", "", ""), 200)
	for _, scope := range []string{"today", "history"} {
		response := request(router, http.MethodGet, base+"&scope="+scope, "", "")
		requireStatus(t, response, 200)
		var body struct {
			Data model.OccurrencePage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.Total != 51 || len(body.Data.Items) != 50 || body.Data.NextCursor == "" {
			t.Fatal(response.Body.String(), err)
		}
		cursor := url.QueryEscape(body.Data.NextCursor)
		requireStatus(t, request(router, http.MethodGet, base+"&scope="+scope+"&cursor="+cursor, "", ""), 200)
		other := "history"
		if scope == other {
			other = "today"
		}
		requireStatus(t, request(router, http.MethodGet, base+"&scope="+other+"&cursor="+cursor, "", ""), 400)
		requireStatus(t, request(router, http.MethodGet, base+"&cursor="+cursor, "", ""), 400)
	}
}
