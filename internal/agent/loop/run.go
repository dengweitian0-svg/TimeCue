package loop

import (
	"context"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

// SchedulePort 提供六个日程工具使用的正式业务用例。
type SchedulePort interface {
	tools.ScheduleQueries
	tools.ScheduleWrites
	tools.ScheduleDeletes
}

// Run 是生产 Agent 实例，过渡期委托旧 Runtime 执行聊天用例。
type Run struct {
	*chat.Runtime
}

// New 装配模型客户端及工具，并只读准备恢复所需的持久化事实。
func New(ctx context.Context, storage chat.Storage, settings func() ([]chat.ModelConfig, error), schedules SchedulePort) (*Run, error) {
	scheduleTools := append(tools.ScheduleTools(schedules), tools.ScheduleWriteTools(schedules, nil)...)
	scheduleTools = append(scheduleTools, tools.ScheduleDeleteTools(schedules)...)
	registry, err := tools.NewRegistry(ctx, scheduleTools...)
	if err != nil {
		return nil, err
	}
	runtime := chat.NewService(storage, completion.NewClient(nil), settings, registry)
	if err := runtime.PrepareRecovery(ctx); err != nil {
		runtime.Shutdown(ctx)
		return nil, err
	}
	return &Run{Runtime: runtime}, nil
}

// Serve 维持已接纳 Loop 的运行，过渡 Runtime 自行处理单次运行的错误。
func (r *Run) Serve(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Shutdown 停止推进并等待仍使用 App 或数据库的工具返回。
func (r *Run) Shutdown(ctx context.Context) {
	r.Runtime.Shutdown(ctx)
	r.WaitForTools()
}
