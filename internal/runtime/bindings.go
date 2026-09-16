package runtime

import (
	"errors"
	"fmt"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// WailsBindings 是不依赖 Wails SDK 的强类型桌面绑定集合。
// 后续 Wails 适配器只需把这些方法暴露给生成器，不应直接调用 Store 或调度器。
type WailsBindings struct {
	application *DesktopApplication
}

// CreateSchedule 创建单次或重复日程。
func (b *WailsBindings) CreateSchedule(payload string) (app.ScheduleDTO, error) {
	if err := b.validate(); err != nil {
		return app.ScheduleDTO{}, err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return app.ScheduleDTO{}, err
	}
	request, err := app.DecodeCreateSchedule([]byte(payload))
	if err != nil {
		return app.ScheduleDTO{}, err
	}
	return b.application.service.CreateSchedule(ctx, request)
}

// GetSchedule 读取单条日程及其取消例外。
func (b *WailsBindings) GetSchedule(id int64) (app.ScheduleDTO, error) {
	if err := b.validate(); err != nil {
		return app.ScheduleDTO{}, err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return app.ScheduleDTO{}, err
	}
	return b.application.service.GetSchedule(ctx, domain.ScheduleID(id))
}

// ListSchedules 按游标读取日程列表。
func (b *WailsBindings) ListSchedules(request app.ListSchedulesRequest) (app.ListSchedulesResponse, error) {
	if err := b.validate(); err != nil {
		return app.ListSchedulesResponse{}, err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return app.ListSchedulesResponse{}, err
	}
	return b.application.service.ListSchedules(ctx, request)
}

// DeleteSchedule 永久删除日程并返回已删除标题。
func (b *WailsBindings) DeleteSchedule(id int64, payload string) (DeleteScheduleDTO, error) {
	if err := b.validate(); err != nil {
		return DeleteScheduleDTO{}, err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return DeleteScheduleDTO{}, err
	}
	request, err := app.DecodeDeleteSchedule([]byte(payload))
	if err != nil {
		return DeleteScheduleDTO{}, err
	}
	title, err := b.application.service.DeleteSchedule(ctx, domain.ScheduleID(id), request)
	return DeleteScheduleDTO{Title: title}, err
}

// GetScheduleTimeBounds 返回当前时间环境下的日程时间边界。
func (b *WailsBindings) GetScheduleTimeBounds() (app.TimeBoundsDTO, error) {
	if err := b.validate(); err != nil {
		return app.TimeBoundsDTO{}, err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return app.TimeBoundsDTO{}, err
	}
	return b.application.service.GetScheduleTimeBounds(ctx)
}

// GetRuntimeStatus 返回提醒调度器状态。
func (b *WailsBindings) GetRuntimeStatus() app.SchedulerStatus {
	if b == nil || b.application == nil || b.application.scheduler == nil {
		return app.SchedulerStatus{}
	}
	return b.application.scheduler.GetRuntimeStatus()
}

// GetDesktopStatus 返回桌面宿主和激活握手状态。
func (b *WailsBindings) GetDesktopStatus() DesktopStatusDTO {
	if b == nil || b.application == nil {
		return DesktopStatusDTO{}
	}
	return b.application.GetDesktopStatus()
}

// GetCloseBehavior 返回当前窗口关闭偏好。
func (b *WailsBindings) GetCloseBehavior() (CloseBehaviorDTO, error) {
	if err := b.validate(); err != nil {
		return CloseBehaviorDTO{}, err
	}
	b.application.mu.Lock()
	defer b.application.mu.Unlock()
	return CloseBehaviorDTO{Behavior: string(b.application.config.Window.CloseBehavior)}, nil
}

// SaveCloseBehavior 校验并持久化窗口关闭偏好。
func (b *WailsBindings) SaveCloseBehavior(request CloseBehaviorDTO) error {
	if err := b.validate(); err != nil {
		return err
	}
	if err := b.application.ensureWritable(); err != nil {
		return err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.application.mu.Lock()
	defer b.application.mu.Unlock()
	if b.application.config == nil {
		return errors.New("runtime config is not configured")
	}
	if b.application.state != DesktopStateRunning {
		return domain.NewRuntimeStopping()
	}
	if err := b.application.config.SaveCloseBehavior(config.CloseBehavior(request.Behavior)); err != nil {
		return fmt.Errorf("save close behavior: %w", err)
	}
	return nil
}

// FrontendReady 完成 React 激活事件订阅后的序列握手。
func (b *WailsBindings) FrontendReady(request FrontendReadyRequest) (FrontendReadyResponse, error) {
	if b == nil || b.application == nil {
		return FrontendReadyResponse{}, errors.New("desktop bindings are not configured")
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return FrontendReadyResponse{}, err
	}
	return b.application.FrontendReady(ctx, request)
}

// RetryCleanup 重试通知清理和桌面资源关闭。
func (b *WailsBindings) RetryCleanup() (DesktopStatusDTO, error) {
	if b == nil || b.application == nil {
		return DesktopStatusDTO{}, errors.New("desktop bindings are not configured")
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return DesktopStatusDTO{}, err
	}
	return b.application.RetryCleanup(ctx)
}

// validate 确保绑定始终指向已经完成组装的应用对象。
func (b *WailsBindings) validate() error {
	if b == nil || b.application == nil {
		return errors.New("desktop bindings are not configured")
	}
	b.application.mu.Lock()
	defer b.application.mu.Unlock()
	if b.application.service == nil || b.application.config == nil {
		return errors.New("desktop application is not assembled")
	}
	return nil
}

// ensureWritable 只允许运行状态下的偏好写入，停止阶段统一返回机器可读错误。
func (a *DesktopApplication) ensureWritable() error {
	a.mu.Lock()
	state := a.state
	a.mu.Unlock()
	if state != DesktopStateRunning {
		return domain.NewRuntimeStopping()
	}
	return nil
}

// ErrorDTO 是前端稳定错误结构，不暴露底层错误 cause。
type ErrorDTO struct {
	Code    string             `json:"code"`
	Message string             `json:"message"`
	Fields  []string           `json:"fields,omitzero"`
	Bounds  *app.TimeBoundsDTO `json:"bounds,omitzero"`
}

// ErrorFormatter 是供 Wails 边界使用的稳定错误格式化器。
type ErrorFormatter struct{}

// Format 将业务错误转换为不泄露底层 cause 的前端错误 DTO。
func (ErrorFormatter) Format(err error) ErrorDTO {
	return FormatError(err)
}

// FormatError 将业务错误转换为稳定的前端错误视图。
func FormatError(err error) ErrorDTO {
	if err == nil {
		return ErrorDTO{Code: "INTERNAL", Message: "internal error"}
	}
	result := ErrorDTO{Code: "INTERNAL", Message: "internal error"}
	var businessErr *domain.Error
	if errors.As(err, &businessErr) {
		result.Code = string(businessErr.Code)
		result.Message = businessErr.Message
		result.Fields = append([]string(nil), businessErr.Fields...)
		if businessErr.Bounds != nil {
			bounds := app.TimeBoundsDTO{
				SingleMinUnix:          businessErr.Bounds.SingleMin.Unix(),
				SingleMaxExclusiveUnix: businessErr.Bounds.SingleMaxExclusive.Unix(),
				QueryMaxExclusiveUnix:  businessErr.Bounds.QueryMaxExclusive.Unix(),
			}
			result.Bounds = &bounds
		}
	}
	return result
}
