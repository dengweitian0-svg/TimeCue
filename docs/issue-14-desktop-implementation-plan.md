# Issue #14：Windows 桌面运行时实施方案

## 1. 目标

在不改变现有领域模型和日程存储契约的前提下，将 TimeCue 从空闲启动壳替换为可用的 Windows 桌面应用，完成：

- Wails v2.15 桌面主机和 React 生产前端接入；
- 数据库初始化前的单实例守卫；
- 第二实例和 Toast 点击激活；
- Windows 托盘与关闭行为；
- Windows Toast 通知；
- 睡眠、恢复、墙钟和时区变化事件；
- 唯一的 Service、MutationCoordinator 和 ReminderScheduler；
- 明确、可重入、可重试的正式停止流程。

最终开发分支从 `dev` 创建，Pull Request 指向 `dev`，描述中使用 `Closes #14`。

## 2. 已冻结的产品决策

- 关闭窗口默认隐藏到托盘，用户可以切换为直接退出。
- 关闭偏好保存在可执行文件旁的 `config.yaml`。
- 配置优先级为内嵌 `defaults.yaml`，再由可执行文件旁的 `config.yaml` 覆盖。
- #12 的数据库路径规则保持不变。
- 单实例范围为当前 Windows 登录用户。
- 同时到期的 occurrence 逐条发送 Toast，不聚合。
- Toast 只携带 `schedule_id + original_date`。
- Toast 点击只负责激活窗口并把目标转交前端；完整详情导航留给 #31。
- Toast 不自动确认、不重发。
- 应用未运行时，Toast 激活不冷启动完整应用；允许 Windows 创建瞬时激活进程，但它必须在进入 Bootstrap、打开数据库或创建 UI 前退出。
- 通知清理失败时不直接退出：调度器保持停止，窗口继续存在并允许重试清理。
- #29、#30、#31 不属于本 Issue；生产前端隐藏这些尚未实现的入口。
- Wails 应用入口和工程文件统一位于仓库根目录；实现时将现有 `cmd/timecue` 入口迁移到根目录并同步更新仓库结构文档。

开始开发前，应将尚未写入 Issue 的“逐条 Toast、无冷启动、单实例作用域”补充到 Issue #14 评论中。

## 3. 架构关系

```text
仓库根目录 main.go
    │  只负责依赖注入、启动和最终错误展示
    ▼
bootstrap
    ├── ExclusiveStartup：单实例锁和第二实例转发
    ├── 配置、目录、日志、SQLite
    └── 逆序释放：Store → Logger → InstanceBroker
            │
            ▼
runtime.DesktopApplication
    ├── 唯一 Service
    ├── 唯一 MutationCoordinator
    ├── 唯一 ReminderScheduler
    ├── WailsBindings
    └── 桌面资源生命周期
            │
            ▼
platform 窄契约
    ├── DesktopHost
    ├── WindowController
    ├── FrontendEvents
    ├── ActivationSource
    ├── EnvironmentEventSource / Clock
    ├── NotificationCenter
    └── TrayPort
```

### 3.1 恒定规则

1. 单实例守卫必须早于目录、日志和 SQLite 初始化。
2. 领域状态只能通过现有 `Service`、`MutationCoordinator` 和 `ReminderScheduler` 修改。
3. Windows、Wails 和 Toast 适配器不得直接修改领域内部状态。
4. 全进程只能存在一个 coordinator 和一个 scheduler loop。
5. 所有退出来源必须汇入同一个可重入、可重试的停止状态机。
6. Bootstrap 拥有单实例锁和 Store，Runtime 不得提前释放它们。
7. 外部激活只作为事件回流，不得绕过正式业务边界写入数据。

### 3.2 Wails 生命周期映射

Wails 不规定唯一的应用内部分层，但正式实现必须遵循其生命周期和 Runtime context 规则：

