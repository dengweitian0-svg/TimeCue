import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

import '../test/fixtures/chat_widget_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('生成阶段 6 聊天状态截图到 build/visuals/chat-phase6', (tester) async {
    final gateway = ChatWidgetGateway()..arrangeHistory(5);
    final chat = ChatViewModel(
      gateway: gateway,
      retryDelay: (_) => const Duration(seconds: 3),
    );
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;

    await tester.runAsync(() async {
      for (final entry in {
        'Microsoft YaHei': 'C:/Windows/Fonts/msyh.ttc',
        'Roboto': 'C:/Windows/Fonts/msyh.ttc',
        'Segoe UI': 'C:/Windows/Fonts/segoeui.ttf',
        'SimSun': 'C:/Windows/Fonts/simsun.ttc',
      }.entries) {
        final bytes = await File(entry.value).readAsBytes();
        await (FontLoader(
          entry.key,
        )..addFont(Future.value(ByteData.sublistView(bytes)))).load();
      }
      await (FontLoader(
        'MaterialIcons',
      )..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
      await (FontLoader('packages/gpt_markdown/JetBrainsMono')..addFont(
            rootBundle.load(
              'packages/gpt_markdown/lib/fonts/JetBrainsMono-Regular.ttf',
            ),
          ))
          .load();
    });

    Future<void> show({double textScale = 1}) async {
      await tester.pumpWidget(
        _visualApp(notebook: notebook, chat: chat, textScale: textScale),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 20));
    }

    await show();
    await _capture(tester, '01-welcome.png');
    await chat.loadHistory();
    chat.selectSession('acceptance-history-1');
    await show();
    await _capture(tester, '02-double-page.png');

    notebook.showChatHistory();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    await tester.enterText(_findHint('搜索名称或摘要'), '项目计划 1');
    await tester.pump();
    await _capture(tester, '03-history-search.png');

    notebook.returnToChat();
    tester.view.physicalSize = const Size(640, 480);
    await show();
    await _capture(tester, '04-single-column.png');
    await tester.tap(find.widgetWithText(TextButton, '历史会话'));
    await tester.pump();
    await _capture(tester, '05-single-column-history.png');
    await tester.tap(find.widgetWithText(TextButton, '返回对话'));
    await tester.pump();

    chat.createLocalSession();
    await tester.pump();
    final sessionId = chat.selectedSessionId!;
    chat.updateDraft(sessionId, '检查过程状态与工具调用');
    await chat.send(sessionId, model: 'acceptance-model');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    var runId = gateway.createdRunIds.last;
    gateway.emitThinking(runId, 1, '仅供内部处理的 Thinking 原文');
    await _pumpUntil(
      tester,
      () =>
          chat.selectedConversation!.activeRun!.phase == ChatRunPhase.thinking,
    );
    await _capture(tester, '06-thinking.png');

    for (var index = 0; index < 3; index++) {
      gateway.emitTool(
        runId,
        index + 2,
        'tool-call-$index',
        index.isEven ? 'search_schedules' : 'calendar_tool_with_a_long_name',
      );
    }
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.toolCalls.length == 3,
    );
    await _scrollToLatest(tester, sessionId);
    await _capture(tester, '07-multi-tools-initial.png');
    for (var index = 3; index < 18; index++) {
      gateway.emitTool(
        runId,
        index + 2,
        'tool-call-$index',
        index.isEven ? 'search_schedules' : 'calendar_tool_with_a_long_name',
      );
    }
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.toolCalls.length == 18,
    );
    await _scrollToLatest(tester, sessionId);
    final lastTool = find.byKey(const ValueKey('chat-tool-19:tool-call-17'));
    if (lastTool.evaluate().isEmpty) {
      await tester.tap(find.text('Tool calling · 已发起 18 次调用'));
      await tester.pump(const Duration(milliseconds: 250));
    }
    expect(lastTool, findsOneWidget);
    await tester.ensureVisible(lastTool);
    await _capture(tester, '08-multi-tools-sequential.png');

    final markdown = gateway.longChatMarkdown;
    gateway.emitText(runId, 20, markdown);
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.rawText == markdown,
    );
    await _scrollToLatest(tester, sessionId);
    final tableDate = find.text('2026-10-18', findRichText: true);
    expect(tableDate, findsWidgets);
    await tester.ensureVisible(tableDate.first);
    await tester.pump();
    await _capture(tester, '09-table-content.png');
    await tester.ensureVisible(find.byTooltip('Copy code').first);
    await tester.pump();
    await _capture(tester, '10-code-block.png');
    gateway.finish(runId, status: ChatRunStatus.succeeded, output: markdown);
    await _flushEventTasks(tester);
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.runs.any(
        (run) => run.runId == runId && run.phase == ChatRunPhase.succeeded,
      ),
    );

    chat.updateDraft(sessionId, '检查取消中的操作');
    await chat.send(sessionId, model: 'acceptance-model');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    runId = gateway.createdRunIds.last;
    await tester.tap(find.byTooltip('停止生成'));
    await _pumpUntil(
      tester,
      () =>
          gateway.cancelCalls > 0 &&
          chat.selectedConversation!.activeRun!.phase == ChatRunPhase.stopping,
    );
    expect(find.byTooltip('正在停止'), findsOneWidget);
    await _capture(tester, '11-stopping.png');
    gateway.finish(runId, status: ChatRunStatus.cancelled);
    await _flushEventTasks(tester);
    final stoppedRun = chat.selectedConversation!.runs.singleWhere(
      (run) => run.runId == runId,
    );
    for (
      var attempt = 0;
      attempt < 30 && stoppedRun.phase != ChatRunPhase.cancelled;
      attempt++
    ) {
      await tester.pump(const Duration(milliseconds: 20));
    }
    expect(
      stoppedRun.phase,
      ChatRunPhase.cancelled,
      reason:
          'status=${stoppedRun.status}, event=${stoppedRun.lastEventId}, '
          'error=${stoppedRun.error}, fixture=${(await gateway.getRun(runId)).status}',
    );

    chat.updateDraft(sessionId, '检查断线重连');
    await chat.send(sessionId, model: 'acceptance-model');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    runId = gateway.createdRunIds.last;
    gateway.disconnect(runId);
    await tester.runAsync(() => Future<void>.delayed(Duration.zero));
    await tester.pump(const Duration(milliseconds: 20));
    await _pumpUntil(
      tester,
      () =>
          chat.selectedConversation!.activeRun!.phase ==
          ChatRunPhase.reconnecting,
    );
    expect(find.textContaining('正在重连'), findsWidgets);
    await _capture(tester, '12-reconnecting.png');
    await chat.retryConnection(sessionId);
    await tester.runAsync(() => Future<void>.delayed(Duration.zero));
    await tester.pump(const Duration(milliseconds: 20));
    gateway.finish(
      runId,
      status: ChatRunStatus.failed,
      error: const ChatProtocolError(
        code: 'acceptance_failure',
        message: '阶段六验收失败样例',
      ),
    );
    await _flushEventTasks(tester);
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.runs.any(
        (run) => run.runId == runId && run.phase == ChatRunPhase.failed,
      ),
    );
    await _scrollToLatest(tester, sessionId);
    await tester.ensureVisible(find.textContaining('阶段六验收失败样例'));
    await tester.pump();
    expect(find.text('回复失败'), findsWidgets);
    expect(find.textContaining('阶段六验收失败样例'), findsOneWidget);
    await _capture(tester, '13-failed.png');

    await tester.pumpWidget(const SizedBox.shrink());
    chat.dispose();
    notebook.dispose();
    repository.dispose();
    await gateway.dispose();
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
}

