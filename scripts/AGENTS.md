# 构建脚本

- `build-release.ps1`：检查 Go/Flutter、编译服务及正式 Flutter 入口，将完整 Windows x64 运行目录与 Go、说明、文件校验清单打包为 ZIP。
- 打包优先完整复制 `docs/RELEASE.md`；该文档缺失时在发行目录生成 UTF-8 `README.md`，说明完整目录、默认托管启动与数据目录、托盘退出、自定义 data-dir、开发外连和端口占用行为。README 在发行清单枚举前生成并参与哈希。
- 构建使用现有 SDK，版本来自 desktop/pubspec.yaml；每次创建新的输出目录，失败不生成完整包，不删除旧产物。默认输出 build/releases。
- SkipChecks 只用于已完成本轮验证的本地构建；SkipFlutterBuild 只用于 CI 复用同一任务刚构建的正式入口。
- 脚本不包含业务数据库、用户配置、日志或签名凭据，不发布 GitHub Release。
- 打包将 Flutter 构建目标的相对/绝对路径规范化后与 desktop/lib/main.dart 比较，再检查 DART_DEFINES；拒绝测试入口或 TIMECUE_DEMO=true。清单中的 Flutter 版本和渠道来自生成配置所指 SDK 的版本文件，避免依赖 DART_DEFINES 或当前 PATH；同时记录工作区是否有未提交内容。
- `release-smoke.py --package <发行目录>`：使用全新隔离中文路径，验证首次运行、正式接口写入/重启保留、普通重复启动及宿主强退后的 Job 清理；只结束自身宿主，不操作用户实例，端口占用时拒绝测试。
