# Flutter Windows 集成

- `CMakeLists.txt`：Flutter 引擎、包装库与生成构建步骤。
- `generated_plugin_registrant.*`、`generated_plugins.cmake`：Flutter 工具生成的插件注册入口和插件构建清单。
- Windows 通知通过 flutter_local_notifications_windows 的 Dart FFI 注册及原生 DLL 集成，generated_plugins.cmake 由 Flutter 工具维护，通知注册不另加手写 C++ 注册入口。

- window_manager、tray_manager 及其 screen_retriever Windows 依赖由 Flutter 工具生成注册和构建清单；关闭、窗口操作及托盘菜单通过这些平台插件接入。

约束：生成的 C++ 注册函数仅在头文件以中文标明用途、入参和出参，实现不重复说明，采用四空格缩进；Flutter 重新生成文件时可能覆盖这些注释与格式。
