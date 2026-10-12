# 构建脚本

- `build-release.ps1`：检查 Go/Flutter、编译服务及正式 Flutter 入口，将完整 Windows x64 运行目录与 Go、说明、文件校验清单打包为 ZIP。
- 构建使用现有 SDK，版本来自 desktop/pubspec.yaml；每次创建新的输出目录，失败不生成完整包，不删除旧产物。默认输出 build/releases。
- SkipChecks 只用于已完成本轮验证的本地构建；SkipFlutterBuild 只用于 CI 复用同一任务刚构建的正式入口。
- 脚本不包含业务数据库、用户配置、日志或签名凭据，不发布 GitHub Release。
- 打包验证 Flutter 构建目标和 DART_DEFINES，拒绝测试入口或 TIMECUE_DEMO=true；清单包含实际 SDK 和工作区是否有未提交内容。
- `release-smoke.py --package <发行目录>`：使用全新隔离中文路径，验证首次运行、正式接口写入/重启保留、普通重复启动及宿主强退后的 Job 清理；只结束自身宿主，不操作用户实例，端口占用时拒绝测试。
