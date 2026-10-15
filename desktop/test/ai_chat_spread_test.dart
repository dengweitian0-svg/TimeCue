import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/ai_chat.dart';
import 'package:timecue_desktop/features/ai_chat/ai_chat_model.dart';
import 'package:timecue_desktop/features/ai_chat/ai_chat_spread.dart';

void main() {
  late _Gateway gateway;
  late AiChatModel model;
  var nextId = 0;

  setUp(() {
    gateway = _Gateway();
    nextId = 0;
    model = AiChatModel(gateway, createId: () => 'new-id-${nextId++}');
  });

  tearDown(() {
    model.dispose();
    gateway.dispose();
  });

  Future<void> mount(
    WidgetTester tester, {
    double height = 700,
    double scale = 1,
  }) async {
    await tester.binding.setSurfaceSize(Size(1040, height));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await tester.pumpWidget(
      MaterialApp(
        home: MediaQuery(
          data: MediaQueryData(
            size: Size(1040, height),
            textScaler: TextScaler.linear(scale),
          ),
          child: Scaffold(
            body: SizedBox.expand(child: AiChatSpread(model: model)),
          ),
        ),
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1));
  }

  testWidgets('加载失败显示真实错误，重试成功后展示历史', (tester) async {
    gateway.listError = const AiChatGatewayException(
      statusCode: 503,
      endpoint: '/sessions',
      message: '服务暂时不可用',
    );
    await mount(tester);
    await tester.pumpAndSettle();
    expect(find.text('服务暂时不可用'), findsOneWidget);
    gateway.listError = null;
    gateway.sessionIds.add('history-1');
    gateway.runsBySession['history-1'] = [
      _run('run-1', 'history-1', input: '上周阅读安排', output: '每天读半小时'),
    ];
    await tester.tap(find.text('重试加载'));
    await tester.pumpAndSettle();
    expect(find.text('上周阅读安排'), findsWidgets);
    expect(gateway.listCalls, 2);
  });

  testWidgets('新会话发送保留原文；Enter 发送、Shift+Enter 换行，IME composing 不发送', (
    tester,
  ) async {
    await mount(tester);
    await tester.pumpAndSettle();
    final field = find.byKey(const ValueKey('composer-new-id-0'));
    expect(field, findsOneWidget);
    await tester.enterText(field, '  请帮我总结\n原始格式  ');
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 30));
    expect(gateway.inputs, ['  请帮我总结\n原始格式  ']);

    gateway.finishLatest('整理好了');
    await tester.pump();
    final currentField = find.byKey(const ValueKey('composer-new-id-0'));
    await tester.enterText(currentField, '第二行');
    await tester.enterText(currentField, '第二行\n仍可换行');
    await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
    await tester.pump();
    expect(gateway.inputs, hasLength(1));
    expect(
      tester.widget<TextField>(currentField).controller!.text,
      '第二行\n仍可换行',
    );

    final controller = tester.widget<TextField>(currentField).controller!;
    controller.value = TextEditingValue(
      text: '拼音',
      selection: const TextSelection.collapsed(offset: 2),
      composing: const TextRange(start: 0, end: 2),
    );
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();
    expect(gateway.inputs, hasLength(1));
  });

  testWidgets('提交请求未返回时锁定输入并保留原文，成功后可继续编辑', (tester) async {
    gateway.createGate = Completer<String>();
    await mount(tester);
    await tester.pumpAndSettle();
    final field = find.byKey(const ValueKey('composer-new-id-0'));
    await tester.enterText(field, '等待返回期间保留这段原文');
    await tester.pump();
    await tester.tap(find.byTooltip('发送'));
    await tester.pump();

    var composer = tester.widget<TextField>(field);
    expect(model.selectedSession!.creating, isTrue);
    expect(composer.readOnly, isTrue);
    expect(composer.controller!.text, '等待返回期间保留这段原文');
    expect(model.draftFor('new-id-0'), '等待返回期间保留这段原文');
    expect(find.text('正在提交本轮输入，请稍候。'), findsOneWidget);

    gateway.createGate!.complete('delayed-run');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    composer = tester.widget<TextField>(field);
    expect(model.selectedSession!.creating, isFalse);
    expect(composer.readOnly, isFalse);
    expect(composer.controller!.text, isEmpty);

    await tester.enterText(field, '下一条草稿');
    await tester.pump();
    expect(model.draftFor('new-id-0'), '下一条草稿');
  });

  testWidgets('创建被拒绝时显示错误并保留草稿供重新发送', (tester) async {
    gateway.createError = const AiChatGatewayException(
      statusCode: 409,
      endpoint: '/v1/session/run',
      code: 'session_busy',
      message: '该会话正在处理上一条请求。',
    );
    await mount(tester);
    await tester.pumpAndSettle();
    final field = find.byKey(const ValueKey('composer-new-id-0'));
    await tester.enterText(field, '保留失败前输入');
    await tester.pump();
    await tester.tap(find.byTooltip('发送'));
    await tester.pumpAndSettle();

    expect(find.text('该会话正在处理上一条请求。'), findsOneWidget);
    expect(model.draftFor('new-id-0'), '保留失败前输入');
    expect(tester.widget<TextField>(field).controller!.text, '保留失败前输入');

    gateway.createError = null;
    await tester.tap(find.byTooltip('发送'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    expect(gateway.inputs, ['保留失败前输入', '保留失败前输入']);
  });

  testWidgets('删除会话尚在停止时锁定原文并提供明确重试', (tester) async {
    gateway.sessionIds.add('stopping-session');
    gateway.runsBySession['stopping-session'] = [
      _run('stopping-run', 'stopping-session', status: AiChatStatus.running),
    ];
    await mount(tester);
    await tester.pump();
    await model.deleteSession('stopping-session');
    await tester.pumpAndSettle();

    gateway.createError = const AiChatGatewayException(
      statusCode: 409,
      endpoint: '/v1/run',
      code: 'session_concurrency_limit',
      message: '并发名额尚未释放',
    );
    final field = find.byKey(const ValueKey('composer-new-id-0'));
    await tester.enterText(field, '需要保留的原文');
    await tester.pump();
    await tester.tap(find.byTooltip('发送'));
    await tester.pumpAndSettle();

    expect(model.selectedSession!.createRetryable, isTrue);
    expect(tester.widget<TextField>(field).readOnly, isTrue);
    expect(tester.widget<TextField>(field).controller!.text, '需要保留的原文');
    expect(find.text('会话正在停止，请稍后重试原内容。'), findsWidgets);
    expect(model.canRetryCreate(model.selectedSessionId!), isTrue);
    final originalKey = gateway.keys.single;

    gateway.createError = null;
    await tester.tap(find.widgetWithText(TextButton, '重试原内容'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));

    expect(gateway.keys, [originalKey, originalKey]);
    expect(gateway.inputs, ['需要保留的原文', '需要保留的原文']);
    expect(model.selectedSession!.createRetryable, isFalse);
    expect(tester.widget<TextField>(field).controller!.text, isEmpty);
  });

  testWidgets('会话草稿按会话保留，搜索过滤并在确认后删除', (tester) async {
    gateway.sessionIds.addAll(['history-a', 'history-b']);
    gateway.runsBySession['history-a'] = [
      _run('run-a', 'history-a', input: '计划阅读', output: '先选一本书'),
    ];
    gateway.runsBySession['history-b'] = [
      _run('run-b', 'history-b', input: '周末整理', output: '分区完成'),
    ];
    await mount(tester);
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('history-history-a')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('composer-history-a')));
    await tester.enterText(
      find.byKey(const ValueKey('composer-history-a')),
      'A 的草稿',
    );
    await tester.tap(find.byKey(const ValueKey('history-history-b')));
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('composer-history-b')))
          .controller!
          .text,
      '',
    );
    await tester.enterText(
      find.byKey(const ValueKey('composer-history-b')),
      'B 的草稿',
    );
    await tester.tap(find.byKey(const ValueKey('history-history-a')));
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('composer-history-a')))
          .controller!
          .text,
      'A 的草稿',
    );

    await tester.enterText(find.byType(TextField).first, '周末');
    await tester.pumpAndSettle();
    expect(find.text('周末整理'), findsOneWidget);
    expect(find.byKey(const ValueKey('history-history-a')), findsNothing);
    await tester.tap(find.byKey(const ValueKey('history-history-b')));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('更多会话操作'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('删除会话'));
    await tester.pumpAndSettle();
    expect(find.text('删除会话？'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, '删除'));
    await tester.pumpAndSettle();
    expect(model.sessions.any((session) => session.id == 'history-b'), isFalse);
  });

  testWidgets('流式内容显示，可停止且失败重连入口按模型状态出现', (tester) async {
    await mount(tester);
    await tester.pumpAndSettle();
    final field = find.byKey(const ValueKey('composer-new-id-0'));
    await tester.enterText(field, '流式问题');
    await tester.pump();
    expect(model.selectedSessionId, 'new-id-0');
    expect(model.sendDisabledReasonFor('new-id-0'), isNull);
    expect(model.draftFor('new-id-0'), '流式问题');
    final sendButton = find.ancestor(
      of: find.byIcon(Icons.send_rounded),
      matching: find.byType(IconButton),
    );
    expect(sendButton, findsOneWidget);
    expect(tester.widget<IconButton>(sendButton).onPressed, isNotNull);
    await tester.tap(sendButton);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 30));
    expect(gateway.inputs, ['流式问题']);
    final run = model.selectedSession!.turns.single;
    gateway.events[run.runId]!.add(
      const AiChatEvent(id: 1, type: 'text', data: {'content': '逐字'}),
    );
    await tester.pump();
    expect(find.text('逐字'), findsWidgets);
    await tester.tap(find.byTooltip('停止回复'));
    await tester.pumpAndSettle();
    expect(gateway.cancelCalls, 1);
  });

  testWidgets('长回复、小高度和大字可滚动访问，不触发布局异常', (tester) async {
    gateway.sessionIds.add('long-session');
    gateway.runsBySession['long-session'] = [
      _run(
        'long-run',
        'long-session',
        input: '很长的问题 ' * 30,
        output: '很长的回复内容。' * 200,
        status: AiChatStatus.succeeded,
      ),
    ];
    await mount(tester, height: 260, scale: 2);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.byKey(const ValueKey('composer-long-session')), findsOneWidget);
    expect(find.textContaining('很长的回复内容。'), findsWidgets);
  });
}

