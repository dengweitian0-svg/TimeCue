import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/desktop_lifecycle.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';

void main() {
  testWidgets('host keeps chat model and draft across notebook navigation', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final repository = DemoNotebookRepository();
    final platform = _HostDesktopPlatform();
    final desktop = DesktopLifecycle(platform);
    final gateway = _HostChatGateway();
    var sessionSequence = 0;
    var keySequence = 0;
    final chat = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 'session-${++sessionSequence}',
      createIdempotencyKey: () => 'create_session:key-${++keySequence}',
      retryDelay: (_) => const Duration(hours: 1),
    );
    var chatUpdates = 0;
    chat.addListener(() => chatUpdates++);
    var releases = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: MediaQuery(
          data: const MediaQueryData(disableAnimations: true),
          child: DesktopShell(
            repository: repository,
            desktop: desktop,
            chat: chat,
            releaseChatTransport: () {
              releases++;
              throw StateError('transport close failed');
            },
          ),
        ),
      ),
    );
    await tester.pump();

    await tester.tap(find.textContaining('日程本'));
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.text('AI 对话').first);
    await tester.pump(const Duration(milliseconds: 50));

    final input = find.byKey(const ValueKey('chat-draft-input'));
    expect(chat.selectedConversation, isNotNull);
    await tester.enterText(input, '发给宿主模型');
    await tester.pump();
    expect(chat.selectedConversation!.draft, '发给宿主模型');
    expect(chat.canSend(chat.selectedConversation!.sessionId), isTrue);
    expect(
      tester
          .widget<IconButton>(find.byKey(const ValueKey('chat-send-button')))
          .onPressed,
      isNotNull,
    );
    await tester.tap(find.byKey(const ValueKey('chat-send-button')));
    await tester.pump(const Duration(milliseconds: 50));
    expect(gateway.createdInputs, ['发给宿主模型']);
    expect(gateway.eventConnections, 1);

    chatUpdates = 0;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    gateway.emitText('失焦期间仍可见');
    await tester.pump(const Duration(milliseconds: 30));
    expect(chat.selectedConversation!.runs.single.rawText, '失焦期间仍可见');
    expect(chatUpdates, 1);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);

    chatUpdates = 0;
    await desktop.hideWindow();
    expect(desktop.state, DesktopState.hidden);
    gateway.emitText('隐藏期间到达');
    await tester.pump(const Duration(milliseconds: 30));
    expect(chat.selectedConversation!.runs.single.rawText, '失焦期间仍可见隐藏期间到达');
    expect(chatUpdates, 0);
    await desktop.openWindow();
    expect(desktop.state, DesktopState.visible);
    expect(chatUpdates, 1);
    expect(gateway.eventConnections, 1);

    await tester.enterText(input, '切页后保留的草稿');
    await tester.tap(find.text('日历'));
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.text('AI 对话').first);
    await tester.pump(const Duration(milliseconds: 50));
    expect(tester.widget<TextField>(input).controller!.text, '切页后保留的草稿');
    expect(gateway.eventConnections, 1);

    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.textContaining('日程本'));
    await tester.pump(const Duration(milliseconds: 50));
    expect(tester.widget<TextField>(input).controller!.text, '切页后保留的草稿');
    expect(gateway.eventConnections, 1);

    await desktop.requestExit();
    expect(platform.destroyed, isTrue);
    expect(platform.trayRemoved, isTrue);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    expect(releases, 1);
    expect(gateway.cancelledConnections, 1);
    await expectLater(
      chat.send('session-1', model: 'deepseek-flash'),
      throwsA(isA<StateError>()),
    );
    expect(gateway.createdInputs, ['发给宿主模型']);
    repository.dispose();
  });

  testWidgets(
    'exit does not wait for pending create and ignores late response',
    (tester) async {
      final repository = DemoNotebookRepository();
      final platform = _HostDesktopPlatform();
      final desktop = DesktopLifecycle(platform);
      final gateway = _HostChatGateway()
        ..pendingCreate = Completer<ChatCreateRunResult>();
      final chat = ChatViewModel(gateway: gateway);
      var modelUpdates = 0;
      chat.addListener(() => modelUpdates++);
      await tester.pumpWidget(
        MaterialApp(
          home: DesktopShell(
            repository: repository,
            desktop: desktop,
            chat: chat,
          ),
        ),
      );
      await tester.pump();

      final conversation = chat.createLocalSession();
      chat.updateDraft(conversation.sessionId, '退出时仍在创建');
      final pending = chat.send(
        conversation.sessionId,
        model: 'deepseek-flash',
      );
      await desktop.requestExit();
      await tester.pump();
      final updatesAtExit = modelUpdates;
      expect(platform.destroyed, isTrue);
      gateway.pendingCreate!.complete(ChatCreateRunResult('late-run'));
      await pending;
      await tester.pump();

      expect(conversation.runs.single.runId, isNull);
      expect(conversation.runs.single.error, isNull);
      expect(gateway.eventConnections, 0);
      expect(modelUpdates, updatesAtExit);
      await tester.pumpWidget(const SizedBox.shrink());
      repository.dispose();
    },
  );
}

