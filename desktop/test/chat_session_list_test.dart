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
  testWidgets('搜索使用展示名称和摘要并保留规范化及清除行为', (tester) async {
    final gateway = _HistoryGateway(
      sessionIds: const ['unnamed', 'named', 'summary-only'],
      names: const {'named': '中文计划 Task Alpha', 'summary-only': '摘要测试会话'},
      runIds: const {
        'summary-only': ['summary-run'],
      },
      runs: {
        'summary-run': ChatRun(
          runId: 'summary-run',
          sessionId: 'summary-only',
          model: 'test-model',
          input: '旧输入',
          status: ChatRunStatus.succeeded,
          output: '摘要独有词用于搜索',
          error: null,
          createdAt: DateTime.utc(2026, 10, 18),
          finishedAt: DateTime.utc(2026, 10, 18, 0, 1),
        ),
      },
    );
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();

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

    expect(
      chat.historyConversations
          .firstWhere((c) => c.sessionId == 'unnamed')
          .name,
      '',
    );
    await tester.enterText(find.byType(TextField).first, '未');
    await tester.pumpAndSettle();
    expect(find.text('未命名会话'), findsOneWidget);
    expect(find.text('中文计划 Task Alpha'), findsNothing);

    await tester.enterText(find.byType(TextField).first, '计划');
    await tester.pumpAndSettle();
    expect(find.text('中文计划 Task Alpha'), findsOneWidget);
    expect(find.text('未命名会话'), findsNothing);

    await tester.enterText(find.byType(TextField).first, '摘要独有词');
    await tester.pumpAndSettle();
    expect(find.text('摘要独有词用于搜索'), findsOneWidget);
    expect(find.text('中文计划 Task Alpha'), findsNothing);

    await tester.enterText(find.byType(TextField).first, '  tAsK  ');
    await tester.pumpAndSettle();
    expect(find.text('中文计划 Task Alpha'), findsOneWidget);
    expect(find.text('摘要独有词用于搜索'), findsNothing);

    await tester.tap(find.byTooltip('清空搜索'));
    await tester.pumpAndSettle();
    expect(find.text('未命名会话'), findsOneWidget);
    expect(find.text('中文计划 Task Alpha'), findsOneWidget);
    expect(find.text('摘要独有词用于搜索'), findsOneWidget);
  });

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

  testWidgets('删除当前会话后直接进入新会话输入页面', (tester) async {
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository);
    final gateway = _HistoryGateway(
      sessionIds: const ['remote-session'],
      names: const {'remote-session': '即将删除的会话'},
    );
    final chat = ChatViewModel(gateway: gateway);
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
    expect(find.byKey(const ValueKey('chat-draft-input')), findsOneWidget);

    await tester.tap(find.widgetWithText(TextButton, '历史会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('即将删除的会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('会话操作'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('删除会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '删除'));
    await tester.pumpAndSettle();

    expect(find.text('当前没有可显示的会话。'), findsNothing);
    expect(find.text('新会话'), findsOneWidget);
    expect(find.byKey(const ValueKey('chat-draft-input')), findsOneWidget);
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('chat-draft-input')))
          .decoration!
          .hintText,
      '写下你的想法…',
    );
    expect(gateway.createRequests, isEmpty);
    expect(gateway.deleteCalls, 1);
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

  testWidgets('创建结果待确认时保留本地入口并用原幂等请求重试', (tester) async {
    final gateway = _HistoryGateway()
      ..createCompleter = Completer<ChatCreateRunResult>();
    var sessionIndex = 0;
    final chat = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 'local-${++sessionIndex}',
      createIdempotencyKey: () => 'same-key',
    );
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();
    final original = chat.createLocalSession();
    chat.updateDraft(original.sessionId, '需要安全重试的原始输入');

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 380,
            height: 600,
            child: ChatSessionList(
              chat: chat,
              selectedSessionId: chat.selectedSessionId,
              onSelect: chat.selectSession,
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    final firstSend = chat.send(original.sessionId, model: 'deepseek-flash');
    await tester.pumpAndSettle();
    expect(find.text('创建请求处理中'), findsOneWidget);
    expect(find.text('需要安全重试的原始输入'), findsOneWidget);
    expect(original.draft, isEmpty);
    expect(chat.activeRunCount, 1);

    final second = chat.createLocalSession();
    chat.updateDraft(original.sessionId, '这条下一条消息仍是未发送草稿');
    await tester.pumpAndSettle();
    expect(chat.selectedSessionId, second.sessionId);
    expect(find.text('新会话草稿'), findsNothing);
    expect(find.textContaining('未发送草稿：这条下一条消息仍是未发送草稿'), findsOneWidget);
    await tester.tap(find.text('创建请求处理中'));
    await tester.pumpAndSettle();
    expect(chat.selectedSessionId, original.sessionId);

    gateway.createCompleter!.completeError(
      const ChatHttpError(
        statusCode: 500,
        code: 'temporary_failure',
        message: '服务端结果未知',
      ),
    );
    await firstSend;
    await tester.pumpAndSettle();
    expect(find.text('创建结果待确认'), findsOneWidget);
    expect(chat.activeRunCount, 1);
    expect(gateway.createRequests, hasLength(1));

    gateway.createCompleter = null;
    await chat.retryCreate(original.sessionId);
    await tester.pumpAndSettle();
    expect(gateway.createRequests, hasLength(2));
    expect(gateway.createRequests[1], gateway.createRequests[0]);
    expect(gateway.createRequests.last.sessionId, original.sessionId);
    expect(gateway.createRequests.last.input, '需要安全重试的原始输入');
    expect(gateway.createRequests.last.model, 'deepseek-flash');
    expect(gateway.createRequests.last.idempotencyKey, 'same-key');
    expect(chat.activeRunCount, 1);
    expect(original.isLocal, isFalse);
    expect(
      chat.historyConversations.where(
        (conversation) => conversation.sessionId == original.sessionId,
      ),
      hasLength(1),
    );
    expect(find.text('创建结果待确认'), findsNothing);
    expect(find.text('未命名会话'), findsOneWidget);
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
    this.runIds = const {},
    this.runs = const {},
    this.failList = false,
  });

  final List<String> sessionIds;
  final Map<String, String> names;
  final Map<String, List<String>> runIds;
  final Map<String, ChatRun> runs;
  final bool failList;
  bool failDelete = false;
  int listCalls = 0;
  final List<
    ({String sessionId, String input, String model, String idempotencyKey})
  >
  createRequests = [];
  Completer<List<String>>? sessionIdsCompleter;
  Completer<ChatCreateRunResult>? createCompleter;
  int cancelCalls = 0;
  int deleteCalls = 0;
  final StreamController<ChatRunEventSignal> events =
      StreamController<ChatRunEventSignal>.broadcast();

  void dispose() => events.close();

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) async {
    createRequests.add((
      sessionId: sessionId,
      input: input,
      model: model,
      idempotencyKey: idempotencyKey,
    ));
    return createCompleter == null
        ? const ChatCreateRunResult('run-created')
        : createCompleter!.future;
  }

  @override
  Future<ChatRun> getRun(String runId) async => runs[runId]!;

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
    runIds: runIds[sessionId] ?? const [],
  );

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async {
    deleteCalls++;
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