Widget _visualApp({
  required NotebookViewModel notebook,
  required ChatViewModel chat,
  required double textScale,
}) => MaterialApp(
  theme: NotebookTheme.data,
  builder: (context, child) => MediaQuery(
    data: MediaQuery.of(
      context,
    ).copyWith(textScaler: TextScaler.linear(textScale)),
    child: child!,
  ),
  home: Scaffold(
    body: RepaintBoundary(
      key: const ValueKey('chat-visual-boundary'),
      child: Center(
        child: Builder(
          builder: (context) {
            final size = MediaQuery.sizeOf(context);
            final width = (size.width - 64).clamp(260.0, 1120.0).toDouble();
            final height = (size.height - 108).clamp(160.0, 720.0).toDouble();
            return SizedBox(
              width: width,
              height: height,
              child: ListenableBuilder(
                listenable: notebook,
                builder: (context, _) => NotebookFrame(
                  model: notebook,
                  onClose: () {},
                  onManage: () {},
                  onTab: notebook.changeTab,
                  spread: ChatSpread(model: notebook, chat: chat),
                ),
              ),
            );
          },
        ),
      ),
    ),
  ),
);

Future<void> _capture(WidgetTester tester, String filename) async {
  await tester.runAsync(() async {
    final boundary = tester.renderObject<RenderRepaintBoundary>(
      find.byKey(const ValueKey('chat-visual-boundary')),
    );
    final image = await boundary.toImage(pixelRatio: 1);
    final data = await image.toByteData(format: ui.ImageByteFormat.png);
    image.dispose();
    if (data == null) throw StateError('截图编码失败：$filename');
    final file = File('build/visuals/chat-phase6/$filename');
    await file.parent.create(recursive: true);
    await file.writeAsBytes(data.buffer.asUint8List());
  });
}

Finder _findHint(String hint) => find.byWidgetPredicate(
  (widget) => widget is TextField && widget.decoration?.hintText == hint,
);

Future<void> _pumpUntil(WidgetTester tester, bool Function() condition) async {
  for (var attempt = 0; attempt < 30 && !condition(); attempt++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
  expect(condition(), isTrue, reason: '异步聊天事件未在截图时限内处理');
  await tester.pump(const Duration(milliseconds: 20));
  await tester.pump();
}

Future<void> _flushEventTasks(WidgetTester tester) async {
  await tester.pump();
  await tester.pump();
  await tester.pump();
  await tester.runAsync(() async {
    await Future<void>.delayed(Duration.zero);
  });
  await tester.pump(const Duration(milliseconds: 20));
  await tester.pump(const Duration(milliseconds: 50));
}

Future<void> _scrollToLatest(WidgetTester tester, String sessionId) async {
  final scrollable = find
      .descendant(
        of: find.byKey(ValueKey('chat-message-list-$sessionId')),
        matching: find.byType(Scrollable),
      )
      .first;
  for (var attempt = 0; attempt < 5; attempt++) {
    final state = tester.state<ScrollableState>(scrollable);
    if (state.position.hasContentDimensions) {
      state.position.jumpTo(state.position.maxScrollExtent);
    }
    await tester.pump(const Duration(milliseconds: 20));
  }
}
