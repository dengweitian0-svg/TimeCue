package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
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
	Host          platform.DesktopHost
	Window        platform.WindowController
	Frontend      platform.FrontendEvents
	Activation    platform.ActivationSource
	Environment   platform.EnvironmentEventSource
	Tray          platform.TrayPort
	Notifications platform.NotificationCenter
	Clock         app.Clock
	TimerFactory  app.TimerFactory
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

	mu              sync.Mutex
	config          *config.Config
	service         *app.Service
	coordinator     *app.MutationCoordinator
	scheduler       *app.ReminderScheduler
	bindings        *WailsBindings
	schedulerCancel context.CancelFunc
	schedulerDone   chan error
	schedulerJoined bool
	eventCancel     context.CancelFunc

	state              DesktopLifecycleState
	cleanupError       error
	resourcesClosed    bool
	lastError          error
	runtimeContext     context.Context
	domReady           bool
	stopDone           chan struct{}
	activationSequence uint64
	latestActivation   *ActivationDTO
	frontendReady      bool
}

// NewDesktopApplication 创建尚未组装 Store 的桌面运行单元。
func NewDesktopApplication(dependencies DesktopDependencies) *DesktopApplication {
	return &DesktopApplication{deps: dependencies, state: DesktopStateStopped}
}

// Service 返回运行时唯一共享的日程应用服务，主要供组装边界测试使用。
func (a *DesktopApplication) Service() *app.Service {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.service
}

// Coordinator 返回运行时唯一共享的变更协调器。
func (a *DesktopApplication) Coordinator() *app.MutationCoordinator {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.coordinator
}

// Scheduler 返回运行时唯一共享的提醒调度器。
func (a *DesktopApplication) Scheduler() *app.ReminderScheduler {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scheduler
}

// Bindings 返回运行时唯一共享的绑定对象。
func (a *DesktopApplication) Bindings() *WailsBindings {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bindings
}

// Logger 返回 bootstrap 注入的统一结构化日志器；组装完成前返回 nil。
func (a *DesktopApplication) Logger() *slog.Logger {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.logger
}

// Run 组装并启动桌面应用，主机运行路径保持同步且 scheduler 只启动一个 goroutine。
func (a *DesktopApplication) Run(ctx context.Context, cfg config.Config, logger *slog.Logger, store bootstrap.Store) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		return a.failStart(errors.New("desktop application logger is nil"))
	}
	desktopStore, ok := store.(DesktopStore)
	if !ok || desktopStore == nil {
		return a.failStart(errors.New("bootstrap store does not implement desktop store"))
	}
	if err := a.assemble(cfg, logger, desktopStore); err != nil {
		return a.failStart(err)
	}

	var hostErr error
	if a.deps.Host != nil {
		// DesktopHost.Run 必须同步执行，具体 Wails 实现不得把主循环再放入后台。
		hostErr = a.deps.Host.Run(ctx)
	} else {
		<-ctx.Done()
		hostErr = ctx.Err()
	}
	stopErr := a.stop(context.WithoutCancel(ctx), false)
	if hostErr != nil && !errors.Is(hostErr, context.Canceled) && !errors.Is(hostErr, context.DeadlineExceeded) {
		return errors.Join(hostErr, stopErr)
	}
	return stopErr
}

// OnStartup 保存桌面宿主提供的运行时 context；此阶段不调用窗口或通知 Runtime API。
func (a *DesktopApplication) OnStartup(ctx context.Context) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if ctx == nil {
		return errors.New("desktop startup context is nil")
	}
	a.mu.Lock()
	a.runtimeContext = ctx
	a.mu.Unlock()
	return nil
}

