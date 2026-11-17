# TimeCue Agent 架构与流程图

从 [HTML 导航页](index.html) 可以打开本组全部 11 张图。先看[总体架构](overview/overview.html)了解模块边界，再看[一轮对话的主流程](run-flow/run-flow.html)。模块图展开各自的分支、事务和恢复规则。图中的「来源」按钮可打开固定版本的源码行。

## 图表索引

| 图表 | 类型 | 覆盖范围 | 可编辑源文件 | 校验回执 |
| --- | --- | --- | --- | --- |
| [总体架构](overview/overview.html) | Architecture | Go 宿主接纳 Loop、聊天路由开放条件，以及 Flutter、模型适配器、工具与 SQLite 的职责 | [JSON](overview/candidate.json) | [摘要](overview/review-20261117-3/overview.finalize-summary.json) · [浏览器](overview/review-20261117-3/overview.browser-check.json) |
| [一轮对话的主流程](run-flow/run-flow.html) | Workflow | 从消息准入和请求登记，到模型分支、工具回填、摘要、续写与终态 | [JSON](run-flow/candidate.json) | [摘要](run-flow/review-20261117-final/run-flow.finalize-summary.json) · [浏览器](run-flow/review-20261117-final/run-flow.browser-check.json) |
| [会话与运行准入](admission/admission.html) | Workflow | 同会话原文幂等、模型门禁、占用限制与创建前最终正文预算 | [JSON](admission/candidate.json) | [摘要](admission/review-20261117-2/admission.finalize-summary.json) · [浏览器](admission/review-20261117-2/admission.browser-check.json) |
| [模型流式调用与有限重试](model/model.html) | Workflow | 最终请求正文原样发送、首包与空闲超时、同一调用重试 | [JSON](model/candidate.json) | [摘要](model/review-20261117-final/model.finalize-summary.json) · [浏览器](model/review-20261117-final/model.browser-check.json) |
| [日程工具执行、重试与回填](tools/tools.html) | Workflow | ToolFactory 批次调度、并发与串行门禁、只读删除回执和全批配对 | [JSON](tools/candidate.json) | [摘要](tools/review-20261117-final/tools.finalize-summary.json) · [浏览器](tools/review-20261117-final/tools.browser-check.json) |
| [上下文预算、摘要与续写](context/context.html) | Workflow | 最终正文预算、有效普通请求前缀、摘要原子切换与 length 续写 | [JSON](context/candidate.json) | [摘要](context/review-20261117-final/context.finalize-summary.json) · [浏览器](context/review-20261117-final/context.browser-check.json) |
| [SQLite 持久化与事务](sqlite/sqlite.html) | Workflow | 模型调用登记、流式增量批次和终态原子提交 | [JSON](sqlite/candidate.json) | [摘要](sqlite/sqlite.finalize-summary.json) · [浏览器](sqlite/sqlite.browser-check.json) |
| [SSE 订阅与补读](events/events.html) | Workflow | 按游标续接、处理 HTTP 204 和分页补齐缺失事件 | [JSON](events/candidate.json) | [摘要](events/review-20261117-sse/events.finalize-summary.json) · [浏览器](events/review-20261117-sse/events.browser-check.json) |
| [模型用量查询与汇总](usage/usage.html) | Workflow | 读取已保存的 usage，分别汇总输入、输出与总量 | [JSON](usage/candidate.json) | [摘要](usage/usage.finalize-summary.json) · [浏览器](usage/usage.browser-check.json) |
| [取消、退出与重启恢复](recovery/recovery.html) | Workflow | 取消与回执、8 秒接纳门槛、仅 App 降级、独立退出期限和持久化故障 | [JSON](recovery/candidate.json) | [摘要](recovery/review-20261117-final/recovery.finalize-summary.json) · [浏览器](recovery/review-20261117-final/recovery.browser-check.json) |
| [模型连接保存与热加载](config/config.html) | Workflow | 多模型预算、密钥保留、版本匹配和按原身份热读配置 | [JSON](config/candidate.json) | [摘要](config/review-20261117-final/config.finalize-summary.json) · [浏览器](config/review-20261117-final/config.browser-check.json) |

## 版本与验证

- 源码固定在提交 `8cadb08307b72d6dca64f7e92699ed769d23a99f`。图表基于生产装配和该提交的实现，证据范围见各图节点。 [固定源码版本](https://github.com/dengweitian0-svg/TimeCue/tree/8cadb08307b72d6dca64f7e92699ed769d23a99f)
- 11 张图均通过 Archify 源码、产物完整性及 Chrome 自动检查。各图的当前回执记录对应的源码 JSON 和 HTML 指纹。验证沿用交付时的文件字节，目录中的早期回执及截图属于此前版本。
- 总体架构、对话主流程、工具执行另生成了当前版本的双主题截图，人工查看了各图的 1440×900 浅色与 2048×1320 深色截图。 [总体架构截图](overview/visual-check-20261117/overview.visual-check.html) · [主流程截图](run-flow/visual-check-20261117/run-flow.visual-check.html) · [工具执行截图](tools/visual-check-20261117/tools.visual-check.html)

## 重新生成

在仓库根目录运行，把 `<archify>` 替换为本机技能目录。总体架构使用 `architecture`，其余图使用 `workflow`。修改候选后用新的 `--out-dir` 保存该版本的回执。

```powershell
node <archify>/bin/archify.mjs finalize architecture docs/archify/architecture-agent-20261108-172843/overview/candidate.json docs/archify/architecture-agent-20261108-172843/overview/overview.html --repo-root . --quality showcase --out-dir docs/archify/architecture-agent-20261108-172843/overview/review-next --json
```