```text
bootstrap.Start
    ├── 单实例锁
    ├── 配置、日志、SQLite
    └── 创建 DesktopApplication
            ↓
DesktopHost.Run → wails.Run（只调用一次）
    ├── OnStartup
    │     └── 保存 Wails context，不执行窗口操作
    ├── OnDomReady
    │     ├── 初始化 Wails Notification
    │     ├── 标记 DesktopHost ready
    │     └── 启动 scheduler 和前端事件发送
    ├── Windows.OnSuspend / OnResume
    │     └── 转换为 app.RuntimeEvent
    ├── OnBeforeClose
    │     └── 隐藏窗口或启动正式 Stop
    └── OnShutdown
          └── 只执行幂等兜底清理
```

- `wails.Run` 必须在 DesktopHost 的主运行路径中同步执行，不能放入后台 goroutine。
- 所有 Wails Runtime 调用必须使用 `OnStartup` 或 `OnDomReady` 提供的 context。
- 窗口、事件发送和通知初始化等 Runtime 操作在 `OnDomReady` 后执行。
- `OnShutdown` 发生在前端销毁后，不能承担可失败、需要用户重试的主要清理流程。
- `OnBeforeClose` 同时会被窗口关闭和 `runtime.Quit` 触发，因此必须使用显式的 `allowQuit` 门闩避免递归。

## 4. 接口设计

### 4.1 保持不变

- `bootstrap.Application`
- `bootstrap.ExclusiveStartup`
- `bootstrap.Start`
- `app.ScheduleStore`、`ScheduleUpdater`、`ScheduleDeleter`、`OccurrenceReader`
- `app.Clock`
- `app.ReminderNotifier`
- `app.RuntimeEvent`
- `app.ReminderScheduler.ApplyRuntimeEvent`
- `app.ReminderScheduler.Run`
- `app.ReminderScheduler.GetRuntimeStatus`
- `app.NewServiceWithCoordinator`
- #12 的配置加载和数据库路径规则

### 4.2 不新增重复抽象

- 不新增 `platform.InstanceLock`：Windows 单实例实现直接满足现有 `bootstrap.ExclusiveStartup`。
- 不新增独立的 `platform.ActivationTarget`：Go 内部复用 `domain.OccurrenceIdentity`，只在前端边界转换为 DTO。

### 4.3 新增窄契约

- `platform.DesktopHost`
  - 只负责桌面主循环和退出。
- `platform.WindowController`
  - 负责显示、隐藏、最小化和激活窗口。
- `platform.FrontendEvents`
  - 向 React 发送受控事件。
- `platform.ActivationSource`
  - 输出普通第二实例激活或 Toast 激活。
- `platform.EnvironmentEventSource`
  - 输出现有 `app.RuntimeEvent`。
- `platform.TrayPort`
  - 管理托盘、打开回调、退出回调和资源释放。
- `platform.NotificationCenter`
  - 实现现有 `app.ReminderNotifier`；
  - 优先包装 Wails v2.15 Notification Runtime；
  - 额外负责等待在途提交、清理系统通知和关闭资源。

激活事件包含：

```text
Activation
├── Kind: second_instance | toast
└── Target: *domain.OccurrenceIdentity
```

## 5. 前置技术验证

以下验证在正式提交前完成。验证代码可以丢弃，但结果需要记录：

1. Wails v2.15.0 能在 Go 1.27 下完成 Windows 构建。
2. Wails v2.15 Notification Runtime 能携带稳定身份，并在进程存活时收到点击回调。
3. Wails 的 Windows 通知删除接口能否满足按应用身份清理通知历史；不足时只补充窄的 WinRT 清理适配器。
4. Wails Toast 激活可以转发给已有实例，且没有主实例时不会打开数据库和 UI。
5. Windows 时区变化后能够重新取得当前时区规则，不能继续依赖缓存的 `time.Local`。
6. 托盘、`WM_TIMECHANGE` 和时区消息可以共享受控的 Win32 消息线程。
7. Wails 关闭拦截支持运行期切换 `tray` 和 `exit`。
8. `wails generate module` 在正式实例运行期间也不会获取实例锁、打开数据库或启动 scheduler。

Toast 清理和动态时区是最高风险项。验证不通过时，应先调整平台方案，不继续叠加 Runtime 和 UI 实现。

## 6. Atomic Commit 顺序

### Commit 1：`feat(platform): define narrow desktop boundary contracts`

