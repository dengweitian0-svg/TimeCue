package tools

import (
	"context"
	"encoding/json"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// ScheduleDeletes 仅暴露正式版本删除入口，回执和提醒由业务事务统一维护。
type ScheduleDeletes interface {
	// DeleteScheduleVersion 原子检查版本、软删除并保存同一执行身份的业务回执。
	DeleteScheduleVersion(context.Context, int, int, string) (*model.ScheduleWrite, error)
}

type executionKeyContext struct{}

// WithExecutionKey 将已持久化的后台执行身份注入工具上下文，不接受模型参数提供身份。
func WithExecutionKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, executionKeyContext{}, key)
}

// ScheduleDeleteTools 返回唯一危险且串行的日程删除工具。
func ScheduleDeleteTools(port ScheduleDeletes) []Tool {
	return []Tool{&deleteScheduleTool{port: port}}
}

type deleteScheduleTool struct{ port ScheduleDeletes }

// Info 声明唯一目标及版本参数，不向模型暴露后台执行键。
func (t *deleteScheduleTool) Info(context.Context) (*ToolInfo, error) {
	positive := 1
	return &ToolInfo{
		Name:        "delete_schedule",
		Description: "软删除唯一日程并按正式规则停止提醒、保留历史。先查询唯一 id 和 revision，以 expectedRevision 提交。用户明确且目标唯一时直接执行；目标有歧义先澄清。版本冲突或目标不存在如实报告，不自动重试，不使用新版本强制覆盖。批量意图拆为独立单条操作，有依赖时分模型轮次。",
		Dangerous:   true,
		Parameters: Schema{
			Type: "object",
			Properties: map[string]Property{
				"id": {
					Type:    "integer",
					Minimum: &positive,
				},
				"expectedRevision": {
					Type:    "integer",
					Minimum: &positive,
				},
			},
			Required: []string{"id", "expectedRevision"},
		},
	}, nil
}

// InvokableRun 传递后端身份调用正式删除，不自行读版本或拼装进度。
func (t *deleteScheduleTool) InvokableRun(ctx context.Context, raw string) (string, error) {
	key, _ := ctx.Value(executionKeyContext{}).(string)
	if key == "" {
		return "", &Failure{
			Code:    "tool_execution_failed",
			Message: "删除缺少后台执行身份，未执行",
		}
	}
	var input struct {
		ID               int `json:"id"`
		ExpectedRevision int `json:"expectedRevision"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", err
	}
	result, err := t.port.DeleteScheduleVersion(ctx, input.ID, input.ExpectedRevision, key)
	if err != nil {
		return "", scheduleFailure(err)
	}
	output := scheduleView(result.Schedule)
	output["deleted"] = result.Schedule.Deleted
	output["mutation"] = schedule.SummarizeScheduleWrite(result)
	return encodeResult(output)
}
