import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
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
    final gateway = _HostChatGateway();
    var sessionSequence = 0;
    var keySequence = 0;
    final chat = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 'session-${++sessionSequence}',
      createIdempotencyKey: () => 'create_session:key-${++keySequence}',
      retryDelay: (_) => const Duration(hours: 1),
    );
    var releases = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: MediaQuery(
          data: const MediaQueryData(disableAnimations: true),
          child: DesktopShell(
            repository: repository,
            chat: chat,
            releaseChatTransport: () => releases++,
          ),
        ),
      ),
    );

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
}

class _HostChatGateway implements ChatGateway {
  final List<String> createdInputs = [];
  int eventConnections = 0, cancelledConnections = 0;

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) async {
    createdInputs.add(input);
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
      controller.onCancel = () => cancelledConnections++;
    });
  }

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
