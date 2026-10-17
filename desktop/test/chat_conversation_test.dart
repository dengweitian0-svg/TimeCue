import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/chat/chat_composer.dart';
import 'package:timecue_desktop/features/chat/chat_markdown_body.dart';
import 'package:timecue_desktop/features/chat/chat_session_list.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  testWidgets('单会话发送、保留下一条草稿、展示工具及取消 checkpoint', (tester) async {
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    final gateway = _ControllableChatGateway();
    final chat = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 'session-ui',
      createIdempotencyKey: () => 'message-stable',
      retryDelay: (_) => const Duration(days: 1),
    );
    addTearDown(() {
      chat.dispose();
      notebook.dispose();
      repository.dispose();
      gateway.dispose();
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    Future<void> mount(double width, double height, double scale) async {
      tester.view.physicalSize = Size(width, height);
      tester.view.devicePixelRatio = 1;
      await tester.pumpWidget(
        MaterialApp(
          theme: NotebookTheme.data,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: TextScaler.linear(scale)),
            child: child!,
          ),
          home: Scaffold(
            body: Center(
              child: SizedBox(
                width: (width - 64).clamp(260.0, 1120.0),
                height: (height - 108).clamp(160.0, 720.0),
                child: ListenableBuilder(
                  listenable: notebook,
                  builder: (context, _) =>
                      ChatSpread(model: notebook, chat: chat),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.pump();
    }

    await mount(1280, 800, 1);
    await tester.enterText(
      find.byKey(const ValueKey('chat-draft-input')),
      '请查一下',
    );
    await tester.pump();
    expect(chat.selectedSessionId, 'session-ui');
    expect(chat.selectedConversation?.draft, '请查一下');
    expect(chat.canSend('session-ui'), isTrue);
    final composer = tester.widget<ChatComposer>(find.byType(ChatComposer));
    expect(composer.canSend, isTrue);
    expect(composer.controller.text, '请查一下');
    expect(
      composer.controller.text.trim().isEmpty || !composer.canSend,
      isFalse,
    );
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('chat-draft-input')))
          .controller!
          .text,
      '请查一下',
    );
    final sendButton = tester.widget<IconButton>(
      find.byKey(const ValueKey('chat-send-button')),
    );
    expect(sendButton.tooltip, '发送');
    expect(composer.sending, isFalse);
    expect(sendButton.onPressed, isNotNull, reason: '$sendButton');
    await tester.tap(find.byKey(const ValueKey('chat-send-button')));
    await tester.pump();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));

    expect(gateway.createCalls, hasLength(1));
    expect(gateway.createCalls.single.input, '请查一下');
    expect(chat.selectedConversation?.activeRun?.runId, 'run-ui');
    expect(gateway.events.hasListener, isTrue);

    gateway.events.add(
      _frameSignal(
        1,
        ChatEventType.thinking,
        const ChatContentEventData(content: 'private reasoning', error: null),
      ),
    );
    gateway.events.add(_toolFrame(2, 'call-one', 'search_schedules'));
    gateway.events.add(_toolFrame(3, 'call-two', 'search_schedules'));
    gateway.events.add(_toolFrame(4, 'call-three', 'get_calendar'));
    gateway.events.add(
      _frameSignal(
        5,
        ChatEventType.text,
        const ChatContentEventData(
          content:
              '**你好**，请稍等。\n\n$_longReply\n\n```dart\nfinal answer = 42;\n```',
          error: null,
        ),
      ),
    );
    await tester.pump();
    await tester.pump();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));

    expect(chat.selectedConversation?.activeRun?.toolCalls, hasLength(3));
    expect(find.text('Tool calling · 已发起 3 次调用'), findsOneWidget);
    expect(find.text('search_schedules'), findsNWidgets(2));
    expect(find.text('get_calendar'), findsOneWidget);
    expect(find.text('private reasoning'), findsNothing);
    expect(find.text('private args'), findsNothing);
    expect(find.textContaining('你好'), findsOneWidget);
    expect(find.byKey(const ValueKey('chat-stop-button')), findsOneWidget);
    expect(tester.takeException(), isNull);

    await tester.drag(_chatMessageList(tester), const Offset(0, 250));
    await tester.pumpAndSettle();
    final readingOffset = _verticalScrollOffset(tester);
    expect(readingOffset, greaterThan(0));
    await mount(640, 480, 1);
    expect(_verticalScrollOffset(tester), closeTo(readingOffset, 1));
    gateway.events.add(
      _frameSignal(
        6,
        ChatEventType.text,
        const ChatContentEventData(content: '\n新到的段落。', error: null),
      ),
    );
    await tester.pump();
    await tester.pump();
    expect(_verticalScrollOffset(tester), closeTo(readingOffset, 1));
    await mount(1280, 800, 1);
    expect(_verticalScrollOffset(tester), closeTo(readingOffset, 1));
    await tester.tap(find.text('回到最新'));
    await tester.pumpAndSettle();

    await tester.enterText(
      find.byKey(const ValueKey('chat-draft-input')),
      '下一条草稿',
    );
    await tester.tap(find.byKey(const ValueKey('chat-stop-button')));
    await tester.pump();
    expect(chat.selectedConversation?.activeRun?.phase, ChatRunPhase.stopping);
    expect(chat.activeRunCount, 1);
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('chat-draft-input')))
          .controller!
          .text,
      '下一条草稿',
    );

    gateway.snapshot = _terminalRun(
      status: ChatRunStatus.cancelled,
      output: 'final cancellation checkpoint',
    );
    gateway.events.add(
      _frameSignal(
        7,
        ChatEventType.end,
        const ChatEndEventData(stopReason: 'cancelled', error: null),
      ),
    );
    await tester.pump();
    await tester.pump();
    await tester.pump();
    await tester.runAsync(() async {
      await Future<void>.delayed(Duration.zero);
    });
    await tester.pump(const Duration(milliseconds: 20));
    await tester.pump(const Duration(milliseconds: 50));
    final stoppedRun = chat.selectedConversation?.runs.single;
    expect(gateway.getRunCalls, greaterThan(0));
    expect(
      chat.selectedConversation?.activeRun,
      isNull,
      reason:
          '${stoppedRun?.phase} ${stoppedRun?.lastEventId} ${stoppedRun?.status} ${stoppedRun?.error} snapshot=${gateway.snapshot.status}',
    );
    expect(chat.activeRunCount, 0);
    expect(find.text('final cancellation checkpoint'), findsNWidgets(2));
    expect(tester.takeException(), isNull);

    for (final (width, height, scale) in [(640.0, 480.0, 2.0)]) {
      await mount(width, height, scale);
      expect(find.byKey(const ValueKey('chat-draft-input')), findsOneWidget);
      expect(find.widgetWithText(TextButton, '历史会话'), findsOneWidget);
      await tester.tap(find.widgetWithText(TextButton, '历史会话'));
      await tester.pump();
      expect(find.text('测试会话'), findsOneWidget);
      await tester.tap(find.widgetWithText(TextButton, '返回对话'));
      await tester.pump();
      expect(find.byKey(const ValueKey('chat-draft-input')), findsOneWidget);
      expect(tester.takeException(), isNull);
    }
  });

  testWidgets('Enter 发送且 composing 状态下 Enter 不提交', (tester) async {
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    final gateway = _ControllableChatGateway();
    final chat = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 'session-ime',
      createIdempotencyKey: () => 'message-ime',
      retryDelay: (_) => const Duration(days: 1),
    );
    addTearDown(() {
      chat.dispose();
      notebook.dispose();
      repository.dispose();
      gateway.dispose();
    });

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: ChatSpread(model: notebook, chat: chat),
        ),
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    final input = tester.widget<TextField>(
      find.byKey(const ValueKey('chat-draft-input')),
    );
    input.controller!.value = const TextEditingValue(
      text: '正在输入',
      composing: TextRange(start: 0, end: 4),
    );
    chat.updateDraft('session-ime', '正在输入');
    await tester.tap(find.byKey(const ValueKey('chat-draft-input')));
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();
    expect(gateway.createCalls, isEmpty);

    input.controller!.value = const TextEditingValue(text: '换行草稿');
    chat.updateDraft('session-ime', '换行草稿');
    await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
    await tester.pump();
    expect(input.controller!.text, '换行草稿\n');
    expect(gateway.createCalls, isEmpty);

    input.controller!.value = const TextEditingValue(text: '立即发送');
    chat.updateDraft('session-ime', '立即发送');
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    expect(gateway.createCalls, hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('历史 run 按创建时间从旧到新显示，即使服务端按新到旧返回', (tester) async {
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository);
    final gateway = _ControllableChatGateway()
      ..historySessionIds = const ['session-history']
      ..historyRunIds = const ['run-new', 'run-old']
      ..historySnapshots = {
        'run-new': _historyRun(
          runId: 'run-new',
          input: '新输入',
          output: null,
          status: ChatRunStatus.failed,
          error: const ChatProtocolError(code: 'fixture', message: '测试失败'),
          createdAt: DateTime.utc(2026, 10, 17, 2),
        ),
        'run-old': _historyRun(
          runId: 'run-old',
          input: '旧输入',
          output: '旧回答',
          createdAt: DateTime.utc(2026, 10, 17, 1),
        ),
      };
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      notebook.dispose();
      repository.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();
    chat.selectSession('session-history');

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: ChatSpread(model: notebook, chat: chat),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('旧输入'), findsOneWidget);
    expect(find.text('新输入'), findsOneWidget);
    expect(
      tester.getTopLeft(find.text('旧输入')).dy,
      lessThan(tester.getTopLeft(find.text('新输入')).dy),
    );
    final failureLabels = find.text('回复失败');
    expect(failureLabels, findsNWidgets(2));
    expect(
      tester.getTopLeft(failureLabels.first).dy,
      lessThan(tester.getTopLeft(failureLabels.last).dy),
      reason: '页头需按最新 run 显示失败状态，消息区仍保留该轮状态。',
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('切换会话后各自恢复阅读位置', (tester) async {
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository);
    final gateway = _ControllableChatGateway()
      ..historySessionIds = const ['session-a', 'session-b']
      ..historyRunIdsBySession = const {
        'session-a': ['run-a'],
        'session-b': ['run-b'],
      }
      ..historyNames = const {'session-a': 'A会话', 'session-b': 'B会话'}
      ..historySnapshots = {
        'run-a': _historyRun(
          sessionId: 'session-a',
          runId: 'run-a',
          input: 'A的问题',
          output: _readingBody('A'),
          createdAt: DateTime.utc(2026, 10, 17, 1),
        ),
        'run-b': _historyRun(
          sessionId: 'session-b',
          runId: 'run-b',
          input: 'B的问题',
          output: _readingBody('B'),
          createdAt: DateTime.utc(2026, 10, 17, 2),
        ),
      };
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      notebook.dispose();
      repository.dispose();
      gateway.dispose();
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    await chat.loadHistory();
    chat.selectSession('session-a');
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: ChatSpread(model: notebook, chat: chat),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.drag(_messageList('session-a'), const Offset(0, 240));
    await tester.pumpAndSettle();
    final aOffset = _messageOffset(tester, 'session-a');
    expect(aOffset, greaterThan(0));

    await tester.tap(
      find.descendant(
        of: find.byType(ChatSessionList),
        matching: find.text('B会话'),
      ),
    );
    await tester.pumpAndSettle();
    expect(chat.selectedSessionId, 'session-b');
    await tester.tap(
      find.descendant(
        of: find.byType(ChatSessionList),
        matching: find.text('A会话'),
      ),
    );
    await tester.pumpAndSettle();
    expect(chat.selectedSessionId, 'session-a');
    expect(_messageOffset(tester, 'session-a'), closeTo(aOffset, 1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('创建结果未知时页头删除提示不会谎称已请求停止', (tester) async {
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository);
    final gateway = _ControllableChatGateway()
      ..historySessionIds = const ['session-uncertain']
      ..historyRunIdsBySession = const {'session-uncertain': <String>[]}
      ..historyNames = const {'session-uncertain': '创建中的会话'}
      ..createCompleter = Completer<ChatCreateRunResult>();
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      notebook.dispose();
      repository.dispose();
      gateway.dispose();
    });
    await chat.loadHistory();
    chat.selectSession('session-uncertain');

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: SizedBox(
            width: 1280,
            height: 760,
            child: ChatSpread(model: notebook, chat: chat),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    chat.updateDraft('session-uncertain', '待确认的请求');
    unawaited(chat.send('session-uncertain', model: 'deepseek-flash'));
    await tester.pump(const Duration(milliseconds: 20));
    expect(chat.selectedConversation?.activeRun?.runId, isNull);

    await tester.tap(find.byTooltip('会话操作'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    await tester.tap(find.text('删除会话').last);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    await tester.tap(find.widgetWithText(FilledButton, '删除'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('创建结果尚未确认，请先恢复本轮状态再删除。'), findsOneWidget);
    expect(find.text('已请求停止，请等待本轮进入终态后再删除。'), findsNothing);
    expect(gateway.cancelCalls, 0);

    gateway.createCompleter!.complete(const ChatCreateRunResult('run-late'));
    await tester.pump(const Duration(milliseconds: 20));
  });

  testWidgets('Markdown 代码复制将代码原文写入系统剪贴板', (tester) async {
    String? copiedText;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, (call) async {
          if (call.method == 'Clipboard.setData') {
            copiedText = (call.arguments as Map)['text'] as String?;
          }
          return null;
        });
    addTearDown(
      () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null),
    );
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: const Scaffold(
          body: SingleChildScrollView(
            child: ChatMarkdownBody(data: '```dart\nfinal value = 42;\n```'),
          ),
        ),
      ),
    );
    await tester.tap(find.byTooltip('Copy code'));
    await tester.pump();
    expect(copiedText, 'final value = 42;');
    await tester.pump(const Duration(seconds: 5));
    expect(tester.takeException(), isNull);
  });
}

Finder _chatMessageList(WidgetTester tester) => _messageList('session-ui');

Finder _messageList(String sessionId) => find.descendant(
  of: find.byKey(ValueKey('chat-message-list-$sessionId')),
  matching: find.byType(ListView),
);

double _verticalScrollOffset(WidgetTester tester) =>
    _messageOffset(tester, 'session-ui');

double _messageOffset(WidgetTester tester, String sessionId) => tester
    .state<ScrollableState>(
      find
          .descendant(
            of: find.byKey(ValueKey('chat-message-list-$sessionId')),
            matching: find.byType(Scrollable),
          )
          .first,
    )
    .position
    .pixels;

const _longReply = '''
段落 0：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 1：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 2：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 3：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 4：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 5：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 6：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 7：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 8：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 9：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 10：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 11：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 12：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 13：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 14：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 15：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 16：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 17：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 18：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 19：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 20：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 21：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 22：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 23：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 24：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 25：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 26：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 27：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 28：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
段落 29：这是较长的滚动内容，用来确认阅读位置不会被布局切换抢走。
''';

String _readingBody(String prefix) => List<String>.generate(
  48,
  (index) => '$prefix 段落 $index：这是一段较长的历史回复，用来验证切换会话时独立保存阅读位置。',
).join('\n\n');

ChatSseEvent _frame(int id, ChatEventType type, Object data) =>
    ChatSseEvent(eventId: id, type: type, data: data);

ChatRunEventFrame _toolFrame(int eventId, String callId, String name) =>
    _frameSignal(
      eventId,
      ChatEventType.tool,
      ChatToolEventData(
        id: callId,
        function: ChatToolFunction(
          name: name,
          args: const ChatToolArgument(name: 'private args', value: 'secret'),
        ),
      ),
    );

ChatRunEventFrame _frameSignal(int id, ChatEventType type, Object data) =>
    ChatRunEventFrame(_frame(id, type, data));

ChatRun _terminalRun({required ChatRunStatus status, required String output}) =>
    ChatRun(
      runId: 'run-ui',
      sessionId: 'session-ui',
      model: 'deepseek-flash',
      input: '请查一下',
      status: status,
      output: output,
      error: null,
      createdAt: DateTime.utc(2026, 10, 17),
      finishedAt: DateTime.utc(2026, 10, 17, 1),
    );

ChatRun _historyRun({
  String sessionId = 'session-history',
  required String runId,
  required String input,
  required String? output,
  required DateTime createdAt,
  ChatRunStatus status = ChatRunStatus.succeeded,
  ChatProtocolError? error,
}) => ChatRun(
  runId: runId,
  sessionId: sessionId,
  model: 'deepseek-flash',
  input: input,
  status: status,
  output: output,
  error: error,
  createdAt: createdAt,
  finishedAt: createdAt.add(const Duration(minutes: 1)),
);

class _CreateCall {
  const _CreateCall(this.sessionId, this.input, this.model, this.key);

  final String sessionId;
  final String input;
  final String model;
  final String key;
}

class _ControllableChatGateway implements ChatGateway {
  final events = StreamController<ChatRunEventSignal>.broadcast();
  final createCalls = <_CreateCall>[];
  int getRunCalls = 0;
  int cancelCalls = 0;
  List<String> historySessionIds = const [];
  List<String> historyRunIds = const [];
  Map<String, List<String>> historyRunIdsBySession = const {};
  Map<String, String> historyNames = const {};
  Map<String, ChatRun> historySnapshots = const {};
  Completer<ChatCreateRunResult>? createCompleter;
  ChatRun snapshot = _terminalRun(
    status: ChatRunStatus.succeeded,
    output: '最终回答',
  );

  void dispose() => events.close();

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) async {
    createCalls.add(_CreateCall(sessionId, input, model, idempotencyKey));
    return createCompleter == null
        ? const ChatCreateRunResult('run-ui')
        : createCompleter!.future;
  }

  @override
  Future<ChatRun> getRun(String runId) async {
    getRunCalls++;
    return historySnapshots[runId] ?? snapshot;
  }

  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) =>
      events.stream;

  @override
  Future<ChatCancelRunResult> cancelRun(String runId) async {
    cancelCalls++;
    return ChatCancelRunResult(runId: runId, status: ChatRunStatus.running);
  }

  @override
  Future<ChatSession> getSession(String sessionId) async => ChatSession(
    sessionId: sessionId,
    name: historyNames[sessionId] ?? '测试会话',
    runIds:
        historyRunIdsBySession[sessionId] ??
        (historyRunIds.isEmpty ? const ['run-ui'] : historyRunIds),
  );

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async =>
      ChatDeleteSessionResult(sessionId: sessionId, status: 'deleted');

  @override
  Future<List<String>> listSessions() async => historySessionIds;
}
