package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Stream 是统一响应出口使用的窄流式响应契约。
type Stream interface {
	// Write 按序写入流内容，并在需要时调用 flush 提交内容。
	Write(context.Context, io.Writer, func() error) error
	// Close 释放流所持有的订阅或其他资源。
	Close()
}

var allowedSSEEvents = map[string]struct{}{
	"start": {}, "thinking": {}, "text": {}, "tool": {}, "end": {},
}

// EncodeSSE 编码一条 SSE 帧。事件 ID 由调用方提供，内容始终通过 JSON 编码。
func EncodeSSE(id int64, event string, data any) ([]byte, error) {
	if id <= 0 {
		return nil, fmt.Errorf("SSE event ID must be positive")
	}
	if _, ok := allowedSSEEvents[event]; !ok {
		return nil, fmt.Errorf("unsupported SSE event type %q", event)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode SSE event data: %w", err)
	}
	frame := make([]byte, 0, len(payload)+len(event)+32)
	frame = fmt.Appendf(frame, "id: %d\nevent: %s\ndata: %s\n\n", id, event, payload)
	return frame, nil
}
