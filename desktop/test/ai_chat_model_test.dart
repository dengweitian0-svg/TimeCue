import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/ai_chat.dart';
import 'package:timecue_desktop/features/ai_chat/ai_chat_model.dart';

const _sessionA = '550e8400-e29b-41d4-a716-446655440000';
const _sessionB = '550e8400-e29b-41d4-a716-446655440001';
const _runA = 'd9428888-122b-4f6f-9b47-5ea7b2915210';

void main() {
  late _FakeGateway gateway;
  late AiChatModel model;
  var id = 0;

  setUp(() {
    gateway = _FakeGateway();
    id = 0;
    model = AiChatModel(gateway, createId: () => 'id-${id++}');
  });

  tearDown(() {
    model.dispose();
    gateway.dispose();
  });

  test('并发上限按活跃会话计数，提交中的创建立即占位', () async {
    gateway.sessionIds.addAll([_sessionA, _sessionB]);
    gateway.runsBySession[_sessionA] = [_runA];
    gateway.runsBySession[_sessionB] = ['r-b'];
    gateway.runs[_runA] = _run(_runA, _sessionA);
    gateway.runs['r-b'] = _run('r-b', _sessionB);
    await model.refresh();
    expect(model.activeSessionCount, 2);

    final local = model.createSession();
    gateway.createGate = Completer<String>();
    final send = model.send(local.id, '新输入');
    expect(model.activeSessionCount, 3); // 两个已有名额加一个尚未返回的创建
    expect(model.sendDisabledReasonFor(local.id), contains('正在运行'));
    model.setDraft(local.id, '提交中的编辑');
    expect(model.draftFor(local.id), '新输入');
    final competing = model.createSession();
    await model.send(competing.id, '竞争输入');
    expect(gateway.keys, hasLength(1));
    gateway.createGate!.complete('r-new');
    await send;
    expect(model.draftFor(local.id), isEmpty);
    model.setDraft(local.id, '下一条输入');
    expect(model.draftFor(local.id), '下一条输入');
  });

  test('删除运行的并发拒绝保留原内容和幂等键供明确重试', () async {
    gateway.sessionIds.add(_sessionA);
    gateway.runsBySession[_sessionA] = [_runA];
    gateway.runs[_runA] = _run(_runA, _sessionA);
    await model.refresh();
    await model.deleteSession(_sessionA);
    expect(model.activeSessionCount, 1);

    final local = model.createSession();
    gateway.createError = const AiChatGatewayException(
      statusCode: 409,
      endpoint: '/v1/run',
      code: 'session_concurrency_limit',
    );
    await model.send(local.id, '保留原文');
    final originalKey = gateway.keys.single;
    expect(model.sessions.single.createRetryable, isTrue);
    expect(model.sessions.single.createResultUnknown, isFalse);
    expect(model.draftFor(local.id), '保留原文');
    expect(model.sendDisabledReasonFor(local.id), contains('正在停止'));

    model.setDraft(local.id, '不能替换');
    expect(model.draftFor(local.id), '保留原文');
    await model.send(local.id, '不能绕过');
    expect(model.draftFor(local.id), '保留原文');
    expect(gateway.keys, hasLength(1));

    gateway.createError = null;
    gateway.createdRunId = 'new-run';
    await model.retry(local.id);
    expect(gateway.keys, [originalKey, originalKey]);
    expect(gateway.inputs, ['保留原文', '保留原文']);
    expect(model.sessions.first.id, local.id);
    expect(model.sessions.first.createRetryable, isFalse);
  });

  test('删除墓碑不能允许超过三个已知活跃会话', () async {
    final sessions = [_sessionA, _sessionB, 'session-c', 'session-d'];
    gateway.sessionIds.addAll(sessions);
    for (final sessionId in sessions) {
      final runId = 'run-$sessionId';
      gateway.runsBySession[sessionId] = [runId];
      gateway.runs[runId] = _run(runId, sessionId);
    }
    await model.refresh();
    await model.deleteSession(_sessionA);
    expect(model.activeSessionCount, 4);

    final local = model.createSession();
    expect(model.sendDisabledReasonFor(local.id), contains('最多同时运行 3'));
    await model.send(local.id, '不能发送第四个');
    expect(gateway.keys, isEmpty);
  });

  test('并发拒绝的本地空会话可直接删除', () async {
    gateway.sessionIds.add(_sessionA);
    gateway.runsBySession[_sessionA] = [_runA];
    gateway.runs[_runA] = _run(_runA, _sessionA);
    await model.refresh();
    await model.deleteSession(_sessionA);
    final local = model.createSession();
    gateway.createError = const AiChatGatewayException(
      statusCode: 409,
      endpoint: '/v1/run',
      code: 'session_concurrency_limit',
    );
    await model.send(local.id, '保留原文');
    expect(model.sessions.single.createRetryable, isTrue);
    final deleteCalls = gateway.deleteCalls;

    await model.deleteSession(local.id);

    expect(model.sessions, isEmpty);
    expect(gateway.deleteCalls, deleteCalls);
    expect(gateway.keys, hasLength(1));
  });

  test('未知创建结果保留原文和幂等键，编辑不能绕过原请求', () async {
    await model.refresh();
    final session = model.createSession();
    gateway.createError = StateError('响应丢失');
    await model.send(session.id, '  原始输入\n第二行  ');
    final key = gateway.keys.single;
    expect(model.sessions.single.createResultUnknown, isTrue);
    model.setDraft(session.id, '修改后的输入');
    expect(model.draftFor(session.id), '  原始输入\n第二行  ');

    gateway.createError = null;
    gateway.createdRunId = _runA;
    await model.retry(session.id);
    expect(gateway.keys.last, key);
    expect(gateway.inputs.last, '  原始输入\n第二行  ');
    expect(model.sessions.single.turns.single.input, '  原始输入\n第二行  ');
  });

  test('成功状态码但响应格式无效时保留幂等请求供原文重试', () async {
    await model.refresh();
    final session = model.createSession();
    gateway.createError = const AiChatGatewayException(
      statusCode: 202,
      endpoint: '/v1/run',
      message: '响应格式无效',
    );
    await model.send(session.id, '  原始输入  ');
    final originalKey = gateway.keys.single;

    expect(model.sessions.single.createResultUnknown, isTrue);
    expect(model.draftFor(session.id), '  原始输入  ');
    await model.send(session.id, '替代输入');
    expect(gateway.keys, hasLength(1));

    gateway.createError = null;
    gateway.createdRunId = _runA;
    await model.retry(session.id);
    expect(gateway.keys.last, originalKey);
    expect(gateway.inputs.last, '  原始输入  ');
    expect(model.sessions.single.createResultUnknown, isFalse);
  });

  test('未知创建的重复重试复用同一在途请求，不追加第二条 run', () async {
    await model.refresh();
    final session = model.createSession();
    gateway.createError = StateError('响应丢失');
    await model.send(session.id, '原文');
    gateway.createError = null;
    gateway.createGate = Completer<String>();
    final retry = model.retry(session.id);
    await model.retry(session.id);
    expect(gateway.keys, hasLength(2));
    gateway.createGate!.complete(_runA);
    await retry;
    expect(model.sessions.single.turns, hasLength(1));
  });

  test('重复事件忽略，按顺序拼接并只推进成功处理的游标', () async {
    await model.refresh();
    final session = model.createSession();
    await model.send(session.id, '原文');
    final events = gateway.controllers[_runA]!;
    events.add(const AiChatEvent(id: 2, type: 'text', data: {'content': 'B'}));
    events.add(
      const AiChatEvent(id: 1, type: 'thinking', data: {'content': '想'}),
    );
    events.add(
      const AiChatEvent(id: 1, type: 'thinking', data: {'content': '重复'}),
    );
    events.add(const AiChatEvent(id: 3, type: 'text', data: {'content': 'C'}));
    await Future<void>.delayed(Duration.zero);
    final turn = model.sessions.single.turns.single;
    expect(turn.thinking, '想');
    expect(turn.text, 'BC');
    expect(turn.lastProcessedEventId, 3);
  });

  test('204 及断线从已处理游标恢复并读取正式终态', () async {
    await model.refresh();
    final session = model.createSession();
    await model.send(session.id, '原文');
    gateway.controllers[_runA]!.add(
      const AiChatEvent(id: 1, type: 'text', data: {'content': '保留'}),
    );
    await Future<void>.delayed(Duration.zero);
    gateway.completeRun(
      _run(_runA, session.id, status: AiChatStatus.succeeded, output: '正式答案'),
    );
    gateway.controllers[_runA]!.add(const AiChatEvent.noContent());
    await Future<void>.delayed(const Duration(milliseconds: 5));
    expect(gateway.watchCursors[_runA], [null]);
    expect(model.sessions.single.turns.single.text, '正式答案');
    expect(model.activeSessionCount, 0);
  });

  test('内容保留时断线重连使用最后成功处理的事件 ID', () async {
    await model.refresh();
    final session = model.createSession();
    await model.send(session.id, '原文');
    gateway.controllers[_runA]!.add(
      const AiChatEvent(id: 1, type: 'text', data: {'content': '保留内容'}),
    );
    await Future<void>.delayed(const Duration(milliseconds: 5));
    await gateway.controllers[_runA]!.close();
    await Future<void>.delayed(const Duration(milliseconds: 180));
    expect(gateway.watchCursors[_runA], [null, 1]);
    expect(model.sessions.single.turns.single.text, '保留内容');
  });

  test('自动重连收到有效新事件后清除瞬时传输错误', () async {
    await model.refresh();
    final session = model.createSession();
    await model.send(session.id, '原文');
    final firstConnection = gateway.controllers[_runA]!;
    firstConnection.add(
      const AiChatEvent(id: 1, type: 'text', data: {'content': '原内容'}),
    );
    await Future<void>.delayed(Duration.zero);
    firstConnection.addError(StateError('连接断开'));
    await Future<void>.delayed(const Duration(milliseconds: 5));
    expect(model.sessions.single.turns.single.error, isNotNull);
    expect(model.sessions.single.turns.single.retryable, isTrue);

    await Future<void>.delayed(const Duration(milliseconds: 180));
    expect(gateway.watchCursors[_runA], [null, 1]);
    expect(model.sessions.single.turns.single.error, isNotNull);
    final replacement = gateway.controllers[_runA]!;
    replacement.add(
      const AiChatEvent(id: 2, type: 'text', data: {'content': '续接'}),
    );
    await Future<void>.delayed(Duration.zero);

    final turn = model.sessions.single.turns.single;
    expect(turn.text, '原内容续接');
    expect(turn.lastProcessedEventId, 2);
    expect(turn.error, isNull);
    expect(turn.retryable, isFalse);

    replacement.add(
      const AiChatEvent(
        id: 3,
        type: 'thinking',
        data: {
          'content': '',
          'error': {'code': 'model_error', 'message': '模型返回业务错误'},
        },
      ),
    );
    replacement.add(
      const AiChatEvent(id: 4, type: 'text', data: {'content': '后续'}),
    );
    await Future<void>.delayed(Duration.zero);
    final failedTurn = model.sessions.single.turns.single;
    expect(failedTurn.error?.code, 'model_error');
    expect(failedTurn.error?.message, '模型返回业务错误');
    expect(failedTurn.retryable, isFalse);
    expect(failedTurn.lastProcessedEventId, 4);
  });

  test('发送旧会话后将其移到历史列表顶部', () async {
    gateway.sessionIds.addAll([_sessionA, _sessionB]);
    gateway.runsBySession[_sessionA] = ['old-a'];
    gateway.runsBySession[_sessionB] = ['new-b'];
    gateway.runs['old-a'] = _run(
      'old-a',
      _sessionA,
      status: AiChatStatus.succeeded,
      output: '旧回复',
    );
    gateway.runs['new-b'] = _run(
      'new-b',
      _sessionB,
      status: AiChatStatus.succeeded,
      output: '近期回复',
    );
    await model.refresh();
    expect(model.sessions.map((session) => session.id), [_sessionA, _sessionB]);
    model.selectSession(_sessionA);
    gateway.createdRunId = 'latest-a';

    await model.send(_sessionA, 'A 的新问题');

    expect(model.sessions.first.id, _sessionA);
    expect(model.sessions.first.turns.last.input, 'A 的新问题');
  });

  test('sse_connection_exists 不触发自动重复连接', () async {
    await model.refresh();
    final session = model.createSession();
    gateway.watchError = const AiChatGatewayException(
      statusCode: 409,
      endpoint: '/v1/run/event',
      code: 'sse_connection_exists',
    );
    await model.send(session.id, '原文');
    await Future<void>.delayed(const Duration(milliseconds: 200));
    expect(gateway.watchCalls, 1);
    expect(model.sessions.single.turns.single.retryable, isTrue);
  });

  test('取消返回 running 时保留名额，终态 checkpoint 覆盖流式文本', () async {
    await model.refresh();
    final session = model.createSession();
    await model.send(session.id, '原文');
    gateway.controllers[_runA]!.add(
      const AiChatEvent(id: 1, type: 'text', data: {'content': '较新流文本'}),
    );
    await Future<void>.delayed(Duration.zero);
    gateway.cancelStatus = AiChatStatus.running;
    await model.cancel(session.id, _runA);
    expect(model.activeSessionCount, 1);
    expect(model.sessions.single.turns.single.status, AiChatStatus.running);
    expect(model.sessions.single.turns.single.cancelling, isTrue);

    gateway.cancelStatus = AiChatStatus.cancelled;
    gateway.completeRun(
      _run(
        _runA,
        session.id,
        status: AiChatStatus.cancelled,
        output: 'checkpoint',
      ),
    );
    gateway.controllers[_runA]!.add(
      const AiChatEvent(
        id: 2,
        type: 'end',
        data: {'stop_reason': 'cancelled', 'error': null},
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 5));
    expect(model.sessions.single.turns.single.text, 'checkpoint');
    expect(model.activeSessionCount, 0);
    expect(model.sessions.single.turns.single.cancelling, isFalse);
  });

  test('删除成功立即移除，迟到创建响应不会复活会话', () async {
    await model.refresh();
    final session = model.createSession();
    gateway.createGate = Completer<String>();
    final send = model.send(session.id, '输入');
    final deleteGate = Completer<void>();
    gateway.deleteGate = deleteGate;
    final deletion = model.deleteSession(session.id);
    deleteGate.complete();
    await deletion;
    gateway.createGate!.complete(_runA);
    await send;
    expect(model.sessions, isEmpty);
    expect(model.activeSessionCount, 1); // 删除中的创建结果按不确定名额保守保留
  });

  test('dispose 关闭监听但不取消外部运行', () async {
    await model.refresh();
    final session = model.createSession();
    await model.send(session.id, '输入');
    expect(gateway.cancelCalls, 0);
    model.dispose();
    await Future<void>.delayed(Duration.zero);
    expect(gateway.cancelCalls, 0);
    expect(gateway.closedControllers, 1);
  });

  test('invalidate 使迟到创建响应失效且不取消 Go run', () async {
    await model.refresh();
    final session = model.createSession();
    gateway.createGate = Completer<String>();
    final send = model.send(session.id, '输入');
    model.invalidate();
    gateway.createGate!.complete(_runA);
    await send;
    expect(model.sessions.single.turns, isEmpty);
    expect(model.synchronized, isFalse);
    expect(gateway.cancelCalls, 0);
  });

  test('空白输入与未提交的本地会话删除不会访问网关', () async {
    await model.refresh();
    final session = model.createSession();
    await model.send(session.id, '  \n');
    expect(gateway.keys, isEmpty);
    await model.deleteSession(session.id);
    expect(gateway.deleteCalls, 0);
    expect(model.sessions, isEmpty);
  });

  test('刷新发现远端会话已删除后隐藏旧历史', () async {
    gateway.sessionIds.add(_sessionA);
    gateway.runsBySession[_sessionA] = ['old-run'];
    await model.refresh();
    expect(model.sessions, hasLength(1));
    gateway.sessionIds.clear();
    await model.refresh();
    expect(model.sessions, isEmpty);
  });
}

