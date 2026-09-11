# internal/utils

- 本目录提供跨模块使用的错误和进程退出码基础设施，不承载具体业务流程，也不负责记录日志。
- 当前文件：
  - `errors.go`：定义错误分类 `Kind`、进程退出码 `ExitCode` 和错误构造、包装、分类、映射函数。
  - `errors_test.go`：验证错误链保留和退出码映射。
- 当前错误分类包括 `ConfigError`、`StartupError`、`RuntimeError`、`CleanupError` 和 `InternalError`；新增分类前必须确认现有分类无法表达。
- `Wrap` 必须保留原始 cause，以支持 `errors.Is` 和 `errors.As`；传入 `nil` 时必须保持 `nil` 语义。
- `KindOf` 无法识别错误时返回 `InternalError`；`ExitCodeFor` 对 `nil` 和 `context.Canceled` 返回成功码，对配置错误返回配置失败码，其余错误返回通用失败码。
- 本目录不得依赖 `bootstrap`、具体日志实现或业务模块；错误上下文应在产生错误的适当层补充，最终日志由系统边界负责。
- 修改错误分类、错误链行为或退出码映射时必须同步更新测试，并检查启动、运行和清理失败场景。
