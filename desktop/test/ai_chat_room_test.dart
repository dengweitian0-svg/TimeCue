import 'dart:async';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/contracts/ai_chat.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/ai_chat/ai_chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/room/cat_chat_hotspot.dart';
import 'package:timecue_desktop/features/room/room_screen.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';

void main() {
  Future<void> mount(
    WidgetTester tester,
    DemoNotebookRepository repository,
    _FakeAiChatGateway gateway, {
    Size size = const Size(1280, 800),
    bool ownsChatGateway = true,
  }) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      TimeCueApp(
        repository: repository,
        chatGateway: gateway,
        ownsChatGateway: ownsChatGateway,
      ),
    );
    await tester.pump();
  }

  testWidgets('猫咪入口聚焦显示可换行提示，Enter 打开 AI 对话页', (tester) async {
    tester.binding.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(
      tester.binding.platformDispatcher.clearTextScaleFactorTestValue,
    );
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway);
    final hotspot = tester.widget<CatChatHotspot>(find.byType(CatChatHotspot));
    final mouse = await tester.createGesture(kind: ui.PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(tester.getCenter(find.byType(CatChatHotspot)));
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('和我聊聊'), findsOneWidget);
    expect(find.text('点击猫咪，打开 AI 对话'), findsOneWidget);

    await mouse.removePointer();
    hotspot.focusNode.requestFocus();
    await tester.pump();
    expect(hotspot.focusNode.hasFocus, isTrue);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pumpAndSettle();
    expect(find.text('和我聊聊'), findsNothing);
    expect(find.byType(AiChatSpread), findsOneWidget);
    expect(find.text('历史会话'), findsOneWidget);
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    hotspot.focusNode.unfocus();
    await tester.pump();
    await tester.tap(find.byType(CatChatHotspot));
    await tester.pumpAndSettle();
    expect(find.byType(AiChatSpread), findsOneWidget);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox());
    repository.dispose();
    expect(gateway.disposed, isTrue);
  });

  testWidgets('第四标签可进入 AI，再切回原有本子页面', (tester) async {
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway);
    await tester.tap(find.byType(RoomActions));
    await tester.pumpAndSettle();
    expect(find.byType(TodaySpread), findsOneWidget);

    await tester.tap(find.text('AI 对话'));
    await tester.pumpAndSettle();
    expect(find.byType(AiChatSpread), findsOneWidget);
    expect(find.text('07 — 08'), findsOneWidget);

    await tester.tap(find.widgetWithText(TextButton, '今天'));
    await tester.pumpAndSettle();
    expect(find.byType(TodaySpread), findsOneWidget);
    expect(find.byType(AiChatSpread), findsNothing);

    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });

  testWidgets('切换标签仍守卫未保存的日程草稿', (tester) async {
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway);
    await tester.tap(find.byType(RoomActions));
    await tester.pumpAndSettle();
    await tester.tap(find.text('日历'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('7').first);
    await tester.pumpAndSettle();
    await tester.tap(find.text('喝水，伸个懒腰'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditor), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, '修改中的日程草稿');
    await tester.tap(find.text('AI 对话'));
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('继续编辑'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditor), findsOneWidget);

    await tester.tap(find.text('AI 对话'));
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('舍弃修改'));
    await tester.pumpAndSettle();
    expect(find.byType(AiChatSpread), findsOneWidget);

    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });

  testWidgets('合上本子后 AI 运行继续且聊天草稿保留', (tester) async {
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway);
    await tester.tap(find.byType(CatChatHotspot));
    await tester.pumpAndSettle();

    final composer = find.byType(TextField).last;
    await tester.enterText(composer, '请帮我安排阅读时间');
    await tester.pump();
    final model = tester.widget<AiChatSpread>(find.byType(AiChatSpread)).model;
    expect(model.sendDisabledReasonFor(model.selectedSessionId!), isNull);
    final sendButton = find.ancestor(
      of: find.byIcon(Icons.send_rounded),
      matching: find.byType(IconButton),
    );
    expect(tester.widget<IconButton>(sendButton).onPressed, isNotNull);
    await tester.tap(find.byIcon(Icons.send_rounded));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));
    expect(gateway.createCalls, 1);
    final runId = gateway.createdRunIds.single;
    expect(gateway.events[runId]?.hasListener, isTrue);

    await tester.enterText(find.byType(TextField).last, '稍后继续问的草稿');
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    expect(gateway.events[runId]?.hasListener, isTrue);
    gateway.addText(runId, 1, '运行仍在继续');

    await tester.tap(find.byType(CatChatHotspot));
    await tester.pumpAndSettle();
    final reopenedModel = tester
        .widget<AiChatSpread>(find.byType(AiChatSpread))
        .model;
    expect(reopenedModel.selectedSession!.turns.last.text, '运行仍在继续');
    expect(
      tester.widget<TextField>(find.byType(TextField).last).controller!.text,
      '稍后继续问的草稿',
    );
    expect(gateway.cancelCalls, 0);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox());
    await tester.pump();
    expect(gateway.invalidateCalls, 1);
    expect(gateway.disposeCalls, 1);
    expect(gateway.events[runId]!.hasListener, isFalse);
    repository.dispose();
  });

  testWidgets('猫咪完全裁切时出现固定 AI 入口', (tester) async {
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway, size: const Size(150, 800));
    expect(find.byType(CatChatHotspot), findsNothing);
    expect(find.byKey(const ValueKey('ai-chat-fallback')), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('ai-chat-fallback')));
    await tester.pumpAndSettle();
    expect(find.byType(AiChatSpread), findsOneWidget);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });

  testWidgets('借用聊天网关时释放宿主只停止宿主模型', (tester) async {
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway, ownsChatGateway: false);
    final independentEvents = <AiChatEvent>[];
    gateway.watchRun('independent-run').listen(independentEvents.add);

    await tester.pumpWidget(const SizedBox());
    await tester.pump();
    expect(gateway.invalidateCalls, 0);
    expect(gateway.disposeCalls, 0);
    var sessionsLoaded = false;
    List<String>? sessions;
    gateway.listSessions().then((value) {
      sessions = value;
      sessionsLoaded = true;
    });
    await tester.pump();
    expect(sessionsLoaded, isTrue);
    expect(sessions, isEmpty);
    gateway.addText('independent-run', 1, '共享网关仍可用');
    await tester.pump();
    expect(independentEvents, hasLength(1));
    expect(independentEvents.single.data['content'], '共享网关仍可用');
    var streamClosed = false;
    gateway.events['independent-run']!.close().then((_) {
      streamClosed = true;
    });
    for (var pump = 0; pump < 5 && !streamClosed; pump++) {
      await tester.pump();
    }
    expect(streamClosed, isTrue);

    repository.dispose();
  }, timeout: const Timeout(Duration(seconds: 15)));

  testWidgets('备用 AI 入口打开后放大窗口，关闭时焦点回到新挂载的猫咪', (tester) async {
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway, size: const Size(150, 800));
    await tester.tap(find.byKey(const ValueKey('ai-chat-fallback')));
    await tester.pumpAndSettle();

    tester.view.physicalSize = const Size(1280, 800);
    await tester.pumpAndSettle();
    final catFocus = tester
        .widget<CatChatHotspot>(find.byType(CatChatHotspot))
        .focusNode;
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    expect(catFocus.context, isNotNull);
    expect(catFocus.hasFocus, isTrue);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });

  testWidgets('猫咪打开后缩窄窗口，关闭时焦点回到备用入口并可用 Enter 重开', (tester) async {
    final repository = DemoNotebookRepository();
    final gateway = _FakeAiChatGateway();
    await mount(tester, repository, gateway);
    await tester.tap(find.byType(CatChatHotspot));
    await tester.pumpAndSettle();

    tester.view.physicalSize = const Size(150, 800);
    await tester.pumpAndSettle();
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    final fallback = tester.widget<FilledButton>(
      find.byKey(const ValueKey('ai-chat-fallback')),
    );
    final fallbackFocus = fallback.focusNode!;
    expect(fallbackFocus.context, isNotNull);
    expect(fallbackFocus.hasFocus, isTrue);

    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pumpAndSettle();
    expect(find.byType(AiChatSpread), findsOneWidget);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });

  testWidgets('可选：生成 AI 对话与猫咪提示视觉截图', (tester) async {
    if (!const bool.fromEnvironment('TIMECUE_CHAT_CAPTURE')) return;
    if (!Platform.isWindows) {
      throw StateError('AI 对话视觉截图需要 Windows 字体。');
    }
    tester.view.physicalSize = const Size(1440, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.runAsync(() async {
      for (final entry in {
        'Microsoft YaHei': 'C:/Windows/Fonts/msyh.ttc',
        'SimSun': 'C:/Windows/Fonts/simsun.ttc',
        'Georgia': 'C:/Windows/Fonts/georgia.ttf',
      }.entries) {
        final bytes = await File(entry.value).readAsBytes();
        await (FontLoader(
          entry.key,
        )..addFont(Future.value(ByteData.sublistView(bytes)))).load();
      }
      await (FontLoader(
        'MaterialIcons',
      )..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
    });

    const sessionId = '550e8400-e29b-41d4-a716-446655440000';
    final gateway = _FakeAiChatGateway()
      ..sessionIds.add(sessionId)
      ..sessions[sessionId] = [
        AiChatRun(
          runId: 'd9428888-122b-4f6f-9b47-5ea7b2915210',
          sessionId: sessionId,
          model: 'deepseek-flash',
          input: '明天下午想读书，帮我留半小时。',
          status: AiChatStatus.succeeded,
          createdAt: DateTime.utc(2026, 10, 15, 2, 41),
          output: '可以把 15:00 到 15:30 留给阅读。',
          finishedAt: DateTime.utc(2026, 10, 15, 2, 42),
        ),
        AiChatRun(
          runId: 'd9428888-122b-4f6f-9b47-5ea7b2915211',
          sessionId: sessionId,
          model: 'deepseek-flash',
          input: '读完以后提醒我记一句喜欢的话。',
          status: AiChatStatus.succeeded,
          createdAt: DateTime.utc(2026, 10, 15, 2, 43),
          output: '好，读完后记下一句喜欢的话。',
          finishedAt: DateTime.utc(2026, 10, 15, 2, 44),
        ),
      ];
    for (final run in gateway.sessions[sessionId]!) {
      gateway.runs[run.runId] = run;
    }
    final repository = DemoNotebookRepository();
    final boundaryKey = GlobalKey();
    await tester.pumpWidget(
      RepaintBoundary(
        key: boundaryKey,
        child: TimeCueApp(
          repository: repository,
          chatGateway: gateway,
          ownsChatGateway: true,
        ),
      ),
    );
    await tester.tap(find.byType(CatChatHotspot));
    await tester.pumpAndSettle();
    await _capture(tester, boundaryKey, '12-ai-chat');

    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    final mouse = await tester.createGesture(kind: ui.PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(tester.getCenter(find.byType(CatChatHotspot)));
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('和我聊聊'), findsOneWidget);
    await _capture(tester, boundaryKey, '13-cat-chat');
    await mouse.removePointer();

    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });
}

Future<void> _capture(
  WidgetTester tester,
  GlobalKey boundaryKey,
  String name,
) async {
  await tester.runAsync(() async {
    await Future<void>.delayed(const Duration(milliseconds: 150));
  });
  await tester.pumpAndSettle();
  await tester.runAsync(() async {
    final boundary =
        boundaryKey.currentContext!.findRenderObject() as RenderRepaintBoundary;
    final image = await boundary.toImage();
    final data = await image.toByteData(format: ui.ImageByteFormat.png);
    await Directory('build/visuals').create(recursive: true);
    await File(
      'build/visuals/$name.png',
    ).writeAsBytes(data!.buffer.asUint8List());
    image.dispose();
  });
}

class _FakeAiChatGateway implements AiChatGateway {
  final List<String> sessionIds = [];
  final Map<String, List<AiChatRun>> sessions = {};
  final Map<String, AiChatRun> runs = {};
  final Map<String, StreamController<AiChatEvent>> events = {};
  final List<String> createdRunIds = [];
  int createCalls = 0;
  int cancelCalls = 0;
  int invalidateCalls = 0;
  int disposeCalls = 0;
  bool disposed = false;
  bool invalidated = false;

  void _ensureAvailable() {
    if (invalidated || disposed) throw StateError('gateway unavailable');
  }

  @override
  Future<List<String>> listSessions() async {
    _ensureAvailable();
    return List.of(sessionIds);
  }

  @override
  Future<List<AiChatRun>> getSessionRuns(String sessionId) async {
    _ensureAvailable();
    return List.of(sessions[sessionId] ?? const []);
  }

  @override
  Future<AiChatRun> getRun(String runId) async {
    _ensureAvailable();
    return runs[runId]!;
  }

  @override
  Future<String> createRun(
    String sessionId,
    String input, {
    required String idempotencyKey,
    String model = 'deepseek-flash',
  }) async {
    _ensureAvailable();
    createCalls++;
    final runId = 'run-$createCalls';
    createdRunIds.add(runId);
    final run = AiChatRun(
      runId: runId,
      sessionId: sessionId,
      model: model,
      input: input,
      status: AiChatStatus.running,
      createdAt: DateTime.utc(2026, 10, 15, 7),
    );
    runs[runId] = run;
    sessions.putIfAbsent(sessionId, () => []).add(run);
    return runId;
  }

  @override
  Future<AiChatStatus> cancelRun(String runId) async {
    _ensureAvailable();
    cancelCalls++;
    return AiChatStatus.running;
  }

  @override
  Future<void> deleteSession(String sessionId) async {
    _ensureAvailable();
    sessionIds.remove(sessionId);
    sessions.remove(sessionId);
  }

  @override
  Stream<AiChatEvent> watchRun(String runId, {int? lastEventId}) =>
      _watchRun(runId);

  Stream<AiChatEvent> _watchRun(String runId) {
    _ensureAvailable();
    return events
        .putIfAbsent(runId, StreamController<AiChatEvent>.broadcast)
        .stream;
  }

  void addText(String runId, int id, String content) {
    events[runId]!.add(
      AiChatEvent(id: id, type: 'text', data: {'content': content}),
    );
  }

  @override
  void invalidate() {
    if (invalidated || disposed) return;
    invalidateCalls++;
    invalidated = true;
    for (final event in events.values) {
      if (!event.isClosed) unawaited(event.close());
    }
  }

  @override
  void dispose() {
    if (disposed) return;
    disposeCalls++;
    disposed = true;
    for (final event in events.values) {
      if (!event.isClosed) unawaited(event.close());
    }
  }
}
