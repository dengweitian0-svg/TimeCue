package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

// AcceptResponse 原子接纳完整模型响应及尾批，工具执行只能在提交后推进。
func (s *chatStore) AcceptResponse(ctx context.Context, call chat.ModelCall) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := saveCall(ctx, tx, call, true); err != nil {
			return err
		}
		if err := appendChatEvents(ctx, tx, call); err != nil {
			return err
		}
		return checkpoint(ctx, tx, call, "model_call_end")
	})
}

// ReadTools 只回读当前模型回复的工具记录，不跨调用按同值供应商 ID 匹配。
func (s *chatStore) ReadTools(ctx context.Context, modelCall int64) ([]chat.ToolCall, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id,tool_call_id,name,input,output,error,status,max_retry,retry_count FROM ai_tool_calls WHERE model_call_id=? ORDER BY id`, modelCall)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []chat.ToolCall{}
	for rows.Next() {
		call := chat.ToolCall{ModelCallID: modelCall}
		var input string
		var output, failure sql.NullString
		if err = rows.Scan(&call.ID, &call.Request.ID, &call.Request.Function.Name, &input, &output, &failure, &call.State, &call.MaxRetry, &call.RetryCount); err != nil {
			return nil, err
		}
		call.Request.Type = "function"
		if err = json.Unmarshal([]byte(input), &call.Request.Function.Arguments); err != nil {
			return nil, err
		}
		if output.Valid {
			call.Output = json.RawMessage(output.String)
		}
		if failure.Valid {
			if err = json.Unmarshal([]byte(failure.String), &call.Error); err != nil {
				return nil, err
			}
		}
		result = append(result, call)
	}
	return result, rows.Err()
}

func toolStartEvent(tool chat.ToolCall) chat.Event {
	data, _ := json.Marshal(chat.ToolStartData{
		Function: []chat.ToolStartFunction{
			{
				ID:       tool.Request.ID,
				Name:     tool.Request.Function.Name,
				Argument: tools.Argument(tool.Request.Function.Arguments),
			},
		},
	})
	return chat.Event{
		Type: chat.EventToolStart,
		Data: data,
	}
}

// BeginTool 在实际处理前登记 running、原参数、开始事件与 tool_use checkpoint。
func (s *chatStore) BeginTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (int64, error) {
	var id int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if tool.ID != 0 {
			// 恢复只读查询复用原身份，不再发送开始事件。
			_, err := tx.ExecContext(ctx, `UPDATE ai_tool_calls SET status='running',retry_count=? WHERE id=? AND model_call_id=?`, tool.RetryCount, tool.ID, call.ID)
			if err != nil {
				return err
			}
			id = tool.ID
		} else {
			result, err := tx.ExecContext(ctx, `INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,status,max_retry,created_at) VALUES(?,?,?,?,?,?,'running',?,?)`, call.RunID, call.SessionID, call.ID, tool.Request.ID, tool.Request.Function.Name, jsonValue(tool.Request.Function.Arguments), tool.MaxRetry, time.Now().UnixNano())
			if err != nil {
				return err
			}
			id, err = result.LastInsertId()
			if err != nil {
				return err
			}
			if err = appendChatEvent(ctx, tx, call, toolStartEvent(tool)); err != nil {
				return err
			}
		}
		return checkpoint(ctx, tx, call, "tool_start")
	})
	return id, err
}

// FinishTool 同事务保存工具最终结果、原始 JSON、配对事件和逐项 checkpoint 进度。
func (s *chatStore) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) error {
	return s.transaction(ctx, func(tx *sql.Tx) error { return finishTool(ctx, tx, call, tool) })
}

func finishTool(ctx context.Context, tx *sql.Tx, call chat.ModelCall, tool chat.ToolCall) error {
	// 提交结果不明后的保存重试只复用已提交结果，不能再次生成结果事件。
	var state string
	err := tx.QueryRowContext(ctx, `SELECT status FROM ai_tool_calls WHERE model_call_id=? AND tool_call_id=?`, call.ID, tool.Request.ID).Scan(&state)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if state == "succeeded" || state == "failed" || state == "skipped" {
		return nil
	}
	if tool.ID == 0 {
		// 未开始便取消时直接保存 skipped，补齐开始与结果，不伪造 running/tool_use。
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,status,max_retry,error,finished_at,created_at) VALUES(?,?,?,?,?,?,'skipped',?,?,?,?)`, call.RunID, call.SessionID, call.ID, tool.Request.ID, tool.Request.Function.Name, jsonValue(tool.Request.Function.Arguments), tool.MaxRetry, jsonValue(tool.Error), time.Now().UnixNano(), time.Now().UnixNano())
		if err != nil {
			return err
		}
		if err = appendChatEvent(ctx, tx, call, toolStartEvent(tool)); err != nil {
			return err
		}
	} else {
		_, err := tx.ExecContext(ctx, `UPDATE ai_tool_calls SET output=?,error=?,status=?,finished_at=? WHERE id=? AND model_call_id=?`, jsonValue(tool.Output), jsonValue(tool.Error), tool.State, time.Now().UnixNano(), tool.ID, call.ID)
		if err != nil {
			return err
		}
	}
	data, _ := json.Marshal(chat.ToolResultData{
		ToolCallID: tool.Request.ID,
		Output:     tool.Output,
		Error:      tool.Error,
	})
	if err := appendChatEvent(ctx, tx, call, chat.Event{
		Type: chat.EventToolResult,
		Data: data,
	}); err != nil {
		return err
	}
	return checkpoint(ctx, tx, call, call.CheckpointStage)
}
