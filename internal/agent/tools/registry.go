// Package tools 提供代码内置工具和正式日程业务的窄端口。
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Definition 是发送给模型的标准函数定义，执行元数据仅保留在后端。
type Definition struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function 包含工具名称、说明和参数 schema。
type Function struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  Schema `json:"parameters"`
}

// Schema 描述工具的对象参数，拒绝未声明字段。
type Schema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required"`
	AdditionalProperties bool                `json:"additionalProperties"`
}

// Property 描述工具参数的类型、范围、允许值及字符串数组元素。
type Property struct {
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Minimum     *int      `json:"minimum,omitempty"`
	Maximum     *int      `json:"maximum,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
	Items       *Property `json:"items,omitempty"`
}

// Failure 是工具自身的公开错误，不作为运行 error 事件发送。
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error 使具体工具能通过标准 error 返回统一的公开工具错误。
func (f *Failure) Error() string { return f.Message }

// Tool 统一工具的元数据和 JSON 调用行为，方法职责参考 Eino 的同步工具接口。
type Tool interface {
	// Info 返回工具名称、说明、参数 schema 和后端执行元数据。
	Info(context.Context) (*ToolInfo, error)
	// InvokableRun 接收参数原文并返回 JSON 结果字符串；由注册中心统一校验及转换错误。
	InvokableRun(context.Context, string) (string, error)
}

// ToolInfo 定义模型描述与后端固定的安全性、调度方式。
type ToolInfo struct {
	Name        string
	Description string
	Parameters  Schema
	Dangerous   bool
	Parallel    bool
}

type registeredTool struct {
	info ToolInfo
	tool Tool
}

// Registry 在启动时统一注册全部工具，运行时只按注册信息分发。
type Registry struct {
	entries []registeredTool
}

// NewRegistry 注册指定工具，重复名称或无效定义使装配失败。
func NewRegistry(ctx context.Context, toolList ...Tool) (*Registry, error) {
	registry := &Registry{}
	for _, tool := range toolList {
		if err := registry.Register(ctx, tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register 在开始服务前注册工具，名称唯一且只支持当前可校验的参数 schema。
func (r *Registry) Register(ctx context.Context, tool Tool) error {
	if tool == nil {
		return errors.New("工具不能为空")
	}
	info, err := tool.Info(ctx)
	if err != nil {
		return fmt.Errorf("读取工具定义：%w", err)
	}
	if info == nil || strings.TrimSpace(info.Name) == "" || strings.TrimSpace(info.Description) == "" || info.Parameters.Type != "object" || info.Parameters.AdditionalProperties {
		return errors.New("工具必须提供名称、说明和严格对象参数 schema")
	}
	if _, exists := r.Lookup(info.Name); exists {
		return fmt.Errorf("工具名称重复：%s", info.Name)
	}
	for key, property := range info.Parameters.Properties {
		if key == "" || property.Type != "integer" && property.Type != "string" && property.Type != "boolean" && property.Type != "array" {
			return fmt.Errorf("工具 %s 的字段 %s 类型不受支持", info.Name, key)
		}
		if property.Type == "array" && (property.Items == nil || property.Items.Type != "string") {
			return fmt.Errorf("工具 %s 的字段 %s 只支持字符串数组", info.Name, key)
		}
	}
	for _, key := range info.Parameters.Required {
		if _, ok := info.Parameters.Properties[key]; !ok {
			return fmt.Errorf("工具 %s 的必填字段 %s 未定义", info.Name, key)
		}
	}
	r.entries = append(r.entries, registeredTool{
		info: *info,
		tool: tool,
	})
	return nil
}

// Definitions 返回所有已注册工具的模型描述，不包含后端安全与调度元数据。
func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	result := make([]Definition, 0, len(r.entries))
	for _, entry := range r.entries {
		result = append(result, Definition{
			Type: "function",
			Function: Function{
				Name:        entry.info.Name,
				Description: entry.info.Description,
				Parameters:  entry.info.Parameters,
			},
		})
	}
	return result
}

// Lookup 查询已注册工具的固定元数据，未注册工具不能进入执行。
func (r *Registry) Lookup(name string) (ToolInfo, bool) {
	if r != nil {
		for _, entry := range r.entries {
			if entry.info.Name == name {
				return entry.info, true
			}
		}
	}
	return ToolInfo{}, false
}

// Argument 仅生成事件的对象投影，不校验或修复实际参数原文。
func Argument(raw string) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) != nil || object == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(raw)
}

// Execute 在工具开始事务提交后解析、校验并执行正式业务。
func (r *Registry) Execute(ctx context.Context, name, raw string) (json.RawMessage, *Failure) {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, &Failure{
			Code:    "tool_argument_parse_error",
			Message: "工具参数不是完整合法 JSON，未执行，请重新提供完整参数",
		}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, validation("参数应为 JSON 对象")
	}
	entry, ok := r.Lookup(name)
	if !ok {
		return nil, &Failure{
			Code:    "tool_not_found",
			Message: "工具不存在：" + name,
		}
	}
	schema := entry.Parameters
	for _, key := range schema.Required {
		if _, ok := object[key]; !ok {
			return nil, validation("缺少必填字段 " + key)
		}
	}
	for key, value := range object {
		property, ok := schema.Properties[key]
		if !ok {
			return nil, validation("不支持的字段 " + key)
		}
		switch property.Type {
		case "boolean":
			if _, ok := value.(bool); !ok {
				return nil, validation(key + " 必须为布尔值")
			}
		case "array":
			items, ok := value.([]any)
			if !ok {
				return nil, validation(key + " 必须为字符串数组")
			}
			for _, item := range items {
				if _, ok := item.(string); !ok {
					return nil, validation(key + " 必须为字符串数组")
				}
			}
		case "integer":
			number, ok := value.(float64)
			if !ok || math.Trunc(number) != number || number >= float64(math.MaxInt) || property.Minimum != nil && number < float64(*property.Minimum) || property.Maximum != nil && number > float64(*property.Maximum) {
				return nil, validation(key + " 必须为 schema 范围内的整数")
			}
		case "string":
			text, ok := value.(string)
			if !ok {
				return nil, validation(key + " 必须为字符串")
			}
			if len(property.Enum) > 0 {
				valid := false
				for _, allowed := range property.Enum {
					valid = valid || text == allowed
				}
				if !valid {
					return nil, validation(key + " 不在允许值中")
				}
			}
		}
	}
	var implementation Tool
	for _, registered := range r.entries {
		if registered.info.Name == name {
			implementation = registered.tool
			break
		}
	}
	output, err := implementation.InvokableRun(ctx, raw)
	var result json.RawMessage
	if output != "" && json.Valid([]byte(output)) {
		result = json.RawMessage(output)
	}
	if err != nil {
		var failure *Failure
		if errors.As(err, &failure) {
			return result, failure
		}
		code, message := "tool_execution_failed", "工具执行失败"
		if errors.Is(err, context.DeadlineExceeded) {
			code, message = "tool_timeout", "工具执行超时"
		}
		return result, &Failure{
			Code:    code,
			Message: message,
		}
	}
	if !json.Valid([]byte(output)) {
		return nil, &Failure{
			Code:    "tool_execution_failed",
			Message: "工具返回的结果不是合法 JSON",
		}
	}
	return json.RawMessage(output), nil
}

func validation(message string) *Failure {
	return &Failure{
		Code:    "tool_argument_validation_error",
		Message: message,
	}
}