class _HostDesktopPlatform implements DesktopPlatform {
  bool destroyed = false, trayRemoved = false;

  @override
  Future<void> initialize(DesktopLifecycle lifecycle) async {}
  @override
  Future<List<DesktopLaunchRequest>> ready() async => const [];
  @override
  Future<void> createTray() async {}
  @override
  Future<bool> hasTray() async => true;
  @override
  Future<void> show() async {}
  @override
  Future<void> hide() async {}
  @override
  Future<void> beginExit() async {}
  @override
  Future<void> removeTray() async => trayRemoved = true;
  @override
  Future<void> destroy() async => destroyed = true;
  @override
  void detach() {}
}

class _HostChatGateway implements ChatGateway, ChatModelGateway {
  @override
  Future<List<ChatModelOption>> getModels() async => const [
    ChatModelOption(
      provider: 'deepseek',
      model: 'deepseek-flash',
      available: true,
    ),
  ];

  final List<String> createdInputs = [];
  int eventConnections = 0, cancelledConnections = 0;
  int _eventId = 0;
  Completer<ChatCreateRunResult>? pendingCreate;
  MultiStreamController<ChatRunEventSignal>? _eventController;

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    String provider = 'deepseek',
    required String idempotencyKey,
  }) async {
    createdInputs.add(input);
    if (pendingCreate != null) return pendingCreate!.future;
    return ChatCreateRunResult('run-${createdInputs.length}');
  }

  @override
  Future<ChatRun> getRun(String runId) async => ChatRun(
    runId: runId,
    sessionId: 'session-1',
    model: 'deepseek-flash',
    input: createdInputs.last,
    status: ChatRunStatus.running,
    output: null,
    error: null,
    createdAt: DateTime.utc(2026, 10, 17),
    finishedAt: null,
  );

  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) {
    eventConnections++;
    return Stream<ChatRunEventSignal>.multi((controller) {
      _eventController = controller;
      controller.onCancel = () {
        cancelledConnections++;
        if (identical(_eventController, controller)) _eventController = null;
      };
    });
  }

  void emitText(String text) => _eventController?.add(
    ChatRunEventFrame(
      ChatSseEvent(
        // SSE 身份按事件递增；后台增量应继续驱动模型游标。
        eventId: ++_eventId,
        type: ChatEventType.text,
        data: ChatContentEventData(content: text),
      ),
    ),
  );

  @override
  Future<ChatCancelRunResult> cancelRun(String runId) async =>
      ChatCancelRunResult(runId: runId, status: ChatRunStatus.running);

  @override
  Future<ChatSession> getSession(String sessionId) async =>
      ChatSession(sessionId: sessionId, name: '宿主测试会话', runIds: ['run-1']);

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async =>
      ChatDeleteSessionResult(sessionId: sessionId, status: 'deleted');

  @override
  Future<List<String>> listSessions() async => const [];
}
