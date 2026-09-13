package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

var rowsErrorDriverID uint64

// scanRowsErrorDriver 创建一个可分别注入扫描错误和关闭错误的测试驱动。
type scanRowsErrorDriver struct {
	closeErr error
}

// Open 创建返回单行错误数据的测试连接。
func (d scanRowsErrorDriver) Open(string) (driver.Conn, error) {
	return scanRowsErrorConn{closeErr: d.closeErr}, nil
}

// scanRowsErrorConn 实现数据库连接的最小接口并直接提供查询结果。
type scanRowsErrorConn struct {
	closeErr error
}

// Prepare 保留连接接口要求；测试查询直接走 Query。
func (scanRowsErrorConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported by test driver")
}

// Close 关闭测试连接。
func (scanRowsErrorConn) Close() error {
	return nil
}

// Begin 保留连接接口要求；扫描测试不使用事务。
func (scanRowsErrorConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported by test driver")
}

// Query 返回一行首列类型错误的数据，使上层 Scan 稳定失败。
func (c scanRowsErrorConn) Query(string, []driver.Value) (driver.Rows, error) {
	return &scanRowsErrorRows{closeErr: c.closeErr}, nil
}

// scanRowsErrorRows 返回固定列并在关闭时报告注入错误。
type scanRowsErrorRows struct {
	closeErr error
	returned bool
}

// Columns 返回与日程扫描器相同数量的结果列。
func (scanRowsErrorRows) Columns() []string {
	return []string{
		"id", "title", "cron", "open", "first_start_local",
		"original_start_local", "duration_minutes", "revision",
		"created_at_utc", "updated_at_utc", "until_date_local",
	}
}

// Close 返回测试注入的 rows 关闭错误。
func (r *scanRowsErrorRows) Close() error {
	return r.closeErr
}

// Next 返回首列不可转换的数据，触发数据库扫描错误。
func (r *scanRowsErrorRows) Next(dest []driver.Value) error {
	if r.returned {
		return io.EOF
	}
	r.returned = true
	values := []driver.Value{
		"not-an-integer", "title", nil, int64(1),
		"2026-01-02T10:00:00+08:00", "2026-01-02T10:00:00+08:00",
		int64(90), int64(1), "2026-01-01T04:00:00Z", "2026-01-01T04:00:00Z", nil,
	}
	copy(dest, values)
	return nil
}

// newRowsErrorDriverName 为每次测试生成未注册过的驱动名称。
func newRowsErrorDriverName() string {
	id := atomic.AddUint64(&rowsErrorDriverID, 1)
	return fmt.Sprintf("timecue-rows-error-%d", id)
}

// TestScanScheduleRowsJoinsScanAndCloseErrors 验证扫描错误和关闭错误都会保留在返回链中。
func TestScanScheduleRowsJoinsScanAndCloseErrors(t *testing.T) {
	closeErr := errors.New("forced rows close failure")
	driverName := newRowsErrorDriverName()
	sql.Register(driverName, scanRowsErrorDriver{closeErr: closeErr})

	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.Query("SELECT schedules")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}

	_, err = scanScheduleRows(rows)
	if err == nil {
		t.Fatal("scanScheduleRows() error = nil")
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("scanScheduleRows() error = %v, want close error in chain", err)
	}
	if !strings.Contains(err.Error(), "Scan error") {
		t.Fatalf("scanScheduleRows() error = %v, want scan error text", err)
	}
}
