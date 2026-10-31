package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/google/uuid"
)

func managedChatRecovery(t *testing.T, binary, root string) {
	t.Helper()
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact=%v", compact), func(t *testing.T) {
			requests := make(chan chat.ModelRequest, 10)
			release := make(chan struct{})
			var releaseOnce sync.Once
			resume := func() { releaseOnce.Do(func() { close(release) }) }
			var continuations atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chat.ModelRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- request
				last := request.Messages[len(request.Messages)-1]
				w.Header().Set("Content-Type", "text/event-stream")
				switch {
				case last.Content == "history":
					writeProcessDelta(w, strings.Repeat("old-history-", 1600), "stop")
				case last.Content == "继续未完成的输出":
					if continuations.Add(1) == 1 {
						fmt.Fprint(w, "data: {\"id\":\"paused\",\"choices\":[{\"delta\":{\"content\":\"旧片段\",\"reasoning_content\":\"保留思考\"}}],\"usage\":{\"total_tokens\":17}}\n\n")
						for i := 0; i < 4; i++ {
							writeProcessDelta(w, "片段", "")
						}
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					select {
					case <-release:
						writeProcessDelta(w, "恢复后段", "stop")
					case <-r.Context().Done():
					}
				case compact && strings.Contains(request.Messages[0].Content, "上下文检查点压缩"):
					writeProcessDelta(w, "摘要前段", "length")
				case compact:
					writeProcessDelta(w, "普通结果", "stop")
				default:
					writeProcessDelta(w, "普通前段", "length")
				}
			}))
			defer func() { resume(); upstream.Close() }()
			directory := processFixture(t, root, "valid")
			config := "db:\n  db_path: ./fixture.db\nlog:\n  log_path: ./logs\nchat:\n  max_retry: 0\n  max_continue: 1\nchat_model:\n  - provider: test\n    base_url: " + upstream.URL + "\n    models:\n      - model: model\n        context_window: 6500\n        max_output_tokens: 1000\n"
			if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("TEST_MODEL=process-fixture-only\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			p := launchProcess(t, binary, "--data-dir", directory, "--instance-id", "chat-first")
			p.ready(t)
			session := uuid.NewString()
			if compact {
				prior := createProcessChatRun(t, session, "history")
				awaitProcessChatRun(t, prior.RunID)
				<-requests
			}
			receipt := createProcessChatRun(t, session, "本轮真实输入")
			awaitProcessChatEvents(t, receipt.RunID, chat.EventThinking)
			<-requests
			original := <-requests
			stopProcessChat(t, p)

			db := store.NewSqlite()
			if err := db.Connect(context.Background(), filepath.Join(directory, "fixture.db")); err != nil {
				t.Fatal(err)
			}
			ports := store.NewChatStorage(db)
			call, err := ports.Execution.ReadCall(context.Background(), receipt.RunID)
			if err != nil || call == nil || call.State != chat.StateRunning || !call.Continuation || call.RetryCount != 0 || call.MaxRetry != 0 || call.Output == nil || call.Output.ID != "paused" || string(call.Output.Usage) != `{"total_tokens":17}` || call.Output.Message.ReasoningContent != "保留思考" {
				t.Fatalf("退出丢失原调用或部分响应: %+v err=%v", call, err)
			}
			page, err := ports.Events.Events(context.Background(), receipt.RunID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range page.Events {
				if event.Type == chat.EventEnd || event.Type == chat.EventError {
					t.Fatalf("正常退出伪造终态: %+v", event)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			p = launchProcess(t, binary, "--data-dir", directory, "--instance-id", "chat-restarted")
			info := p.ready(t)
			var health struct{ Data app.RuntimeInfo }
			processChatHTTP(t, http.MethodGet, "/health", "", http.StatusOK, &health)
			if !reflect.DeepEqual(health.Data, info) {
				t.Fatal("ready 和 health 身份不一致")
			}
			// 恢复请求仍被上游阻塞，ready/health 必须已经可用。
			resume()
			run := awaitProcessChatRun(t, receipt.RunID)
			want := "普通前段恢复后段"
			if compact {
				want = "普通结果"
			}
			if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != want {
				t.Fatalf("重启恢复失败: %+v error=%+v", run, run.Error)
			}
			replayed := <-requests
			if !reflect.DeepEqual(original, replayed) {
				t.Fatal("重启重复追加了消息或改变实际请求")
			}
			if compact {
				ordinary := <-requests
				if ordinary.Messages[len(ordinary.Messages)-2].Content != "摘要前段恢复后段" || strings.Contains(string(mustProcessJSON(t, ordinary)), "old-history-") {
					t.Fatal("摘要续写身份或成功切换丢失")
				}
			}
			stopProcessChat(t, p)

			if err := db.Connect(context.Background(), filepath.Join(directory, "fixture.db")); err != nil {
				t.Fatal(err)
			}
			ports = store.NewChatStorage(db)
			used, err := ports.Execution.ContinuationCallCount(context.Background(), receipt.RunID)
			if err != nil || used != 1 {
				t.Fatalf("生命周期恢复额外扣续写预算: %d %v", used, err)
			}
			cancelIDs := []string{uuid.NewString(), uuid.NewString()}
			for i, id := range cancelIDs {
				if err := ports.Runs.Create(context.Background(), chat.Record{
					Run: chat.Run{
						RunID:     id,
						SessionID: id,
						Provider:  "test",
						Model:     "model",
						Input:     "取消不恢复执行",
						CreatedAt: time.Now(),
					},
					State:          chat.StateWaitForCancel,
					IdempotencyKey: "cancel",
				}); err != nil {
					t.Fatal(err)
				}
				if i == 1 {
					if err := ports.Sessions.Hide(context.Background(), id); err != nil {
						t.Fatal(err)
					}
				}
			}
			_ = db.Close()
			if err := os.Remove(filepath.Join(directory, ".env")); err != nil {
				t.Fatal(err)
			}
			p = launchProcess(t, binary, "--data-dir", directory, "--instance-id", "cancel-only")
			p.ready(t)
			cancelled := awaitProcessChatRun(t, cancelIDs[0])
			if cancelled.Status != chat.StatusCancelled || cancelled.Error != nil {
				t.Fatalf("取消只读收尾被模型门禁阻断: %+v", cancelled)
			}
			stopProcessChat(t, p)
			if err := db.Connect(context.Background(), filepath.Join(directory, "fixture.db")); err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			hidden, err := ports.Runs.ReadRun(context.Background(), cancelIDs[1], false)
			if err != nil || hidden.State != chat.StateCancelled || hidden.FinishedAt == nil {
				t.Fatalf("隐藏会话没有后台收尾: %+v %v", hidden, err)
			}
			select {
			case <-requests:
				t.Fatal("终态或取消运行被重新执行")
			default:
			}
		})
	}
}

