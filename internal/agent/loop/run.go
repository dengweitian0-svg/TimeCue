package loop

import (
	"context"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

// New 装配模型客户端及工具，并只读准备恢复所需的持久化事实。
func New(ctx context.Context, storage chat.Storage, settings func() ([]chat.ModelConfig, error), schedules tools.SchedulePort) (*Run, error) {
	registry, err := tools.NewScheduleRegistry(ctx, schedules)
	if err != nil {
		return nil, err
	}
	run := NewWithPorts(storage, completion.NewClient(nil), settings, registry)
	if err := run.prepareRecovery(ctx); err != nil {
		run.Shutdown(ctx)
		return nil, err
	}
	return run, nil
}

// Serve 维持已接纳 Loop 的运行。
func (r *Run) Serve(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Shutdown 停止推进并等待仍使用 App 或数据库的工具返回。
func (r *Run) Shutdown(ctx context.Context) {
	r.shutdown(ctx)
	r.toolFactory.Wait()
}
