package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// ScheduleWrites 只暴露正式规则回读和带版本保护、提醒联动的写入入口。
type ScheduleWrites interface {
	// GetSchedule 返回修改前的完整规则，不以名称猜测写入目标。
	GetSchedule(context.Context, int) (*model.Schedule, error)
	// WriteSchedule 在正式业务事务内校验版本并更新日程与提醒。
	WriteSchedule(context.Context, model.Schedule, int, string) (*model.ScheduleWrite, error)
}

// ScheduleWriteTools 返回非危险、串行的创建和修改工具；now 为空时使用实时时钟。
func ScheduleWriteTools(port ScheduleWrites, now func() time.Time) []Tool {
	if now == nil {
		now = time.Now
	}
	return []Tool{
		&writeScheduleTool{
			port: port,
			now:  now,
		},
		&writeScheduleTool{
			port:   port,
			now:    now,
			update: true,
		},
	}
}

type writeScheduleTool struct {
	port   ScheduleWrites
	now    func() time.Time
	update bool
}

// Info 返回写入工具定义；版本和唯一目标由模型先查询取得，禁止猜测或自动覆盖冲突。
func (t *writeScheduleTool) Info(context.Context) (*ToolInfo, error) {
	properties := map[string]Property{
		"name": {
			Type:        "string",
			Description: "日程名称，不可为空",
		},
		"description": {
			Type:        "string",
			Description: "可选说明；修改时省略保留，空字符串清空",
		},
		"notifyTime": {
			Type:        "string",
			Description: "单次时间：RFC3339、YYYY-MM-DD HH:mm 或 今天/明天/后天/昨天 HH:mm；相对日期必须保留原词，由后端按实际解析时北京时间转换。不能只给日期或时刻，不能猜测。",
		},
		"cron": {
			Type:        "array",
			Items:       &Property{Type: "string"},
			Description: "周期规则的五字段表达式数组：每天 '分 时 * * *'、每周 '分 时 * * 1,3'（周一=1，周日=7）、每月 '分 时 1,15 * *'；不可为空",
		},
		"open": {
			Type:        "boolean",
			Description: "周期规则是否启用，创建周期日程必须明确提供",
		},
	}
	info := &ToolInfo{
		Name:        "create_schedule",
		Description: "创建一条真实日程并按正式规则生成提醒。明确完整的创建指令直接执行；缺名称、日期、时刻或周期规则先澄清。single 必须提供 notifyTime 且不能带 cron/open；period 必须提供 cron/open 且不能带 notifyTime。不按名称或时间去重；多条操作独立成功或失败，有依赖时取得本次结果后下一模型轮次再调用。返回真实完整规则、revision 及提醒变更。",
		Parameters: Schema{
			Type:       "object",
			Properties: properties,
			Required:   []string{"name", "type"},
		},
	}
	if t.update {
		positive := 1
		info.Name = "update_schedule"
		info.Description = "修改唯一日程并联动提醒。先查询唯一目标的 id 和 revision，必填 expectedRevision；仅提供需修改字段，其他规则保留，不允许转换 single/period 类型。信息不足或多目标先澄清；明确完整且唯一时直接执行。版本冲突或不存在如实报告，不自动重试或用新版本覆盖。多条独立执行，有依赖须等待前次结果后分模型轮次调用。"
		properties["id"] = Property{
			Type:    "integer",
			Minimum: &positive,
		}
		properties["expectedRevision"] = Property{
			Type:    "integer",
			Minimum: &positive,
		}
		info.Parameters.Required = []string{"id", "expectedRevision"}
	} else {
		properties["type"] = Property{
			Type: "string",
			Enum: []string{"single", "period"},
		}
	}
	return info, nil
}

type scheduleWriteInput struct {
	ID               int                `json:"id"`
	ExpectedRevision int                `json:"expectedRevision"`
	Name             *string            `json:"name"`
	Type             model.ScheduleType `json:"type"`
	Description      *string            `json:"description"`
	NotifyTime       *string            `json:"notifyTime"`
	Cron             *[]string          `json:"cron"`
	Open             *bool              `json:"open"`
}

// InvokableRun 将参数转换成正式规则并使用空业务 key 写入，不自行维护进度或推断幂等。
func (t *writeScheduleTool) InvokableRun(ctx context.Context, raw string) (string, error) {
	var input scheduleWriteInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", err
	}
	value := model.Schedule{Type: input.Type}
	if t.update {
		if input.Name == nil && input.Description == nil && input.NotifyTime == nil && input.Cron == nil && input.Open == nil {
			return "", validation("修改至少需要一个规则字段；信息不足请先澄清")
		}
		current, err := t.port.GetSchedule(ctx, input.ID)
		if err != nil {
			return "", scheduleFailure(err)
		}
		value = *current
	}
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.Description != nil {
		value.Description = input.Description
	}
	switch value.Type {
	case model.SingleSchedule:
		if input.Cron != nil || input.Open != nil || !t.update && input.NotifyTime == nil {
			return "", validation("single 需要 notifyTime 且不能包含 cron/open；缺时间先澄清")
		}
		if input.NotifyTime != nil {
			at, err := parseScheduleTime(*input.NotifyTime, t.now)
			if err != nil {
				return "", err
			}
			value.Single = &model.Single{NotifyTime: at}
		}
	case model.PeriodSchedule:
		if input.NotifyTime != nil || !t.update && (input.Cron == nil || input.Open == nil) {
			return "", validation("period 需要 cron/open 且不能包含 notifyTime；缺规则先澄清")
		}
		period := model.Period{}
		if value.Period != nil {
			period = *value.Period
		}
		if input.Cron != nil {
			period.Cron = *input.Cron
		}
		if input.Open != nil {
			period.Open = *input.Open
		}
		value.Period = &period
	}
	result, err := t.port.WriteSchedule(ctx, value, input.ExpectedRevision, "")
	if err != nil {
		return "", scheduleFailure(err)
	}
	output := scheduleView(result.Schedule)
	output["mutation"] = schedule.SummarizeScheduleWrite(result)
	return encodeResult(output)
}
