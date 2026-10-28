package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	return readTools(ctx, db, modelCall)
}

func readTools(ctx context.Context, reader schemaReader, modelCall int64) ([]chat.ToolCall, error) {
	rows, err := reader.QueryContext(ctx, `SELECT id,tool_call_id,name,input,output,error,status,max_retry,retry_count,idempotency_key,next_retry_at FROM ai_tool_calls WHERE model_call_id=? ORDER BY id`, modelCall)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []chat.ToolCall{}
	for rows.Next() {
		call := chat.ToolCall{ModelCallID: modelCall}
		var input string
		var output, failure, key sql.NullString
		var next sql.NullInt64
		if err = rows.Scan(&call.ID, &call.Request.ID, &call.Request.Function.Name, &input, &output, &failure, &call.State, &call.MaxRetry, &call.RetryCount, &key, &next); err != nil {
			return nil, err
		}
		if next.Valid {
			at := time.Unix(0, next.Int64)
			call.NextRetryAt = &at
		}
		call.IdempotencyKey = key.String
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
			// 恢复复用原身份和已登记执行键，不再发送开始事件。
			_, err := tx.ExecContext(ctx, `UPDATE ai_tool_calls SET status='running',retry_count=?,error=NULL,output=NULL,next_retry_at=NULL WHERE id=? AND model_call_id=?`, tool.RetryCount, tool.ID, call.ID)
			if err != nil {
				return err
			}
		} else {
			var key any
			if tool.IdempotencyKey != "" {
				key = tool.IdempotencyKey
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,status,max_retry,created_at,idempotency_key) VALUES(?,?,?,?,?,?,'running',?,?,?)`, call.RunID, call.SessionID, call.ID, tool.Request.ID, tool.Request.Function.Name, jsonValue(tool.Request.Function.Arguments), tool.MaxRetry, time.Now().UnixNano(), key)
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

// WaitToolRetry 只保存一次尝试的暂时错误和退避，不生成最终消息或结果事件。
func (s *chatStore) WaitToolRetry(ctx context.Context, tool chat.ToolCall) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE ai_tool_calls SET status='retry_wait',output=?,error=?,next_retry_at=? WHERE id=? AND model_call_id=? AND status='running' AND retry_count=?`, jsonValue(tool.Output), jsonValue(tool.Error), timeValue(tool.NextRetryAt), tool.ID, tool.ModelCallID, tool.RetryCount)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("工具尝试已结算或身份不匹配: %d", tool.ID)
		}
		return nil
	})
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
	saved, err := saveToolResult(ctx, tx, call, tool)
	if err != nil || !saved {
		return call, err
	}
	call = call.WithToolResult(tool)
	return call, checkpoint(ctx, tx, call)
}

// saveToolResult 只核验与保存工具事实和事件，不决定进度或异常结果。
func saveToolResult(ctx context.Context, tx *sql.Tx, call chat.ModelCall, tool chat.ToolCall) (bool, error) {
	// 提交结果不明后的保存重试只复用已提交事实，不能再次生成结果事件。
	var id int64
	var state string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM ai_tool_calls WHERE model_call_id=? AND tool_call_id=?`, call.ID, tool.Request.ID).Scan(&id, &state)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if state == "succeeded" || state == "failed" || state == "skipped" {
		return false, nil
	}
	if err == sql.ErrNoRows {
		// 未开始的最终事实直接入库，不伪造 running/tool_use；保留原有展示配对事件。
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,output,status,max_retry,error,finished_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, call.RunID, call.SessionID, call.ID, tool.Request.ID, tool.Request.Function.Name, jsonValue(tool.Request.Function.Arguments), jsonValue(tool.Output), tool.State, tool.MaxRetry, jsonValue(tool.Error), time.Now().UnixNano(), time.Now().UnixNano())
		if err != nil {
			return false, err
		}
		if err = appendChatEvent(ctx, tx, call, toolStartEvent(tool)); err != nil {
			return false, err
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE ai_tool_calls SET output=?,error=?,status=?,finished_at=?,next_retry_at=NULL WHERE id=? AND model_call_id=?`, jsonValue(tool.Output), jsonValue(tool.Error), tool.State, time.Now().UnixNano(), id, call.ID)
		if err != nil {
			return false, err
		}
	}
	data, _ := json.Marshal(chat.ToolResultData{
		ToolCallID: tool.Request.ID,
		Output:     tool.Output,
		Error:      tool.Error,
	})
	err = appendChatEvent(ctx, tx, call, chat.Event{
		Type: chat.EventToolResult,
		Data: data,
	})
	return err == nil, err
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