// OnDomReady 延迟初始化桌面资源，并在前端可用后启动唯一 scheduler 和事件转发器。
func (a *DesktopApplication) OnDomReady(ctx context.Context) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if ctx == nil {
		return errors.New("desktop DOM-ready context is nil")
	}
	a.mu.Lock()
	if a.domReady {
		a.mu.Unlock()
		return nil
	}
	if a.resourcesClosed || a.state == DesktopStateStopping || a.state == DesktopStateCleanupFailed {
		// 早到的取消可能先于 DOM ready 进入停止；只保存 context 供清理失败页面重试，不得重启资源。
		a.runtimeContext = ctx
		a.domReady = true
		a.mu.Unlock()
		return nil
	}
	if a.scheduler == nil {
		a.mu.Unlock()
		return errors.New("desktop application is not assembled")
	}
	a.runtimeContext = ctx
	a.domReady = true
	// 资源初始化与 scheduler 启动必须发生在 DOM ready 之后，Wails Runtime context 才有效。
	runCtx, cancel := context.WithCancel(ctx)
	a.schedulerCancel = cancel
	a.schedulerDone = make(chan error, 1)
	a.schedulerJoined = false
	a.eventCancel = cancel
	a.state = DesktopStateRunning
	scheduler := a.scheduler
	schedulerDone := a.schedulerDone
	a.mu.Unlock()
	if err := a.startResources(ctx); err != nil {
		cancel()
		return a.failStart(err)
	}
	go func() {
		// scheduler 是唯一的提醒循环；其他 goroutine 只负责转发外部事件。
		schedulerDone <- scheduler.Run(runCtx)
	}()
	a.startEventForwarders(runCtx)
	return nil
}

// assemble 从同一个 Store 创建并保存唯一的应用服务、协调器和提醒调度器。
func (a *DesktopApplication) assemble(cfg config.Config, logger *slog.Logger, store DesktopStore) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.service != nil || a.scheduler != nil {
		return errors.New("desktop application has already been assembled")
	}
	if a.deps.Clock == nil {
		return errors.New("desktop application clock is not configured")
	}
	if a.deps.Notifications == nil {
		return errors.New("desktop application notification center is not configured")
	}
	coordinator := app.NewMutationCoordinator()
	service := app.NewServiceWithCoordinator(store, a.deps.Clock, coordinator)
	scheduler := app.NewReminderScheduler(store, store, a.deps.Notifications, a.deps.Clock, a.deps.TimerFactory, logger, coordinator)
	a.config = &cfg
	a.logger = logger
	a.coordinator = coordinator
	a.service = service
	a.scheduler = scheduler
	a.bindings = &WailsBindings{application: a}
	a.resourcesClosed = false
	return nil
}

// startResources 按运行时拥有权初始化通知、激活源、环境源和托盘资源。
func (a *DesktopApplication) startResources(ctx context.Context) error {
	if err := a.deps.Notifications.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize notification center: %w", err)
	}
	if a.deps.Activation != nil {
		if err := a.deps.Activation.Start(ctx); err != nil {
			return fmt.Errorf("start activation source: %w", err)
		}
	}
	if a.deps.Environment != nil {
		if err := a.deps.Environment.Start(ctx); err != nil {
			return fmt.Errorf("start environment event source: %w", err)
		}
	}
	if a.deps.Tray != nil {
		if err := a.deps.Tray.Start(ctx, platform.TrayCallbacks{OnOpen: a.onTrayOpen, OnExit: a.onTrayExit}); err != nil {
			return fmt.Errorf("start tray: %w", err)
		}
	}
	return nil
}

// failStart 清理启动阶段已经初始化的资源并把应用恢复为可观察的停止状态。
func (a *DesktopApplication) failStart(primary error) error {
	cleanupErr := errors.Join(closeEnvironment(a.deps.Environment), closeActivation(a.deps.Activation), closeTray(a.deps.Tray), closeNotifications(a.deps.Notifications))
	a.mu.Lock()
	a.state = DesktopStateStopped
	a.resourcesClosed = true
	a.domReady = false
	a.schedulerCancel = nil
	a.schedulerDone = nil
	a.schedulerJoined = false
	a.eventCancel = nil
	a.mu.Unlock()
	if cleanupErr != nil {
		return errors.Join(primary, cleanupErr)
	}
	return primary
}

