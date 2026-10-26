# 真实文字对话

正常、开发与发行入口统一使用真实聊天服务和基于官方 openai-go/v3 的 Chat Completions 适配器。首次运行不创建演示会话；没有模型配置或密钥时，应用正常启动，本地日程和历史查询仍可使用。

## 配置与运行

在当前实例 `--data-dir` 指定的数据目录中编辑 `config.yaml`，添加非密钥配置。例如：

```yaml
chat_model:
  - provider: deepseek
    base_url: https://api.deepseek.com
    models:
      - model: deepseek-v4-flash
        context_window: 32768
        max_output_tokens: 8192
chat:
  max_retry: 3
  first_timeout_seconds: 120
  idle_timeout_seconds: 60
```

`context_window` 是本地使用的上下文预算，应不大于所用模型的真实容量；`max_output_tokens` 缺省 8192，必须满足 `0 < max_output_tokens < context_window`。同一个 provider 可配置多个模型，同名模型通过 provider 区分。`base_url` 采用不带 `/chat/completions` 的 API 基址。

复制仓库 `example.env` 为同目录 `.env` 并填写密钥。密钥名由 `{PROVIDER}_{MODEL}` 转大写、非 ASCII 字母数字替换为下划线得到，例如 `DEEPSEEK_DEEPSEEK_V4_FLASH`。密钥从不进入 HTTP 响应、数据库请求记录或日志； `.env` 和模型配置的后续修改不需要重启。模型列表的 available 只表示本地配置及密钥存在，不代表上游认证成功。

打开 AI 页，选择提供方和模型后发送文字。密钥缺失时选项标为不可用；补齐后点击“刷新模型”。请求身份固定到本轮运行，重试相同幂等键始终返回原运行。

## 服务与存储

Go 启动时通过现有 PrepareSchema 初始化或备份升级数据库到版本 3；新增 `ai_sessions`、`ai_runs`、`ai_model_calls`、`ai_checkpoints`、`ai_events`、`ai_tool_calls` 六张表，原四张日程与提醒业务表保留。旧 Flutter 库升级前在原库旁生成一致性 `.before-upgrade-*.bak`，失败回滚；不转换 Wails 或未知结构。

调用模型前，短事务保存实际 input、running 和 checkpoint；流式增量提交后供 SSE 和分页共同读取；完成时原子保存结构化 output、上游原始 usage、checkpoint、运行终态及 error/end。没有返回 usage 时保存 SQL NULL。会话软删除只隐藏父会话和公开子查询，不删除日程或执行事实。

HTTP 使用 `/v1/model`、`/v1/run`、`/v1/run/:run_id`、`/v1/run/:run_id/events`、`/v1/run/:run_id/event`、`/v1/run/:run_id/cancel` 与原有 session 路径。创建必填 session_id/input/provider/model，回执固定 run_id/status/output/error；概况固定十字段，错误与部分正文可以同时保留。thinking/text 仅含 content，error 含 code/message，end 仅含 status。事件页固定 events/next_after_seq/has_more；默认 10、最大 100 条，事件序号与 SSE 共用。

## 当前切片边界

T01 提供真实文字模型请求、多轮原始历史、完成会话重启查询、无在途工具的模型取消及会话隐藏。正常退出关闭上游连接、保留未完成事实，下次启动沿同一逻辑调用与原 input 重发；保留此前已持久化的展示事件。

工具执行、完整工具取消与恢复、模型网络重试及自动续写、摘要和上下文裁剪由后续切片扩展。当前不向上游提供工具定义，也不执行业务工具；意外返回的工具调用或非 stop 完成原因如实失败。max_retry 首版保存到模型调用记录，尚不启用网络退避重试；输入超预算保存可查询失败运行，不发请求。原始会话历史不会被展示事件或模型侧裁剪覆盖。

启动恢复只推进同一会话最新的未完成运行；已有后续运行的旧任务只失败收尾。恢复所选模型配置或密钥缺失时，先以 model_config_unavailable 收尾并保留已有部分正文，不将缺配置误记为预算错误。

未进入模型调用的预算拒绝输入仅保留在原始历史中，不加入后续有效模型上下文。恢复重发失败或再次退出且尚无新有效输出时保留原部分回复及响应信息；取得新正文或思考后用新响应替换。完整成功响应即使为空也保存本次响应和 usage，旧展示事件仍保留。

启动先取得监听端口，再启动聊天恢复；端口冲突不触发恢复模型请求。SSE 的 Last-Event-ID 必须为 0 或已持久化序号，非法游标返回 400 invalid_request，并保留已有订阅。事件分页 after_seq 仍允许超过当前末尾并返回空页。
