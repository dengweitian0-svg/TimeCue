import 'dart:async';

import 'package:timecue_desktop/contracts/chat_contracts.dart';

/// 阶段 6 复跑验收使用的纯 Dart 聊天服务替身。
///
/// 不依赖 Flutter 测试框架，可由 Widget 测试和 Windows profile 入口共用。
class ChatAcceptanceGateway implements ChatGateway {
  ChatAcceptanceGateway({int historySessionCount = 12, DateTime? now})
    : _now = (now ?? DateTime.now()).toUtc() {
    _seedHistory(historySessionCount);
  }

  final DateTime _now;
  final Map<String, ChatSession> _sessions = {};
  final Map<String, ChatRun> _runs = {};
  final Map<String, StreamController<ChatRunEventSignal>> _eventControllers =
      {};
  final Map<String, int> _lastEventIds = {};
  final List<String> _sessionOrder = [];
  final List<String> createdRunIds = [];
  final List<String> cancelledRunIds = [];
  final Map<String, int> createCountBySession = {};
  int _nextRun = 1;

  /// 下一次创建、快照、会话读取或取消操作是否应模拟失败。
  bool failNextCreate = false;
  bool failNextSnapshot = false;
  bool failNextSessionRead = false;
  bool failNextCancel = false;

  /// 已有会话中的多轮输入与回复，供历史和搜索验收使用。
  final Map<String, List<String>> historicalInputs = {};

  /// 创建请求的总次数。
  int get createCalls => createdRunIds.length;

  /// 取消请求的总次数。
  int get cancelCalls => cancelledRunIds.length;

  /// 关闭所有事件控制器，供可复跑验收入口在退出时释放资源。
  Future<void> dispose() async {
    final controllers = _eventControllers.values.toList();
    _eventControllers.clear();
    await Future.wait(controllers.map((controller) => controller.close()));
  }

  /// 返回指定会话的历史及本次运行 ID。
  List<String> runIdsFor(String sessionId) =>
      List.unmodifiable(_sessions[sessionId]?.runIds ?? const <String>[]);

  /// 可复现的长 Markdown，包含中文段落、长代码和宽表格，长度超过两万字符。
  String get longChatMarkdown {
    final buffer = StringBuffer('''# 长内容综合验收

这段内容用于检查累计 Markdown 在窄屏、大字、流式更新和历史加载下的表现。中文段落、**加粗文本**、`行内代码`、列表、引用、链接和任务框都混合在同一份原文中。

> 每轮更新都应保留已经收到的原文，过程状态不会混入正式回复。

- 第一项：检查正文换行与选择复制。
- 第二项：检查代码块内部滚动。
- 第三项：检查宽表格不会撑破消息列。

| 日期 | 计划 | 负责人 | 状态 | 补充说明 |
| --- | --- | --- | --- | --- |
| 2026-10-18 | 检查流式一致性 | 小明 | 进行中 | 包含较长的中文说明与接口字段 |
| 2026-10-19 | 验证工具名称 | 小红 | 已完成 | 相同名称但不同调用 ID 仍分别显示 |
| 2026-10-20 | 检查历史索引 | 小李 | 待开始 | 标题与摘要均可搜索 |

```dart
''');
    for (var index = 0; index < 240; index++) {
      buffer.writeln(
        'final responsePart$index = "区块 $index: preserve the exact original text, '
        'including punctuation, newlines, identifiers, and JSON-like payloads.";',
      );
    }
    buffer.write('''
```

## 验收补充

''');
    for (var index = 0; index < 100; index++) {
      buffer.writeln(
        '第 ${index + 1} 段用于覆盖足够长的混合中文和 English 内容，'
        '重点观察滚动位置、代码换行、表格横向访问以及终态原文一致性。',
      );
    }
    return buffer.toString();
  }

