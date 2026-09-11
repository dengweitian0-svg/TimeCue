package clock

import (
	"context"
	"testing"
)

// TestSystemClockSnapshotReturnsEnvironment 验证系统时钟提供非空时间环境。
func TestSystemClockSnapshotReturnsEnvironment(t *testing.T) {
	snapshot, err := (SystemClock{}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Now.IsZero() || snapshot.Location == nil {
		t.Fatalf("snapshot = %#v, want current time and location", snapshot)
	}
}

// TestSystemClockSnapshotHonorsCancellation 验证已取消请求不会读取系统时钟。
func TestSystemClockSnapshotHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (SystemClock{}).Snapshot(ctx); err == nil {
		t.Fatal("Snapshot() error = nil")
	}
}
