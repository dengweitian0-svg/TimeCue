package completion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	first, idle := cfg.FirstTimeout, cfg.IdleTimeout
	if first <= 0 {
		first = 120 * time.Second
	}
	if idle <= 0 {
		idle = 60 * time.Second
	}
	timer := time.AfterFunc(first, cancel)
	defer timer.Stop()
	client := openai.NewClient(option.WithAPIKey(cfg.Key), option.WithBaseURL(strings.TrimRight(cfg.BaseURL, "/")+"/"), option.WithHTTPClient(c.http), option.WithMaxRetries(0))
	stream := client.Chat.Completions.NewStreaming(requestCtx, params)
	defer stream.Close()
	finished := false
	for stream.Next() {
		chunk := stream.Current()
		if chunk.ID != "" {
			output.ID = chunk.ID
		}
		if raw := chunk.JSON.Usage.Raw(); raw != "" && raw != "null" {
			output.Usage = json.RawMessage(raw)
		}
		for _, choice := range chunk.Choices {
			if len(choice.Delta.ToolCalls) > 0 {
				return output, failure("model_invalid_format")
			}
			var thinking string
			if raw := choice.Delta.JSON.ExtraFields["reasoning_content"].Raw(); raw != "" && raw != "null" {
				if json.Unmarshal([]byte(raw), &thinking) != nil {
					return output, failure("model_invalid_format")
				}
			}
			for _, delta := range []struct {
				kind    chat.EventType
				content string
			}{{chat.EventThinking, thinking}, {chat.EventText, choice.Delta.Content}} {
				if delta.content == "" {
					continue
				}
				timer.Reset(idle)
				if delta.kind == chat.EventText {
					output.Message.Content += delta.content
				} else {
					output.Message.ReasoningContent += delta.content
				}
				if err = emit(delta.kind, delta.content); err != nil {
					return output, failure("persistence_failed")
				}
			}
			if choice.FinishReason != "" {
				output.FinishReason = choice.FinishReason
				finished = true
			}
		}
	}
	if requestCtx.Err() != nil && ctx.Err() == nil {
		return output, failure("model_timeout")
	}
	if err = stream.Err(); err != nil {
		var apiError *openai.Error
		if errors.As(err, &apiError) {
			code := map[int]string{400: "model_invalid_format", 401: "model_auth_failed", 402: "model_insufficient_balance", 422: "model_invalid_params", 429: "model_rate_limited", 500: "model_server_error", 503: "model_server_busy"}[apiError.StatusCode]
			if code == "" {
				code = "model_server_error"
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
	return &chat.RunError{Code: code, Message: fmt.Sprintf("模型请求失败（%s）", code)}
}