#### 原子目标

建立平台无关的桌面边界，不接入 Wails 或 Win32。

#### 实现范围

- 新增第 4.3 节中的窄接口。
- 激活目标复用 `domain.OccurrenceIdentity`。
- 增加测试 fake。
- 明确资源所有权和关闭职责。
- 同步补充对应模块的 `AGENTS.md`。

#### 验证

- 接口契约测试。
- 非 Windows 环境编译。
- `go test ./...`
- `go vet ./...`

#### 不包含

- Wails 依赖。
- Win32 API。
- 配置持久化。
- Runtime 组装。

### Commit 2：`feat(config): persist user window close preference`

#### 原子目标

实现关闭行为的读取、校验和原子持久化。

#### 配置层次

```text
defaults.yaml
    ↓
可执行文件旁 config.yaml
```

#### 实现范围

- 为 `config.Config` 增加 `Window.CloseBehavior`。
- 枚举只允许 `tray` 和 `exit`，默认值为 `tray`。
- 关闭行为写回 `ResolvedPaths.ConfigPath` 指向的 `config.yaml`。
- 复用配置文件同目录临时文件、flush/sync、rename 替换的原子保存边界。
- 保存失败时保留旧文件和旧内存值。
- 不创建或读取独立的 `preferences.yaml`。
- 非法配置在启动配置边界返回带路径上下文的错误。

#### 验证

- 无用户配置时默认为 `tray`。
- 保存 `exit` 后重启仍读取为 `exit`。
- 非法枚举。
- 写入和原子替换失败。
- 保存时保留其他用户配置字段。
- #12 数据库路径规则不变。

### Commit 3：`feat(runtime): assemble desktop service scheduler and bindings`

#### 原子目标

完成平台无关的桌面应用组装。

#### 实现范围

- 新增 `runtime.DesktopApplication`。
- 新增 `runtime.WailsBindings`。
- 从 Bootstrap Store 一次性取得桌面运行需要的组合端口。
- 只构造一个 Service、一个 MutationCoordinator、一个 ReminderScheduler 和一个 scheduler goroutine。
- 查询结果、运行状态和偏好使用带 `json` tag 的强类型 DTO，并由 Wails 生成 TypeScript 模型。
- 前端统一通过生成的 `wailsjs` 方法调用绑定，不直接访问底层 IPC 名称。
- 使用 Wails `ErrorFormatter` 把现有业务错误转换为稳定的前端错误结构。
- 只有必须区分“字段缺失”和显式 `null` 的 mutation 请求才使用 JSON payload 或 `map[string]json.RawMessage`，并复用现有解码器。

#### 最小绑定

- `CreateSchedule`
- `GetSchedule`
- `ListSchedules`
- `DeleteSchedule`
- `GetScheduleTimeBounds`
- `GetRuntimeStatus`
- `GetDesktopStatus`
- `GetCloseBehavior`
- `SaveCloseBehavior`
- `FrontendReady`
- `RetryCleanup`

`DeleteSchedule` 必须包含在本次绑定中，否则 React 原型中的“我的日程”不能完整接入。

#### 停止状态机

```text
Running
   ↓
Stopping
   ├── 清理成功 → Stopped
   └── 清理失败 → CleanupFailed
                       │
                       └── RetryCleanup → Stopped
```

#### 验证

- 使用临时 SQLite 验证创建、读取、列表、删除和重启读取。
- 时间边界。
- JSON 错误码和字段错误转换。
- Wails 生成的 TypeScript 声明与绑定 DTO 一致。
- 单 coordinator 和单 scheduler。
- 重复 Stop。
- 清理失败后重试。
- 停止后拒绝新的业务写入。
- Fake Host、Tray、Notifier 和 EventSource 的释放顺序。

### Commit 4：`feat(windows): enforce pre-database instance and activation broker`

#### 原子目标

实现数据库初始化前的 Windows 单实例守卫。

#### 实现范围

