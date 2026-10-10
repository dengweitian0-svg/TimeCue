# Flutter Windows 集成

- `CMakeLists.txt`：Flutter 引擎、包装库与生成构建步骤。
- `generated_plugin_registrant.*`、`generated_plugins.cmake`：Flutter 工具生成的插件注册入口和插件构建清单。
- Windows 通知通过 flutter_local_notifications_windows 的 Dart FFI 注册及原生 DLL 集成，generated_plugins.cmake 由 Flutter 工具维护，通知注册不另加手写 C++ 注册入口。