AiChatRun _run(
  String id,
  String sessionId, {
  AiChatStatus status = AiChatStatus.running,
  String? output,
}) => AiChatRun(
  runId: id,
  sessionId: sessionId,
  model: 'deepseek-flash',
  input: '输入',
  status: status,
  output: output,
  createdAt: DateTime.utc(2026),
);

class _FakeGateway implements AiChatGateway {
  final List<String> sessionIds = [];
  final Map<String, List<String>> runsBySession = {};
  final Map<String, AiChatRun> runs = {};
  final Map<String, StreamController<AiChatEvent>> controllers = {};
  final Map<String, List<int?>> watchCursors = {};
  final List<String> keys = [];
  final List<String> inputs = [];
  int cancelCalls = 0;
  int deleteCalls = 0;
  int closedControllers = 0;
  int watchCalls = 0;
  Completer<String>? createGate;
  Completer<void>? deleteGate;
  Object? createError;
  String createdRunId = _runA;
  AiChatStatus cancelStatus = AiChatStatus.running;
  Object? watchError;

  @override
  Future<List<String>> listSessions() async => List.of(sessionIds);

  @override
  Future<List<AiChatRun>> getSessionRuns(String sessionId) async =>
      (runsBySession[sessionId] ?? const <String>[])
          .map((id) => runs[id] ?? _run(id, sessionId))
          .toList();

