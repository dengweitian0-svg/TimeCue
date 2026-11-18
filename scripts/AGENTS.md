# 构建、更新与验收脚本

- `build-release.ps1` 构建并打包 Windows 发行目录，`update.py` 校验发行清单并更新安装。
- `release-smoke.py` 验证正式发行入口，`chat-real-smoke.py` 验证真实模型、Go 服务及聊天恢复。
- `test_update.py`、`test_release_smoke.py` 和 `test_chat_real_smoke.py` 覆盖更新及验收脚本回归。

## 工程约束

- 发行包排除运行数据和密钥。验收使用隔离目录，只停止测试创建的进程。
