package syncutil

import (
	"context"
	"sync"
)

// Mutex 串行化操作，零值可用，支持取消等待。
type Mutex struct {
	once sync.Once
	held chan struct{}
}

// LockContext 获取锁，取消后释放已获取的锁并返回上下文错误。
func (l *Mutex) LockContext(ctx context.Context) error {
	l.once.Do(func() { l.held = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case l.held <- struct{}{}:
		if err := ctx.Err(); err != nil {
			l.Unlock()
			return err
		}
		return nil
	}
}

// Lock 等待不带请求上下文的进程内操作。
func (l *Mutex) Lock() { _ = l.LockContext(context.Background()) }

// Unlock 结束当前正式操作。
func (l *Mutex) Unlock() { <-l.held }
