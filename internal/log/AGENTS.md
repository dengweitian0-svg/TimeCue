# internal/log

- 本目录负责创建项目统一使用的结构化日志器，不负责决定业务事件或错误处理策略。
- 当前文件：
  - `log.go`：实现日志文件创建、目录准备、JSONL `slog` Handler 和本地级别解析。
  - `log_test.go`：验证 JSONL 输出、追加写入、级别过滤和非法路径处理。
- `New` 将 `config.Config.LogFilePath` 作为日志路径校验和相对路径解析的唯一入口，再由本目录的 `parseLevel` 将级别转换为 `slog.Level`；随后创建目录、以追加方式打开 `.log` 文件，并返回 logger 与由调用方关闭的文件句柄。
- 本目录不读取 `config.yaml` 或 `defaults.yaml`，也不负责注入配置默认值；配置来源和默认值由 `internal/config` 处理。
- 日志输出使用标准库 `log/slog` 的 JSON Handler；不得在本目录引入第二套日志框架或混入业务日志内容。
- 调用方负责关闭 `New` 返回的文件；资源关闭错误不得静默吞掉。
- 日志属性必须遵循根目录的级别、上下文、脱敏和高频路径规则；不得记录密码、Token、Secret、API Key 或未脱敏个人信息。
- 修改输出格式、级别解析或文件生命周期时必须同步更新测试，并验证已有日志文件的追加行为。
