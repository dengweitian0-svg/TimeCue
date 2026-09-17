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

// StorageDirectoryError 标记目录设置边界错误，并保留底层原因供日志和测试检查。
type StorageDirectoryError struct {
	Operation string
	Kind      string
	Cause     error
}

// Error 返回带操作和目录类型上下文的目录设置错误。
func (e *StorageDirectoryError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return fmt.Sprintf("%s (%s)", e.Operation, e.Kind)
	}
	return fmt.Sprintf("%s (%s): %v", e.Operation, e.Kind, e.Cause)
}

// Unwrap 返回目录设置的底层原因，保持正式错误链路。
func (e *StorageDirectoryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewWailsBindingsForGeneration 创建只供 Wails 反射生成前端声明的无副作用绑定实例。
func NewWailsBindingsForGeneration() *WailsBindings {
	return &WailsBindings{}
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

// GetStorageDirectories 返回日志和备份目录的当前生效值与已保存值。
func (b *WailsBindings) GetStorageDirectories() (StorageDirectoriesDTO, error) {
	if err := b.validate(); err != nil {
		return StorageDirectoriesDTO{}, err
	}
	return b.application.GetStorageDirectories()
}

// SaveStorageDirectory 保存一类目录配置，并返回重启提示所需的完整对照 DTO。
func (b *WailsBindings) SaveStorageDirectory(request SaveStorageDirectoryRequest) (StorageDirectoryDTO, error) {
	if err := b.validate(); err != nil {
		return StorageDirectoryDTO{}, err
	}
	if err := b.application.ensureWritable(); err != nil {
		return StorageDirectoryDTO{}, err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return StorageDirectoryDTO{}, err
	}
	return b.application.SaveStorageDirectory(ctx, request)
}

// ChooseStorageDirectory 打开日志或备份目录选择器；用户取消时返回空字符串。
func (b *WailsBindings) ChooseStorageDirectory(kind string) (string, error) {
	if err := b.validate(); err != nil {
		return "", err
	}
	ctx, err := b.application.bindingContext()
	if err != nil {
		return "", err
	}
	return b.application.ChooseStorageDirectory(ctx, kind)
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

// newStorageOperationError 创建不向前端泄露底层路径细节的目录设置错误。
func (a *DesktopApplication) newStorageOperationError(operation, kind string, cause error) error {
	return &StorageDirectoryError{Operation: operation, Kind: kind, Cause: cause}
}

// storageOperationError 记录目录设置最终失败并返回保留 cause 的错误。
func (a *DesktopApplication) storageOperationError(operation, kind string, cause error) error {
	err := a.newStorageOperationError(operation, kind, cause)
	a.recordError(err)
	return err
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
	var storageDirectoryErr *StorageDirectoryError
	if errors.As(err, &storageDirectoryErr) {
		return ErrorDTO{Code: "STORAGE", Message: "目录设置未完成，请检查目录和配置文件权限。"}
	}
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
