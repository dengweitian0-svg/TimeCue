package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// openTestStore 创建一个临时 SQLite Store。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timecue.db")
	store, err := OpenStore(context.Background(), path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})
	return store
}

// createTestSchedule 返回用于持久化测试的合法候选。
func createTestSchedule() domain.ScheduleCandidate {
	location := time.FixedZone("UTC+8", 8*60*60)
	start := time.Date(2026, 1, 2, 10, 0, 0, 0, location)
	created := time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC)
	return domain.ScheduleCandidate{
		Title:              "test schedule",
		Open:               true,
		FirstStartLocal:    start,
		OriginalStartLocal: start,
		DurationMinutes:    90,
		CreatedAtUTC:       created,
		UpdatedAtUTC:       created,
	}
}

// TestCreateAndGetSchedule 验证创建后读取的日程来自数据库实际状态。
func TestCreateAndGetSchedule(t *testing.T) {
	store := openTestStore(t)
	candidate := createTestSchedule()
	created, err := store.CreateSchedule(context.Background(), candidate)
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	got, err := store.GetSchedule(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetSchedule() error = %v", err)
	}
	if got.ID != created.ID || got.Title != candidate.Title || !got.Open || got.Cron != nil || got.Revision != 1 {
		t.Fatalf("stored schedule = %#v", got)
	}
	if !got.FirstStartLocal.Equal(candidate.FirstStartLocal) || !got.OriginalStartLocal.Equal(candidate.OriginalStartLocal) {
		t.Fatalf("local starts = %v, %v", got.FirstStartLocal, got.OriginalStartLocal)
	}
	if got.DurationMinutes != candidate.DurationMinutes {
		t.Fatalf("duration = %d, want %d", got.DurationMinutes, candidate.DurationMinutes)
	}
	missing, err := store.GetSchedule(context.Background(), 999)
	if err == nil || missing != (domain.Schedule{}) {
		t.Fatalf("missing schedule = %#v, error = %v", missing, err)
	}
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.NotFound {
		t.Fatalf("missing error = %#v, want not found", err)
	}
}

// TestUpdateSchedulePreservesIdentityAndIncrementsRevision 验证补丁只修改允许字段并推进版本。
func TestUpdateSchedulePreservesIdentityAndIncrementsRevision(t *testing.T) {
	store := openTestStore(t)
	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	newTitle := "updated schedule"
	newStart := created.FirstStartLocal.Add(2 * time.Hour)
	newDuration := 45
	updated, err := store.UpdateSchedule(context.Background(), created.ID, domain.ScheduleUpdateCandidate{
		Title:            &newTitle,
		FirstStartLocal:  &newStart,
		DurationMinutes:  &newDuration,
		UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
		ExpectedRevision: created.Revision,
	})
	if err != nil {
		t.Fatalf("UpdateSchedule() error = %v", err)
	}
	if updated.ID != created.ID || updated.Title != newTitle || updated.Revision != created.Revision+1 {
		t.Fatalf("updated schedule = %#v", updated)
	}
	if !updated.FirstStartLocal.Equal(newStart) || updated.DurationMinutes != newDuration {
		t.Fatalf("updated time fields = %v/%d", updated.FirstStartLocal, updated.DurationMinutes)
	}
	if updated.Open != created.Open || updated.Cron != nil || !updated.OriginalStartLocal.Equal(created.OriginalStartLocal) ||
		!updated.CreatedAtUTC.Equal(created.CreatedAtUTC) || !updated.UpdatedAtUTC.Equal(created.UpdatedAtUTC.Add(time.Minute)) {
		t.Fatalf("immutable or unrelated fields changed: before=%#v after=%#v", created, updated)
	}
}

