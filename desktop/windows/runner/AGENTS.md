# Windows 原生宿主

- `main.cpp` 装配主实例、应用窗口和消息循环，`single_instance.*` 管理选主及参数转交。
- `flutter_window.*` 承载 Flutter 视图与平台通道，`desktop_window.h`、`win32_window.*` 提供窗口操作。
- `launch_configuration.*` 解析启动配置，`managed_process.*` 监管自有 Go，`install_directory.*` 登记发行目录。
- `utils.*` 提供启动辅助。`CMakeLists.txt` 定义构建目标，`Runner.rc`、`resource.h`、`resources/` 和 `runner.exe.manifest` 提供应用资源与清单。

## 工程约束

- 主实例持有业务宿主和自有进程，后续实例仅转交激活意图。
- C++ 函数说明集中在一处，优先放头文件。缩进遵循 Windows 平台的 `.clang-format`。
