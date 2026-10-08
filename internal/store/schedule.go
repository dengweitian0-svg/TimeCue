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
	db  *Sqlite
	now func() time.Time
}

// NewScheduleStore 创建使用指定数据库的日程存储。
func NewScheduleStore(db *Sqlite) (*ScheduleStore, error) {
	if db == nil {
		return nil, fmt.Errorf("[store] cannot create schedule store, database is nil")
	}
	return &ScheduleStore{db: db, now: time.Now}, nil
}

// CreateSchedule 校验并写入日程，返回生成的 ID 与时间戳。
func (s *ScheduleStore) CreateSchedule(ctx context.Context, schedule model.Schedule) (*model.Schedule, error) {
	created, _, err := s.CreateScheduleWithNotification(ctx, schedule, nil)
	return created, err
}

// CreateScheduleWithNotification 在同一事务中写入日程及可选的首条提醒。
func (s *ScheduleStore) CreateScheduleWithNotification(ctx context.Context, schedule model.Schedule, notifyAt *time.Time) (*model.Schedule, *model.Notification, error) {
	if err := schedule.Validate(); err != nil {
		return nil, nil, fmt.Errorf("[store] invalid schedule, name=%q: %w", schedule.Name, err)
	}
	if schedule.ID != 0 || schedule.Cancelled || schedule.Deleted {
		return nil, nil, fmt.Errorf("[store] new schedule must not have a preset state, schedule_id=%d, cancelled=%t, deleted=%t", schedule.ID, schedule.Cancelled, schedule.Deleted)
	}
	if notifyAt != nil {
		if notifyAt.IsZero() {
			return nil, nil, fmt.Errorf("[store] initial notification time must not be zero, name=%q", schedule.Name)
		}
		if schedule.Type == model.SingleSchedule && schedule.Single.NotifyTime.UTC().UnixNano() != notifyAt.UTC().UnixNano() {
			return nil, nil, fmt.Errorf("[store] initial notification time does not match single schedule, name=%q, notify_at=%q", schedule.Name, notifyAt.UTC().Format(time.RFC3339Nano))
		}
		if schedule.Type == model.PeriodSchedule && !schedule.Period.Open {
			return nil, nil, fmt.Errorf("[store] cannot create initial notification for paused schedule, name=%q", schedule.Name)
		}
	}
	result, err := s.ApplyScheduleChange(ctx, model.ScheduleChange{Schedule: schedule}, func(_ *model.Schedule, _ time.Time) (*time.Time, error) {
		return notifyAt, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return result.Schedule, result.Notification, nil
}

// GetScheduleByID 要求单独传入日程 ID，其余函数式选项也参与过滤。
func (s *ScheduleStore) GetScheduleByID(ctx context.Context, scheduleID int, options ...model.QueryOption) (*model.Schedule, error) {
	if scheduleID <= 0 {
		return nil, fmt.Errorf("[store] schedule ID must be positive, schedule_id=%d", scheduleID)
	}
	query := model.ResolveQueryOptions(options...)
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

// GetAllSchedule 按函数式选项分页查询日程，默认排除已软删除记录。
func (s *ScheduleStore) GetAllSchedule(ctx context.Context, options ...model.QueryOption) ([]*model.Schedule, error) {
	query := model.ResolveQueryOptions(options...)
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
func schedulePagination(query model.Option, defaultLimit int) (int, int, error) {
	limit, offset := defaultLimit, 0
	if requestedLimit, ok := query.Limit(); ok {
		if requestedLimit < 1 || requestedLimit > LimitMax {
			return 0, 0, fmt.Errorf("schedule limit must be between 1 and %d, limit=%d", LimitMax, requestedLimit)
		}
		limit = requestedLimit
	}
	if requestedOffset, ok := query.Offset(); ok {
		if requestedOffset < 0 {
			return 0, 0, fmt.Errorf("schedule offset must not be negative, offset=%d", requestedOffset)
		}
		offset = requestedOffset
	}
	return limit, offset, nil
}

// scheduleQueryDetails 只记录本次实际设置的查询条件，供错误信息使用。
func scheduleQueryDetails(query model.Option) string {
	parts := make([]string, 0, 6)
	if id, ok := query.ID(); ok {
		parts = append(parts, fmt.Sprintf("filter_id=%d", id))
	}
	if name, ok := query.Name(); ok {
		parts = append(parts, fmt.Sprintf("name=%q", name))
	}
	if scheduleType, ok := query.Type(); ok {
		parts = append(parts, fmt.Sprintf("type=%q", scheduleType))
	}
	if limit, ok := query.Limit(); ok {
		parts = append(parts, fmt.Sprintf("limit=%d", limit))
	}
	if offset, ok := query.Offset(); ok {
		parts = append(parts, fmt.Sprintf("offset=%d", offset))
	}
	if query.IncludeDeleted() {
		parts = append(parts, "include_deleted=true")
	}
	return strings.Join(parts, ", ")
}

// scheduleFilter 让单条查询与列表查询使用同一套过滤规则。
func scheduleFilter(query model.Option, requiredID *int) (string, []any, error) {
	var statement strings.Builder
	statement.WriteString(" WHERE 1 = 1")
	args := make([]any, 0, 4)
	if requiredID != nil {
		statement.WriteString(" AND id = ?")
		args = append(args, *requiredID)
	}
	if id, ok := query.ID(); ok {
		if id <= 0 {
			return "", nil, fmt.Errorf("schedule ID must be positive, schedule_id=%d", id)
		}
		statement.WriteString(" AND id = ?")
		args = append(args, id)
	}
	if !query.IncludeDeleted() {
		statement.WriteString(" AND deleted = 0")
	}
	if name, ok := query.Name(); ok {
		statement.WriteString(" AND instr(name, ?) > 0")
		args = append(args, name)
	}
	if scheduleType, ok := query.Type(); ok {
		if scheduleType != model.SingleSchedule && scheduleType != model.PeriodSchedule {
			return "", nil, fmt.Errorf("unknown schedule type, type=%q", scheduleType)
		}
		statement.WriteString(" AND type = ?")
		args = append(args, scheduleType)
	}
	return statement.String(), args, nil
}

// SetScheduleOpen 暂停或恢复未取消、未删除的周期日程。
func (s *ScheduleStore) SetScheduleOpen(ctx context.Context, scheduleID int, open bool) error {
	current, err := s.GetScheduleByID(ctx, scheduleID)
	if err != nil {
		return err
	}
	if current.Type != model.PeriodSchedule || current.Cancelled {
		return ErrInvalidTransition
	}
	current.Period.Open = open
	_, err = s.ApplyScheduleChange(ctx, model.ScheduleChange{Schedule: *current, ExpectedRevision: current.Revision}, func(_ *model.Schedule, _ time.Time) (*time.Time, error) { return nil, nil })
	return err
}

// CancelScheduleByID 终止未删除日程，并隐藏其未来待投递提醒。
func (s *ScheduleStore) CancelScheduleByID(ctx context.Context, scheduleID int) error {
	_, err := s.stopSchedule(ctx, scheduleID, false)
	return err
}

// DeleteScheduleByID 在事务内软删除日程，并永久停止已有提醒的后续投递。
// 隐藏未来未确认的 pending 提醒，保留历史记录和投递/确认状态，
// 返回包含事务内前后状态的已提交变更结果。
func (s *ScheduleStore) DeleteScheduleByID(ctx context.Context, scheduleID int) (*model.ScheduleWrite, error) {
	return s.stopSchedule(ctx, scheduleID, true)
}

const scheduleSelect = `SELECT id, name, type, description, period_open, period_cron,
	single_notify_time, cancelled, deleted, created_at, updated_at, revision FROM schedules`

type scanner interface{ Scan(dest ...any) error }

func scanSchedule(row scanner) (*model.Schedule, error) {
	var s model.Schedule
	var description, cron sql.NullString
	var periodOpen, singleTime sql.NullInt64
	var cancelled, deleted int
	var createdAt, updatedAt int64
	err := row.Scan(&s.ID, &s.Name, &s.Type, &description, &periodOpen, &cron,
		&singleTime, &cancelled, &deleted, &createdAt, &updatedAt, &s.Revision)
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
	s.CreatedAt, s.UpdatedAt = time.Unix(0, createdAt).In(model.BeijingLocation()), time.Unix(0, updatedAt).In(model.BeijingLocation())
	if s.Type == model.PeriodSchedule {
		s.Period = &model.Period{Open: periodOpen.Int64 != 0}
		if err := json.Unmarshal([]byte(cron.String), &s.Period.Cron); err != nil {
			return nil, fmt.Errorf("failed to decode cron rules, schedule_id=%d: %w", s.ID, err)
		}
	} else {
		s.Single = &model.Single{NotifyTime: time.Unix(0, singleTime.Int64).In(model.BeijingLocation())}
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

// ReadScheduleState 在同一事务读取 schedules 和 notifications，直接返回规则、事实及待确认页。
func (s *ScheduleStore) ReadScheduleState(ctx context.Context, start, end, asOf time.Time, after model.NotificationPosition, limit int) (schedules []*model.Schedule, occurrences, pending []*model.Notification, total int, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("[store] cannot read schedule state, start=%q, end=%q: %w", start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), err)
		}
	}()
	db, err := s.db.handle()
	if err != nil {
		return nil, nil, nil, 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("[store] cannot begin schedule state snapshot: %w", err)
	}
	defer tx.Rollback()
	schedules, occurrences, pending = []*model.Schedule{}, []*model.Notification{}, []*model.Notification{}
	rows, err := tx.QueryContext(ctx, scheduleSelect)
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("[store] cannot read schedule state schedules: %w", err)
	}
	for rows.Next() {
		schedule, scanErr := scanSchedule(rows)
		if scanErr != nil {
			rows.Close()
			return nil, nil, nil, 0, scanErr
		}
		schedules = append(schedules, schedule)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, nil, 0, err
	}
	rows, err = tx.QueryContext(ctx, notificationSelect+` WHERE deleted = 0 AND notify_time >= ? AND notify_time < ? ORDER BY notify_time, id`, start.UnixNano(), end.UnixNano())
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("[store] cannot read schedule state occurrences: %w", err)
	}
	occurrences, err = scanNotifications(rows)
	rows.Close()
	if err != nil {
		return nil, nil, nil, 0, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE deleted = 0 AND acknowledged_at IS NULL AND notify_time <= ?`, asOf.UnixNano()).Scan(&total); err != nil {
		return nil, nil, nil, 0, err
	}
	if limit > 0 {
		statement := notificationSelect + ` WHERE deleted = 0 AND acknowledged_at IS NULL AND notify_time <= ?`
		args := []any{asOf.UnixNano()}
		if after.ID > 0 {
			statement += ` AND (notify_time > ? OR (notify_time = ? AND id > ?))`
			args = append(args, after.Time.UnixNano(), after.Time.UnixNano(), after.ID)
		}
		rows, err = tx.QueryContext(ctx, statement+` ORDER BY notify_time, id LIMIT ?`, append(args, limit)...)
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("[store] cannot read unacknowledged page: %w", err)
		}
		pending, err = scanNotifications(rows)
		rows.Close()
		if err != nil {
			return nil, nil, nil, 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, nil, 0, err
	}
	return schedules, occurrences, pending, total, nil
}