// TestUpdateScheduleRejectsStaleRevisionWithoutWrite 验证旧版本更新返回业务错误且不产生任何写入。
func TestUpdateScheduleRejectsStaleRevisionWithoutWrite(t *testing.T) {
	store := openTestStore(t)
	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	newTitle := "must not win"
	_, err = store.UpdateSchedule(context.Background(), created.ID, domain.ScheduleUpdateCandidate{
		Title:            &newTitle,
		UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
		ExpectedRevision: created.Revision + 1,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("error = %#v, want stale revision", err)
	}
	got, err := store.GetSchedule(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetSchedule() error = %v", err)
	}
	if got.Title != created.Title || got.Revision != created.Revision || !got.UpdatedAtUTC.Equal(created.UpdatedAtUTC) {
		t.Fatalf("stale update changed schedule: %#v", got)
	}
}

// TestUpdateScheduleCompetesOnRevision 验证并发使用同一版本时只有一个更新成功。
func TestUpdateScheduleCompetesOnRevision(t *testing.T) {
	store := openTestStore(t)
	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, title := range []string{"first", "second"} {
		title := title
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, updateErr := store.UpdateSchedule(context.Background(), created.ID, domain.ScheduleUpdateCandidate{
				Title:            &title,
				UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
				ExpectedRevision: created.Revision,
			})
			results <- updateErr
		}()
	}
	wait.Wait()
	close(results)
	var successes, stale int
	for updateErr := range results {
		if updateErr == nil {
			successes++
			continue
		}
		var businessErr *domain.Error
		if errors.As(updateErr, &businessErr) && businessErr.Code == domain.StaleRevision {
			stale++
		}
	}
	if successes != 1 || stale != 1 {
		t.Fatalf("concurrent results = successes %d, stale %d", successes, stale)
	}
	got, err := store.GetSchedule(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetSchedule() error = %v", err)
	}
	if got.Revision != created.Revision+1 {
		t.Fatalf("revision = %d, want %d", got.Revision, created.Revision+1)
	}
}