  /// 向指定 run 发送 Thinking 事件；原文只用于验证隐私过滤。
  void emitThinking(String runId, int eventId, String raw) => _emit(
    runId,
    ChatSseEvent(
      eventId: eventId,
      type: ChatEventType.thinking,
      data: ChatContentEventData(content: raw, error: null),
    ),
  );

  /// 向指定 run 发送正式回复文本增量。
  void emitText(String runId, int eventId, String text) => _emit(
    runId,
    ChatSseEvent(
      eventId: eventId,
      type: ChatEventType.text,
      data: ChatContentEventData(content: text, error: null),
    ),
  );

  /// 发送工具调用事件；工具调用 ID 独立于 SSE 事件 ID。
  void emitTool(
    String runId,
    int eventId,
    String toolCallId,
    String name, {
    Object? args = const <String, Object?>{},
  }) => _emit(
    runId,
    ChatSseEvent(
      eventId: eventId,
      type: ChatEventType.tool,
      data: ChatToolEventData(
        id: toolCallId,
        function: ChatToolFunction(
          name: name,
          args: ChatToolArgument(name: 'input', value: args),
        ),
      ),
    ),
  );

  /// 关闭一次事件连接；模型重新订阅时会获得新的可控流。
  void disconnect(String runId) {
    final controller = _eventControllers.remove(runId);
    if (controller != null && !controller.isClosed) {
      unawaited(controller.close());
    }
  }

  /// 设置服务端终态并发送 end 事件，便于验收停止、成功和失败收敛。
  void finish(
    String runId, {
    required ChatRunStatus status,
    String? output,
    ChatProtocolError? error,
  }) {
    final run = _runs[runId];
    if (run == null) throw ArgumentError.value(runId, 'runId', '未知 run');
    final finishedAt = DateTime.now().toUtc();
    _runs[runId] = ChatRun(
      runId: run.runId,
      sessionId: run.sessionId,
      model: run.model,
      input: run.input,
      status: status,
      output: output ?? (status == ChatRunStatus.succeeded ? '' : null),
      error: error,
      createdAt: run.createdAt,
      finishedAt: finishedAt,
    );
    final stopReason = switch (status) {
      ChatRunStatus.succeeded => 'stop',
      ChatRunStatus.cancelled => 'cancelled',
      ChatRunStatus.failed => 'aborted',
      ChatRunStatus.running => throw ArgumentError('running 不能作为终态'),
    };
    _emit(
      runId,
      ChatSseEvent(
        eventId: (_lastEventIds[runId] ?? 0) + 1,
        type: ChatEventType.end,
        data: ChatEndEventData(stopReason: stopReason, error: error),
      ),
    );
  }

  /// 返回 run 当前的服务端快照。
  @override
  Future<ChatRun> getRun(String runId) async {
    if (failNextSnapshot) {
      failNextSnapshot = false;
      throw StateError('验收夹具：快照读取失败');
    }
    final run = _runs[runId];
    if (run == null) throw StateError('验收夹具：run 不存在');
    return run;
  }

