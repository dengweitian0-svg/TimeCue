package app

import "sync"

// MutationCoordinator 串行协调日程事实变更和提醒候选消费。
// epoch 每次正式变更前递增，使已读取但尚未发送的旧候选失效；wake 用于唤醒无计时器的调度循环。
type MutationCoordinator struct {
	mu    sync.Mutex
	epoch uint64
	wake  chan struct{}
}

// NewMutationCoordinator 创建从首个运行 epoch 开始的协调器。
func NewMutationCoordinator() *MutationCoordinator {
	return &MutationCoordinator{wake: make(chan struct{}, 1)}
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
