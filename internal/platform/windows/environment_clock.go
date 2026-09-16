package windows

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// EnvironmentClockOptions 注入时钟、时区读取和事件队列参数，供平台实现及确定性测试使用。
type EnvironmentClockOptions struct {
	Now            func() time.Time
	LoadLocation   func() (*time.Location, error)
	EventQueueSize int
}

// EnvironmentClock 共享当前时间事实，并同时实现 app.Clock 与环境事件源。
type EnvironmentClock struct {
	mu          sync.RWMutex
	now         func() time.Time
	load        func() (*time.Location, error)
	events      chan app.RuntimeEvent
	snapshot    domain.TimeSnapshot
	suspended   bool
	started     bool
	closed      bool
	epoch       uint64
	suspendAt   time.Time
	done        chan struct{}
	loopDone    chan struct{}
	loopErr     error
	thread      messageThread
	threadToken MessageHandlerToken
	ownsThread  bool
}

// newEnvironmentClock 使用注入的时间事实创建共享时钟状态。
func newEnvironmentClock(options EnvironmentClockOptions) (*EnvironmentClock, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.LoadLocation == nil {
		options.LoadLocation = func() (*time.Location, error) { return time.UTC, nil }
	}
	if options.EventQueueSize <= 0 {
		options.EventQueueSize = 16
	}
	location, err := options.LoadLocation()
	if err != nil {
		return nil, fmt.Errorf("initialize environment clock: load time zone: %w", err)
	}
	if location == nil {
		return nil, errors.New("initialize environment clock: time zone is nil")
	}
	return &EnvironmentClock{
		now:      options.Now,
		load:     options.LoadLocation,
		events:   make(chan app.RuntimeEvent, options.EventQueueSize),
		snapshot: domain.TimeSnapshot{Now: options.Now(), Location: location},
		done:     make(chan struct{}),
		loopDone: make(chan struct{}),
	}, nil
}

// NewTestEnvironmentClock 创建不启动 Win32 消息线程的确定性测试时钟。
func NewTestEnvironmentClock(options EnvironmentClockOptions) (*EnvironmentClock, error) {
	return newEnvironmentClock(options)
}

