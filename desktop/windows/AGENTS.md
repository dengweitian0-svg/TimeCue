# Windows 平台工程

构建并启动承载 Flutter 界面的 Windows 应用。

- `CMakeLists.txt`：平台构建入口、目标装配与运行文件打包。
- `.clang-format`：C++ 四空格缩进及续行格式，保留包含顺序和中文说明。
- [runner](runner/AGENTS.md)：原生窗口、消息循环和应用资源。
- [flutter](flutter/AGENTS.md)：Flutter 引擎、插件注册与构建集成。

# 约束
- C++ 所有代码注释使用中文
- 每个函数只保留一处中文说明，包含用途、入参和出参；头文件已有说明时，实现不重复，文件内函数及回调在定义处说明
- 自动生成文件重新生成后可能覆盖注释与格式
