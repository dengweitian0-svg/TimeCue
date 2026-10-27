package completion_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
)

// TestUnknownHTTPFailureIsNotTemporaryServerError 验证未分类 HTTP 错误不会被当作暂时性 500。
func TestUnknownHTTPFailureIsNotTemporaryServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"message":"secret-token"}}`)
	}))
	defer server.Close()
	_, failure := complete(t, server.URL, time.Second, time.Second, nil)
	if failure == nil || failure.Code != "model_invalid_format" || strings.Contains(failure.Message, "secret-token") {
		t.Fatalf("未分类上游错误不能冒充可重试的 500: %+v", failure)
	}
}

// TestUpstreamSemanticErrorsNeverExposeProviderBody 验证七类上游语义错误不回显供应商正文。
func TestUpstreamSemanticErrorsNeverExposeProviderBody(t *testing.T) {
	for status, code := range map[int]string{
		400: "model_invalid_format", 401: "model_auth_failed", 402: "model_insufficient_balance",
		422: "model_invalid_params", 429: "model_rate_limited", 500: "model_server_error", 503: "model_server_busy",
	} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"secret-token"}}`)
			}))
			defer server.Close()
			_, failure := complete(t, server.URL, time.Second, time.Second, nil)
			if failure == nil || failure.Code != code || strings.Contains(failure.Message, "secret-token") {
				t.Fatalf("错误语义或脱敏错误: %+v", failure)
			}
		})
	}
}

// TestKeepAliveDoesNotExtendEffectiveOutputDeadline 验证保活不延长首次或空闲有效输出期限。
func TestKeepAliveDoesNotExtendEffectiveOutputDeadline(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if partial {
					fmt.Fprint(w, "data: {\"id\":\"partial\",\"choices\":[{\"delta\":{\"content\":\"保留正文\",\"reasoning_content\":\"保留思考\"}}],\"usage\":{\"total_tokens\":9}}\n\n")
				}
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					fmt.Fprint(w, ": keep-alive\n\ndata: {\"choices\":[{\"delta\":{}}]}\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
				}
			}))
			defer server.Close()
			started := time.Now()
			output, failure := complete(t, server.URL, 120*time.Millisecond, 80*time.Millisecond, nil)
			if failure == nil || failure.Code != "model_timeout" || time.Since(started) > time.Second {
				t.Fatalf("保活延长了有效输出超时: %+v", failure)
			}
			if partial && (output.Message.Content != "保留正文" || output.Message.ReasoningContent != "保留思考" || string(output.Usage) != `{"total_tokens":9}`) {
				t.Fatalf("超时丢失了部分响应: %+v", output)
			}
		})
	}
}

// TestAbruptStreamPreservesPartialResponse 验证异常断流保留真实正文、响应标识和用量。
func TestAbruptStreamPreservesPartialResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"partial\",\"choices\":[{\"delta\":{\"content\":\"部分正文\"}}],\"usage\":{\"total_tokens\":3}}\n\n")
	}))
	defer server.Close()
	output, failure := complete(t, server.URL, time.Second, time.Second, nil)
	if failure == nil || failure.Code != "model_network_error" || output.Message.Content != "部分正文" || output.ID != "partial" || string(output.Usage) != `{"total_tokens":3}` {
		t.Fatalf("断流应保留真实响应: %+v %+v", output, failure)
	}
}

// TestUnexpectedToolDeltaPreservesAccompanyingText 验证意外工具增量仍保留同块正文、思考及用量。
func TestUnexpectedToolDeltaPreservesAccompanyingText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"mixed\",\"choices\":[{\"delta\":{\"content\":\"部分正文\",\"reasoning_content\":\"部分思考\",\"tool_calls\":[{\"index\":0,\"id\":\"tool\",\"type\":\"function\",\"function\":{\"name\":\"query\",\"arguments\":\"{\"}}]}}],\"usage\":{\"total_tokens\":3}}\n\n")
	}))
	defer server.Close()
	output, failure := complete(t, server.URL, time.Second, time.Second, nil)
	if failure == nil || failure.Code != "model_invalid_format" || output.Message.Content != "部分正文" || output.Message.ReasoningContent != "部分思考" || string(output.Usage) != `{"total_tokens":3}` {
		t.Fatalf("意外工具拒绝丢失了同块已收到内容: %+v %+v", output, failure)
	}
}

// TestTransportTimeoutUsesTimeoutSemanticCode 验证传输层超时使用统一超时错误码。
func TestTransportTimeoutUsesTimeoutSemanticCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer server.Close()
	_, failure := completion.NewClient(&http.Client{Timeout: 50 * time.Millisecond}).Complete(context.Background(), chat.ModelConfig{
		BaseURL:      server.URL,
		Key:          "secret-token",
		FirstTimeout: time.Second,
	}, chat.ModelRequest{
		Model: "model",
		Messages: []chat.Message{{
			Role:    "user",
			Content: "hello",
		}},
		Stream:    true,
		MaxTokens: 100,
	}, func(chat.EventType, string) error { return nil })
	if failure == nil || failure.Code != "model_timeout" {
		t.Fatalf("传输超时错误: %+v", failure)
	}
}

func complete(t *testing.T, url string, first, idle time.Duration, emit func(chat.EventType, string) error) (chat.ModelOutput, *chat.RunError) {
	t.Helper()
	if emit == nil {
		emit = func(chat.EventType, string) error { return nil }
	}
	return completion.NewClient(nil).Complete(context.Background(), chat.ModelConfig{
		BaseURL:      url,
		Key:          "secret-token",
		FirstTimeout: first,
		IdleTimeout:  idle,
	}, chat.ModelRequest{
		Model: "model",
		Messages: []chat.Message{{
			Role:    "user",
			Content: "hello",
		}},
		Stream:        true,
		MaxTokens:     100,
		StreamOptions: map[string]bool{"include_usage": true},
	}, emit)
}