// Snapshot 返回当前时间与共享时区位置组成的实时快照。
func (c *EnvironmentClock) Snapshot(ctx context.Context) (domain.TimeSnapshot, error) {
	if c == nil {
		return domain.TimeSnapshot{}, errors.New("snapshot environment clock: clock is nil")
	}
	if ctx == nil {
		return domain.TimeSnapshot{}, errors.New("snapshot environment clock: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return domain.TimeSnapshot{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return domain.TimeSnapshot{}, errors.New("snapshot environment clock: clock is closed")
	}
	// 调度器需要每次读取都观察墙钟推进；挂起时长和变化标志只属于事件，不应滞留在普通快照中。
	snapshot := domain.TimeSnapshot{
		Now:      c.now(),
		Location: c.snapshot.Location,
	}
	return snapshot, nil
}

// Start 标记环境时钟就绪并启动唯一的受控平台消息线程。
func (c *EnvironmentClock) Start(ctx context.Context) error {
	if c == nil {
		return errors.New("start environment clock: clock is nil")
	}
	if ctx == nil {
		return errors.New("start environment clock: context is nil")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("start environment clock: clock is closed")
	}
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	thread := c.thread
	c.mu.Unlock()
	if thread != nil {
		go func() {
			if err := thread.Start(ctx); err != nil {
				c.mu.Lock()
				if !c.closed {
					c.loopErr = fmt.Errorf("environment message loop: %w", err)
				}
				c.mu.Unlock()
			}
			close(c.loopDone)
		}()
	} else {
		close(c.loopDone)
	}
	return nil
}

// Events 返回环境事实事件流。
func (c *EnvironmentClock) Events() <-chan app.RuntimeEvent {
	if c == nil {
		return nil
	}
	return c.events
}

// OnSuspend 将系统挂起转换为一次幂等 RuntimeEvent。
func (c *EnvironmentClock) OnSuspend() error {
	if c == nil {
		return errors.New("handle suspend: clock is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureRunningLocked("handle suspend"); err != nil {
		return err
	}
	if c.suspended {
		return nil
	}
	if err := c.refreshLocked(false, false); err != nil {
		return fmt.Errorf("handle suspend: %w", err)
	}
	c.suspended = true
	c.suspendAt = c.snapshot.Now
	return c.emitLocked(app.RuntimeEventSuspend)
}

// OnResume 将系统恢复转换为一次幂等 RuntimeEvent；没有对应挂起时忽略恢复。
func (c *EnvironmentClock) OnResume() error {
	if c == nil {
		return errors.New("handle resume: clock is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureRunningLocked("handle resume"); err != nil {
		return err
	}
	if !c.suspended {
		return nil
	}
	if err := c.refreshLocked(false, false); err != nil {
		return fmt.Errorf("handle resume: %w", err)
	}
	c.snapshot.SleepDuration = c.snapshot.Now.Sub(c.suspendAt)
	if c.snapshot.SleepDuration < 0 {
		c.snapshot.SleepDuration = 0
	}
	c.suspended = false
	return c.emitLocked(app.RuntimeEventResume)
}

// NotifyClockChanged 刷新墙钟事实后发送时钟变化事件。
func (c *EnvironmentClock) NotifyClockChanged() error {
	return c.notifyChange(false)
}

// NotifyTimeZoneChanged 重新读取动态时区规则后发送时区变化事件。
func (c *EnvironmentClock) NotifyTimeZoneChanged() error {
	return c.notifyChange(true)
}

// notifyChange 统一执行“刷新快照后发送事件”的顺序约束。
func (c *EnvironmentClock) notifyChange(timeZoneChanged bool) error {
	if c == nil {
		return errors.New("handle environment change: clock is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureRunningLocked("handle environment change"); err != nil {
		return err
	}
	if err := c.refreshLocked(!timeZoneChanged, timeZoneChanged); err != nil {
		return fmt.Errorf("handle environment change: %w", err)
	}
	return c.emitLocked(app.RuntimeEventClockChanged)
}

// refreshLocked 取得全新的 now/location，并记录本次环境变化事实；调用方必须持有 mu。
func (c *EnvironmentClock) refreshLocked(wallClockChanged, timeZoneChanged bool) error {
	previous := c.snapshot
	now := c.now()
	location, err := c.load()
	if err != nil {
		return fmt.Errorf("refresh environment snapshot: load time zone: %w", err)
	}
	if location == nil {
		return errors.New("refresh environment snapshot: time zone is nil")
	}
	c.snapshot = domain.TimeSnapshot{Now: now, Location: location, TimeZoneChanged: timeZoneChanged}
	if wallClockChanged {
		c.snapshot.WallClockJump = now.Sub(previous.Now)
	}
	return nil
}

// emitLocked 将当前快照装入事件并用 epoch/关闭信号阻止关闭后的回调，调用方必须持有 mu。
func (c *EnvironmentClock) emitLocked(eventType app.RuntimeEventType) error {
	if c.closed {
		return errors.New("emit environment event: clock is closed")
	}
	c.epoch++
	event := app.RuntimeEvent{Type: eventType, Snapshot: c.snapshot}
	select {
	case c.events <- event:
		return nil
	case <-c.done:
		return errors.New("emit environment event: clock is closed")
	default:
		return errors.New("emit environment event: event queue is full")
	}
}

// ensureRunningLocked 验证环境时钟已经启动且仍可产生事件，调用方必须持有 mu。
func (c *EnvironmentClock) ensureRunningLocked(operation string) error {
	if c.closed {
		return fmt.Errorf("%s: clock is closed", operation)
	}
	if !c.started {
		return fmt.Errorf("%s: clock is not started", operation)
	}
	return nil
}

// Close 使旧 epoch 的平台回调失效并关闭事件流，且保持幂等。
func (c *EnvironmentClock) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.epoch++
	close(c.done)
	close(c.events)
	loopDone := c.loopDone
	if !c.started {
		close(loopDone)
	}
	thread := c.thread
	token := c.threadToken
	ownsThread := c.ownsThread
	c.mu.Unlock()
	if thread != nil {
		if token != 0 {
			if err := thread.UnregisterHandler(token); err != nil {
				c.mu.Lock()
				c.loopErr = errors.Join(c.loopErr, fmt.Errorf("unregister environment message handler: %w", err))
				c.mu.Unlock()
			}
		}
		if ownsThread {
			if err := thread.Close(); err != nil {
				c.mu.Lock()
				if c.loopErr == nil {
					c.loopErr = fmt.Errorf("close environment message thread: %w", err)
				} else {
					c.loopErr = errors.Join(c.loopErr, fmt.Errorf("close environment message thread: %w", err))
				}
				c.mu.Unlock()
			}
		}
	}
	if ownsThread {
		<-loopDone
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loopErr
}

var _ app.Clock = (*EnvironmentClock)(nil)
var _ platform.EnvironmentEventSource = (*EnvironmentClock)(nil)
