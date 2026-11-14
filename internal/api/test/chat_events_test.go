package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

// TestLoopHTTPEventBatchesAndCursors 验证生产 Loop 的增量提交、分页与 SSE 重连。
func TestLoopHTTPEventBatchesAndCursors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	deltas := make(chan string)
	router, _ := chatHTTPFixture(t, "secret", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for {
			select {
			case delta, ok := <-deltas:
				if !ok {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", delta)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	server := httptest.NewServer(router)
	defer func() {
		cancel()
		server.Close()
	}()
	body := `{"session_id":"` + testSessionID + `","input":"事件续接","provider":"test","model":"configured-model"}`
	response := chatRequest(router, http.MethodPost, "/v1/run", body)
	var receipt chat.Receipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || response.Code != http.StatusAccepted || receipt.RunID == "" {
		t.Fatalf("创建运行失败: %d %s %v", response.Code, response.Body, err)
	}
	path := "/v1/run/" + receipt.RunID
	pageAfter := func(after, limit int64) chat.EventPage {
		t.Helper()
		response := chatRequest(router, http.MethodGet, fmt.Sprintf("%s/events?after_seq=%d&limit=%d", path, after, limit), "")
		var page chat.EventPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != http.StatusOK {
			t.Fatalf("读取事件失败: %d %s %v", response.Code, response.Body, err)
		}
		return page
	}
	openStream := func(cursor string, status int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path+"/event", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "127.0.0.1:10086"
		if cursor != "" {
			req.Header.Set("Last-Event-ID", cursor)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		if res.StatusCode != status {
			t.Fatalf("SSE 游标 %q 返回 %d，预期 %d", cursor, res.StatusCode, status)
		}
		return res
	}
	emit := func(delta string) {
		t.Helper()
		select {
		case deltas <- delta:
		case <-ctx.Done():
			t.Fatal("上游未接收增量控制信号")
		}
	}

	old := openStream("", http.StatusOK)
	defer old.Body.Close()
	oldStream := bufio.NewScanner(old.Body)
	requireSSEEvent(t, oldStream, 1, chat.EventStart, "")
	for _, delta := range []string{"甲", "乙", "丙", "丁"} {
		emit(delta)
	}
	if page := pageAfter(1, 10); len(page.Events) != 0 || page.NextAfterSeq != 1 {
		t.Fatalf("未满五条提前提交: %+v", page)
	}
	openStream("2", http.StatusBadRequest).Body.Close()
	openStream("invalid", http.StatusBadRequest).Body.Close()
	emit("戊")
	for i, delta := range []string{"甲", "乙", "丙", "丁", "戊"} {
		requireSSEEvent(t, oldStream, int64(i+2), chat.EventText, delta)
	}

	page := pageAfter(1, 2)
	if len(page.Events) != 2 || !page.HasMore || page.NextAfterSeq != 3 || page.Events[0].ID != 2 || page.Events[1].ID != 3 {
		t.Fatalf("分页游标错误: %+v", page)
	}
	current := openStream(strconv.FormatInt(page.NextAfterSeq, 10), http.StatusOK)
	defer current.Body.Close()
	stream := bufio.NewScanner(current.Body)
	for i, delta := range []string{"丙", "丁", "戊"} {
		requireSSEEvent(t, stream, int64(i+4), chat.EventText, delta)
	}
	if _, err := io.ReadAll(old.Body); err != nil {
		t.Fatalf("合法重连未关闭旧订阅: %v", err)
	}

	start := time.Now()
	emit("超时批次")
	time.Sleep(3 * time.Second)
	emit("期限内的增量")
	requireSSEEvent(t, stream, 7, chat.EventText, "超时批次")
	if elapsed := time.Since(start); elapsed < 5*time.Second || elapsed >= 8*time.Second {
		t.Fatalf("首条增量提交耗时 %s，预期约五秒", elapsed)
	}
	requireSSEEvent(t, stream, 8, chat.EventText, "期限内的增量")
	emit("尾批")
	if page := pageAfter(8, 10); len(page.Events) != 0 {
		t.Fatalf("尾批提前提交: %+v", page)
	}
	close(deltas)
	for {
		page := pageAfter(8, 10)
		if len(page.Events) != 0 {
			if len(page.Events) != 2 || page.NextAfterSeq != 10 || page.HasMore || page.Events[1].Type != chat.EventEnd {
				t.Fatalf("尾批和终态未一起提交: %+v", page)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("未提交尾批与终态")
		case <-time.After(time.Millisecond):
		}
	}
	requireSSEEvent(t, stream, 9, chat.EventText, "尾批")
	requireSSEEvent(t, stream, 10, chat.EventEnd, "")
}

func requireSSEEvent(t *testing.T, stream *bufio.Scanner, seq int64, kind chat.EventType, content string) {
	t.Helper()
	var lines []string
	for stream.Scan() && stream.Text() != "" {
		lines = append(lines, stream.Text())
	}
	if len(lines) != 3 || lines[0] != fmt.Sprintf("id: %d", seq) || lines[1] != "event: "+string(kind) || !strings.HasPrefix(lines[2], "data: ") {
		t.Fatalf("SSE 序号或类型错误，预期 %d %s: %v %v", seq, kind, lines, stream.Err())
	}
	if content != "" {
		var data chat.TextData
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[2], "data: ")), &data); err != nil || data.Content != content {
			t.Fatalf("SSE 增量错误，预期 %q: %s %v", content, lines[2], err)
		}
	}
}
