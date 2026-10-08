# Windows 应用入口

- `main.cpp`：初始化运行环境，创建 TimeCue 窗口并运行消息循环。
- `flutter_window.*`：承载 Flutter 视图与插件注册；`win32_window.*`：Win32 窗口和消息处理。
- `utils.*`：控制台与启动参数辅助功能。
- `Runner.rc`、`resource.h`、`resources/`、`runner.exe.manifest`：版本、图标及应用清单；`CMakeLists.txt`：runner 编译目标。
