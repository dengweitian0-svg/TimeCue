import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';

void main() {
  group('ChatViewModel', () {
    test('结果不明的创建复用原幂等请求并持续占用名额', () async {
      final gateway = _FakeChatGateway();
      final firstAttempt = Completer<ChatCreateRunResult>();
      gateway.createResponses.add(firstAttempt.future);
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '原始输入');

      firstAttempt.completeError(StateError('unknown create result'));
      await model.send(conversation.sessionId, model: 'model-a');
      expect(model.activeRunCount, 1);
      expect(conversation.activeRun?.phase, ChatRunPhase.creating);
      expect(conversation.activeRun?.error, contains('unknown'));
      model.updateDraft(conversation.sessionId, '下一条草稿');

      await model.retryCreate(conversation.sessionId);
      expect(gateway.createCalls, hasLength(2));
      expect(gateway.createCalls[0], gateway.createCalls[1]);
      expect(gateway.createCalls.last.input, '原始输入');
      expect(gateway.createCalls.last.model, 'model-a');
      expect(gateway.createCalls.last.sessionId, conversation.sessionId);
      expect(gateway.createCalls.last.key, 'create_session:key-1');
      expect(conversation.draft, '下一条草稿');
      expect(conversation.activeRun?.runId, 'run-1');
    });

    test('同会话与全局并发限制生效，取消受理不释放名额', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.addAll([
        for (var i = 1; i <= 3; i++)
          Future.value(ChatCreateRunResult('run-$i')),
      ]);
      final model = _model(gateway);
      addTearDown(model.dispose);
      final sessions = [
        model.createLocalSession(),
        model.createLocalSession(),
        model.createLocalSession(),
        model.createLocalSession(),
      ];
      for (var i = 0; i < 3; i++) {
        model.updateDraft(sessions[i].sessionId, '输入 $i');
        await model.send(sessions[i].sessionId, model: 'model');
      }
      expect(model.activeRunCount, 3);
      model.updateDraft(sessions[3].sessionId, '保留输入');
      await model.send(sessions[3].sessionId, model: 'model');
      expect(sessions[3].draft, '保留输入');
      expect(gateway.createCalls, hasLength(3));

      await model.cancel(sessions.first.sessionId);
      expect(sessions.first.activeRun?.phase, ChatRunPhase.stopping);
      expect(sessions.first.activeRun?.cancelPending, isTrue);
      expect(model.activeRunCount, 3);
      expect(model.canSend(sessions.first.sessionId), isFalse);
    });

    test('取消响应迟到时不能覆盖 SSE 已确认的成功终态', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      final cancelResponse = Completer<ChatCancelRunResult>();
      gateway.cancelResponse = cancelResponse.future;
      gateway.snapshots['run-1'] = _snapshot(
        'run-1',
        status: ChatRunStatus.succeeded,
        output: '正式终态文本',
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      final run = conversation.runs.single;
      final cancelFuture = model.cancel(conversation.sessionId);
      expect(run.phase, ChatRunPhase.stopping);

      gateway.controllers.single.add(
        _frame(
          1,
          ChatEventType.end,
          const ChatEndEventData(stopReason: 'stop', error: null),
        ),
      );
      await _pumpEvents(run);
      expect(run.status, ChatRunStatus.succeeded);
      expect(run.phase, ChatRunPhase.succeeded);
      expect(run.cancelPending, isFalse);

      cancelResponse.complete(
        const ChatCancelRunResult(
          runId: 'run-1',
          status: ChatRunStatus.running,
        ),
      );
      await cancelFuture;
      expect(run.status, ChatRunStatus.succeeded);
      expect(run.phase, ChatRunPhase.succeeded);
      expect(run.cancelPending, isFalse);
      expect(model.activeRunCount, 0);
    });

    test('迟到的 running 快照不能复活已确认的终态', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      gateway.cancelResponse = Future.value(
        const ChatCancelRunResult(
          runId: 'run-1',
          status: ChatRunStatus.cancelled,
        ),
      );
      final firstSnapshot = Completer<ChatRun>();
      final laterSnapshot = Completer<ChatRun>();
      gateway.snapshotResponses.addAll([
        firstSnapshot.future,
        laterSnapshot.future,
      ]);
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      final run = conversation.runs.single;

      final cancelFuture = model.cancel(conversation.sessionId);
      await Future<void>.delayed(Duration.zero);
      expect(gateway.getRunCalls, 1);
      gateway.controllers.single.add(
        _frame(
          1,
          ChatEventType.end,
          const ChatEndEventData(stopReason: 'cancelled', error: null),
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));
      expect(gateway.getRunCalls, 2);

      firstSnapshot.complete(
        _snapshot(
          'run-1',
          status: ChatRunStatus.cancelled,
          output: 'checkpoint',
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));
      expect(run.status, ChatRunStatus.cancelled);
      expect(run.rawText, 'checkpoint');
      expect(model.activeRunCount, 0);
      laterSnapshot.complete(_snapshot('run-1'));
      await Future.wait([cancelFuture, _pumpEvents(run)]);

      expect(run.status, ChatRunStatus.cancelled);
      expect(run.phase, ChatRunPhase.cancelled);
      expect(run.rawText, 'checkpoint');
      expect(model.activeRunCount, 0);
    });

    test('手动重连取消待执行的自动重连定时器', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      gateway.snapshots['run-1'] = _snapshot('run-1');
      final model = _model(
        gateway,
        retryDelay: (_) => const Duration(milliseconds: 60),
      );
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      gateway.controllers.single.addError(StateError('socket closed'));
      await Future<void>.delayed(const Duration(milliseconds: 20));
      expect(gateway.controllers, hasLength(1));

      await model.retryConnection(conversation.sessionId);
      expect(gateway.controllers, hasLength(2));
      await Future<void>.delayed(const Duration(milliseconds: 80));
      expect(gateway.controllers, hasLength(2));
    });

    test('事件游标去重，工具按调用 ID 首次顺序保留并用最终快照收敛', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      gateway.snapshots['run-1'] = _snapshot(
        'run-1',
        status: ChatRunStatus.succeeded,
        output: '最终 checkpoint',
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '问候');
      await model.send(conversation.sessionId, model: 'model');
      final first = gateway.controllers.single;
      first.add(
        _frame(
          1,
          ChatEventType.text,
          const ChatContentEventData(content: '片段', error: null),
        ),
      );
      first.add(_toolFrame(2, 'call-a', 'search_schedules'));
      first.add(_toolFrame(3, 'call-a', 'renamed_duplicate'));
      first.add(_toolFrame(4, 'call-b', 'search_schedules'));
      first.add(
        _frame(
          4,
          ChatEventType.text,
          const ChatContentEventData(content: '重复传输', error: null),
        ),
      );
      await _pumpEvents(conversation.activeRun!);

      expect(conversation.activeRun?.rawText, '片段');
      expect(conversation.activeRun?.lastEventId, 4);
      expect(conversation.activeRun?.toolCalls.map((tool) => tool.id), [
        'call-a',
        'call-b',
      ]);
      expect(conversation.activeRun?.toolCalls.map((tool) => tool.name), [
        'search_schedules',
        'search_schedules',
      ]);

      first.add(
        _frame(
          5,
          ChatEventType.end,
          const ChatEndEventData(stopReason: 'stop', error: null),
        ),
      );
      await _pumpEvents(conversation.runs.single);
      expect(conversation.runs.single.rawText, '最终 checkpoint');
      expect(conversation.runs.single.status, ChatRunStatus.succeeded);
      expect(conversation.activeRun, isNull);
      expect(model.activeRunCount, 0);
    });

    test('缺失事件不推进游标且断线后从最后成功游标继续', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      gateway.snapshots['run-1'] = _snapshot('run-1');
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');

      final first = gateway.controllers.single;
      first.add(
        _frame(
          1,
          ChatEventType.text,
          const ChatContentEventData(content: 'A', error: null),
        ),
      );
      first.add(
        _frame(
          3,
          ChatEventType.text,
          const ChatContentEventData(content: '丢失', error: null),
        ),
      );
      await _pumpEvents(conversation.runs.single);
      await Future<void>.delayed(Duration.zero);
      expect(conversation.runs.single.rawText, 'A');
      expect(conversation.runs.single.lastEventId, 1);
      expect(gateway.cursors, [null, 1]);

      gateway.controllers.last.add(
        _frame(
          2,
          ChatEventType.text,
          const ChatContentEventData(content: 'B', error: null),
        ),
      );
      gateway.controllers.last.add(
        _frame(
          3,
          ChatEventType.text,
          const ChatContentEventData(content: 'C', error: null),
        ),
      );
      await _pumpEvents(conversation.runs.single);
      expect(conversation.runs.single.rawText, 'ABC');
      expect(conversation.runs.single.lastEventId, 3);
    });

    test('创建后服务端名称读取失败保留当前名称和内联错误', () async {
      final gateway = _FakeChatGateway()..sessionError = StateError('名称读取失败');
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      await Future<void>.delayed(Duration.zero);

      expect(conversation.name, '');
      expect(conversation.error, '名称读取失败');
      expect(conversation.isLocal, isFalse);
    });

    test('运行失效后迟到事件不能污染状态或通知已释放模型', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      final model = _model(gateway);
      final conversation = model.createLocalSession();
      var notifications = 0;
      model.addListener(() => notifications++);
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      final run = conversation.runs.single;
      final before = model.activeRunCount;

      model.invalidateRuntime();
      gateway.controllers.single.add(
        _frame(
          1,
          ChatEventType.text,
          const ChatContentEventData(content: '迟到', error: null),
        ),
      );
      await _pumpEvents(run);
      expect(run.rawText, '');
      expect(model.activeRunCount, before);
      final countAtDispose = notifications;
      model.dispose();
      gateway.controllers.single.add(
        _frame(
          2,
          ChatEventType.text,
          const ChatContentEventData(content: '更迟', error: null),
        ),
      );
      await Future<void>.delayed(Duration.zero);
      expect(notifications, countAtDispose);
    });

    test('运行失效后禁止创建重试、连接重试和取消请求', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '首次输入');
      await model.send(conversation.sessionId, model: 'model');
      gateway.createResponses.add(
        Future.error(StateError('unknown create result')),
      );
      final uncertain = model.createLocalSession();
      model.updateDraft(uncertain.sessionId, '结果不明输入');
      await model.send(uncertain.sessionId, model: 'model');
      expect(uncertain.activeRun?.phase, ChatRunPhase.creating);
      model.invalidateRuntime();

      await model.retryConnection(conversation.sessionId);
      await model.cancel(conversation.sessionId);
      await model.retryCreate(uncertain.sessionId);
      model.updateDraft(conversation.sessionId, '新草稿');
      await model.send(conversation.sessionId, model: 'model');

      expect(gateway.createCalls, hasLength(2));
      expect(gateway.cursors, [null]);
      expect(gateway.cancelCalls, 0);
      expect(conversation.draft, '新草稿');
      expect(model.activeRunCount, 2);
    });

    test('错误的服务端身份不会覆盖会话名称或运行消息', () async {
      final gateway = _FakeChatGateway()
        ..sessionOverride = const ChatSession(
          sessionId: 'other-session',
          name: '串会话名称',
          runIds: [],
        )
        ..snapshots['run-1'] = _snapshot(
          'other-run',
          status: ChatRunStatus.succeeded,
          output: '串会话正文',
        );
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      final model = _model(gateway, maxReconnectAttempts: 0);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '本会话输入');
      await model.send(conversation.sessionId, model: 'model');
      final run = conversation.runs.single;
      await Future<void>.delayed(const Duration(milliseconds: 20));
      gateway.controllers.single.add(
        _frame(
          1,
          ChatEventType.text,
          const ChatContentEventData(content: '本地已收到', error: null),
        ),
      );
      gateway.controllers.single.add(
        _frame(
          2,
          ChatEventType.end,
          const ChatEndEventData(stopReason: 'stop', error: null),
        ),
      );
      await _pumpEvents(run);

      expect(conversation.name, '');
      expect(conversation.error, contains('session ID'));
      expect(run.rawText, '本地已收到');
      expect(run.status, ChatRunStatus.running);
      expect(run.error, contains('身份'));
    });

    test('SSE 返回 204 后用 GET checkpoint 确认取消终态', () async {
      final gateway = _FakeChatGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-1')),
      );
      gateway.snapshots['run-1'] = _snapshot(
        'run-1',
        status: ChatRunStatus.cancelled,
        output: '确认后的 checkpoint',
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      gateway.controllers.single.add(const ChatRunEventsNoContent());
      await _pumpEvents(conversation.runs.single);

      expect(conversation.runs.single.status, ChatRunStatus.cancelled);
      expect(conversation.runs.single.rawText, '确认后的 checkpoint');
      expect(conversation.runs.single.phase, ChatRunPhase.cancelled);
      expect(model.activeRunCount, 0);
    });
  });
}

