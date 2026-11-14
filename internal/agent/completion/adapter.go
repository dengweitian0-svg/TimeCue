package completion

import (
	"encoding/json"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

// APIAdapter 负责按模型协议准备最终 Chat Completions 正文。
type APIAdapter struct{}

// PrepareRequest 生成供预算、SQLite 和 SDK 共用的最终 UTF-8 正文。
func (APIAdapter) PrepareRequest(_ string, input chat.ModelRequest) (string, *chat.RunError) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", failure("model_invalid_params")
	}
	return string(encoded), nil
}
