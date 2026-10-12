# Windows 原生测试

- single_instance_test.cpp：同步窗口显示完成、隐藏/重开、最小化恢复、失效句柄及 32 KiB 启动协议、Unicode、并发选主、就绪前缓冲、重复/非法 IPC、无响应退出及崩溃后的所有权恢复；使用独立测试锁名和自有隐藏子进程，不触及生产实例。
- CMakeLists.txt：独立构建/CTest 入口，直接编译 runner 的单实例实现，不依赖 Flutter 引擎。
- 原生测试启用 /EHsc、UTF-8 及严格告警；覆盖发行默认目录/Go 路径、普通唤回兼容，以及显式 external 保留开发所有权。

- `managed_process_test.cpp`：直接验证生产自有进程实现的 Unicode/引号参数、无控制台、stderr 排空、失败重试、独占创建、嵌套 Job、后代清理和宿主硬崩溃；--go 测试驱动真实 Go 的 ready/优雅停止，用持有的句柄观察退出。CMake 使用 /W4 /WX 同时运行 D7/D8 回归，全部进程为测试自有。