- 新增直接实现 `bootstrap.ExclusiveStartup` 的 `WindowsInstanceBroker`。
- 不启用 Wails `options.SingleInstanceLock`：它进入 `wails.Run` 后才生效，无法满足数据库初始化前加锁的现有 Bootstrap 契约。
- 使用每用户命名 Mutex 确认主实例。
- 使用命名 Pipe 接收第二实例和 Toast 激活。
- 主实例在 `Acquire` 返回成功前启动 Pipe listener。
- Runtime 尚未订阅时暂存激活事件。
- 锁保持到 Bootstrap 最终清理。
- 第二实例转发成功后返回 `bootstrap.ErrAlreadyRunning`。
- Bootstrap 只将“已成功转发”的 `ErrAlreadyRunning` 视为正常退出。
- Pipe 超时、协议错误和锁错误必须保留上下文。

#### 冷启动规则

- 已有主实例：转发 Toast target 后退出。
- 没有主实例且属于 Toast 激活：直接退出，不打开数据库和 UI。
- 普通启动且没有主实例：成为主实例。

#### 安全约束

- 限制 Pipe 消息长度。
- 严格校验消息类型、正整数 ID 和日期格式。
- 不记录原始命令行和原始 Pipe 内容。
- 关闭阶段返回明确状态，避免锁存在但 Pipe 已不可用的模糊窗口。

#### 验证

- 锁早于目录、日志和 SQLite。
- 第二实例不打开数据库。
- 普通激活和 Toast target 转发。
- 无主实例时 Toast 不冷启动。
- Pipe 超时和无效协议。
- 重复启动。
- 主进程异常退出后的锁恢复。
- 子进程集成测试。

### Commit 5：`feat(windows): observe power clock and timezone changes`

#### 原子目标

把 Windows 环境事实转换成现有 Runtime 事件。

#### 实现范围

- 新增共享状态的 Windows `EnvironmentClock`。
- 同一状态同时服务 `app.Clock.Snapshot` 和 `EnvironmentEventSource`。
- suspend 和 resume 只接入 Wails Windows `OnSuspend`、`OnResume`。
- Win32 消息窗口只接入墙钟变化、时区变化和托盘消息。
- 收到系统消息时先更新时钟快照，再发送 Runtime 事件。
- 时区变化后重新读取 Windows 当前时区规则，不依赖缓存的 `time.Local`。
- 不重复注册 Win32 电源回调；scheduler/coordinator 的幂等仍作为异常重复事件的最终保护。
- 停止后旧 epoch 的回调不得继续发送事件。

#### 验证

- 重复 suspend/resume。
- resume 无对应 suspend。
- 墙钟前跳和后跳。
- 时区规则变化。
- 恢复后不追补已错过提醒。
- 停止后旧回调无效。
- Clock 和 RuntimeEvent 使用同一份时间事实。

### Commit 6：`feat(windows): add Wails notification center and tray adapters`

#### 原子目标

复用 Wails v2.15 Notification Runtime 完成 Windows Toast，并补充 Wails v2 缺失的托盘能力。

#### NotificationCenter

- `WailsNotificationCenter` 包装 `runtime.InitializeNotifications`、`runtime.SendNotification` 和 `runtime.OnNotificationResponse`。
- 每个 occurrence 单独调用现有 `ReminderNotifier.Submit`，由适配器转换为 Wails `NotificationOptions`。
- `Submit` 只表示操作系统接受提交，不表示用户已看到或处理。
- `NotificationOptions.Data` 只包含 `schedule_id` 和 `original_date`。
- 不确认、不重试、不聚合。
- 跟踪正在执行的提交，退出时等待其结算。
- 优先使用 Wails 的通知删除接口清理通知历史。
- 如果 Wails 的 Windows 实现不能满足清理语义，只新增窄的 `NotificationHistoryCleaner`，不得重新实现 Toast 发送、COM class factory 和点击回调。
- 正式路径不使用 PowerShell Toast fallback，也不直接依赖 go-toast；Wails 已拥有通知发送和激活链路。

#### 托盘

- 使用 Win32 消息窗口。
- 菜单只包含“打开 TimeCue”和“退出”。
- 与墙钟和时区事件共用受控消息线程，避免多套隐藏窗口和关闭协议。

#### 验证

