package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
)

// ReadUsage 只读指定运行的独立 usage 列；SQL NULL 保持未知，不从 output 或事件补值。
func (s *chatStore) ReadUsage(ctx context.Context, runID string) ([]chat.CallUsage, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id,usage FROM ai_model_calls WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := []chat.CallUsage{}
	for rows.Next() {
		var call chat.CallUsage
		var usage sql.NullString
		if err := rows.Scan(&call.ModelCallID, &usage); err != nil {
			return nil, err
		}
		if usage.Valid {
			call.Usage = json.RawMessage(usage.String)
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}
