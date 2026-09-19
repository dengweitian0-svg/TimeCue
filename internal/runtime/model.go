package runtime

import (
	"context"
	"log/slog"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// DesktopStore 是桌面运行时一次性取得的完整存储组合端口。
// 具体 Store 仍由 bootstrap 拥有和关闭；runtime 只在组装期间借用这些能力。
type DesktopStore interface {
	bootstrap.Store
	app.ScheduleStore
	app.ScheduleUpdater
	app.ScheduleDeleter
	app.OccurrenceReader
	app.OccurrenceUpdater
	app.OccurrenceCanceller
	app.OccurrenceRestorer
	app.ScheduleCanceller
	app.ScheduleRestorer
	app.ReminderSourceStore
	app.ReminderAttemptStore
}

// DesktopDependencies 描述 DesktopApplication 需要的运行时和平台端口。
// platform 字段均为可替换窄契约，runtime 不依赖任何具体桌面工具包。
type DesktopDependencies struct {
	Host             platform.DesktopHost
	Window           platform.WindowController
	WindowTheme      platform.WindowThemeController
	Frontend         platform.FrontendEvents
	Activation       platform.ActivationSource
	Environment      platform.EnvironmentEventSource
	Tray             platform.TrayPort
	Notifications    platform.NotificationCenter
	DirectoryAccess  platform.DirectoryAccess
	DirectoryChooser platform.DirectoryChooser
	Clock            app.Clock
	TimerFactory     app.TimerFactory
}

// DesktopLifecycleState 是桌面资源停止状态机的受控状态。
type DesktopLifecycleState string

const (
	// DesktopStateRunning 表示运行时正在接受只读请求和业务写入。
	DesktopStateRunning DesktopLifecycleState = "running"
	// DesktopStateStopping 表示已拒绝新的业务写入并正在释放资源。
	DesktopStateStopping DesktopLifecycleState = "stopping"
	// DesktopStateStopped 表示所有运行时拥有的资源均已释放。
	DesktopStateStopped DesktopLifecycleState = "stopped"
	// DesktopStateCleanupFailed 表示调度已停止但桌面资源清理失败，可重试。
	DesktopStateCleanupFailed DesktopLifecycleState = "cleanup_failed"
)

// DesktopStatusDTO 是前端可见的桌面生命周期和激活握手状态。
type DesktopStatusDTO struct {
	State              DesktopLifecycleState `json:"state"`
	CleanupError       string                `json:"cleanup_error,omitzero"`
	LastError          string                `json:"last_error,omitzero"`
	ActivationSequence uint64                `json:"activation_sequence"`
	FrontendReady      bool                  `json:"frontend_ready"`
	RecoveryStatus     app.RecoveryStatus    `json:"recovery_status"`
}

// ActivationTargetDTO 是前端边界使用的稳定发生身份视图。
type ActivationTargetDTO struct {
	ScheduleID   int64  `json:"schedule_id"`
	OriginalDate string `json:"original_date"`
}

// ActivationDTO 是带单调序列的前端激活事件。
type ActivationDTO struct {
	Sequence uint64                  `json:"sequence"`
	Kind     platform.ActivationKind `json:"kind"`
	Target   *ActivationTargetDTO    `json:"target,omitzero"`
}

// FrontendReadyRequest 描述前端已消费到的激活序列。
type FrontendReadyRequest struct {
	LastSequence uint64 `json:"last_sequence"`
}

// FrontendReadyResponse 返回握手时仍需消费的最新激活。
type FrontendReadyResponse struct {
	Activation *ActivationDTO `json:"activation,omitzero"`
}

// CloseBehaviorDTO 是窗口关闭偏好的稳定 JSON 视图。
type CloseBehaviorDTO struct {
	Behavior string `json:"behavior"`
}

// LogLevelDTO 返回日志级别当前生效值与下次启动保存值的受控视图。
type LogLevelDTO struct {
	EffectiveLevel  string `json:"effective_level"`
	SavedLevel      string `json:"saved_level"`
	RestartRequired bool   `json:"restart_required"`
}

// ThemeDTO 返回当前生效的应用主题。
type ThemeDTO struct {
	Theme string `json:"theme"`
}

// SaveLogLevelRequest 描述一次日志级别保存请求。
type SaveLogLevelRequest struct {
	Level string `json:"level"`
}

// SaveThemeRequest 描述一次主题保存请求。
type SaveThemeRequest struct {
	Theme string `json:"theme"`
}

// ApplyWindowThemeRequest 描述前端传递给原生窗口的语义颜色。
type ApplyWindowThemeRequest struct {
	Background string `json:"background"`
	Text       string `json:"text"`
}

// RestoreConfigDefaultRequest 描述一次受限配置属性恢复默认请求。
type RestoreConfigDefaultRequest struct {
	Property string `json:"property"`
}

// StorageDirectoryDTO 返回目录当前生效值和配置保存值的受控视图。
type StorageDirectoryDTO struct {
	Kind            string `json:"kind"`
	EffectivePath   string `json:"effective_path"`
	SavedPath       string `json:"saved_path"`
	RestartRequired bool   `json:"restart_required"`
}

// StorageDirectoriesDTO 同时返回日志和备份目录设置，不包含数据库路径。
type StorageDirectoriesDTO struct {
	Logs    StorageDirectoryDTO `json:"logs"`
	Backups StorageDirectoryDTO `json:"backups"`
}

// SaveStorageDirectoryRequest 描述一次目录保存请求。
type SaveStorageDirectoryRequest struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// DeleteScheduleDTO 是永久删除结果的稳定 JSON 视图。
type DeleteScheduleDTO struct {
	Title string `json:"title"`
}

// DesktopApplication 是平台无关的桌面运行单元。
// 它只创建一个 Service、一个 MutationCoordinator 和一个 ReminderScheduler；
// bootstrap Store 的所有权不转移到本结构。
type DesktopApplication struct {
	deps   DesktopDependencies
	logger *slog.Logger

	mu                   sync.Mutex
	config               *config.Config
	effectiveLogLevel    string
	service              *app.Service
	coordinator          *app.MutationCoordinator
	scheduler            *app.ReminderScheduler
	bindings             *WailsBindings
	schedulerCancel      context.CancelFunc
	schedulerDone        chan error
	schedulerJoined      bool
	eventCancel          context.CancelFunc
	environmentForwarder sync.WaitGroup

	state                   DesktopLifecycleState
	cleanupError            error
	resourcesClosed         bool
	lastError               error
	runtimeContext          context.Context
	domReady                bool
	starting                bool
	startupDone             chan struct{}
	startupCancel           context.CancelFunc
	stopDone                chan struct{}
	activationSequence      uint64
	latestActivation        *ActivationDTO
	frontendReady           bool
	latestRecovery          *app.RecoveryEvent
	pendingScheduleHidden   map[domain.ScheduleID]struct{}
	pendingRemindersChanged bool
}
