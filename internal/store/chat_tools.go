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
		call = call.WithResponseAccepted()
		if err := saveCall(ctx, tx, call, true); err != nil {
			return err
		}
		if err := appendChatEvents(ctx, tx, call); err != nil {
			return err
		}
		return checkpoint(ctx, tx, call)
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

// BeginTool 沿已提交进度原子登记 running、原参数、开始事件及开始侧 checkpoint。
func (s *chatStore) BeginTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, chat.ToolCall, error) {
	originalCall, originalTool := call, tool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		call, err = readCheckpoint(ctx, tx, call)
		if err != nil {
			return err
		}
		if tool.ID != 0 {
			// 恢复只读查询复用原身份，不再发送开始事件。
			_, err := tx.ExecContext(ctx, `UPDATE ai_tool_calls SET status='running',retry_count=? WHERE id=? AND model_call_id=?`, tool.RetryCount, tool.ID, call.ID)
			if err != nil {
				return err
			}
		} else {
			result, err := tx.ExecContext(ctx, `INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,status,max_retry,created_at) VALUES(?,?,?,?,?,?,'running',?,?)`, call.RunID, call.SessionID, call.ID, tool.Request.ID, tool.Request.Function.Name, jsonValue(tool.Request.Function.Arguments), tool.MaxRetry, time.Now().UnixNano())
			if err != nil {
				return err
			}
			tool.ID, err = result.LastInsertId()
			if err != nil {
				return err
			}
			if err = appendChatEvent(ctx, tx, call, toolStartEvent(tool)); err != nil {
				return err
			}
		}
		call = call.WithToolStarted(tool)
		return checkpoint(ctx, tx, call)
	})
	if err != nil {
		return originalCall, originalTool, err
	}
	return call, tool, nil
}

// FinishTool 同事务合并实际结果、原始 JSON、配对事件和进度，提交成功才返回新快照。
func (s *chatStore) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	original := call
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		call, err = finishTool(ctx, tx, call, tool)
		return err
	})
	if err != nil {
		return original, err
	}
	return call, nil
}

func finishTool(ctx context.Context, tx *sql.Tx, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	// 正常结果与失败收尾都从事务内的完整快照合并，不采用调用方预先拼装的进度。
	call, err := readCheckpoint(ctx, tx, call)
	if err != nil {
		return call, err
	}
	// 提交结果不明后的保存重试只复用已提交结果，不能再次生成结果事件。
	var state string
	err = tx.QueryRowContext(ctx, `SELECT status FROM ai_tool_calls WHERE model_call_id=? AND tool_call_id=?`, call.ID, tool.Request.ID).Scan(&state)
	if err != nil && err != sql.ErrNoRows {
		return call, err
	}
	if state == "succeeded" || state == "failed" || state == "skipped" {
		return call, nil
	}
	if tool.ID == 0 {
		// 未开始便取消或因持久化故障收尾时直接保存最终事实，不伪造 running/tool_use。
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,status,max_retry,error,finished_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, call.RunID, call.SessionID, call.ID, tool.Request.ID, tool.Request.Function.Name, jsonValue(tool.Request.Function.Arguments), tool.State, tool.MaxRetry, jsonValue(tool.Error), time.Now().UnixNano(), time.Now().UnixNano())
		if err != nil {
			return call, err
		}
		if err = appendChatEvent(ctx, tx, call, toolStartEvent(tool)); err != nil {
			return call, err
		}
	} else {
		_, err := tx.ExecContext(ctx, `UPDATE ai_tool_calls SET output=?,error=?,status=?,finished_at=? WHERE id=? AND model_call_id=?`, jsonValue(tool.Output), jsonValue(tool.Error), tool.State, time.Now().UnixNano(), tool.ID, call.ID)
		if err != nil {
			return call, err
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
		return call, err
	}
	call = call.WithToolResult(tool)
	return call, checkpoint(ctx, tx, call)
}

// closeToolBatch 在运行异常收尾事务中补齐已接纳但尚无结果的原调用，避免终态历史失配。
func closeToolBatch(ctx context.Context, tx *sql.Tx, call chat.ModelCall, run chat.Record) (chat.ModelCall, error) {
	if call.State != chat.StateSucceeded || call.Output == nil || len(call.Output.Message.ToolCalls) == 0 || run.State == chat.StateSucceeded {
		return call, nil
	}
	var requests []chat.ToolRequest
	if err := json.Unmarshal(call.Output.Message.ToolCalls, &requests); err != nil {
		return call, err
	}
	var err error
	call, err = readCheckpoint(ctx, tx, call)
	if err != nil {
		return call, err
	}
	for _, request := range requests {
		if call.ToolCompleted(request.ID) {
			continue
		}
		tool := chat.ToolCall{
			ModelCallID: call.ID,
			Request:     request,
			State:       "failed",
			MaxRetry:    3,
		}
		err := tx.QueryRowContext(ctx, `SELECT id,max_retry,retry_count FROM ai_tool_calls WHERE model_call_id=? AND tool_call_id=?`, call.ID, request.ID).Scan(&tool.ID, &tool.MaxRetry, &tool.RetryCount)
		if err != nil && err != sql.ErrNoRows {
			return call, err
		}
		tool.Error = &chat.RunError{
			Code:    "tool_result_unconfirmed",
			Message: "持久化故障中断处理，未取得可确认的工具结果",
		}
		if run.State == chat.StateCancelled {
			tool.State = "skipped"
			tool.Error = &chat.RunError{
				Code:    "tool_skipped",
				Message: "运行已取消，未启动或未恢复此工具",
			}
		}
		call, err = finishTool(ctx, tx, call, tool)
		if err != nil {
			return call, err
		}
	}
	return call, nil
}

func readCheckpoint(ctx context.Context, tx *sql.Tx, call chat.ModelCall) (chat.ModelCall, error) {
	var stage, raw string
	if err := tx.QueryRowContext(ctx, `SELECT status,state FROM ai_checkpoints WHERE run_id=? AND model_call_id=?`, call.RunID, call.ID).Scan(&stage, &raw); err != nil {
		return call, err
	}
	var state chat.CheckpointState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return call, err
	}
	return call.WithCheckpoint(stage, state), nil
}
