# Windows 原生测试

- `CMakeLists.txt` 提供独立构建和 CTest 入口。
- `single_instance_test.cpp` 验证选主、窗口与参数转交。
- `managed_process_test.cpp` 验证自有进程监管、停止和回收。
- `install_directory_test.cpp` 验证发行目录识别与登记。

## 工程约束

- 使用隔离锁名、测试自有进程和隔离注册表测试项。
