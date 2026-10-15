# AI Chat API 验收记录

当前记录范围为 [AI Chat HTTP 接口设计](design/ai-chat-http-api.md) 第 9 章的后端 API 层 Mock 阶段。`service.App` 持有 `chat.Service` 窄端口，HTTP 路由和 Handler 只依赖该端口，不构造 Mock。开发入口在 [cmd/flutter/main.go](../cmd/flutter/main.go) 注入临时内存 Mock；Apifox 使用 `.apifox/chat100/server/main.go` 下的本地临时 fixture。自动化用例使用函数型 Service stub 校验下层调用参数，并使用真实 `httptest.Server` 和可控 Mock 验收 SSE 逐条推送、回放及连接释放。当前没有连接数据库、真实模型或 Agent loop。

App 改为通过 `service.App.ChatService` 装配后，主任务已重新运行 Go 检查及七个 Apifox 用例，以下结果均通过。

设计源文件保持本地忽略，不纳入版本控制。

## 自动化测试映射

| 设计编号 | 自动化测试 | 结果 |
| --- | --- | --- |
| HTTP-01、HTTP-02 | `TestChatHTTPRoutesAndDirectResponses_HTTP01_HTTP02` | 通过 |
| HTTP-03 | `TestCreateRunBusinessMappingsAndIdempotency_CREATE04_CREATE05_CREATE06_CREATE07_CREATE08_CREATE09_CREATE10_HTTP03`；`TestRunAndSessionBusinessErrorMappings_HTTP03` | 通过 |
| HTTP-04 | `TestRunQueryStatesAndVisibility_RUN01_RUN02_RUN03_RUN04_RUN05_RUN06_HTTP04_HTTP08`；`TestInvalidUUIDPathsDoNotCallChatService_HTTP04` | 通过 |
| HTTP-08 | `TestRunQueryStatesAndVisibility_RUN01_RUN02_RUN03_RUN04_RUN05_RUN06_HTTP04_HTTP08` | 通过 |
| HTTP-05、HTTP-06、HTTP-07 | `TestCreateRunValidationAndRawInput_HTTP05_HTTP06_HTTP07_CREATE01_CREATE02_CREATE03` | 通过 |
| CREATE-01、CREATE-02、CREATE-03 | `TestCreateRunValidationAndRawInput_HTTP05_HTTP06_HTTP07_CREATE01_CREATE02_CREATE03` | 通过 |
| CREATE-04、CREATE-05、CREATE-06、CREATE-07、CREATE-08、CREATE-09、CREATE-10 | `TestCreateRunBusinessMappingsAndIdempotency_CREATE04_CREATE05_CREATE06_CREATE07_CREATE08_CREATE09_CREATE10_HTTP03` | 通过 |
| RUN-01、RUN-02、RUN-03、RUN-04、RUN-05、RUN-06 | `TestRunQueryStatesAndVisibility_RUN01_RUN02_RUN03_RUN04_RUN05_RUN06_HTTP04_HTTP08` | 通过 |
| CANCEL-01、CANCEL-02、CANCEL-03 | `TestCancelRunStatusAndNotFound_CANCEL01_CANCEL02_CANCEL03_CANCEL04`；`TestCancelTerminalRunWarningsContainOnlyRunAndStatus_CANCEL02` | 通过 |
| CANCEL-04 | `TestCancelRunConfirmedCheckpointFlow_CANCEL04` | 通过 |
| SSE-01、SSE-02、SSE-03、SSE-04、SSE-06 | `TestSSELiveEventsFlushIndividuallyAndPreserveJSON_SSE01_SSE02_SSE03_SSE04_SSE06` | 通过 |
| SSE-05、SSE-06 | `TestSSEEndReasonsAndIncrementalErrorFollowLowerLayer_SSE05_SSE06`；`TestSSEThinkingErrorPrecedesMatchingEnd_SSE06` | 通过 |
| SSE-07、SSE-08、SSE-09、SSE-10、SSE-11、SSE-12、SSE-13、SSE-15、SSE-16 | `TestSSEReplayCursorConnectionAndDisconnectLifecycle_SSE07_SSE08_SSE09_SSE10_SSE11_SSE12_SSE13_SSE15_SSE16` | 通过 |
| SSE-14、DELETE-02、DELETE-05、DELETE-06 | `TestSSEDeleteHidesNewRequestsButExistingConnectionGetsRealEnd_SSE14_DELETE02_DELETE05_DELETE06` | 通过 |
| SESSION-01、SESSION-02、SESSION-03、DELETE-01、DELETE-02、DELETE-03、DELETE-04 | `TestSessionQueriesAndDeletion_SESSION01_SESSION02_SESSION03_DELETE01_DELETE02_DELETE03_DELETE04` | 通过 |
| DELETE-05 | `TestSSEDeleteHidesNewRequestsButExistingConnectionGetsRealEnd_SSE14_DELETE02_DELETE05_DELETE06` | 通过 |

