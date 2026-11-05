import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/features/chat/chat_markdown_body.dart';
import 'package:timecue_desktop/features/chat/chat_message_list.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

import 'fixtures/chat_widget_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('思考、正文与工具按事件顺序展示，完成后折叠且尊重手动选择', (tester) async {
    final gateway = ChatWidgetGateway();
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() async {
      chat.dispose();
      await gateway.dispose();
    });
    final sessionId = chat.createLocalSession().sessionId;
    chat.updateDraft(sessionId, '帮我看看今天的安排');
    await chat.send(sessionId, model: 'widget-model');
    await tester.pump();
    final runId = gateway.createdRunIds.single;
    await tester.pumpWidget(_chat(sessionId, chat));
    await tester.pump(const Duration(milliseconds: 20));

    gateway.emitThinking(runId, 1, '先确认可用时间');
    await _pumpUntil(tester, () => _active(chat).segments.length == 1);
    expect(find.text('先确认可用时间'), findsOneWidget);
    expect(_active(chat).segments.single.kind, ChatSegmentKind.reasoning);

    gateway.emitThinking(runId, 2, '，再检查冲突');
    await _pumpUntil(
      tester,
      () => _active(chat).segments.single.content == '先确认可用时间，再检查冲突',
    );

    // 手动折叠优先于后续自动更新。
    await tester.tap(
      find.byKey(const ValueKey('chat-reasoning-toggle-reasoning-1')),
    );
    await tester.pump(const Duration(milliseconds: 50));
    expect(find.text('先确认可用时间，再检查冲突'), findsNothing);
    gateway.emitThinking(runId, 3, '，继续核对');
    await _pumpUntil(
      tester,
      () => _active(chat).segments.single.content == '先确认可用时间，再检查冲突，继续核对',
    );
    expect(find.text('先确认可用时间，再检查冲突，继续核对'), findsNothing);

    gateway.emitTool(
      runId,
      4,
      'lookup',
      'search_schedules',
      args: {'day': 'today'},
    );
    await _pumpUntil(
      tester,
      () => find
          .byKey(const ValueKey('chat-tool-4:lookup'))
          .evaluate()
          .isNotEmpty,
    );
    expect(_active(chat).segments.first.completed, isTrue);

    // 用户重新展开后可以看到完整思考原文。
    await tester.tap(
      find.byKey(const ValueKey('chat-reasoning-toggle-reasoning-1')),
    );
    await tester.pump(const Duration(milliseconds: 50));
    expect(find.text('先确认可用时间，再检查冲突，继续核对'), findsOneWidget);
    expect(find.text('思考过程（已完成）'), findsOneWidget);
    expect(find.text('正在思考…'), findsNothing);

    gateway.emitText(runId, 5, '我查一下');
    gateway.emitTool(
      runId,
      6,
      'write',
      'create_schedule',
      args: {'title': '周会', 'start': null},
    );
    gateway.emitThinking(runId, 7, '已创建');
    gateway.emitText(runId, 8, '已为你安排周会');
    await _pumpUntil(tester, () => _active(chat).segments.length == 6);
    gateway.finish(runId, status: ChatRunStatus.succeeded, output: '已为你安排周会');
    await _pumpUntil(tester, () => _active(chat).isTerminal);
    await tester.pump(const Duration(milliseconds: 60));

    final run = _active(chat);
    expect(
      [for (final segment in run.segments) segment.kind],
      [
        ChatSegmentKind.reasoning,
        ChatSegmentKind.tool,
        ChatSegmentKind.text,
        ChatSegmentKind.tool,
        ChatSegmentKind.reasoning,
        ChatSegmentKind.text,
      ],
    );
    // 终态概况不会覆盖中间正文，也不会重复追加最终答案。
    expect(find.text('我查一下'), findsOneWidget);
    expect(find.text('已为你安排周会'), findsOneWidget);
    expect(find.text('先确认可用时间，再检查冲突，继续核对'), findsOneWidget);
    expect(find.text('已创建'), findsNothing);

    final first = tester.getTopLeft(
      find.byKey(const ValueKey('chat-reasoning-toggle-reasoning-1')),
    );
    final firstTool = tester.getTopLeft(
      find.byKey(const ValueKey('chat-tool-4:lookup')),
    );
    final middle = tester.getTopLeft(
      find.byKey(const ValueKey('chat-text-text-3')),
    );
    final secondTool = tester.getTopLeft(
      find.byKey(const ValueKey('chat-tool-6:write')),
    );
    final secondReasoning = tester.getTopLeft(
      find.byKey(const ValueKey('chat-reasoning-toggle-reasoning-5')),
    );
    final finalText = tester.getTopLeft(
      find.byKey(const ValueKey('chat-text-text-6')),
    );
    expect(first.dy, lessThan(firstTool.dy));
    expect(firstTool.dy, lessThan(middle.dy));
    expect(middle.dy, lessThan(secondTool.dy));
    expect(secondTool.dy, lessThan(secondReasoning.dy));
    expect(secondReasoning.dy, lessThan(finalText.dy));
  });

  testWidgets('新到达正文逐步显示，确认结束时立即补齐全部已收文字', (tester) async {
    final gateway = ChatWidgetGateway();
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() async {
      chat.dispose();
      await gateway.dispose();
    });
    final sessionId = chat.createLocalSession().sessionId;
    chat.updateDraft(sessionId, '说说今天的安排');
    await chat.send(sessionId, model: 'widget-model');
    await tester.pump();
    final runId = gateway.createdRunIds.single;
    await tester.pumpWidget(_chat(sessionId, chat));
    await tester.pump(const Duration(milliseconds: 20));

    gateway.emitText(runId, 1, '开始');
    await _pumpUntil(tester, () => _active(chat).rawText == '开始');
    gateway.emitText(runId, 2, '继续输入更多文字');
    const full = '开始继续输入更多文字';
    await _pumpUntil(tester, () => _active(chat).rawText == full);
    await tester.pump();
    final firstShown = _lastBody(tester).data;
    expect(firstShown.length, lessThan(full.length));
    await tester.pump(const Duration(milliseconds: 20));
    expect(_lastBody(tester).data.length, greaterThan(firstShown.length));

    gateway.finish(runId, status: ChatRunStatus.succeeded, output: full);
    await _pumpUntil(tester, () => _active(chat).isTerminal);
    await tester.pump(const Duration(milliseconds: 20));
    expect(_lastBody(tester).data, full);
    expect(_lastBody(tester).isStreaming, isFalse);
  });

  testWidgets('历史运行直接显示完整正文，不重播打字动画', (tester) async {
    final gateway = ChatWidgetGateway()..arrangeHistory(1);
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() async {
      chat.dispose();
      await gateway.dispose();
    });
    await chat.loadHistory();
    expect(chat.historyConversations, isNotEmpty);
    final conversation = chat.historyConversations.first;
    chat.selectSession(conversation.sessionId);
    await tester.pumpWidget(_chat(conversation.sessionId, chat));
    await tester.pump(const Duration(milliseconds: 20));

    final runs = chat.selectedConversation!.runs;
    expect(runs, isNotEmpty);
    final run = runs.last;
    expect(run.segments, isEmpty);
    expect(run.rawText, isNotEmpty);
    expect(_lastBody(tester).data, run.rawText);
    expect(_lastBody(tester).isStreaming, isFalse);
  });
}

