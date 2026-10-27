package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/migrations"
)

// chatStore 用同一 SQLite 连接实现独立持久化端口，保持跨表事务原子性。
type chatStore struct{ db *Sqlite }

// NewChatStorage 分别提供运行、会话、模型执行及事件查询端口，不打开或迁移数据库。
func NewChatStorage(db *Sqlite) chat.Storage {
	adapter := &chatStore{db: db}
	return chat.Storage{
		Runs:      adapter,
		Sessions:  adapter,
		Execution: adapter,
		Events:    adapter,
	}
}

func checkAIChatSchema(ctx context.Context, db schemaReader) error {
	normalize := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || r == ';' {
				return -1
			}
			return unicode.ToLower(r)
		}, s)
	}
	for _, statement := range strings.Split(migrations.AIChat, ";") {
		fields := strings.Fields(statement)
		if len(fields) < 3 || fields[0] != "CREATE" {
			continue
		}
		var ddl string
		if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, fields[2]).Scan(&ddl); err != nil {
			return fmt.Errorf("%w: missing %s", ErrSchemaInvalid, fields[2])
		}
		if normalize(ddl) != normalize(statement) {
			return fmt.Errorf("%w: incompatible %s", ErrSchemaInvalid, fields[2])
		}
	}
	return nil
}

func (s *chatStore) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	db, err := s.db.handle()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func jsonValue(v any) any {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return nil
	}
	return string(b)
}
func timeValue(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.UnixNano()
}
func stringValue(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

const runColumns = `r.run_id,r.session_id,r.provider,r.model,r.input,r.status,r.output,r.error,r.idempotency_key,r.created_at,r.finished_at`

func readRecord(row *sql.Row) (chat.Record, error) {
	var r chat.Record
	var output, failure sql.NullString
	var created int64
	var finished sql.NullInt64
	err := row.Scan(&r.RunID, &r.SessionID, &r.Provider, &r.Model, &r.Input, &r.State, &output, &failure, &r.IdempotencyKey, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return r, chat.ErrRunNotFound
	}
	if err != nil {
		return r, err
	}
	r.Status = r.State.Public()
	r.CreatedAt = time.Unix(0, created).In(model.BeijingLocation())
	if output.Valid {
		r.Output = &output.String
	}
	if failure.Valid {
		if err = json.Unmarshal([]byte(failure.String), &r.Error); err != nil {
			return r, err
		}
	}
	if finished.Valid {
		t := time.Unix(0, finished.Int64).In(model.BeijingLocation())
		r.FinishedAt = &t
	}
	return r, nil
}

// FindReplay 查询幂等回执，同时拒绝复用已删除会话。
func (s *chatStore) FindReplay(ctx context.Context, session, key string) (chat.Record, error) {
	db, err := s.db.handle()
	if err != nil {
		return chat.Record{}, err
	}
	var deleted int
	err = db.QueryRowContext(ctx, `SELECT deleted FROM ai_sessions WHERE session_id=?`, session).Scan(&deleted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return chat.Record{}, err
	}
	if deleted == 1 {
		return chat.Record{}, chat.ErrSessionDeleted
	}
	return readRecord(db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM ai_runs r WHERE session_id=? AND idempotency_key=?`, session, key))
}

// Create 原子登记会话、运行和开始事件。
func (s *chatStore) Create(ctx context.Context, r chat.Record) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_sessions(session_id,created_at,updated_at) VALUES(?,?,?) ON CONFLICT(session_id) DO NOTHING`, r.SessionID, r.CreatedAt.UnixNano(), r.CreatedAt.UnixNano())
		if err != nil {
			return err
		}
		var deleted int
		if err = tx.QueryRowContext(ctx, `SELECT deleted FROM ai_sessions WHERE session_id=?`, r.SessionID).Scan(&deleted); err != nil {
			return err
		}
		if deleted == 1 {
			return chat.ErrSessionDeleted
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_runs(run_id,session_id,provider,model,input,status,idempotency_key,created_at) VALUES(?,?,?,?,?,?,?,?)`, r.RunID, r.SessionID, r.Provider, r.Model, r.Input, r.State, r.IdempotencyKey, r.CreatedAt.UnixNano())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE ai_sessions SET updated_at=? WHERE session_id=?`, r.CreatedAt.UnixNano(), r.SessionID)
		if err != nil {
			return err
		}
		return appendChatEvent(ctx, tx, chat.ModelCall{
			RunID:     r.RunID,
			SessionID: r.SessionID,
		}, chat.Event{
			Type: chat.EventStart,
			Data: json.RawMessage(`{}`),
		})
	})
}

// ReadRun 读取内部运行；公开查询须同时检查父会话可见性。
func (s *chatStore) ReadRun(ctx context.Context, id string, visible bool) (chat.Record, error) {
	db, err := s.db.handle()
	if err != nil {
		return chat.Record{}, err
	}
	query := `SELECT ` + runColumns + ` FROM ai_runs r JOIN ai_sessions s ON s.session_id=r.session_id WHERE r.run_id=?`
	if visible {
		query += ` AND s.deleted=0`
	}
	return readRecord(db.QueryRowContext(ctx, query, id))
}