## 本地检查

| 检查 | 结果 |
| --- | --- |
| Go 文件格式检查（internal/agent、internal/api、internal/middleware、internal/service、cmd/flutter；71 个文件） | 通过 |
| `git diff --check` | 通过 |
| `go vet ./...` | 通过 |
| `go test -p 1 ./...` | 通过：api 6.104s、middleware 3.100s、service 1.678s；其余包无测试或使用缓存。本机已有用户实例占用固定端口，相关用例可能跳过；本结果不代表完整验证了运行实例生命周期。 |
| Go race 检查（agent/chat、api、middleware） | 通过 |

执行命令：

```powershell
go test -race -p 1 ./internal/agent/chat ./internal/api ./internal/middleware -run 'Test(Mock|Chat|CreateRun|RunQuery|CancelRun|CancelTerminal|SessionQueries|InvalidUUID|RunAndSession|SSE|EncodeSSE|WriteHTTPStream|HTTPStream|HTTPResultLogLevel)' -count=1
```

## Apifox

项目：TimeCue（项目 ID `8902401`）。测试目标分支：`ai/20261008-from-main-http-tests`，环境 ID `49486384`。装配调整后使用 Apifox CLI 2.2.11 重跑七个用例，全部 exit 0，共 13 个请求、20 条断言、0 失败。SSE 用例收到实际流式 `end` 事件；Go 自动化测试补充了时序行为证据。

| 接口 | Endpoint ID | Case ID | 执行结果 |
| --- | ---: | ---: | --- |
| `POST /v1/run` | `524788105` | `421668951` | 1 请求，2 断言，通过 |
| `GET /v1/run/:run_id` | `524788159` | `421668954` | 2 请求，3 断言，通过 |
| `GET /v1/run/:run_id/event` | `524788160` | `421668960` | 2 请求，3 断言，通过 |
| `POST /v1/run/:run_id/cancel` | `524788161` | `421668962` | 2 请求，3 断言，通过 |
| `GET /v1/session/:session_id` | `524788162` | `421668965` | 2 请求，3 断言，通过 |
| `DELETE /v1/session/:session_id` | `524788164` | `421668967` | 2 请求，3 断言，通过 |
| `GET /v1/session` | `524788166` | `421668970` | 2 请求，3 断言，通过 |

Apifox fixture 重新构建并通过 `service.App.ChatService` 注入 Mock，监听 `127.0.0.1:10087` 并设置 `Host: 127.0.0.1:10086`，没有中断用户正在 `10086` 端口运行的实例。fixture 源码位于忽略路径 `.apifox/chat100/server/main.go`，测试后已停止。最新执行报告位于忽略的本地路径 `.apifox/chat100/reports/{key}.json` 和 `final-summary.json`；本地 JSON 未上传 Apifox 云端，不能作为云端报告链接。

## P2 审查修复复验

- `go vet ./...` 与 `go test -p 1 ./...` 复验通过。新增 SSE 回归测试的定向 race 检查通过：

```powershell
go test -race ./internal/api -run 'TestSSE(IOErrorsAfterRequestCancellationAreClean|ClientDisconnectIsCleanAndRunCanReconnect|UnexpectedEventSourceCloseRemainsError)$' -count=1
```

- `TestSSEClientDisconnectIsCleanAndRunCanReconnect` 验证正常断线及游标重连保持 run 运行且没有 ERROR 日志；`TestSSEUnexpectedEventSourceCloseRemainsError` 验证事件源意外提前关闭仍报告错误；`TestSSEIOErrorsAfterRequestCancellationAreClean` 验证写入或 flush 与请求取消竞争时正常结束。
- App 装配的 fixture 停止并重建后，单独重跑 Apifox SSE case `421668960`：创建请求返回 202，SSE 请求返回 200 并收到 `end`，三条断言通过。报告为 `.apifox/chat100/reports/run-events.json`。此前七接口总计 13 请求、20 断言、0 失败的基线保留；本次只重跑 SSE case，没有重跑其余六个接口。

## 后续替换与复验

- 接入真实应用层后，移除或替换 `internal/agent/chat` 下的临时 Mock 和 API 验收装配；Handler 继续依赖窄 Service 契约。
- 使用真实持久化和 Agent loop 复验七个接口、幂等记录、并发名额、删除后的异步收尾、checkpoint、历史上下文和取消名额释放。本阶段不声明这些业务能力已经实现。
- 前端开发须等待七个 Apifox 接口测试通过；SSE 的时序、并发连接和断线恢复继续由自动化测试提供补充证据。
- CI 与 GitHub Codex Review 以关联 PR 当前提交的检查结果为准。