  @override
  Future<AiChatRun> getRun(String runId) async =>
      runs[runId] ?? _run(runId, '');

  @override
  Future<String> createRun(
    String sessionId,
    String input, {
    required String idempotencyKey,
    String model = 'deepseek-flash',
  }) async {
    keys.add(idempotencyKey);
    inputs.add(input);
    if (createError != null) throw createError!;
    final runId = createGate != null ? await createGate!.future : createdRunId;
    runs[runId] = _run(runId, sessionId);
    return runId;
  }

  @override
  Future<AiChatStatus> cancelRun(String runId) async {
    cancelCalls++;
    return cancelStatus;
  }

  @override
  Future<void> deleteSession(String sessionId) async {
    deleteCalls++;
    await deleteGate?.future;
    sessionIds.remove(sessionId);
  }

  @override
  Stream<AiChatEvent> watchRun(String runId, {int? lastEventId}) {
    watchCalls++;
    watchCursors.putIfAbsent(runId, () => []).add(lastEventId);
    if (watchError != null) return Stream.error(watchError!);
    final controller = StreamController<AiChatEvent>();
    controllers[runId] = controller;
    controller.onCancel = () => closedControllers++;
    return controller.stream;
  }

  void completeRun(AiChatRun run) => runs[run.runId] = run;

  @override
  void invalidate() {}

  @override
  void dispose() {
    for (final controller in controllers.values) {
      if (!controller.isClosed) unawaited(controller.close());
    }
  }
}