func writeProcessDelta(w http.ResponseWriter, content, reason string) {
	data := fmt.Sprintf(`{"choices":[{"delta":{"content":%s},"finish_reason":%s}]}`, mustProcessString(content), mustProcessString(reason))
	fmt.Fprintf(w, "data: %s\n\n", data)
	if reason != "" {
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func mustProcessString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func mustProcessJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func processChatHTTP(t *testing.T, method, path, body string, status int, result any) {
	t.Helper()
	req, err := http.NewRequest(method, "http://127.0.0.1:1145"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "create_session:"+uuid.NewString())
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != status {
		t.Fatalf("%s %s: status=%d body=%s err=%v", method, path, resp.StatusCode, data, err)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatal(err)
		}
	}
}

func createProcessChatRun(t *testing.T, session, input string) chat.Receipt {
	t.Helper()
	var receipt chat.Receipt
	body := mustProcessJSON(t, map[string]string{
		"session_id": session,
		"provider":   "test",
		"model":      "model",
		"input":      input,
	})
	processChatHTTP(t, http.MethodPost, "/v1/run", string(body), http.StatusAccepted, &receipt)
	return receipt
}

func awaitProcessChatRun(t *testing.T, id string) chat.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var run chat.Run
		processChatHTTP(t, http.MethodGet, "/v1/run/"+id, "", http.StatusOK, &run)
		if run.FinishedAt != nil {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("运行未结束: %+v", run)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func awaitProcessChatEvents(t *testing.T, id string, kind chat.EventType) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var page chat.EventPage
		processChatHTTP(t, http.MethodGet, "/v1/run/"+id+"/events?limit=100", "", http.StatusOK, &page)
		for _, event := range page.Events {
			if event.Type == kind {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("进程没有提交部分响应")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stopProcessChat(t *testing.T, p *testProcess) {
	t.Helper()
	started := time.Now()
	if _, err := io.WriteString(p.input, "{\"command\":\"shutdown\"}\n"); err != nil {
		t.Fatal(err)
	}
	p.wait(t, true)
	if time.Since(started) > 6*time.Second {
		t.Fatal("正常退出超过共享五秒预算及测试观察余量")
	}
}