// TestUpdateScheduleRollsBackOnFailure 验证更新后的回读失败会撤销事务中的修改。
func TestUpdateScheduleRollsBackOnFailure(t *testing.T) {
	store := openTestStore(t)
	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER remove_schedule_after_update
		AFTER UPDATE ON schedules
		BEGIN
			DELETE FROM schedules WHERE id = NEW.id;
		END`); err != nil {
		t.Fatalf("create trigger error = %v", err)
	}
	newTitle := "trigger failure"
	if _, err := store.UpdateSchedule(context.Background(), created.ID, domain.ScheduleUpdateCandidate{
		Title:            &newTitle,
		UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
		ExpectedRevision: created.Revision,
	}); err == nil {
		t.Fatal("UpdateSchedule() error = nil")
	}
	got, err := store.GetSchedule(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetSchedule() after rollback error = %v", err)
	}
	if got.Title != created.Title || got.Revision != created.Revision {
		t.Fatalf("rolled-back schedule = %#v", got)
	}
}

// TestUpdateScheduleRejectsCronConversion 验证存储层也拦截显式 cron 类型转换。
func TestUpdateScheduleRejectsCronConversion(t *testing.T) {
	store := openTestStore(t)
	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	cron := "0 9 * * *"
	_, err = store.UpdateSchedule(context.Background(), created.ID, domain.ScheduleUpdateCandidate{
		Cron:             &cron,
		CronPresent:      true,
		ExpectedRevision: created.Revision,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
		t.Fatalf("error = %#v, want validation", err)
	}
}

// TestSchedulePersistsAcrossReopen 验证关闭并重新打开数据库后仍可读取日程。
func TestSchedulePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timecue.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := OpenStore(context.Background(), path, logger)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}

	reopened, err := OpenStore(context.Background(), path, logger)
	if err != nil {
		t.Fatalf("reopen OpenStore() error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.GetSchedule(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("reopened GetSchedule() error = %v", err)
	}
	if got.ID != created.ID || got.Title != created.Title {
		t.Fatalf("reopened schedule = %#v", got)
	}
}

// TestListSchedulesPaginatesWithoutDuplicates 验证 ID 游标分页不重复、不遗漏。
func TestListSchedulesPaginatesWithoutDuplicates(t *testing.T) {
	store := openTestStore(t)
	for index := 0; index < 5; index++ {
		candidate := createTestSchedule()
		candidate.Title = "schedule " + string(rune('1'+index))
		if _, err := store.CreateSchedule(context.Background(), candidate); err != nil {
			t.Fatalf("CreateSchedule(%d) error = %v", index, err)
		}
	}

	var ids []int64
	cursor := ""
	for page := 0; ; page++ {
		result, err := store.ListSchedules(context.Background(), app.ListSchedulesRequest{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListSchedules(page %d) error = %v", page, err)
		}
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	if len(ids) != 5 {
		t.Fatalf("listed IDs = %v, want 5 records", ids)
	}
	for index, id := range ids {
		if id != int64(index+1) {
			t.Fatalf("listed IDs = %v, want ascending 1..5", ids)
		}
	}
	if _, err := store.ListSchedules(context.Background(), app.ListSchedulesRequest{Limit: 2, Cursor: "not-a-cursor"}); err == nil {
		t.Fatal("invalid cursor error = nil")
	}
}

// TestListSchedulesIncludesHistoricalRecords 验证列表不会因日程已在历史时间而丢弃记录。
func TestListSchedulesIncludesHistoricalRecords(t *testing.T) {
	store := openTestStore(t)
	candidate := createTestSchedule()
	candidate.FirstStartLocal = time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)
	candidate.OriginalStartLocal = candidate.FirstStartLocal
	created, err := store.CreateSchedule(context.Background(), candidate)
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	result, err := store.ListSchedules(context.Background(), app.ListSchedulesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != int64(created.ID) {
		t.Fatalf("historical result = %#v", result)
	}
}

// TestCreateScheduleRollsBackOnInsertFailure 验证 SQLite 写入失败后不会新增记录。
func TestCreateScheduleRollsBackOnInsertFailure(t *testing.T) {
	store := openTestStore(t)
	_, err := store.db.Exec(`
		CREATE TRIGGER fail_schedule_insert
		BEFORE INSERT ON schedules
		WHEN NEW.title = 'trigger failure'
		BEGIN
			SELECT RAISE(ABORT, 'forced insert failure');
		END`)
	if err != nil {
		t.Fatalf("create trigger error = %v", err)
	}
	candidate := createTestSchedule()
	candidate.Title = "trigger failure"
	if _, err := store.CreateSchedule(context.Background(), candidate); err == nil {
		t.Fatal("triggered CreateSchedule() error = nil")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM schedules").Scan(&count); err != nil {
		t.Fatalf("count schedules error = %v", err)
	}
	if count != 0 {
		t.Fatalf("schedule count = %d, want 0", count)
	}
}

// TestCreateScheduleRollsBackOnSelectFailure 验证写入后读取失败也会回滚事务。
func TestCreateScheduleRollsBackOnSelectFailure(t *testing.T) {
	store := openTestStore(t)
	_, err := store.db.Exec(`
		CREATE TRIGGER remove_schedule_after_insert
		AFTER INSERT ON schedules
		BEGIN
			DELETE FROM schedules WHERE id = NEW.id;
		END`)
	if err != nil {
		t.Fatalf("create trigger error = %v", err)
	}
	if _, err := store.CreateSchedule(context.Background(), createTestSchedule()); err == nil {
		t.Fatal("select-failure CreateSchedule() error = nil")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM schedules").Scan(&count); err != nil {
		t.Fatalf("count schedules error = %v", err)
	}
	if count != 0 {
		t.Fatalf("schedule count = %d, want 0", count)
	}
}

// TestCreateScheduleFailsWhenStoreClosed 验证数据库关闭后不会错误地返回创建成功。
func TestCreateScheduleFailsWhenStoreClosed(t *testing.T) {
	store := openTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}
	if _, err := store.CreateSchedule(context.Background(), createTestSchedule()); err == nil {
		t.Fatal("closed-store CreateSchedule() error = nil")
	}
}

// TestCursorRoundTrip 验证分页游标只允许正数日程 ID。
func TestCursorRoundTrip(t *testing.T) {
	for _, id := range []domain.ScheduleID{1, 42, 9223372036854775807} {
		cursor := encodeCursor(id)
		got, err := decodeCursor(cursor)
		if err != nil || got != id {
			t.Fatalf("cursor round trip %d: got %d, error %v", id, got, err)
		}
	}
	for _, cursor := range []string{"", "bad", encodeCursor(0), "MA"} {
		if cursor == "" {
			continue
		}
		if _, err := decodeCursor(cursor); err == nil {
			t.Fatalf("decodeCursor(%q) error = nil", cursor)
		}
	}
}

// TestStoreSatisfiesScheduleStore 保留编译期契约测试，确保生产者隐式实现消费者接口。
func TestStoreSatisfiesScheduleStore(t *testing.T) {
	var store app.ScheduleStore = openTestStore(t)
	if store == nil {
		t.Fatal("ScheduleStore = nil")
	}
}

// TestScanScheduleRowsClosesRows 验证分页结果扫描结束后游标已关闭。
func TestScanScheduleRowsClosesRows(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.CreateSchedule(context.Background(), createTestSchedule()); err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	rows, err := store.db.Query("SELECT " + scheduleColumns + " FROM schedules")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if _, err := scanScheduleRows(rows); err != nil {
		t.Fatalf("scanScheduleRows() error = %v", err)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rows.Err() = %v", err)
	}
}