ChatRunState _active(ChatViewModel chat) =>
    chat.selectedConversation!.activeRun ??
    chat.selectedConversation!.runs.last;

ChatMarkdownBody _lastBody(WidgetTester tester) =>
    tester.widget<ChatMarkdownBody>(find.byType(ChatMarkdownBody).last);

Widget _chat(String sessionId, ChatViewModel chat) => MaterialApp(
  theme: NotebookTheme.data,
  home: Scaffold(
    body: ListenableBuilder(
      listenable: chat,
      builder: (context, _) {
        final conversation = chat.selectedConversation;
        return ChatMessageList(
          key: const ValueKey('ordered-chat'),
          sessionId: sessionId,
          runs: conversation?.runs ?? const [],
          initialOffset: 0,
          initiallyFollowLatest: true,
          onReadingPosition: (_, _) {},
          onRetryCreate: (_) {},
          onRetryConnection: (_) {},
          onToggleReasoning: (runId, segmentId, expanded) =>
              chat.setSegmentExpanded(
                runId: runId,
                segmentId: segmentId,
                expanded: expanded,
              ),
        );
      },
    ),
  ),
);

Future<void> _pumpUntil(WidgetTester tester, bool Function() condition) async {
  for (var attempt = 0; attempt < 40 && !condition(); attempt++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
  expect(condition(), isTrue, reason: '事件未在验收时限内应用');
}