ChatViewModel _model(
  _FakeChatGateway gateway, {
  int maxReconnectAttempts = 4,
  Duration Function(int attempt)? retryDelay,
}) {
  var sessionNumber = 0;
  var keyNumber = 0;
  return ChatViewModel(
    gateway: gateway,
    createSessionId: () => 'session-${++sessionNumber}',
    createIdempotencyKey: () => 'create_session:key-${++keyNumber}',
    retryDelay: retryDelay ?? (_) => Duration.zero,
    maxReconnectAttempts: maxReconnectAttempts,
  );
}

Future<void> _pumpEvents(ChatRunState run) async {
  // SSE listener 和其状态更新均以异步事件队列推进。
  await Future<void>.delayed(const Duration(milliseconds: 20));
}

ChatRunEventFrame _frame(int id, ChatEventType type, Object data) =>
    ChatRunEventFrame(ChatSseEvent(eventId: id, type: type, data: data));

ChatRunEventFrame _toolFrame(int id, String callId, String name) => _frame(
  id,
  ChatEventType.tool,
  ChatToolEventData(
    id: callId,
    function: ChatToolFunction(
      name: name,
      args: const ChatToolArgument(name: '', value: null),
    ),
  ),
);

ChatRun _snapshot(
  String runId, {
  ChatRunStatus status = ChatRunStatus.running,
  String? output,
}) => ChatRun(
  runId: runId,
  sessionId: 'session-1',
  model: 'model',
  input: '输入',
  status: status,
  output: output,
  error: null,
  createdAt: DateTime.utc(2026),
  finishedAt: status == ChatRunStatus.running ? null : DateTime.utc(2026),
);