// startEventForwarders 将平台事实回流至调度器，并保持失败可观察。
func (a *DesktopApplication) startEventForwarders(ctx context.Context) {
	if a.deps.Activation != nil {
		go a.forwardActivations(ctx, a.deps.Activation.Events())
	}
	if a.deps.Environment != nil {
		go a.forwardEnvironmentEvents(ctx, a.deps.Environment.Events())
	}
}

// forwardActivations 为每条激活分配单调序列，并在前端就绪后发送最新事件。
func (a *DesktopApplication) forwardActivations(ctx context.Context, events <-chan platform.Activation) {
	for {
		select {
		case <-ctx.Done():
			return
		case activation, ok := <-events:
			if !ok {
				return
			}
			a.publishActivation(ctx, activation)
		}
	}
}

// forwardEnvironmentEvents 把外部环境事件送入唯一调度器边界。
func (a *DesktopApplication) forwardEnvironmentEvents(ctx context.Context, events <-chan app.RuntimeEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := a.scheduler.ApplyRuntimeEvent(ctx, event); err != nil {
				a.recordError(err)
			}
		}
	}
}

// publishActivation 保存最新激活并在前端已注册时发送；发送失败会保留待发送事件。
func (a *DesktopApplication) publishActivation(ctx context.Context, activation platform.Activation) {
	dto := activationDTO(activation)
	a.mu.Lock()
	a.activationSequence++
	dto.Sequence = a.activationSequence
	if activation.Kind == platform.ActivationKindToast {
		// 普通第二实例只表示窗口激活，不得作为前端目标积压或补发。
		a.latestActivation = cloneActivationDTO(&dto)
	}
	ready := a.frontendReady
	frontend := a.deps.Frontend
	window := a.deps.Window
	a.mu.Unlock()
	// 第二实例激活没有目标，也必须先把窗口带到前台；Toast 激活随后再把目标交给前端。
	lifecycleCtx := a.lifecycleContext()
	if window != nil && lifecycleCtx != nil {
		if err := window.Show(lifecycleCtx); err != nil {
			a.recordError(err)
			return
		}
		if err := window.Activate(lifecycleCtx); err != nil {
			a.recordError(err)
			return
		}
	}
	if activation.Kind != platform.ActivationKindToast || !ready || frontend == nil {
		return
	}
	if lifecycleCtx == nil {
		lifecycleCtx = ctx
	}
	if err := frontend.Emit(lifecycleCtx, platform.FrontendEventActivation, dto); err != nil {
		a.recordError(err)
	}
}