- 逐条发送。
- 稳定身份序列化。
- Wails notification response 到 `ActivationSource` 的转换。
- 重复点击无领域副作用。
- 提交失败错误上下文。
- 等待在途通知。
- 清理成功、失败和重试。
- 托盘打开和退出回调。
- 重复 Close。
- 消息线程退出。

### Commit 7：`feat(frontend): connect React UI through production adapter`

#### 原子目标

把 Issue #28 中已验收的 React UI 接入真实后端。

#### 实现范围

- 在仓库根目录创建生产 `frontend/`，作为后续 Wails 项目的正式前端目录。
- 保留 `prototypes/issue-28` 作为原型和评审依据。
- 抽取可复用的纯展示组件、样式和表单模型，由原型与生产入口共用。
- 生产入口使用 `WailsAdapter`，不得实例化 `MockAdapter`。
- `WailsAdapter` 通过生成的 `wailsjs` 方法和 TypeScript DTO 调用后端。
- `/controls`、故障注入和模拟生命周期不进入生产包。
- 暂时隐藏消息历史、通知确认、日志/备份目录设置和完整提醒详情页。

#### 适配职责

- 领域 DTO 与 UI model 转换；生成 DTO 的字段名由 Go `json` tag 决定。
- 前端 string ID 与 Go `int64` 转换。
- 列表分页转换。
- Go 字段错误转换为表单字段提示。
- 仅对需要保留字段 presence 语义的 mutation payload 做 JSON 编解码。

#### 激活握手

```text
React 注册 EventsOn
        ↓
调用 FrontendReady(lastSequence)
        ↓
后端返回或补发尚未消费的最新 Activation
        ↓
前端显示并激活窗口
```

该握手用于避免 `OnDomReady` 早于 React 事件监听注册而丢失激活目标。

#### 验证

- TypeScript 类型检查。
- 组件测试和 Adapter 契约测试。
- 创建、列表和删除日程。
- 保存关闭行为。
- 启动前积压激活。
- 重复 activation sequence 去重。
- 生产 bundle 不包含 `MockAdapter` 和 `/controls`。

### Commit 8：`refactor(cmd): move timecue entry to repository root`

#### 原子目标

只迁移现有进程入口位置，不改变启动、信号、错误或退出码行为。

#### 实现范围

- 将 `cmd/timecue/main.go` 和 `cmd/timecue/main_test.go` 迁移到仓库根目录。
- 保持原入口调用 Bootstrap 的行为和可测试边界不变。
- 删除迁移完成后为空的 `cmd/timecue`。
- 同步更新根 `AGENTS.md`、`cmd/AGENTS.md`、入口级说明和所有旧路径引用。
- 不在本 Commit 引入 Wails 依赖或桌面行为。

#### 验证

- 原入口测试在根包继续通过。
- `go run .` 取代 `go run ./cmd/timecue`。
- `go test ./...`
- `go vet ./...`

### Commit 9：`feat(cmd): wire Wails desktop lifecycle`

#### 原子目标

完成全部组件的生产接线。

#### 实现范围

- 精确固定 Wails Go 依赖和 CLI 为 `v2.15.0`。
- 在仓库根目录新增 `wails.json`、`build/` 和生产前端构建配置，并接入 Commit 7 创建的 `frontend/`。
- 使用 `//go:embed all:frontend/dist` 把生产资源交给 Wails AssetServer。
- 实现 Wails Host、WindowController 和 FrontendEvents。
- 修改根目录 `main.go`，使入口只负责依赖注入、启动和最终错误展示。
- 将 Wails 日志适配到现有 `slog`，不引入第二套日志系统。
- `OnStartup` 只保存 Wails context；`OnDomReady` 初始化通知、启用 Runtime 调用并启动 scheduler。
- 使用 Wails Windows `OnSuspend` 和 `OnResume` 产生电源 RuntimeEvent。
- `OnBeforeClose` 动态读取当前关闭偏好：
  - `tray`：阻止关闭并隐藏窗口；
  - `exit`：阻止当前关闭，异步进入正式 Stop，随后调用 Host Quit。