  /// 返回指定 run 的当前事件流。
  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) {
    final existing = _eventControllers[runId];
    if (existing != null && !existing.isClosed) return existing.stream;
    final controller = StreamController<ChatRunEventSignal>.broadcast();
    _eventControllers[runId] = controller;
    return controller.stream;
  }

  /// 创建新的服务端 run，并记录会话归属和幂等调用次数。
  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) async {
    if (failNextCreate) {
      failNextCreate = false;
      throw StateError('验收夹具：创建失败');
    }
    final runId = 'acceptance-run-${_nextRun++}';
    final now = DateTime.now().toUtc();
    final run = ChatRun(
      runId: runId,
      sessionId: sessionId,
      model: model,
      input: input,
      status: ChatRunStatus.running,
      output: null,
      error: null,
      createdAt: now,
      finishedAt: null,
    );
    _runs[runId] = run;
    final old = _sessions[sessionId];
    _sessions[sessionId] = ChatSession(
      sessionId: sessionId,
      name: old?.name ?? '服务端名称 $sessionId',
      runIds: [...?old?.runIds, runId],
    );
    if (!_sessionOrder.contains(sessionId)) _sessionOrder.insert(0, sessionId);
    createdRunIds.add(runId);
    createCountBySession.update(
      sessionId,
      (count) => count + 1,
      ifAbsent: () => 1,
    );
    return ChatCreateRunResult(runId);
  }

  /// 返回取消请求仍在处理中的确认，终态由 [finish] 控制。
  @override
  Future<ChatCancelRunResult> cancelRun(String runId) async {
    if (failNextCancel) {
      failNextCancel = false;
      throw StateError('验收夹具：取消失败');
    }
    cancelledRunIds.add(runId);
    return ChatCancelRunResult(runId: runId, status: ChatRunStatus.running);
  }

  /// 读取服务端名称和该会话下所有 run ID。
  @override
  Future<ChatSession> getSession(String sessionId) async {
    if (failNextSessionRead) {
      failNextSessionRead = false;
      throw StateError('验收夹具：会话读取失败');
    }
    final session = _sessions[sessionId];
    if (session == null) throw StateError('验收夹具：会话不存在');
    return session;
  }

  /// 删除指定会话及其历史索引入口。
  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async {
    _sessions.remove(sessionId);
    _sessionOrder.remove(sessionId);
    return ChatDeleteSessionResult(sessionId: sessionId, status: 'deleted');
  }

  /// 返回历史会话 ID 列表，保留最近创建的会话在前。
  @override
  Future<List<String>> listSessions() async => List.unmodifiable(_sessionOrder);

  void _emit(String runId, ChatSseEvent event) {
    final controller = _eventControllers[runId];
    if (controller == null || controller.isClosed) {
      throw StateError('验收夹具：run $runId 当前没有可发送的事件流');
    }
    final previous = _lastEventIds[runId] ?? 0;
    if (event.eventId > previous) {
      _lastEventIds[runId] = event.eventId;
    }
    controller.add(ChatRunEventFrame(event));
  }

  void _seedHistory(int count) {
    final safeCount = count.clamp(0, 80);
    for (var index = 0; index < safeCount; index++) {
      final sessionId = 'acceptance-history-${index + 1}';
      final name = switch (index % 4) {
        0 => '项目计划 ${index + 1}：长内容与表格验收',
        1 => '日程搜索 fixture ${index + 1}',
        2 => '工具调用和重连记录 ${index + 1}',
        _ => '周报：中文历史会话 ${index + 1}',
      };
      final runIds = <String>[];
      final inputs = <String>[];
      for (var turn = 0; turn < 3; turn++) {
        final runId = 'history-run-${index + 1}-$turn';
        final day = DateTime.utc(
          _now.year,
          _now.month,
          _now.day,
        ).subtract(Duration(days: index));
        final createdAt = index == 0
            ? _now.subtract(Duration(minutes: 30 - turn * 10))
            : DateTime.utc(day.year, day.month, day.day, 9 + turn);
        final input = '第 ${turn + 1} 轮：请整理 $name 的安排和验收情况。';
        final output = index == 0 && turn == 2
            ? longChatMarkdown
            : '已整理 $name 第 ${turn + 1} 轮内容，包含可搜索摘要和实际回复。';
        _runs[runId] = ChatRun(
          runId: runId,
          sessionId: sessionId,
          model: 'acceptance-model',
          input: input,
          status: ChatRunStatus.succeeded,
          output: output,
          error: null,
          createdAt: createdAt,
          finishedAt: createdAt.add(const Duration(minutes: 1)),
        );
        runIds.add(runId);
        inputs.add(input);
      }
      historicalInputs[sessionId] = List.unmodifiable(inputs);
      _sessions[sessionId] = ChatSession(
        sessionId: sessionId,
        name: name,
        runIds: List.unmodifiable(runIds),
      );
      _sessionOrder.add(sessionId);
    }
  }
}
