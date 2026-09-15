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
		"id", "title", "notes", "cron", "open", "first_start_local",
		"original_start_local", "duration_minutes", "revision",
		"created_at_utc", "updated_at_utc", "until_date_local", "month_end_policy",
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
		"not-an-integer", "title", "", nil, int64(1),
		"2026-01-02T10:00:00+08:00", "2026-01-02T10:00:00+08:00",
		int64(90), int64(1), "2026-01-01T04:00:00Z", "2026-01-01T04:00:00Z", nil, nil,
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

// rowsFailureMode 描述测试驱动要注入的 rows 故障阶段。
type rowsFailureMode uint8

const (
	rowsScanFailure rowsFailureMode = iota + 1
	rowsIterationFailure
	rowsCloseFailure
)

// faultRowsDriver 为日程和例外扫描器提供统一的扫描、迭代和关闭错误注入。
type faultRowsDriver struct {
	mode       rowsFailureMode
	fault      error
	closeErr   error
	occurrence bool
}

// Open 创建按测试配置返回故障结果的连接。
func (d faultRowsDriver) Open(string) (driver.Conn, error) {
	return faultRowsConn{driver: d}, nil
}

// faultRowsConn 实现扫描器测试所需的最小数据库连接接口。
type faultRowsConn struct {
	driver faultRowsDriver
}

// Prepare 保留连接接口要求；测试查询直接使用 Query。
func (faultRowsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported by test driver")
}

// Close 关闭故障注入连接。
func (faultRowsConn) Close() error { return nil }

// Begin 保留连接接口要求；扫描器测试不使用事务。
func (faultRowsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported by test driver")
}

// Query 返回故障注入 rows。
func (c faultRowsConn) Query(string, []driver.Value) (driver.Rows, error) {
	return &faultRows{driver: c.driver}, nil
}

// faultRows 返回一行合法或故障数据，并独立注入关闭错误。
type faultRows struct {
	driver   faultRowsDriver
	returned bool
}

// Columns 返回目标扫描器对应的列布局。
func (r faultRows) Columns() []string {
	if r.driver.occurrence {
		return []string{"id", "schedule_id", "occurrence_local", "open", "actual_start_local", "duration_minutes", "title", "notes"}
	}
	return []string{
		"id", "title", "notes", "cron", "open", "first_start_local",
		"original_start_local", "duration_minutes", "revision",
		"created_at_utc", "updated_at_utc", "until_date_local", "month_end_policy",
	}
}

// Close 返回独立注入的 rows 关闭错误。
func (r *faultRows) Close() error { return r.driver.closeErr }

// Next 按故障模式返回迭代错误、扫描错误数据或合法的一行。
func (r *faultRows) Next(dest []driver.Value) error {
	if r.returned {
		return io.EOF
	}
	r.returned = true
	if r.driver.mode == rowsIterationFailure {
		return r.driver.fault
	}
	if r.driver.occurrence {
		values := []driver.Value{int64(1), int64(7), "2026-01-02", int64(0), nil, nil, nil, nil}
		if r.driver.mode == rowsScanFailure {
			values[0] = "not-an-integer"
		}
		copy(dest, values)
		return nil
	}
	values := []driver.Value{
		int64(1), "title", "", nil, int64(1),
		"2026-01-02T10:00:00+08:00", "2026-01-02T10:00:00+08:00",
		int64(90), int64(1), "2026-01-01T04:00:00Z", "2026-01-01T04:00:00Z", nil, nil,
	}
	if r.driver.mode == rowsScanFailure {
		values[0] = "not-an-integer"
	}
	copy(dest, values)
	return nil
}

// TestScanRowsRetainsInjectedFailures 验证两类行扫描器保留扫描、迭代和关闭错误链。
func TestScanRowsRetainsInjectedFailures(t *testing.T) {
	scanErr := errors.New("forced iteration failure")
	closeErr := errors.New("forced rows close failure")
	tests := []struct {
		name       string
		mode       rowsFailureMode
		fault      error
		occurrence bool
		wantClose  bool
		wantText   string
	}{
		{name: "schedule scan", mode: rowsScanFailure, wantClose: true, wantText: "Scan error"},
		{name: "schedule iteration", mode: rowsIterationFailure, fault: scanErr, wantText: "iterate schedule rows"},
		{name: "schedule close", mode: rowsCloseFailure, wantClose: true},
		{name: "occurrence scan", mode: rowsScanFailure, occurrence: true, wantClose: true, wantText: "Scan error"},
		{name: "occurrence iteration", mode: rowsIterationFailure, fault: scanErr, occurrence: true, wantText: "iterate occurrence override rows"},
		{name: "occurrence close", mode: rowsCloseFailure, occurrence: true, wantClose: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			registeredCloseErr := error(nil)
			if testCase.wantClose {
				registeredCloseErr = closeErr
			}
			driverName := newRowsErrorDriverName()
			sql.Register(driverName, faultRowsDriver{
				mode: testCase.mode, fault: testCase.fault, closeErr: registeredCloseErr, occurrence: testCase.occurrence,
			})
			db, err := sql.Open(driverName, "")
			if err != nil {
				t.Fatalf("sql.Open() error = %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			rows, err := db.Query("SELECT test")
			if err != nil {
				t.Fatalf("Query() error = %v", err)
			}

			var scanErrResult error
			if testCase.occurrence {
				_, scanErrResult = scanOccurrenceOverrideRows(rows)
			} else {
				_, scanErrResult = scanScheduleRows(rows)
			}
			if scanErrResult == nil {
				t.Fatal("scanner error = nil")
			}
			if testCase.wantClose && !errors.Is(scanErrResult, closeErr) {
				t.Fatalf("scanner error = %v, want close error in chain", scanErrResult)
			}
			if testCase.fault != nil && !errors.Is(scanErrResult, testCase.fault) {
				t.Fatalf("scanner error = %v, want injected error in chain", scanErrResult)
			}
			if testCase.wantText != "" && !strings.Contains(scanErrResult.Error(), testCase.wantText) {
				t.Fatalf("scanner error = %v, want text %q", scanErrResult, testCase.wantText)
			}
		})
	}
}
