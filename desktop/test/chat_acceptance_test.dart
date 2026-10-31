import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

import 'fixtures/chat_widget_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('阶段 6 页面矩阵下输入、历史导航、关闭和布局保持可用', (tester) async {
    const dimensions = [(1280, 800), (1440, 900), (640, 480)];
    const deviceScales = [1.0, 1.25, 1.5, 2.0];
    const textScales = [1.0, 1.6, 2.0];

    for (final (width, height) in dimensions) {
      for (final deviceScale in deviceScales) {
        for (final textScale in textScales) {
          final gateway = ChatWidgetGateway()..arrangeHistory(2);
          final chat = ChatViewModel(gateway: gateway);
          final repository = DemoNotebookRepository();
          final notebook = NotebookViewModel(repository)
            ..changeTab(NotebookTab.ai);
          var closed = false;
          tester.view.devicePixelRatio = deviceScale;
          tester.view.physicalSize = Size(
            width * deviceScale,
            height * deviceScale,
          );
          await tester.pumpWidget(
            _acceptanceApp(
              notebook: notebook,
              chat: chat,
              textScale: textScale,
              onClose: () => closed = true,
            ),
          );
          await tester.pump();
          await tester.pump(const Duration(milliseconds: 20));

          final input = find.byKey(const ValueKey('chat-draft-input'));
          expect(
            input,
            findsOneWidget,
            reason: '$width×$height DPR $deviceScale text $textScale',
          );
          expect(find.byTooltip('合上日程本'), findsOneWidget);
          expect(find.text('AI 对话'), findsWidgets);
          expect(
            tester.takeException(),
            isNull,
            reason: '$width×$height DPR $deviceScale text $textScale',
          );

          await tester.enterText(input, '矩阵内发送可达');
          await tester.pump();
          await tester.tap(find.byTooltip('发送'));
          await tester.pump();
          expect(gateway.createCalls, 1);
          gateway.finish(
            gateway.createdRunIds.single,
            status: ChatRunStatus.succeeded,
            output: '矩阵回复完成',
          );
          await tester.pump();
          await tester.pump(const Duration(milliseconds: 20));

          if (width < 840 || textScale >= 1.6) {
            expect(find.widgetWithText(TextButton, '历史会话'), findsOneWidget);
            await tester.tap(find.widgetWithText(TextButton, '历史会话'));
            await tester.pump();
            expect(
              tester.takeException(),
              isNull,
              reason: '历史导航 $width×$height DPR $deviceScale text $textScale',
            );
            expect(find.widgetWithText(TextButton, '返回对话'), findsOneWidget);
            if (width == 640 &&
                height == 480 &&
                deviceScale == 1 &&
                textScale == 2) {
              final search = _findHint('搜索名称或摘要');
              await tester.ensureVisible(search);
              await tester.enterText(search, '项目计划 1');
              await tester.pump();
              expect(find.textContaining('项目计划 1：长内容与表格验收'), findsOneWidget);
            }
            await tester.tap(find.widgetWithText(TextButton, '返回对话'));
            await tester.pump();
            expect(
              tester.takeException(),
              isNull,
              reason: '返回对话 $width×$height DPR $deviceScale text $textScale',
            );
            expect(
              find.byKey(const ValueKey('chat-draft-input')),
              findsOneWidget,
            );
          } else {
            expect(find.text('历史会话'), findsOneWidget);
            expect(_findHint('搜索名称或摘要'), findsOneWidget);
          }

          await tester.tap(find.byTooltip('合上日程本'));
          await tester.pump();
          expect(closed, isTrue);
          expect(tester.takeException(), isNull);

          await tester.pumpWidget(const SizedBox.shrink());
          chat.dispose();
          notebook.dispose();
          repository.dispose();
          await gateway.dispose();
        }
      }
    }

    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  testWidgets('长历史、Thinking 隐私、多工具同名调用和正式原文可综合复跑', (tester) async {
    final gateway = ChatWidgetGateway()..arrangeHistory(3);
    expect(gateway.longChatMarkdown.length, greaterThanOrEqualTo(20000));
    final chat = ChatViewModel(gateway: gateway);
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    await tester.pumpWidget(
      _acceptanceApp(notebook: notebook, chat: chat, textScale: 1),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));

    final sessionId = chat.selectedSessionId!;
    chat.updateDraft(sessionId, '请整理混合 Markdown');
    await chat.send(sessionId, model: 'acceptance-model');
    await tester.pump();
    final runId = gateway.createdRunIds.single;
    await tester.pump(const Duration(milliseconds: 20));

    gateway.emitThinking(runId, 1, '私密思考原文不得显示');
    await _pumpUntil(
      tester,
      () =>
          chat.selectedConversation!.activeRun!.phase == ChatRunPhase.thinking,
    );
    expect(chat.selectedConversation!.activeRun!.phase, ChatRunPhase.thinking);
    expect(find.text('私密思考原文不得显示'), findsNothing);

    gateway.emitTool(runId, 2, 'tool-a', 'search_schedules');
    gateway.emitTool(runId, 3, 'tool-b', 'search_schedules');
    gateway.emitTool(runId, 4, 'tool-c', 'get_calendar');
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.toolCalls.length == 3,
    );
    expect(
      chat.selectedConversation!.activeRun!.phase,
      ChatRunPhase.toolCalling,
    );
    expect(chat.selectedConversation!.activeRun!.toolCalls, hasLength(3));
    expect(
      chat.selectedConversation!.activeRun!.toolCalls.where(
        (call) => call.name == 'search_schedules',
      ),
      hasLength(2),
    );
    expect(find.byKey(const ValueKey('chat-tool-2:tool-a')), findsOneWidget);
    expect(find.byKey(const ValueKey('chat-tool-3:tool-b')), findsOneWidget);
    expect(find.byKey(const ValueKey('chat-tool-4:tool-c')), findsOneWidget);

    final source = gateway.longChatMarkdown;
    gateway.emitText(runId, 5, source);
    await _pumpUntil(
      tester,
      () => chat.selectedConversation!.activeRun!.rawText == source,
    );
    gateway.finish(runId, status: ChatRunStatus.succeeded, output: source);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 30));
    final finalRun = chat.selectedConversation!.runs.last;
    expect(finalRun.rawText, source);
    expect(finalRun.rawText.length, greaterThanOrEqualTo(20000));
    expect(find.textContaining('私密思考原文不得显示'), findsNothing);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox.shrink());
    chat.dispose();
    notebook.dispose();
    repository.dispose();
    await gateway.dispose();
  });

  testWidgets('640×480 大字下长正文和长工具列表可滚动访问且可停止', (tester) async {
    final gateway = ChatWidgetGateway()..arrangeHistory(1);
    final chat = ChatViewModel(gateway: gateway);
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    tester.view.physicalSize = const Size(640, 480);
    tester.view.devicePixelRatio = 1;
    await tester.pumpWidget(
      _acceptanceApp(notebook: notebook, chat: chat, textScale: 2),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));

    final sessionId = chat.selectedSessionId!;
    chat.updateDraft(sessionId, '窄屏长内容与工具访问验收');
    await chat.send(sessionId, model: 'acceptance-model');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    final runId = gateway.createdRunIds.single;
    gateway.emitText(runId, 1, gateway.longChatMarkdown);
    for (var index = 0; index < 24; index++) {
      gateway.emitTool(
        runId,
        index + 2,
        'narrow-tool-$index',
        'long_calendar_tool_name_for_accessibility_$index',
      );
    }
    await _pumpUntil(
      tester,
      () =>
          chat.selectedConversation!.activeRun!.toolCalls.length == 24 &&
          chat.selectedConversation!.activeRun!.rawText ==
              gateway.longChatMarkdown,
    );
    expect(chat.selectedConversation!.activeRun!.toolCalls, hasLength(24));

    final finalTool = find.byKey(const ValueKey('chat-tool-25:narrow-tool-23'));
    await tester.ensureVisible(finalTool);
    await tester.pump();
    expect(finalTool, findsOneWidget);
    final messageViewport = tester.getRect(
      find.byKey(ValueKey('chat-message-list-$sessionId')),
    );
    final toolRect = tester.getRect(finalTool);
    expect(toolRect.bottom, greaterThan(messageViewport.top));
    expect(toolRect.top, lessThan(messageViewport.bottom));
    final finalToolStatus = find.descendant(
      of: finalTool,
      matching: find.text('已发起'),
    );
    await tester.ensureVisible(finalToolStatus);
    await tester.pump();
    final statusRect = tester.getRect(finalToolStatus);
    expect(statusRect.bottom, greaterThan(messageViewport.top));
    expect(statusRect.top, lessThan(messageViewport.bottom));
    expect(find.byTooltip('停止生成'), findsOneWidget);
    await tester.tap(find.byTooltip('停止生成'));
    await tester.pump();
    expect(chat.selectedConversation!.activeRun!.phase, ChatRunPhase.stopping);
    gateway.finish(runId, status: ChatRunStatus.cancelled);
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
}

Finder _findHint(String hint) => find.byWidgetPredicate(
  (widget) => widget is TextField && widget.decoration?.hintText == hint,
);

Future<void> _pumpUntil(WidgetTester tester, bool Function() condition) async {
  for (var attempt = 0; attempt < 30 && !condition(); attempt++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
  expect(condition(), isTrue, reason: '异步聊天事件未在验收时限内处理');
}

Widget _acceptanceApp({
  required NotebookViewModel notebook,
  required ChatViewModel chat,
  required double textScale,
  VoidCallback? onClose,
}) => MaterialApp(
  theme: NotebookTheme.data,
  builder: (context, child) => MediaQuery(
    data: MediaQuery.of(
      context,
    ).copyWith(textScaler: TextScaler.linear(textScale)),
    child: child!,
  ),
  home: Scaffold(
    body: Center(
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
                onClose: onClose ?? () {},
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
);
