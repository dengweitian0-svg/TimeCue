package loop

import (
	"context"
	"time"
)

const testShutdownBudget = 5 * time.Second

// Start 为通过端口装配的测试 Loop 准备恢复事实并激活。
func (s *Run) Start(ctx context.Context) error {
	if err := s.prepareRecovery(ctx); err != nil {
		return err
	}
	return s.Activate(ctx)
}

// Stop 使用测试清理预算停止 Loop 并等待工具返回。
func (s *Run) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), testShutdownBudget)
	defer cancel()
	s.Shutdown(ctx)
}