class _CreateCall {
  const _CreateCall(this.sessionId, this.input, this.model, this.key);
  final String sessionId;
  final String input;
  final String model;
  final String key;

  @override
  bool operator ==(Object other) =>
      other is _CreateCall &&
      sessionId == other.sessionId &&
      input == other.input &&
      model == other.model &&
      key == other.key;

  @override
  int get hashCode => Object.hash(sessionId, input, model, key);
}

class _FakeChatGateway implements ChatGateway {
  final List<Future<ChatCreateRunResult>> createResponses = [];
  final List<_CreateCall> createCalls = [];
  final List<StreamController<ChatRunEventSignal>> controllers = [];
  final List<int?> cursors = [];
  final Map<String, ChatRun> snapshots = {};
  final List<Future<ChatRun>> snapshotResponses = [];
  Object? sessionError;
  ChatSession? sessionOverride;
  int cancelCalls = 0;
  int getRunCalls = 0;
  Future<ChatCancelRunResult>? cancelResponse;
  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) {
    createCalls.add(_CreateCall(sessionId, input, model, idempotencyKey));
    return createResponses.removeAt(0);
  }

  @override
  Future<ChatRun> getRun(String runId) {
    getRunCalls++;
    if (snapshotResponses.isNotEmpty) return snapshotResponses.removeAt(0);
    return Future.value(snapshots[runId] ?? _snapshot(runId));
  }

  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) {
    cursors.add(lastEventId);
    final controller = StreamController<ChatRunEventSignal>();
    controllers.add(controller);
    return controller.stream;
  }

  @override
  Future<ChatCancelRunResult> cancelRun(String runId) {
    cancelCalls++;
    return cancelResponse ??
        Future.value(
          ChatCancelRunResult(runId: runId, status: ChatRunStatus.running),
        );
  }

  @override
  Future<ChatSession> getSession(String sessionId) async {
    if (sessionError case final error?) throw error;
    return sessionOverride ??
        ChatSession(sessionId: sessionId, name: '服务器名称', runIds: ['run-1']);
  }

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async =>
      ChatDeleteSessionResult(sessionId: sessionId, status: 'deleted');

  @override
  Future<List<String>> listSessions() async => const [];
}
