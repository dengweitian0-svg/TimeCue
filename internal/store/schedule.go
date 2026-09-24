package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// ScheduleStore 负责日程的持久化和软删除，不负责计算触发时间。
type ScheduleStore struct {
	db *Sqlite
}

// NewScheduleStore 创建使用指定数据库的日程存储。
func NewScheduleStore(db *Sqlite) *ScheduleStore { return &ScheduleStore{db: db} }

// CreateSchedule 校验并写入日程，返回生成的 ID 与时间戳。
func (s *ScheduleStore) CreateSchedule(ctx context.Context, schedule model.Schedule) (*model.Schedule, error) {
	if err := schedule.Validate(); err != nil {
		return nil, fmt.Errorf("[store] invalid schedule, name=%q: %w", schedule.Name, err)
	}
	if schedule.ID != 0 || schedule.Cancelled || schedule.Deleted {
		return nil, fmt.Errorf("[store] new schedule must not have a preset state, schedule_id=%d, cancelled=%t, deleted=%t", schedule.ID, schedule.Cancelled, schedule.Deleted)
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, fmt.Errorf("[store] cannot create schedule, name=%q: %w", schedule.Name, err)
	}
	var periodOpen, periodCron, singleTime any
	if schedule.Type == model.PeriodSchedule {
		periodOpen = schedule.Period.Open
		encoded, err := json.Marshal(schedule.Period.Cron)
		if err != nil {
			return nil, fmt.Errorf("[store] failed to encode cron rules, name=%q: %w", schedule.Name, err)
		}
		periodCron = string(encoded)
	} else {
		singleTime = schedule.Single.NotifyTime.UTC().UnixNano()
	}
	var description any
	if schedule.Description != nil {
		description = *schedule.Description
	}
	now := time.Now().UTC()
	result, err := db.ExecContext(ctx, `INSERT INTO schedules
		(name, type, description, period_open, period_cron, single_notify_time, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, schedule.Name, schedule.Type, description,
		periodOpen, periodCron, singleTime, now.UnixNano(), now.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("[store] failed to create schedule, name=%q: %w", schedule.Name, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("[store] failed to read new schedule ID, name=%q: %w", schedule.Name, err)
	}
	schedule.ID = int(id)
	schedule.CreatedAt = now
	schedule.UpdatedAt = now
	return &schedule, nil
}

// GetScheduleByID 要求单独传入日程 ID，其余已设置的查询条件均参与过滤。
func (s *ScheduleStore) GetScheduleByID(ctx context.Context, scheduleID int, query model.QueryOption) (*model.Schedule, error) {
	if scheduleID <= 0 {
		return nil, fmt.Errorf("[store] schedule ID must be positive, schedule_id=%d", scheduleID)
	}
	queryDetails := fmt.Sprintf("schedule_id=%d", scheduleID)
	if optionalDetails := scheduleQueryDetails(query); optionalDetails != "" {
		queryDetails += ", " + optionalDetails
	}
	limit, offset, err := schedulePagination(query, 1)
	if err != nil {
		return nil, fmt.Errorf("[store] invalid schedule pagination, %s: %w", queryDetails, err)
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, fmt.Errorf("[store] cannot get schedule, %s: %w", queryDetails, err)
	}
	filter, args, err := scheduleFilter(query, &scheduleID)
	if err != nil {
		return nil, fmt.Errorf("[store] invalid schedule query, %s: %w", queryDetails, err)
	}
	args = append(args, limit, offset)
	schedule, err := scanSchedule(db.QueryRowContext(ctx, scheduleSelect+filter+` LIMIT ? OFFSET ?`, args...))
	if err != nil {
		return nil, fmt.Errorf("[store] failed to get schedule, %s: %w", queryDetails, err)
	}
	return schedule, nil
}

// GetAllSchedule 按条件分页查询日程，默认排除已软删除记录。
func (s *ScheduleStore) GetAllSchedule(ctx context.Context, query model.QueryOption) ([]*model.Schedule, error) {
	limit, offset, err := schedulePagination(query, LimitMax)
	if err != nil {
		return nil, fmt.Errorf("[store] invalid schedule pagination: %w", err)
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, fmt.Errorf("[store] cannot list schedules: %w", err)
	}
	filter, args, err := scheduleFilter(query, nil)
	if err != nil {
		return nil, fmt.Errorf("[store] invalid schedule query: %w", err)
	}
	statement := scheduleSelect + filter + " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("[store] failed to list schedules, limit=%d, offset=%d: %w", limit, offset, err)
	}
	defer rows.Close()
	result := make([]*model.Schedule, 0)
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("[store] failed to scan schedules, limit=%d, offset=%d: %w", limit, offset, err)
		}
		result = append(result, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("[store] failed to iterate schedules, limit=%d, offset=%d: %w", limit, offset, err)
	}
	return result, nil
}

// schedulePagination 校验可选分页参数并填入方法默认值。
func schedulePagination(query model.QueryOption, defaultLimit int) (int, int, error) {
	limit, offset := defaultLimit, 0
	if query.Limit != nil {
		if *query.Limit < 1 || *query.Limit > LimitMax {
			return 0, 0, fmt.Errorf("schedule limit must be between 1 and %d, limit=%d", LimitMax, *query.Limit)
		}
		limit = *query.Limit
	}
	if query.Offset != nil {
		if *query.Offset < 0 {
			return 0, 0, fmt.Errorf("schedule offset must not be negative, offset=%d", *query.Offset)
		}
		offset = *query.Offset
	}
	return limit, offset, nil
}

// scheduleQueryDetails 只记录本次实际设置的查询条件，供错误信息使用。
func scheduleQueryDetails(query model.QueryOption) string {
	parts := make([]string, 0, 6)
	if query.ID != nil {
		parts = append(parts, fmt.Sprintf("filter_id=%d", *query.ID))
	}
	if query.Name != nil {
		parts = append(parts, fmt.Sprintf("name=%q", *query.Name))
	}
	if query.Type != nil {
		parts = append(parts, fmt.Sprintf("type=%q", *query.Type))
	}
	if query.Limit != nil {
		parts = append(parts, fmt.Sprintf("limit=%d", *query.Limit))
	}
	if query.Offset != nil {
		parts = append(parts, fmt.Sprintf("offset=%d", *query.Offset))
	}
	if query.IncludeDeleted {
		parts = append(parts, "include_deleted=true")
	}
	return strings.Join(parts, ", ")
}

// scheduleFilter 让单条查询与列表查询使用同一套过滤规则。
func scheduleFilter(query model.QueryOption, requiredID *int) (string, []any, error) {
	var statement strings.Builder
	statement.WriteString(" WHERE 1 = 1")
	args := make([]any, 0, 4)
	if requiredID != nil {
		statement.WriteString(" AND id = ?")
		args = append(args, *requiredID)
	}
	if query.ID != nil {
		if *query.ID <= 0 {
			return "", nil, fmt.Errorf("schedule ID must be positive, schedule_id=%d", *query.ID)
		}
		statement.WriteString(" AND id = ?")
		args = append(args, *query.ID)
	}
	if !query.IncludeDeleted {
		statement.WriteString(" AND deleted = 0")
	}
	if query.Name != nil {
		statement.WriteString(" AND instr(name, ?) > 0")
		args = append(args, *query.Name)
	}
	if query.Type != nil {
		if *query.Type != model.SingleSchedule && *query.Type != model.PeriodSchedule {
			return "", nil, fmt.Errorf("unknown schedule type, type=%q", *query.Type)
		}
		statement.WriteString(" AND type = ?")
		args = append(args, *query.Type)
	}
	return statement.String(), args, nil
}

// SetScheduleOpen 暂停或恢复未取消、未删除的周期日程。
func (s *ScheduleStore) SetScheduleOpen(ctx context.Context, scheduleID int, open bool) error {
	db, err := s.db.handle()
	if err != nil {
		return fmt.Errorf("[store] cannot update schedule availability, schedule_id=%d, open=%t: %w", scheduleID, open, err)
	}
	result, err := db.ExecContext(ctx, `UPDATE schedules SET period_open = ?, updated_at = ?
		WHERE id = ? AND type = 'period' AND deleted = 0 AND cancelled = 0`,
		open, time.Now().UTC().UnixNano(), scheduleID)
	if err != nil {
		return fmt.Errorf("[store] failed to update schedule availability, schedule_id=%d, open=%t: %w", scheduleID, open, err)
	}
	if err := requireAffected(result); err != nil {
		return fmt.Errorf("[store] failed to update schedule availability, schedule_id=%d, open=%t: %w", scheduleID, open, err)
	}
	return nil
}

// CancelScheduleByID 终止未删除日程，并隐藏其未来待投递提醒。
func (s *ScheduleStore) CancelScheduleByID(ctx context.Context, scheduleID int) error {
	db, err := s.db.handle()
	if err != nil {
		return fmt.Errorf("[store] cannot cancel schedule, schedule_id=%d: %w", scheduleID, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("[store] failed to begin schedule cancellation, schedule_id=%d: %w", scheduleID, err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixNano()
	// 终止状态与未来提醒隐藏必须一起提交。
	result, err := tx.ExecContext(ctx, `UPDATE schedules SET cancelled = 1, updated_at = ?
		WHERE id = ? AND deleted = 0`, now, scheduleID)
	if err != nil {
		return fmt.Errorf("[store] failed to cancel schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if err := requireAffected(result); err != nil {
		return fmt.Errorf("[store] failed to cancel schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notifications SET deleted = 1
		WHERE schedule_id = ? AND status = 'pending' AND notify_time > ? AND deleted = 0`, scheduleID, now); err != nil {
		return fmt.Errorf("[store] failed to hide future notifications for cancelled schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("[store] failed to commit schedule cancellation, schedule_id=%d: %w", scheduleID, err)
	}
	return nil
}

// DeleteScheduleByID 保留历史提醒，只隐藏尚未到时且仍待投递的提醒。
func (s *ScheduleStore) DeleteScheduleByID(ctx context.Context, scheduleID int) error {
	db, err := s.db.handle()
	if err != nil {
		return fmt.Errorf("[store] cannot delete schedule, schedule_id=%d: %w", scheduleID, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("[store] failed to begin schedule deletion, schedule_id=%d: %w", scheduleID, err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixNano()
	// 日程删除与未来提醒隐藏必须在同一事务内完成。
	result, err := tx.ExecContext(ctx, `UPDATE schedules SET deleted = 1, updated_at = ? WHERE id = ? AND deleted = 0`, now, scheduleID)
	if err != nil {
		return fmt.Errorf("[store] failed to delete schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if err := requireAffected(result); err != nil {
		return fmt.Errorf("[store] failed to delete schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notifications SET deleted = 1
		WHERE schedule_id = ? AND status = 'pending' AND notify_time > ? AND deleted = 0`, scheduleID, now); err != nil {
		return fmt.Errorf("[store] failed to hide future notifications for deleted schedule, schedule_id=%d: %w", scheduleID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("[store] failed to commit schedule deletion, schedule_id=%d: %w", scheduleID, err)
	}
	return nil
}

const scheduleSelect = `SELECT id, name, type, description, period_open, period_cron,
	single_notify_time, cancelled, deleted, created_at, updated_at FROM schedules`

type scanner interface{ Scan(dest ...any) error }

func scanSchedule(row scanner) (*model.Schedule, error) {
	var s model.Schedule
	var description, cron sql.NullString
	var periodOpen, singleTime sql.NullInt64
	var cancelled, deleted int
	var createdAt, updatedAt int64
	err := row.Scan(&s.ID, &s.Name, &s.Type, &description, &periodOpen, &cron,
		&singleTime, &cancelled, &deleted, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan schedule: %w", err)
	}
	if description.Valid {
		s.Description = &description.String
	}
	s.Cancelled, s.Deleted = cancelled != 0, deleted != 0
	s.CreatedAt, s.UpdatedAt = time.Unix(0, createdAt).UTC(), time.Unix(0, updatedAt).UTC()
	if s.Type == model.PeriodSchedule {
		s.Period = &model.Period{Open: periodOpen.Int64 != 0}
		if err := json.Unmarshal([]byte(cron.String), &s.Period.Cron); err != nil {
			return nil, fmt.Errorf("failed to decode cron rules, schedule_id=%d: %w", s.ID, err)
		}
	} else {
		s.Single = &model.Single{NotifyTime: time.Unix(0, singleTime.Int64).UTC()}
	}
	return &s, nil
}

func requireAffected(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
