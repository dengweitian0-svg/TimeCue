package windows

import (
	"context"
	"testing"
	"time"
)

type fakeEnvironmentMessageThread struct {
	unregistered MessageHandlerToken
	closeCalls   int
}

// Start 模拟共享消息线程启动，不创建平台资源。
func (*fakeEnvironmentMessageThread) Start(context.Context) error { return nil }

// Close 记录关闭调用，验证非拥有者不会关闭共享线程。
func (thread *fakeEnvironmentMessageThread) Close() error {
	thread.closeCalls++
	return nil
}

// UnregisterHandler 记录环境处理器注销令牌。
func (thread *fakeEnvironmentMessageThread) UnregisterHandler(token MessageHandlerToken) error {
	thread.unregistered = token
	return nil
}

// TestEnvironmentClockSharedThreadOwnership 验证共享消息线程由外部拥有者关闭，环境时钟只注销自身处理器。
func TestEnvironmentClockSharedThreadOwnership(t *testing.T) {
	clock, err := newEnvironmentClock(EnvironmentClockOptions{
		Now:          func() time.Time { return time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC) },
		LoadLocation: func() (*time.Location, error) { return time.UTC, nil },
	})
	if err != nil {
		t.Fatalf("newEnvironmentClock() error = %v", err)
	}
	thread := &fakeEnvironmentMessageThread{}
	clock.thread = thread
	clock.threadToken = 7
	clock.started = true
	if err := clock.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if thread.unregistered != 7 {
		t.Fatalf("unregistered token = %d, want 7", thread.unregistered)
	}
	if thread.closeCalls != 0 {
		t.Fatalf("shared thread close calls = %d, want 0", thread.closeCalls)
	}
}