// FrontendReady 完成激活握手，并补发前端尚未消费的最新激活。
func (a *DesktopApplication) FrontendReady(ctx context.Context, request FrontendReadyRequest) (FrontendReadyResponse, error) {
	if a == nil {
		return FrontendReadyResponse{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	a.frontendReady = true
	latest := cloneActivationDTO(a.latestActivation)
	frontend := a.deps.Frontend
	a.mu.Unlock()
	if latest == nil || latest.Sequence <= request.LastSequence || frontend == nil {
		return FrontendReadyResponse{}, nil
	}
	if err := frontend.Emit(ctx, platform.FrontendEventActivation, *latest); err != nil {
		a.recordError(err)
		return FrontendReadyResponse{}, fmt.Errorf("emit pending activation: %w", err)
	}
	return FrontendReadyResponse{Activation: latest}, nil
}

// onTrayOpen 将托盘打开动作转换为窗口显示和激活。
func (a *DesktopApplication) onTrayOpen() {
	if a.deps.Window == nil {
		return
	}
	ctx := a.lifecycleContext()
	if ctx == nil {
		return
	}
	if err := a.deps.Window.Show(ctx); err != nil {
		a.recordError(err)
		return
	}
	if err := a.deps.Window.Activate(ctx); err != nil {
		a.recordError(err)
	}
}

// onTrayExit 将托盘退出动作汇入统一停止路径。
func (a *DesktopApplication) onTrayExit() {
	ctx := a.lifecycleContext()
	if ctx == nil {
		return
	}
	if err := a.Stop(ctx); err != nil {
		a.recordError(err)
	}
}

// Stop 进入可重入、可重试的停止状态机，并在成功清理后请求宿主退出。
func (a *DesktopApplication) Stop(ctx context.Context) error {
	return a.stop(ctx, true)
}

// StopFromHost 进入同一停止状态机，但由正在处理关闭的宿主掌握最终 Quit。
func (a *DesktopApplication) StopFromHost(ctx context.Context) error {
	return a.stop(ctx, false)
}

// stop 执行停止状态机；requestQuit 仅由外部停止请求使用，避免 Run 收尾递归退出。
func (a *DesktopApplication) stop(ctx context.Context, requestQuit bool) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	if a.state == DesktopStateStopped && a.resourcesClosed {
		a.mu.Unlock()
		return nil
	}
	if a.state == DesktopStateStopping {
		done := a.stopDone
		a.mu.Unlock()
		if done != nil {
			select {
			case <-done:
				a.mu.Lock()
				err := a.cleanupError
				a.mu.Unlock()
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		a.mu.Lock()
		err := a.cleanupError
		a.mu.Unlock()
		return err
	}
	retryCleanup := a.state == DesktopStateCleanupFailed
	a.state = DesktopStateStopping
	done := make(chan struct{})
	a.stopDone = done
	cancel := a.schedulerCancel
	schedulerDone := a.schedulerDone
	coordinator := a.coordinator
	a.mu.Unlock()

	var stopErr error
	if !retryCleanup {
		if coordinator != nil {
			stopErr = errors.Join(stopErr, coordinator.ApplyRuntimeEvent(app.RuntimeEvent{Type: app.RuntimeEventStop}))
		}
		if cancel != nil {
			cancel()
		}
		if schedulerDone != nil {
			select {
			case schedulerErr := <-schedulerDone:
				a.mu.Lock()
				a.schedulerJoined = true
				a.mu.Unlock()
				if schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
					stopErr = errors.Join(stopErr, schedulerErr)
				}
			case <-ctx.Done():
				stopErr = errors.Join(stopErr, ctx.Err())
			}
		}
	} else if schedulerDone != nil {
		// 首次 Stop 可能因调用方 context 超时提前返回；重试仍必须先等 scheduler 真正退出。
		a.mu.Lock()
		joined := a.schedulerJoined
		a.mu.Unlock()
		if !joined {
			select {
			case schedulerErr := <-schedulerDone:
				a.mu.Lock()
				a.schedulerJoined = true
				a.mu.Unlock()
				if schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
					stopErr = errors.Join(stopErr, schedulerErr)
				}
			case <-ctx.Done():
				stopErr = errors.Join(stopErr, ctx.Err())
			}
		}
	}
	if stopErr == nil {
		stopErr = errors.Join(stopErr, a.deps.Notifications.Cleanup(ctx))
	}
	if stopErr == nil {
		stopErr = errors.Join(stopErr, closeEnvironment(a.deps.Environment), closeActivation(a.deps.Activation), closeTray(a.deps.Tray), closeNotifications(a.deps.Notifications))
	}

	a.mu.Lock()
	a.cleanupError = stopErr
	if stopErr == nil {
		a.state = DesktopStateStopped
		a.resourcesClosed = true
	} else {
		a.state = DesktopStateCleanupFailed
	}
	close(done)
	a.mu.Unlock()
	if stopErr != nil {
		return stopErr
	}
	if requestQuit && a.deps.Host != nil {
		if err := a.deps.Host.Quit(ctx); err != nil {
			return fmt.Errorf("quit desktop host: %w", err)
		}
	}
	return nil
}

// recordError 保存最近一次运行时错误，供状态查询和日志边界使用。
func (a *DesktopApplication) recordError(err error) {
	if a == nil || err == nil {
		return
	}
	a.mu.Lock()
	a.lastError = err
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Error("desktop runtime error", "error", err)
	}
}

