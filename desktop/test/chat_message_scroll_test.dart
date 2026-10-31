import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/chat/chat_message_list.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

import 'fixtures/chat_widget_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('初始跟随最新时追加多轮消息与工具后持续保持在底部', (tester) async {
    final gateway = ChatWidgetGateway()..arrangeHistory(2);
    final chat = ChatViewModel(gateway: gateway);
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    const sessionId = 'acceptance-history-1';
    await chat.loadHistory();
    chat.selectSession(sessionId);
    expect(chat.selectedConversation!.runs, hasLength(3));
    expect(
      chat.selectedConversation!.runs.last.rawText,
      gateway.longChatMarkdown,
    );

    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(1280, 800);
    await tester.pumpWidget(_app(notebook: notebook, chat: chat));
    await _pumpUntil(
      tester,
      () => find
          .byKey(const ValueKey('chat-message-list-$sessionId'))
          .evaluate()
          .isNotEmpty,
    );
    await _pumpFrames(tester, 20);

    final viewport = find.byKey(const ValueKey('chat-message-list-$sessionId'));
    final scroll = _verticalScroll(tester, viewport);
    expect(chat.selectedConversation!.followLatest, isTrue);
    expect(_distanceToLatest(scroll), lessThan(48));

    chat.updateDraft(sessionId, '在长历史之后追加一轮新消息');
    await chat.send(sessionId, model: 'acceptance-model');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    final runId = gateway.createdRunIds.single;
    gateway.emitThinking(runId, 1, '隐藏的 Thinking 原文');
    await _pumpUntil(
      tester,
      () =>
          chat.selectedConversation!.activeRun!.phase == ChatRunPhase.thinking,
    );
    for (var index = 0; index < 3; index++) {
      gateway.emitTool(
        runId,
        index + 2,
        'scroll-tool-$index',
        'tool_name_$index',
      );
    }
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.toolCalls.length == 3,
    );
    await _pumpFrames(tester, 20);

    final displayedRuns = tester
        .widget<ChatMessageList>(
          find.byKey(const ValueKey('chat-message-list-$sessionId')),
        )
        .runs;
    expect(displayedRuns, hasLength(4));
    expect(
      displayedRuns.last.messageId,
      chat.selectedConversation!.activeRun!.messageId,
      reason: '新 run 应按时间排序在历史末尾，测试不能让未来时间的夹具历史倒置',
    );
    expect(
      chat.selectedConversation!.followLatest,
      isTrue,
      reason: '程序恢复滚动时不得把既有跟随意图改成用户上读',
    );
    expect(
      _distanceToLatest(_verticalScroll(tester, viewport)),
      lessThan(48),
      reason: '最新消息应继续保持在实际滚动范围末尾',
    );
    expect(find.text('Tool calling · 已发起 3 次调用'), findsWidgets);
    final lastTool = find.byKey(const ValueKey('chat-tool-4:scroll-tool-2'));
    expect(lastTool, findsOneWidget, reason: '最新多工具消息应自动滚动到视口内，不能停留在旧历史尾部');
    expect(_distanceToLatest(_verticalScroll(tester, viewport)), lessThan(48));

    const firstText = '第一段正式回复';
    gateway.emitText(runId, 5, firstText);
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.rawText == firstText,
    );
    await _pumpFrames(tester, 4);
    expect(_distanceToLatest(_verticalScroll(tester, viewport)), lessThan(48));

    gateway.finish(runId, status: ChatRunStatus.succeeded, output: firstText);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));

    await tester.pumpWidget(const SizedBox.shrink());
    chat.dispose();
    notebook.dispose();
    repository.dispose();
    await gateway.dispose();
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  testWidgets('用户主动上读停止跟随，回到最新后恢复跟随', (tester) async {
    final gateway = ChatWidgetGateway()..arrangeHistory(1);
    final chat = ChatViewModel(gateway: gateway);
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    const sessionId = 'acceptance-history-1';
    await chat.loadHistory();
    chat.selectSession(sessionId);

    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(1280, 800);
    await tester.pumpWidget(_app(notebook: notebook, chat: chat));
    await _pumpUntil(
      tester,
      () => find
          .byKey(const ValueKey('chat-message-list-$sessionId'))
          .evaluate()
          .isNotEmpty,
    );
    await _pumpFrames(tester, 8);

    final viewport = find.byKey(const ValueKey('chat-message-list-$sessionId'));
    final scroll = _verticalScroll(tester, viewport);
    await tester.drag(viewport, const Offset(0, 260));
    await tester.pump();
    expect(chat.selectedConversation!.followLatest, isFalse);
    expect(find.text('回到最新'), findsOneWidget);
    final readingOffset = scroll.position.pixels;

    chat.updateDraft(sessionId, '新增但不抢阅读位置');
    await chat.send(sessionId, model: 'acceptance-model');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    final runId = gateway.createdRunIds.single;
    gateway.emitThinking(runId, 1, 'Thinking');
    await _pumpUntil(
      tester,
      () =>
          chat.selectedConversation!.activeRun!.phase == ChatRunPhase.thinking,
    );
    await _pumpFrames(tester, 3);
    expect(chat.selectedConversation!.followLatest, isFalse);
    expect((scroll.position.pixels - readingOffset).abs(), lessThan(12));

    await tester.tap(find.text('回到最新'));
    await _pumpFrames(tester, 15);
    expect(chat.selectedConversation!.followLatest, isTrue);
    expect(_distanceToLatest(scroll), lessThan(48));

    gateway.emitText(runId, 2, '回到底部后继续到达的正文');
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.rawText == '回到底部后继续到达的正文',
    );
    await _pumpFrames(tester, 4);
    expect(_distanceToLatest(scroll), lessThan(48));
    gateway.finish(
      runId,
      status: ChatRunStatus.succeeded,
      output: '回到底部后继续到达的正文',
    );
    await tester.pump();

    await tester.pumpWidget(const SizedBox.shrink());
    chat.dispose();
    notebook.dispose();
    repository.dispose();
    await gateway.dispose();
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
}

Widget _app({
  required NotebookViewModel notebook,
  required ChatViewModel chat,
}) => MaterialApp(
  theme: NotebookTheme.data,
  home: Scaffold(
    body: Center(
      child: SizedBox(
        width: 1216,
        height: 692,
        child: NotebookFrame(
          model: notebook,
          onClose: () {},
          onManage: () {},
          onTab: notebook.changeTab,
          spread: ChatSpread(model: notebook, chat: chat),
        ),
      ),
    ),
  ),
);

ScrollableState _verticalScroll(WidgetTester tester, Finder viewport) {
  final scrollables = find.descendant(
    of: viewport,
    matching: find.byType(Scrollable),
  );
  for (final element in scrollables.evaluate()) {
    final state = tester.state<ScrollableState>(find.byWidget(element.widget));
    if (state.position.axis == Axis.vertical) return state;
  }
  throw StateError('ChatMessageList 中没有垂直滚动视口');
}

double _distanceToLatest(ScrollableState state) =>
    state.position.maxScrollExtent - state.position.pixels;

Future<void> _pumpFrames(WidgetTester tester, int count) async {
  for (var frame = 0; frame < count; frame++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

Future<void> _pumpUntil(WidgetTester tester, bool Function() condition) async {
  for (var attempt = 0; attempt < 40 && !condition(); attempt++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
  expect(condition(), isTrue, reason: '聊天滚动回归前置状态未及时到达');
}