// Session 查询可见会话与升序运行 ID。
func (s *chatStore) Session(ctx context.Context, id string) (chat.Session, error) {
	result := chat.Session{
		SessionID: id,
		RunIDs:    []string{},
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT session_name FROM ai_sessions WHERE session_id=? AND deleted=0`, id).Scan(&result.SessionName)
		if errors.Is(err, sql.ErrNoRows) {
			return chat.ErrSessionNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT run_id FROM ai_runs WHERE session_id=? ORDER BY id`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var run string
			if err = rows.Scan(&run); err != nil {
				return err
			}
			result.RunIDs = append(result.RunIDs, run)
		}
		return rows.Err()
	})
	return result, err
}

// Sessions 按最新运行顺序返回未删除会话。
func (s *chatStore) Sessions(ctx context.Context) ([]string, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT s.session_id FROM ai_sessions s JOIN ai_runs r ON r.session_id=s.session_id WHERE s.deleted=0 GROUP BY s.session_id ORDER BY MAX(r.id) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

// Hide 只软删除父会话，保留全部子记录。
func (s *chatStore) Hide(ctx context.Context, id string) error {
	db, err := s.db.handle()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `UPDATE ai_sessions SET deleted=1,updated_at=? WHERE session_id=?`, time.Now().UnixNano(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return chat.ErrSessionNotFound
	}
	return nil
}

// History 从实际调用输出和原始输入构造未压缩会话消息，不回放展示事件。
func (s *chatStore) History(ctx context.Context, session string) ([]chat.Message, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT r.input,r.status,m.output FROM ai_runs r JOIN ai_model_calls m ON m.run_id=r.run_id WHERE r.session_id=? AND r.status NOT IN ('running','wait_for_cancel') ORDER BY r.id,m.id`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []chat.Message{}
	for rows.Next() {
		var input, status string
		var output sql.NullString
		if err = rows.Scan(&input, &status, &output); err != nil {
			return nil, err
		}
		result = append(result, chat.Message{
			Role:    "user",
			Content: input,
		})
		if output.Valid {
			var value chat.ModelOutput
			if err = json.Unmarshal([]byte(output.String), &value); err != nil {
				return nil, err
			}
			if value.Message.Content != "" || value.Message.ReasoningContent != "" {
				result = append(result, value.Message)
			}
		}
		if status != "succeeded" {
			result = append(result, chat.Message{
				Role:    "user",
				Content: "上一轮回复未完成；其操作表述不代表业务已经生效。",
			})
		}
	}
	return result, rows.Err()
}

func checkpoint(ctx context.Context, tx *sql.Tx, call chat.ModelCall, stage string) error {
	state := struct {
		Context   []chat.Message `json:"context"`
		Completed []string       `json:"completed_tool"`
		Last      string         `json:"last_tool"`
	}{
		Context:   call.Input.Messages,
		Completed: []string{},
	}
	if stage == "model_call_end" && call.Output != nil {
		state.Context = append(append([]chat.Message{}, state.Context...), call.Output.Message)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO ai_checkpoints(run_id,model_call_id,status,state) VALUES(?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET model_call_id=excluded.model_call_id,status=excluded.status,state=excluded.state`, call.RunID, call.ID, stage, jsonValue(state))
	return err
}

// BeginCall 在请求前原子保存实际 input、running 和 checkpoint。
func (s *chatStore) BeginCall(ctx context.Context, call chat.ModelCall) (int64, error) {
	var id int64
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO ai_model_calls(run_id,session_id,input,status,max_retry,created_at) VALUES(?,?,?,'running',?,?)`, call.RunID, call.SessionID, jsonValue(call.Input), call.MaxRetry, time.Now().UnixNano())
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		call.ID = id
		return checkpoint(ctx, tx, call, "model_call_start")
	})
	return id, err
}

// ReadCall 读取同一逻辑模型调用的原请求与部分输出；首次登记前返回 nil。
func (s *chatStore) ReadCall(ctx context.Context, id string) (*chat.ModelCall, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	call := &chat.ModelCall{RunID: id}
	var input string
	var output, failure sql.NullString
	err = db.QueryRowContext(ctx, `SELECT m.id,m.session_id,m.input,m.output,m.status,m.error,m.max_retry FROM ai_checkpoints c JOIN ai_model_calls m ON m.id=c.model_call_id WHERE c.run_id=?`, id).Scan(&call.ID, &call.SessionID, &input, &output, &call.State, &failure, &call.MaxRetry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(input), &call.Input); err != nil {
		return nil, err
	}
	if output.Valid {
		if err = json.Unmarshal([]byte(output.String), &call.Output); err != nil {
			return nil, err
		}
	}
	if failure.Valid {
		if err = json.Unmarshal([]byte(failure.String), &call.Error); err != nil {
			return nil, err
		}
	}
	return call, nil
}

// Pause 保存正常退出时的真实部分响应，不生成终态或推进 checkpoint。
func (s *chatStore) Pause(ctx context.Context, call chat.ModelCall) error {
	call.State = chat.StateRunning
	call.Error = nil
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := saveCall(ctx, tx, call, false); err != nil {
			return err
		}
		return appendChatEvents(ctx, tx, call)
	})
}

func appendChatEvents(ctx context.Context, tx *sql.Tx, call chat.ModelCall) error {
	for _, e := range call.PendingEvents {
		if err := appendChatEvent(ctx, tx, call, e); err != nil {
			return err
		}
	}
	return nil
}

func appendChatEvent(ctx context.Context, tx *sql.Tx, call chat.ModelCall, e chat.Event) error {
	var modelID, reply any
	if call.ID != 0 {
		modelID = call.ID
	}
	if call.Output != nil && call.Output.ID != "" {
		reply = call.Output.ID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO ai_events(run_id,session_id,model_call_id,model_reply_id,type,data,seq,created_at) SELECT ?,?,?,?,?,?,COALESCE(MAX(seq),0)+1,? FROM ai_events WHERE run_id=?`, call.RunID, call.SessionID, modelID, reply, e.Type, string(e.Data), time.Now().UnixNano(), call.RunID)
	return err
}

