package chat_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
)

// TestRolledBackCallRegistrationCanFinishRun 验证调用插入后 checkpoint 回滚不会留下不存在的调用身份阻塞失败收尾。
func TestRolledBackCallRegistrationCanFinishRun(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer upstream.Close()
	_, storage, path := admissionDatabase(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// 仅在真实事务故障边界注入失败，不改变正式数据库结构。
	if _, err := raw.Exec(`CREATE TRIGGER reject_call_checkpoint BEFORE INSERT ON ai_checkpoints BEGIN SELECT RAISE(ABORT,'injected checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), nil)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	receipt := createAttemptRun(t, service, "registration-rollback")
	run := awaitFinishedRun(t, service, receipt.RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" || requests.Load() != 0 {
		t.Fatalf("登记回滚未保存真实失败终态或仍发出请求: %+v requests=%d", run, requests.Load())
	}
	page := eventPage(t, service, receipt.RunID, 0, 100)
	if len(page.Events) != 3 || page.Events[0].Type != chat.EventStart || page.Events[1].Type != chat.EventError || page.Events[2].Type != chat.EventEnd {
		t.Fatalf("登记回滚丢失原子错误收尾: %s", mustJSON(page))
	}
}