AiChatRun _run(
  String id,
  String session, {
  String input = '问题',
  String output = '',
  AiChatStatus status = AiChatStatus.succeeded,
}) => AiChatRun(
  runId: id,
  sessionId: session,
  model: 'deepseek-flash',
  input: input,
  output: output,
  status: status,
  createdAt: DateTime.now(),
);

class _Gateway implements AiChatGateway {
  final sessionIds = <String>[];
  final runsBySession = <String, List<AiChatRun>>{};
  final runs = <String, AiChatRun>{};
  final keys = <String>[];
  final inputs = <String>[];
  final events = <String, StreamController<AiChatEvent>>{};
  Completer<String>? createGate;
  Object? listError;
  Object? createError;
  int listCalls = 0;
  int cancelCalls = 0;

  @override
  Future<List<String>> listSessions() async {
    listCalls++;
    if (listError != null) throw listError!;
    return List.of(sessionIds);
  }

  @override
  Future<List<AiChatRun>> getSessionRuns(String sessionId) async =>
      List.of(runsBySession[sessionId] ?? const []);

  @override
  Future<AiChatRun> getRun(String runId) async => runs[runId]!;

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
    final id = createGate == null
        ? 'run-${keys.length}'
        : await createGate!.future;
    runs[id] = _run(id, sessionId, input: input, status: AiChatStatus.running);
    events[id] = StreamController<AiChatEvent>();
    return id;
  }

  void finishLatest(String output) {
    final id = 'run-${keys.length}';
    final old = runs[id]!;
    runs[id] = AiChatRun(
      runId: old.runId,
      sessionId: old.sessionId,
      model: old.model,
      input: old.input,
      status: AiChatStatus.succeeded,
      output: output,
      createdAt: old.createdAt,
    );
    events[id]!.add(const AiChatEvent(id: 1, type: 'end', data: {}));
  }

  @override
  Future<AiChatStatus> cancelRun(String runId) async {
    cancelCalls++;
    final old = runs[runId]!;
    runs[runId] = AiChatRun(
      runId: old.runId,
      sessionId: old.sessionId,
      model: old.model,
      input: old.input,
      status: AiChatStatus.cancelled,
      output: old.output,
      createdAt: old.createdAt,
    );
    return AiChatStatus.cancelled;
  }

  @override
  Future<void> deleteSession(String sessionId) async =>
      sessionIds.remove(sessionId);

  @override
  Stream<AiChatEvent> watchRun(String runId, {int? lastEventId}) =>
      events.putIfAbsent(runId, StreamController<AiChatEvent>.new).stream;

  @override
  void invalidate() {}

  @override
  void dispose() {
    for (final event in events.values) {
      if (!event.isClosed) unawaited(event.close());
    }
  }
}