func saveCall(ctx context.Context, tx *sql.Tx, call chat.ModelCall, finished bool) error {
	var usage, reply, at any
	if call.Output != nil {
		if len(call.Output.Usage) > 0 && string(call.Output.Usage) != "null" {
			usage = string(call.Output.Usage)
		}
		if call.Output.ID != "" {
			reply = call.Output.ID
		}
	}
	if finished {
		at = time.Now().UnixNano()
	}
	_, err := tx.ExecContext(ctx, `UPDATE ai_model_calls SET output=?,usage=?,model_reply_id=?,status=?,error=?,finished_at=? WHERE id=?`, jsonValue(call.Output), usage, reply, call.State, jsonValue(call.Error), at, call.ID)
	return err
}

// SaveEvents 原子保存部分响应与原始事件批次，订阅只读取已提交数据。
func (s *chatStore) SaveEvents(ctx context.Context, call chat.ModelCall) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := saveCall(ctx, tx, call, false); err != nil {
			return err
		}
		return appendChatEvents(ctx, tx, call)
	})
}

// Finish 原子保存调用结果、usage、进度、运行终态及唯一 error/end。
func (s *chatStore) Finish(ctx context.Context, call chat.ModelCall, r chat.Record) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var state chat.RunState
		if err := tx.QueryRowContext(ctx, `SELECT status FROM ai_runs WHERE run_id=?`, r.RunID).Scan(&state); err != nil {
			return err
		}
		if state.Terminal() {
			return nil
		}
		if err := appendChatEvents(ctx, tx, call); err != nil {
			return err
		}
		if call.ID != 0 {
			if err := saveCall(ctx, tx, call, true); err != nil {
				return err
			}
			if err := checkpoint(ctx, tx, call, "model_call_end"); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE ai_runs SET status=?,output=?,error=?,finished_at=? WHERE run_id=?`, r.State, stringValue(r.Output), jsonValue(r.Error), timeValue(r.FinishedAt), r.RunID)
		if err != nil {
			return err
		}
		if r.Error != nil {
			data, _ := json.Marshal(r.Error)
			if err = appendChatEvent(ctx, tx, call, chat.Event{
				Type: chat.EventError,
				Data: data,
			}); err != nil {
				return err
			}
		}
		data, _ := json.Marshal(chat.EndData{Status: r.State.Public()})
		return appendChatEvent(ctx, tx, call, chat.Event{
			Type: chat.EventEnd,
			Data: data,
		})
	})
}

// Events 按持久化序号完整读取单条事件，多取一条判断是否有下一页。
func (s *chatStore) Events(ctx context.Context, id string, after int64, limit int) (chat.EventPage, error) {
	page := chat.EventPage{
		Events:       []chat.Event{},
		NextAfterSeq: after,
	}
	db, err := s.db.handle()
	if err != nil {
		return page, err
	}
	rows, err := db.QueryContext(ctx, `SELECT seq,type,data FROM ai_events WHERE run_id=? AND seq>? ORDER BY seq LIMIT ?`, id, after, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var e chat.Event
		var data string
		if err = rows.Scan(&e.ID, &e.Type, &data); err != nil {
			return page, err
		}
		e.Data = json.RawMessage(data)
		if len(page.Events) == limit {
			page.HasMore = true
			break
		}
		page.Events = append(page.Events, e)
		page.NextAfterSeq = e.ID
	}
	return page, rows.Err()
}

// Active 扫描未完成运行，供 ready 前登记和后台处理。
func (s *chatStore) Active(ctx context.Context) ([]chat.Record, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT run_id FROM ai_runs WHERE status IN ('running','wait_for_cancel') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []chat.Record{}
	for _, id := range ids {
		r, e := s.ReadRun(ctx, id, false)
		if e != nil {
			return nil, e
		}
		result = append(result, r)
	}
	return result, nil
}