// lifecycleContext 返回 OnStartup/OnDomReady 保存的宿主 context，禁止平台回调自行创建 context。
func (a *DesktopApplication) lifecycleContext() context.Context {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runtimeContext
}

// bindingContext 返回绑定调用使用的宿主 context。
func (a *DesktopApplication) bindingContext() (context.Context, error) {
	ctx := a.lifecycleContext()
	if ctx == nil {
		return nil, errors.New("desktop runtime context is not ready")
	}
	return ctx, nil
}

// GetDesktopStatus 返回桌面生命周期、清理错误和激活握手状态。
func (a *DesktopApplication) GetDesktopStatus() DesktopStatusDTO {
	if a == nil {
		return DesktopStatusDTO{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	status := DesktopStatusDTO{State: a.state, ActivationSequence: a.activationSequence, FrontendReady: a.frontendReady}
	if a.cleanupError != nil {
		status.CleanupError = a.cleanupError.Error()
	}
	if a.lastError != nil {
		status.LastError = a.lastError.Error()
	}
	return status
}

// RetryCleanup 重试失败的通知清理和桌面资源释放。
func (a *DesktopApplication) RetryCleanup(ctx context.Context) (DesktopStatusDTO, error) {
	if err := a.stop(ctx, false); err != nil {
		return a.GetDesktopStatus(), err
	}
	if a.deps.Host != nil {
		lifecycleCtx := a.lifecycleContext()
		if lifecycleCtx == nil {
			return a.GetDesktopStatus(), errors.New("desktop runtime context is not ready")
		}
		if err := a.deps.Host.Quit(lifecycleCtx); err != nil {
			return a.GetDesktopStatus(), fmt.Errorf("quit desktop host after cleanup retry: %w", err)
		}
	}
	return a.GetDesktopStatus(), nil
}

// closeEnvironment 关闭环境事件源并保留关闭错误上下文。
func closeEnvironment(source platform.EnvironmentEventSource) error {
	if source == nil {
		return nil
	}
	if err := source.Close(); err != nil {
		return fmt.Errorf("close environment event source: %w", err)
	}
	return nil
}

// closeActivation 关闭激活事件源并保留关闭错误上下文。
func closeActivation(source platform.ActivationSource) error {
	if source == nil {
		return nil
	}
	if err := source.Close(); err != nil {
		return fmt.Errorf("close activation source: %w", err)
	}
	return nil
}

// closeTray 关闭托盘资源并保留关闭错误上下文。
func closeTray(tray platform.TrayPort) error {
	if tray == nil {
		return nil
	}
	if err := tray.Close(); err != nil {
		return fmt.Errorf("close tray: %w", err)
	}
	return nil
}

// closeNotifications 关闭通知资源并保留关闭错误上下文。
func closeNotifications(center platform.NotificationCenter) error {
	if center == nil {
		return nil
	}
	if err := center.Close(); err != nil {
		return fmt.Errorf("close notification center: %w", err)
	}
	return nil
}

// activationDTO 将平台激活事实投影为不暴露内部指针的前端 DTO。
func activationDTO(activation platform.Activation) ActivationDTO {
	result := ActivationDTO{Kind: activation.Kind}
	if activation.Target != nil {
		result.Target = &ActivationTargetDTO{ScheduleID: int64(activation.Target.ScheduleID), OriginalDate: activation.Target.OriginalDate}
	}
	return result
}

// cloneActivationDTO 复制激活 DTO，避免前端或测试持有运行时内部指针。
func cloneActivationDTO(value *ActivationDTO) *ActivationDTO {
	if value == nil {
		return nil
	}
	clone := *value
	if value.Target != nil {
		target := *value.Target
		clone.Target = &target
	}
	return &clone
}
