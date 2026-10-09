# D1 验收记录

日期：2026-10-09。实现方案见[D1 分钟调度](phase-d1-minute-scheduler.md)。

## 实现结果

- 每 30 秒扫描前后各 30 秒，pending 正式转换为 loaded 后加入内存等待队列。
- 创建/修改/恢复提交后，未来 1 分钟首提醒即时加载；定时器到点核验完整分钟、冻结快照，再开放一个完整分组。
- 重叠扫描、重启后旧 loaded、暂停/确认竞争、版本端点、提交后回调乱序、回拨后的新分钟及扫描失败重试已覆盖。到期前撤销候选和旧秒级记录不进入分组事实数量。
- service.App.Run 管理配置、日志、数据库、HTTP、调度和退出清理；main 只装配适配器、路由与信号。
- 按用户要求直接修改 001 的 status CHECK，保留 001/002、四张业务表和版本 2，没有新增表、字段、003 或中间表。
- Apifox 通过后适配 Flutter：loaded 详情显示待投递，不显示已提醒。

## 自动检查

| 检查 | 结果 |
| --- | --- |
| Go 格式与 `go vet ./...` | 通过 |
| `go test ./...` | 通过 |
| `go test -race ./internal/service ./internal/store ./cmd/flutter` | 通过，CGO_ENABLED=1，使用本机 GCC |
| Dart 格式检查、`flutter analyze` | 通过 |
| `flutter test` | 51 个测试通过 |
| `flutter build windows --release` | 通过 |
| 独立代码审查 | 回拨、异常退出等待及旧秒级分组问题已修复，复审无阻塞问题 |

本机使用 Go 1.27.0、Flutter 3.49.0-1.0.pre-240 master、Dart 3.14.0 开发版，保留原 SDK 和 lockfile。阶段 D 最终指定 Flutter 3.47.0 的真机平台验收仍在 D10，不将本次 master 构建当作该项验收。

## Apifox

CLI 2.2.11；项目 8902401；环境 49486135「本地隔离回归」。沿用现有 AI 分支 `ai/20261008-from-main-http-tests`（8703058），Occurrence Schema 318689165 已加入 loaded；资源修改保留在该分支，未合并到 main。

本次新增并回读的用例：

| ID | 用例 |
| --- | --- |
| 419891866 | loaded 待确认查询 |
| 419893292 | loaded 提醒详情 |
| 419893305 | 一分钟内首提醒即时加载 |
| 419893316 | 确认保留实际投递状态 |
| 419892129 | loaded 不作为待确认筛选状态 |

原待确认接口和健康接口回归一起执行，共 10 个用例、22 条断言，全部通过。报告以实际 JSON 统计和 failures 为空为准，不只依赖命令退出码。CLI 多 reporter 参数未生成有效报告，改用单一 `--reporters json` 后完成以下验收。

| 报告 | 用例 | 断言 |
| --- | --- | --- |
| [待确认回归](../.apifox/d1/reports/notifications-regression.json) | 6 | 12 |
| [loaded 详情](../.apifox/d1/reports/loaded-detail.json) | 1 | 2 |
| [即时加载](../.apifox/d1/reports/immediate-loading.json) | 1 | 3 |
| [确认独立性](../.apifox/d1/reports/acknowledgement.json) | 1 | 3 |
| [健康回归](../.apifox/d1/reports/health-regression.json) | 1 | 2 |

汇总见[summary.json](../.apifox/d1/reports/summary.json)，写入 payload 与用例 ID 位于 `.apifox/d1/`。

## 数据与后续接入

验收使用 `.gotmp-d1/runtime/d1.db` 临时新库，测试进程已清理，未修改本地业务数据库。直接修改建表 SQL 不会改变旧库；旧 CHECK 不支持 loaded 时，健康检查会明确拒绝启动，旧库调整应单独处理，不自动删库或重建表。

D2 的展示尝试/预算/回填协议和 D3 的全历史恢复/遗漏汇总仍是后续任务。D1 通过 Scheduler.Snapshot 提供完整分钟、当前成员和遗漏窗口的只读副本，不实现 Windows 通知。