- Stop 成功后设置 `allowQuit=true` 再调用 `runtime.Quit`；下一次 `OnBeforeClose` 返回 `false`，避免递归。
- `OnShutdown` 只执行幂等兜底，不承担可重试清理。
- Logger 初始化前的致命错误使用原生对话框展示。
- `ErrAlreadyRunning` 静默退出，不显示错误框。

#### Bindings 生成模式

Wails CLI 会使用 `bindings` build tag 编译并运行应用来生成前端绑定。该路径必须无副作用：

```text
//go:build bindings
    只构造可供反射的 WailsBindings 实例
    调用 wails.Run 生成绑定
    不进入 bootstrap
    不获取实例锁
    不打开 SQLite
    不启动 scheduler

//go:build !bindings
    执行正式 bootstrap 和桌面生命周期
```

应增加验证，确保正式 TimeCue 正在运行时执行 `wails generate module` 仍可成功，且不会访问正式数据库。

## 7. 正式停止顺序

1. 将 Runtime 标记为 `Stopping`，拒绝新的业务写入。
2. 向 scheduler 应用停止事件。
3. 取消 scheduler context 并等待 goroutine 退出。
4. 停止新的 Toast 提交，等待在途提交结算。
5. 清理系统通知。
6. 清理失败时进入 `CleanupFailed`：scheduler 保持停止，Wails 和托盘暂不销毁，前端允许执行 `RetryCleanup`。
7. 清理成功后关闭环境事件源、托盘和通知适配器。
8. 调用 Wails Host Quit。
9. `DesktopApplication.Run` 返回。
10. Bootstrap 逆序关闭 SQLite Store、Logger 和 Instance Broker/Mutex。

外部 cancel、托盘退出、窗口直接退出和 Host 异常必须汇入同一个 Stop 路径。

## 8. Wails 工程布局与发布配置

Wails 项目采用官方模板式的根目录布局。正式入口、项目配置、构建资源和前端均位于仓库根目录：

```text
.
├── main.go
├── main_test.go
├── wails.json
├── build/
├── frontend/
│   ├── src/
│   ├── wailsjs/
│   └── dist/
├── internal/
├── prototypes/
└── docs/
```

所有 Wails CLI 命令从仓库根目录执行：

```powershell
wails dev
wails generate module
wails build -platform windows/amd64 -webview2 embed
```

根目录 `main.go` 继续保持轻量，只负责进程级依赖注入、信号处理、调用 Bootstrap、配置 Wails Options 和处理最终退出码；业务逻辑及资源生命周期仍位于 `internal`。入口迁移不授权把现有 Bootstrap 或领域逻辑搬到根包。

由于当前仓库文档明确声明入口为 `cmd/timecue`，实施该迁移时必须在同一 Atomic Commit 中同步：

- 将根 `AGENTS.md` 的入口说明改为仓库根目录；
- 更新或删除不再适用的 `cmd/timecue/AGENTS.md`；
- 更新 `cmd/AGENTS.md` 的当前目录结构；
- 更新 README、CONTRIBUTING、CI 和脚本中的旧入口路径；
- 保留原入口测试覆盖的信号取消、错误输出和退出码行为。

`wails.json` 至少明确：

- `frontend:dir`；
- 前端安装、开发和生产构建命令；
- `wailsjsdir`；
- `build:dir`；
- 输出文件名和版本信息。

开发环境执行 `wails doctor` 检查 WebView2 和构建依赖。发布前必须冻结 WebView2 策略：

- `embed`：二进制增加少量体积，离线安装体验稳定；
- `download`：包更小，但首次缺失时依赖网络；
- `error`：由安装器保证 WebView2，缺失时明确报错。

本 Issue 推荐 Windows 发布构建使用 `-webview2 embed`；若后续安装器明确捆绑 WebView2，再独立切换为 `error`。

## 9. 测试与 CI

### 9.1 自动验证

每个 Commit 都必须保持可编译，并运行与变更匹配的测试。最终至少执行：

```powershell
gofmt -l .
go test ./...
go vet ./...
```

前端和 Windows 构建还需执行：

