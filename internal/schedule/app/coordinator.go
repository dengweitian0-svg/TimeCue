package app

import (
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// MutationCoordinator 串行协调日程事实变更和提醒候选消费。
// epoch 每次正式变更前递增，使已读取但尚未发送的旧候选失效；wake 用于唤醒无计时器的调度循环。
type MutationCoordinator struct {
	mu    sync.Mutex
	epoch uint64
	wake  chan struct{}
	state RuntimeState
}

// NewMutationCoordinator 创建从首个运行 epoch 开始的协调器。
func NewMutationCoordinator() *MutationCoordinator {
	return &MutationCoordinator{wake: make(chan struct{}, 1), state: RuntimeStateRunning}
}

// Lock 获取统一协调锁。
func (c *MutationCoordinator) Lock() {
	if c != nil {
		c.mu.Lock()
	}
}

// Unlock 释放协调锁并通知调度器重新读取已提交事实。
func (c *MutationCoordinator) Unlock() {
	if c == nil {
		return
	}
	c.mu.Unlock()
	c.Signal()
}

// BeginMutation 获取写入协调锁并检查运行时是否仍接受业务写入。
// 成功返回时锁保持占用，调用方必须配对调用 EndMutation；这样 stop 事件
// 与业务写入会在同一把锁上按先后顺序完成，停止之后不会再进入存储端口。
func (c *MutationCoordinator) BeginMutation() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.state == RuntimeStateStopping {
		c.mu.Unlock()
		return domain.NewRuntimeStopping()
	}
	c.epoch++
	return nil
}

// EndMutation 释放业务写入协调锁并唤醒调度器重新读取已提交事实。
func (c *MutationCoordinator) EndMutation() {
	if c == nil {
		return
	}
	c.mu.Unlock()
	c.Signal()
}

// AdmitMutation 是 BeginMutation 的语义别名，供写入用例使用更明确的准入命名。
func (c *MutationCoordinator) AdmitMutation() error {
	return c.BeginMutation()
}

// State 返回当前运行时生命周期状态。
func (c *MutationCoordinator) State() RuntimeState {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// ApplyRuntimeEvent 按幂等规则转换生命周期状态并使旧提醒候选失效。
func (c *MutationCoordinator) ApplyRuntimeEvent(event RuntimeEvent) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	changed := false
	switch event.Type {
	case RuntimeEventSuspend:
		if c.state == RuntimeStateRunning {
			c.state = RuntimeStateSuspended
			changed = true
		}
	case RuntimeEventResume:
		if c.state != RuntimeStateStopping && c.state == RuntimeStateSuspended {
			c.state = RuntimeStateRunning
			changed = true
		}
	case RuntimeEventClockChanged:
		if c.state != RuntimeStateStopping {
			changed = true
		}
	case RuntimeEventStop:
		if c.state != RuntimeStateStopping {
			c.state = RuntimeStateStopping
			changed = true
		}
	default:
		c.mu.Unlock()
		return domain.NewValidation("runtime event type is invalid", "event")
	}
	if changed {
		c.epoch++
	}
	c.mu.Unlock()
	if changed {
		c.Signal()
	}
	return nil
}

// unlock 释放调度器内部读取/消费所持有的锁，不制造自身的唤醒信号。
func (c *MutationCoordinator) unlock() {
	if c != nil {
		c.mu.Unlock()
	}
}

// Invalidate 在正式写入前使旧提醒候选失效并返回新 epoch。
func (c *MutationCoordinator) Invalidate() uint64 {
	if c == nil {
		return 0
	}
	c.epoch++
	return c.epoch
}

// Epoch 返回当前日程事实对应的调度失效 epoch。
func (c *MutationCoordinator) Epoch() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

// Wake 返回候选变更时使用的唤醒信号通道。
func (c *MutationCoordinator) Wake() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.wake
}

// Signal 主动唤醒调度器，重复信号会合并以避免通道堆积。
func (c *MutationCoordinator) Signal() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
