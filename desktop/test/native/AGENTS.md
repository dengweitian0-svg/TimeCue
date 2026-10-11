# Windows 原生测试

- single_instance_test.cpp：同步窗口显示完成、隐藏/重开、最小化恢复、失效句柄及 32 KiB 启动协议、Unicode、并发选主、就绪前缓冲、重复/非法 IPC、无响应退出及崩溃后的所有权恢复；使用独立测试锁名和自有隐藏子进程，不触及生产实例。
- CMakeLists.txt：独立构建/CTest 入口，直接编译 runner 的单实例实现，不依赖 Flutter 引擎。