```text
frontend typecheck
frontend test
frontend production build
wails generate module
wails build -platform windows/amd64 -webview2 embed
```

CI 建议包括：

- Ubuntu：现有 Go 测试，验证平台抽象和非 Windows stub。
- Windows：Go test/vet、前端检查、bindings 无副作用生成、Wails production build、Windows adapter 单元测试和子进程测试。

仓库当前可能受 CRLF 影响导致 `gofmt -l .` 误报。可增加最小 `.gitattributes` 规则固定 Go 文件为 LF，但不得借机全仓格式化或产生无关代码 diff。

### 9.2 Windows 实机验收

- [ ] 首次启动成功。
- [ ] 普通第二实例激活已有窗口。
- [ ] 第二实例不打开数据库。
- [ ] 关闭窗口隐藏到托盘。
- [ ] 托盘可以重新打开窗口。
- [ ] 切换为直接退出后重启仍保持该偏好。
- [ ] 实际 Toast 可以展示。
- [ ] 多个同时到期提醒逐条展示。
- [ ] Toast 点击激活窗口并传递稳定身份。
- [ ] 应用关闭时点击 Toast 不冷启动。
- [ ] 睡眠恢复后不追补已错过提醒。
- [ ] 手动修改系统时间后调度正确。
- [ ] 切换系统时区后调度正确。
- [ ] 通知清理失败后可以重试。
- [ ] 外部终止和正常退出均无残留 Mutex、Pipe 或托盘图标。
- [ ] 重启后 SQLite 数据保持完整。

## 10. 范围约束

本 Issue 不包含：

- 通知历史查询和确认；
- 完整提醒详情页；
- 日志、备份或数据库目录切换；
- 主题持久化；
- Toast 聚合、自动重试或自动确认；
- 非 Windows 桌面实现；
- 与桌面接入无关的领域层重构。

对应能力分别留给 #29、#30、#31 或后续独立 Issue。

## 11. 交付要求

- 从最新 `dev` 创建 Issue #14 专用开发分支。
- 九个 Commit 按本文顺序进入同一分支。
- 每个 Commit 只包含一个完整行为边界及其测试。
- 修改模块职责时同步更新对应 `AGENTS.md`。
- 根入口迁移由 Commit 8 独立完成，Wails 接线在 Commit 9 进行；迁移后不得留下两个可执行入口。
- 不混入无关格式化、重命名或跨 Issue 重构。
- 最终 Pull Request 指向 `dev`。
- Pull Request 描述包含 `Closes #14`。
- Pull Request 附上 Windows 实机验收记录、CI 结果和已知限制。

## 12. Wails 官方依据

- [Application Development](https://v2.wails.io/docs/guides/application-development/)：`main.go`、应用对象、生命周期和绑定的推荐组织方式。
- [Runtime Introduction](https://v2.wails.io/docs/reference/runtime/intro/)：Wails context 来源以及 `OnDomReady` 前 Runtime 方法的限制。
- [Application Options](https://v2.wails.io/docs/reference/options/)：`OnStartup`、`OnDomReady`、`OnShutdown`、`OnBeforeClose`、Windows 电源回调和单实例选项。
- [How does it work](https://v2.wails.io/docs/howdoesitwork/)：强类型 Go Binding、生成的 JavaScript 方法和 TypeScript 模型。
- [Events](https://v2.wails.io/docs/reference/runtime/events/)：Go 与前端之间的事件发送、监听和取消监听。
- [Notification](https://v2.wails.io/docs/reference/runtime/notification/)：通知初始化、发送、点击响应和通知删除接口。
- [Single Instance Lock](https://v2.wails.io/docs/guides/single-instance-lock/)：官方单实例行为以及第二实例输入必须视为不可信数据的要求。
- [Project Config](https://v2.wails.io/docs/reference/project-config/)：`wails.json`、frontend、build 和 wailsjs 目录配置。
- [Windows Guide](https://v2.wails.io/docs/guides/windows/)：WebView2 运行时和发布策略。
- [Wails v2 System Tray #4990](https://github.com/wailsapp/wails/issues/4990)：Wails v2 缺少内置系统托盘 Runtime API。
