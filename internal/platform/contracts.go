// Package platform 定义桌面运行时与操作系统/桌面工具包之间的窄契约。
//
// 本包只表达边界上的数据、调用和资源所有权，不引入 Wails、Win32 或
// 其他具体平台实现。平台适配器只能通过这些端口把外部事实回流给应用层，
// 不得直接修改日程领域状态。
package platform

import (
	"context"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// DesktopHost 负责运行桌面主循环，并提供请求退出桌面宿主的能力。
// Run 必须由调用方同步调用且每个应用实例至多调用一次；宿主占有其底层
// GUI 资源，Run 返回后这些资源不再由调用方继续使用。Quit 只发出退出
// 请求，不替代应用的正式停止状态机。
type DesktopHost interface {
	Run(context.Context) error
	Quit(context.Context) error
}

// WindowController 管理主窗口的可见性和激活，不拥有桌面主循环。
// 窗口操作使用调用方传入的生命周期 context；实现不得自行创建全局
// context 或绕过宿主直接结束应用。
type WindowController interface {
	Show(context.Context) error
	Hide(context.Context) error
	Minimize(context.Context) error
	Activate(context.Context) error
}

// FrontendEvents 向前端发送受控的应用事件。
// name 由运行时定义的事件名常量约束，payload 必须是边界 DTO；实现不得
// 将数据库连接、领域内部可变对象或底层错误 cause 作为载荷暴露。
type FrontendEvents interface {
	Emit(context.Context, string, any) error
}

// DirectoryAccess 验证用户选择的目录并返回规范物理路径。
// 实现负责创建缺失目录以及验证临时文件的写入、同步和删除；运行时不直接
// 操作文件系统，从而可以用 fake 覆盖权限和失败分支。
type DirectoryAccess interface {
	ValidateDirectory(context.Context, string) (string, error)
}

// DirectoryChooser 提供桌面目录选择对话框。
// 返回空字符串且无错误表示用户取消；取消不能改变输入或已保存配置。
type DirectoryChooser interface {
	ChooseDirectory(context.Context, string, string) (string, error)
}

// ActivationKind 表示一次外部激活的来源。
type ActivationKind string

const (
	// ActivationKindSecondInstance 表示普通第二实例请求激活已有主实例。
	ActivationKindSecondInstance ActivationKind = "second_instance"
	// ActivationKindToast 表示用户点击系统通知后的激活。
	ActivationKindToast ActivationKind = "toast"
)

// Activation 表示一次需要交给运行时处理的窗口激活事实。
// Target 为空表示只激活窗口而没有具体发生目标；非空时其身份仍由领域
// 的稳定 (schedule_id, original_date) 组合定义，实际时间和文本不属于身份。
type Activation struct {
	Kind   ActivationKind
	Target *domain.OccurrenceIdentity
}

// ActivationSource 输出第二实例或 Toast 激活事件。
// Start 成功后 Events 返回稳定的只读事件流；Close 由创建/启动它的运行时
// 负责调用，且必须阻止后续发送并最终关闭事件流。Close 不承担业务停止，
// 也不关闭 bootstrap 所拥有的 Store 或实例锁。
type ActivationSource interface {
	Start(context.Context) error
	Events() <-chan Activation
	Close() error
}

// EnvironmentEventSource 输出已经转换为应用层 RuntimeEvent 的环境事实。
// 适配器负责读取操作系统时钟/电源/时区事实并填入事件快照；应用层负责
// 将事件交给 MutationCoordinator/ReminderScheduler。Close 后不得再发送
// 事件，调用方仍需通过统一停止路径处理已经收到的事件。
type EnvironmentEventSource interface {
	Start(context.Context) error
	Events() <-chan app.RuntimeEvent
	Close() error
}

// TrayCallbacks 定义托盘菜单触发的两个应用动作。
// 回调由托盘资源在其消息线程中调用；回调的错误处理和正式 Stop 编排由
// 注册它的运行时负责，托盘适配器不应自行修改运行时状态。
type TrayCallbacks struct {
	OnOpen func()
	OnExit func()
}

// TrayPort 管理托盘资源及其回调注册。
// Start 成功后托盘拥有传入回调的调用权，直到 Close 返回；Close 必须
// 等待消息线程退出并释放图标/菜单等资源，且可重复调用。Close 不负责
// 调用 OnExit，也不负责关闭 DesktopHost。
type TrayPort interface {
	Start(context.Context, TrayCallbacks) error
	Close() error
}

// NotificationCenter 是提醒调度器使用的通知端口及其桌面资源生命周期。
// Initialize 只负责完成桌面通知运行时初始化；Submit 的成功语义是系统
// 接受提交，不代表用户已经看到或处理通知。Cleanup 清理已提交的系统
// 通知且失败可由运行时重试；Close 必须停止新的提交、等待在途提交结算
// 并释放通知资源，但不把失败的 Cleanup 静默视为成功。
type NotificationCenter interface {
	app.ReminderNotifier
	Initialize(context.Context) error
	Cleanup(context.Context) error
	Close() error
}

// 窗口和前端事件名由运行时统一使用，避免各平台适配器私自发明 IPC 名称。
const (
	FrontendEventActivation    = "activation"
	FrontendEventRuntimeStatus = "runtime_status"
)
