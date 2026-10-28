package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// ScheduleQueries 只暴露正式日程查询，不允许工具访问数据库。
type ScheduleQueries interface {
	// GetSchedule 返回未删除的日程规则及当前版本。
	GetSchedule(context.Context, int) (*model.Schedule, error)
	// ListSchedules 按正式查询条件读取日程规则。
	ListSchedules(context.Context, ...model.QueryOption) ([]*model.Schedule, error)
	// QueryOccurrences 返回指定北京时间日期的发生情况与游标。
	QueryOccurrences(context.Context, string, string, time.Time) (model.OccurrencePage, error)
}

// ScheduleTools 返回代码内置的正式日程查询工具，由入口统一注册。
func ScheduleTools(port ScheduleQueries) []Tool {
	return []Tool{
		&getScheduleTool{port: port},
		&listSchedulesTool{port: port},
		&queryOccurrencesTool{port: port},
	}
}

type getScheduleTool struct{ port ScheduleQueries }

// Info 返回单条日程查询的固定说明、参数及执行元数据。
func (t *getScheduleTool) Info(context.Context) (*ToolInfo, error) {
	positive := 1
	return &ToolInfo{
		Name:        "get_schedule",
		Description: "按日程 ID 查询未删除的完整规则及当前 revision；不存在时返回真实错误。",
		Parallel:    true,
		Parameters: Schema{
			Type: "object",
			Properties: map[string]Property{
				"id": {
					Type:    "integer",
					Minimum: &positive,
				},
			},
			Required: []string{"id"},
		},
	}, nil
}

// InvokableRun 通过正式入口查询日程，参数由注册中心先完成 schema 校验。
func (t *getScheduleTool) InvokableRun(ctx context.Context, raw string) (string, error) {
	var input struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", err
	}
	value, err := t.port.GetSchedule(ctx, input.ID)
	if err != nil {
		return "", scheduleFailure(err)
	}
	return encodeResult(scheduleView(value))
}

type listSchedulesTool struct{ port ScheduleQueries }

// Info 返回分页日程查询的固定说明、参数及执行元数据。
func (t *listSchedulesTool) Info(context.Context) (*ToolInfo, error) {
	positive, zero, fifty := 1, 0, 50
	return &ToolInfo{
		Name:        "list_schedules",
		Description: "分页查询未删除日程规则及 revision；支持名称包含过滤和 single/period 类型，limit 默认 50，offset 默认 0。",
		Parallel:    true,
		Parameters: Schema{
			Type: "object",
			Properties: map[string]Property{
				"name": {Type: "string"},
				"type": {
					Type: "string",
					Enum: []string{"single", "period"},
				},
				"limit": {
					Type:    "integer",
					Minimum: &positive,
					Maximum: &fifty,
				},
				"offset": {
					Type:    "integer",
					Minimum: &zero,
				},
			},
			Required: []string{},
		},
	}, nil
}

// InvokableRun 通过正式入口执行分页查询并返回 JSON 数组，空结果保持空数组。
func (t *listSchedulesTool) InvokableRun(ctx context.Context, raw string) (string, error) {
	input := struct {
		Name   *string             `json:"name"`
		Type   *model.ScheduleType `json:"type"`
		Limit  int                 `json:"limit"`
		Offset int                 `json:"offset"`
	}{Limit: 50}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", err
	}
	options := []model.QueryOption{model.WithScheduleLimit(input.Limit), model.WithScheduleOffset(input.Offset)}
	if input.Name != nil {
		options = append(options, model.WithScheduleName(*input.Name))
	}
	if input.Type != nil {
		options = append(options, model.WithScheduleType(*input.Type))
	}
	values, err := t.port.ListSchedules(ctx, options...)
	if err != nil {
		return "", scheduleFailure(err)
	}
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, scheduleView(value))
	}
	return encodeResult(result)
}

type queryOccurrencesTool struct{ port ScheduleQueries }

// Info 返回日期发生情况查询的固定说明、参数及执行元数据。
func (t *queryOccurrencesTool) Info(context.Context) (*ToolInfo, error) {
	return &ToolInfo{
		Name:        "query_occurrences",
		Description: "分页查询指定北京时间日期的真实提醒及未来规则投影。date 支持 YYYY-MM-DD、今天、明天、后天、昨天；相对日期在工具解析时计算。cursor 采用前页 nextCursor。",
		Parallel:    true,
		Parameters: Schema{
			Type: "object",
			Properties: map[string]Property{
				"date":   {Type: "string"},
				"cursor": {Type: "string"},
			},
			Required: []string{"date"},
		},
	}, nil
}

// InvokableRun 在本次实际解析时按北京时间解释相对日期，复用正式发生情况查询。
func (t *queryOccurrencesTool) InvokableRun(ctx context.Context, raw string) (string, error) {
	var input struct {
		Date   string `json:"date"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", err
	}
	now := time.Now().In(model.BeijingLocation())
	if days, ok := map[string]int{"今天": 0, "明天": 1, "后天": 2, "昨天": -1}[input.Date]; ok {
		input.Date = now.AddDate(0, 0, days).Format("2006-01-02")
	}
	value, err := t.port.QueryOccurrences(ctx, input.Date, input.Cursor, now)
	if err != nil {
		return "", scheduleFailure(err)
	}
	return encodeResult(value)
}

func encodeResult(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func scheduleFailure(err error) error {
	switch {
	case errors.Is(err, model.ErrScheduleConflict):
		return &Failure{
			Code:    "revision_conflict",
			Message: "日程已被修改，版本冲突；请报告冲突，不自动重试或覆盖",
		}
	case errors.Is(err, schedule.ErrInvalidSchedule):
		return &Failure{
			Code:    "invalid_schedule",
			Message: "日程规则不符合正式业务校验，未写入；请澄清或调整参数",
		}
	case errors.Is(err, schedule.ErrScheduleNotFound), errors.Is(err, model.ErrRecordNotFound):
		return &Failure{
			Code:    "schedule_not_found",
			Message: "日程不存在或已删除",
		}
	case errors.Is(err, schedule.ErrInvalidQuery):
		return &Failure{
			Code:    "invalid_query",
			Message: "日期或游标无效",
		}
	default:
		return err
	}
}

func scheduleView(value *model.Schedule) map[string]any {
	result := map[string]any{
		"id": value.ID, "name": value.Name, "revision": value.Revision, "type": value.Type,
		"description": value.Description, "cancelled": value.Cancelled,
		"createdAt": value.CreatedAt.In(model.BeijingLocation()), "updatedAt": value.UpdatedAt.In(model.BeijingLocation()),
		"single": nil, "period": nil,
	}
	if value.Single != nil {
		result["single"] = map[string]any{"notifyTime": value.Single.NotifyTime.In(model.BeijingLocation())}
	}
	if value.Period != nil {
		result["period"] = map[string]any{"open": value.Period.Open, "cron": value.Period.Cron}
	}
	return result
}
