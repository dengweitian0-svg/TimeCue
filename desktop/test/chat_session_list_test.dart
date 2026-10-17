import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/chat/chat_session_list.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  testWidgets('历史列表搜索名称、保留完整标题并在删除确认后移除', (tester) async {
    final gateway = _HistoryGateway(
      sessionIds: const ['session-one', 'session-two'],
      names: const {
        'session-one': '一段很长的中文会话名称，完整内容保留在模型和提示中',
        'session-two': '日历安排讨论',
      },
    );
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();
    String? selected;

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 380,
            height: 600,
            child: ChatSessionList(
              chat: chat,
              selectedSessionId: null,
              onSelect: (id) {
                selected = id;
                chat.selectSession(id);
              },
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('今天'), findsWidgets);
    expect(find.text('日历安排讨论'), findsOneWidget);
    expect(find.byTooltip('一段很长的中文会话名称，完整内容保留在模型和提示中'), findsOneWidget);
    await tester.tap(find.byTooltip('刷新历史'));
    await tester.pumpAndSettle();
    expect(gateway.listCalls, 2);

    await tester.enterText(find.byType(TextField).first, '日历');
    await tester.pumpAndSettle();
    expect(find.text('日历安排讨论'), findsOneWidget);
    expect(find.text('一段很长的中文会话名称，完整内容保留在模型和提示中'), findsNothing);
    final tileSemantics = find
        .ancestor(
          of: find.text('日历安排讨论'),
          matching: find.byWidgetPredicate(
            (widget) =>
                widget is Semantics && widget.properties.selected != null,
          ),
        )
        .first;
    expect(
      tester.widget<Semantics>(tileSemantics).properties.selected,
      isFalse,
    );
    await tester.tap(find.text('日历安排讨论'));
    await tester.pump();
    expect(selected, 'session-two');
    expect(tester.widget<Semantics>(tileSemantics).properties.selected, isTrue);

    await tester.enterText(find.byType(TextField).first, '');
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('删除会话').first);
    await tester.pumpAndSettle();
    expect(find.text('删除会话？'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, '删除'));
    await tester.pumpAndSettle();
    expect(chat.historyConversations, hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('新建会话保留上一会话草稿并可从本地草稿列表恢复', (tester) async {
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository);
    final gateway = _HistoryGateway();
    var id = 0;
    final chat = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 'local-${++id}',
    );
    addTearDown(() {
      chat.dispose();
      notebook.dispose();
      repository.dispose();
      gateway.dispose();
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    tester.view.physicalSize = const Size(640, 480);
    tester.view.devicePixelRatio = 1;

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 576,
            height: 372,
            child: ListenableBuilder(
              listenable: notebook,
              builder: (context, _) => ChatSpread(model: notebook, chat: chat),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('chat-draft-input')),
      '第一段还没发送的草稿',
    );
    await tester.tap(find.widgetWithText(TextButton, '历史会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('新会话'));
    await tester.pumpAndSettle();
    expect(chat.conversations, hasLength(2));
    expect(chat.conversations.first.draft, '第一段还没发送的草稿');
    expect(chat.historyConversations, isEmpty);

    await tester.enterText(
      find.byKey(const ValueKey('chat-draft-input')),
      '第二段草稿',
    );
    await tester.tap(find.widgetWithText(TextButton, '历史会话'));
    await tester.pumpAndSettle();
    expect(find.text('第一段还没发送的草稿'), findsOneWidget);
    await tester.tap(find.text('第一段还没发送的草稿'));
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('chat-draft-input')))
          .controller!
          .text,
      '第一段还没发送的草稿',
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('历史读取失败保留重试错误，不显示无会话空状态', (tester) async {
    final gateway = _HistoryGateway(failList: true);
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();
    await tester.pump(const Duration(milliseconds: 20));
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 380,
            height: 600,
            child: ChatSessionList(
              chat: chat,
              selectedSessionId: null,
              onSelect: (_) {},
            ),
          ),
        ),
      ),
    );
    expect(find.textContaining('历史读取失败'), findsOneWidget);
    expect(find.text('还没有历史会话。'), findsNothing);
    expect(find.text('重试'), findsOneWidget);
  });

  testWidgets('索引未完成不报告无结果，完成后才展示无结果提示', (tester) async {
    final gateway = _HistoryGateway()
      ..sessionIdsCompleter = Completer<List<String>>();
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    final loading = chat.loadHistory();
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 380,
            height: 600,
            child: ChatSessionList(
              chat: chat,
              selectedSessionId: null,
              onSelect: (_) {},
            ),
          ),
        ),
      ),
    );
    await tester.enterText(find.byType(TextField).first, '不存在的会话');
    await tester.pump();
    expect(find.text('没有找到相关会话。'), findsNothing);
    expect(find.text('正在整理历史 0/0'), findsOneWidget);
    gateway.sessionIdsCompleter!.complete(const []);
    await loading;
    await tester.pumpAndSettle();
    expect(find.text('没有找到相关会话。'), findsOneWidget);
  });

  testWidgets('删除失败时历史项仍保留并显示内联错误', (tester) async {
    final gateway = _HistoryGateway(
      sessionIds: const ['session-keep'],
      names: const {'session-keep': '保留会话'},
    )..failDelete = true;
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();
    await tester.pump(const Duration(milliseconds: 20));
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 380,
            height: 600,
            child: ChatSessionList(
              chat: chat,
              selectedSessionId: null,
              onSelect: (_) {},
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('删除会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '删除'));
    await tester.pumpAndSettle();
    expect(chat.historyConversations, hasLength(1));
    expect(find.textContaining('读取或删除失败'), findsOneWidget);
    expect(find.text('保留会话'), findsOneWidget);
  });

  testWidgets('创建结果未知时删除不显示已请求停止', (tester) async {
    final gateway = _HistoryGateway(
      sessionIds: const ['session-uncertain'],
      names: const {'session-uncertain': '创建中的会话'},
    )..createCompleter = Completer<ChatCreateRunResult>();
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();
    await tester.pump(const Duration(milliseconds: 20));
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 380,
            height: 600,
            child: ChatSessionList(
              chat: chat,
              selectedSessionId: null,
              onSelect: chat.selectSession,
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    chat.updateDraft('session-uncertain', '待确认的请求');
    unawaited(chat.send('session-uncertain', model: 'deepseek-flash'));
    await tester.pump(const Duration(milliseconds: 20));
    expect(chat.selectedConversation?.activeRun?.runId, isNull);

    await tester.tap(find.byTooltip('删除会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '删除'));
    await tester.pumpAndSettle();
    expect(find.text('创建结果尚未确认，请先恢复本轮状态再删除。'), findsOneWidget);
    expect(find.text('已请求停止，请等待本轮进入终态后再删除。'), findsNothing);
    expect(gateway.cancelCalls, 0);
    expect(chat.historyConversations, hasLength(1));

    gateway.createCompleter!.complete(const ChatCreateRunResult('run-late'));
    await tester.pump(const Duration(milliseconds: 20));
  });
}

class _HistoryGateway implements ChatGateway {
  _HistoryGateway({
    this.sessionIds = const [],
    this.names = const {},
    this.failList = false,
  });

  final List<String> sessionIds;
  final Map<String, String> names;
  final bool failList;
  bool failDelete = false;
  int listCalls = 0;
  Completer<List<String>>? sessionIdsCompleter;
  Completer<ChatCreateRunResult>? createCompleter;
  int cancelCalls = 0;
  final StreamController<ChatRunEventSignal> events =
      StreamController<ChatRunEventSignal>.broadcast();

  void dispose() => events.close();

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) async => createCompleter == null
      ? const ChatCreateRunResult('run-created')
      : createCompleter!.future;

  @override
  Future<ChatRun> getRun(String runId) async => throw StateError('no runs');

  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) =>
      events.stream;

  @override
  Future<ChatCancelRunResult> cancelRun(String runId) async {
    cancelCalls++;
    return ChatCancelRunResult(runId: runId, status: ChatRunStatus.cancelled);
  }

  @override
  Future<ChatSession> getSession(String sessionId) async => ChatSession(
    sessionId: sessionId,
    name: names[sessionId] ?? '',
    runIds: const [],
  );

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async {
    if (failDelete) throw StateError('fixture deletion failure');
    return ChatDeleteSessionResult(sessionId: sessionId, status: 'deleted');
  }

  @override
  Future<List<String>> listSessions() async {
    listCalls++;
    if (failList) throw StateError('fixture history failure');
    return sessionIdsCompleter == null
        ? sessionIds
        : sessionIdsCompleter!.future;
  }
}
