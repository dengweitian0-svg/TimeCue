package completion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// Client 使用官方 openai-go SDK 调用兼容的 Chat Completions 服务。
type Client struct{ http *http.Client }

// NewClient 注入传输边界，默认禁止重定向以保护当前模型的密钥。
func NewClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Client{http: client}
}

// Complete 通过 SDK 消费流式响应，保留提供方扩展思考字段和原始 usage。
func (c *Client) Complete(ctx context.Context, cfg chat.ModelConfig, input chat.ModelRequest, emit func(chat.EventType, string) error) (chat.ModelOutput, *chat.RunError) {
	output := chat.ModelOutput{Message: chat.Message{Role: "assistant"}}
	params, err := requestParams(input)
	if err != nil {
		return output, failure("model_invalid_params")
	}
	requestCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	first, idle := cfg.FirstTimeout, cfg.IdleTimeout
	if first <= 0 {
		first = 120 * time.Second
	}
	if idle <= 0 {
		idle = 60 * time.Second
	}
	var timerMu sync.Mutex
	timerMu.Lock()
	deadline := time.Now().Add(first)
	timerStopped := false
	timeoutMessage := "等待模型首次有效输出超时"
	var timer *time.Timer
	timer = time.AfterFunc(first, func() {
		timerMu.Lock()
		defer timerMu.Unlock()
		if timerStopped {
			return
		}
		if remaining := time.Until(deadline); remaining > 0 {
			timer.Reset(remaining)
		} else {
			cancel(errors.New(timeoutMessage))
		}
	})
	timerMu.Unlock()
	defer func() {
		timerMu.Lock()
		defer timerMu.Unlock()
		timerStopped = true
		timer.Stop()
	}()
	client := openai.NewClient(option.WithAPIKey(cfg.Key), option.WithBaseURL(strings.TrimRight(cfg.BaseURL, "/")+"/"), option.WithHTTPClient(c.http), option.WithMaxRetries(0))
	stream := client.Chat.Completions.NewStreaming(requestCtx, params)
	defer stream.Close()
	finished := false
	for stream.Next() {
		if requestCtx.Err() != nil {
			break
		}
		chunk := stream.Current()
		if chunk.ID != "" {
			output.ID = chunk.ID
		}
		if raw := chunk.JSON.Usage.Raw(); raw != "" && raw != "null" {
			output.Usage = json.RawMessage(raw)
		}
		for _, choice := range chunk.Choices {
			var thinking string
			if raw := choice.Delta.JSON.ExtraFields["reasoning_content"].Raw(); raw != "" && raw != "null" {
				if json.Unmarshal([]byte(raw), &thinking) != nil {
					return output, failure("model_invalid_format")
				}
			}
			for _, delta := range []struct {
				kind    chat.EventType
				content string
			}{
				{
					kind:    chat.EventThinking,
					content: thinking,
				},
				{
					kind:    chat.EventText,
					content: choice.Delta.Content,
				},
			} {
				if delta.content == "" {
					continue
				}
				timerMu.Lock()
				if requestCtx.Err() != nil {
					timerMu.Unlock()
					break
				}
				deadline = time.Now().Add(idle)
				timeoutMessage = "模型流式输出空闲超时"
				timer.Reset(idle)
				timerMu.Unlock()
				if delta.kind == chat.EventText {
					output.Message.Content += delta.content
				} else {
					output.Message.ReasoningContent += delta.content
				}
				if err = emit(delta.kind, delta.content); err != nil {
					return output, failure("persistence_failed")
				}
			}
			if len(choice.Delta.ToolCalls) > 0 {
				return output, failure("model_invalid_format")
			}
			if choice.FinishReason != "" {
				output.FinishReason = choice.FinishReason
				finished = true
			}
		}
	}
	if requestCtx.Err() != nil && ctx.Err() == nil {
		return output, &chat.RunError{
			Code:    "model_timeout",
			Message: context.Cause(requestCtx).Error(),
		}
	}
	if err = stream.Err(); err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return output, &chat.RunError{
				Code:    "model_timeout",
				Message: "模型网络请求超时",
			}
		}
		var apiError *openai.Error
		if errors.As(err, &apiError) {
			code := map[int]string{400: "model_invalid_format", 401: "model_auth_failed", 402: "model_insufficient_balance", 422: "model_invalid_params", 429: "model_rate_limited", 500: "model_server_error", 503: "model_server_busy"}[apiError.StatusCode]
			if code == "" {
				code = "model_invalid_format"
			}
			return output, failure(code)
		}
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return output, failure("model_invalid_format")
		}
		return output, failure("model_network_error")
	}
	if !finished {
		return output, failure("model_network_error")
	}
	if output.FinishReason != "stop" {
		return output, failure("model_invalid_format")
	}
	return output, nil
}

func requestParams(input chat.ModelRequest) (openai.ChatCompletionNewParams, error) {
	params := openai.ChatCompletionNewParams{
		Model:         shared.ChatModel(input.Model),
		MaxTokens:     openai.Int(int64(input.MaxTokens)),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(input.StreamOptions["include_usage"])},
	}
	for _, message := range input.Messages {
		var value openai.ChatCompletionMessageParamUnion
		switch message.Role {
		case "system":
			value = openai.SystemMessage(message.Content)
		case "user":
			value = openai.UserMessage(message.Content)
		case "assistant":
			value = openai.AssistantMessage(message.Content)
			if message.ReasoningContent != "" {
				value.OfAssistant.SetExtraFields(map[string]any{"reasoning_content": message.ReasoningContent})
			}
		default:
			return params, errors.New("unsupported message role")
		}
		params.Messages = append(params.Messages, value)
	}
	return params, nil
}

func failure(code string) *chat.RunError {
	message := map[string]string{
		"model_invalid_format":       "模型请求或响应格式不受支持",
		"model_auth_failed":          "模型密钥认证失败，请检查实例数据目录的 .env",
		"model_insufficient_balance": "模型账户余额不足",
		"model_invalid_params":       "模型请求参数无效",
		"model_rate_limited":         "模型请求被限流",
		"model_server_error":         "模型服务内部错误",
		"model_server_busy":          "模型服务繁忙",
		"model_network_error":        "模型连接失败或流式响应异常中断",
		"persistence_failed":         "保存模型输出失败",
	}[code]
	if message == "" {
		message = fmt.Sprintf("模型请求失败（%s）", code)
	}
	return &chat.RunError{
		Code:    code,
		Message: message,
	}
}
