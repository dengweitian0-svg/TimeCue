import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';

void main() {
  group('chat history races', () {
    test(
      'history details stay bounded and failed entries do not stop queue',
      () async {
        final gateway = _RaceGateway(
          sessionIds: [for (var i = 1; i <= 6; i++) 'session-$i'],
        );
        final pending = {
          for (var i = 1; i <= 6; i++) 'session-$i': Completer<ChatSession>(),
        };
        for (final entry in pending.entries) {
          gateway.enqueueSession(entry.key, entry.value.future);
        }
        final model = _createModel(gateway);
        addTearDown(model.dispose);

        final loading = model.loadHistory();
        await _until(() => gateway.requestedSessions.length == 4);
        expect(gateway.maxConcurrentSessionRequests, lessThanOrEqualTo(4));
        expect(gateway.requestedSessions, [
          'session-1',
          'session-2',
          'session-3',
          'session-4',
        ]);

        pending['session-1']!.complete(_session('session-1', '名称一'));
        pending['session-2']!.completeError(StateError('详情读取失败'));
        await _until(() => gateway.requestedSessions.length == 6);
        expect(gateway.maxConcurrentSessionRequests, 4);
        pending['session-3']!.complete(_session('session-3', '名称三'));
        pending['session-4']!.complete(_session('session-4', '名称四'));
        pending['session-5']!.complete(_session('session-5', '名称五'));
        pending['session-6']!.complete(_session('session-6', '名称六'));
        await loading;

        expect(gateway.requestedSessions.toSet(), pending.keys.toSet());
        expect(gateway.maxConcurrentSessionRequests, lessThanOrEqualTo(4));
        expect(model.historyCompletedCount, 6);
        expect(model.historyConversations, hasLength(6));
        expect(
          model.historyConversations
              .singleWhere((item) => item.sessionId == 'session-2')
              .error,
          '详情读取失败',
        );
      },
    );

    test(
      'newer session refresh wins and late name stays with its session',
      () async {
        final gateway = _RaceGateway(sessionIds: ['first', 'second'])
          ..sessions['first'] = _session('first', '旧名')
          ..sessions['second'] = _session('second', '另一个会话');
        final model = _createModel(gateway);
        addTearDown(model.dispose);
        await model.loadHistory();
        final firstConversation = model.historyConversations.singleWhere(
          (item) => item.sessionId == 'first',
        );
        final secondConversation = model.historyConversations.singleWhere(
          (item) => item.sessionId == 'second',
        );
        final olderResponse = Completer<ChatSession>();
        final newerResponse = Completer<ChatSession>();
        gateway.enqueueSession('first', olderResponse.future);
        gateway.enqueueSession('first', newerResponse.future);

        final olderRefresh = model.refreshSession('first');
        await _until(
          () =>
              gateway.requestedSessions.where((id) => id == 'first').length ==
              2,
        );
        final newerRefresh = model.refreshSession('first');
        await _until(
          () =>
              gateway.requestedSessions.where((id) => id == 'first').length ==
              3,
        );
        newerResponse.complete(_session('first', '较新的名称'));
        await newerRefresh;

        model.selectSession('second');
        olderResponse.complete(_session('first', '迟到的旧名称'));
        await olderRefresh;

        expect(firstConversation.name, '较新的名称');
        expect(secondConversation.name, '另一个会话');
        expect(model.selectedSessionId, 'second');
      },
    );

    test(
      'three indexed running runs block fourth send before and after indexing',
      () async {
        final ids = ['first', 'second', 'third'];
        final gateway = _RaceGateway(sessionIds: ids);
        final details = {
          for (final sessionId in ids) sessionId: Completer<ChatSession>(),
        };
        for (final entry in details.entries) {
          gateway.enqueueSession(entry.key, entry.value.future);
          gateway.runs['run-${entry.key}'] = _runningRun(
            'run-${entry.key}',
            entry.key,
          );
        }
        final model = _createModel(gateway);
        addTearDown(model.dispose);
        final local = model.createLocalSession();
        model.updateDraft(local.sessionId, '保留的第四条输入');
        final loading = model.loadHistory();
        await _until(() => gateway.requestedSessions.length == 3);

        await model.send(local.sessionId, model: 'test-model');
        expect(local.draft, '保留的第四条输入');
        expect(gateway.createCalls, isEmpty);

        for (final entry in details.entries) {
          entry.value.complete(
            _session(
              entry.key,
              '会话 ${entry.key}',
              runIds: ['run-${entry.key}'],
            ),
          );
        }
        await loading;
        await _until(() => gateway.eventStreams.length == 3);
        expect(model.activeRunCount, 3);
        await model.send(local.sessionId, model: 'test-model');
        expect(local.draft, '保留的第四条输入');
        expect(gateway.createCalls, isEmpty);

        model.selectSession('first');
        model.selectSession(local.sessionId);
        await Future<void>.delayed(Duration.zero);
        expect(gateway.eventStreams, hasLength(3));
        expect(gateway.eventStreams.toSet(), {
          'run-first',
          'run-second',
          'run-third',
        });
      },
    );

    test(
      'pending list and rejected refresh cannot restore successful deletion',
      () async {
        final gateway = _RaceGateway(sessionIds: ['target', 'other'])
          ..sessions['target'] = _session('target', '待删除')
          ..sessions['other'] = _session('other', '其他会话');
        final model = _createModel(gateway);
        addTearDown(model.dispose);
        await model.loadHistory();
        final target = model.historyConversations.singleWhere(
          (item) => item.sessionId == 'target',
        );

        final oldListResponse = Completer<List<String>>();
        gateway.enqueueList(oldListResponse.future);
        final staleIndex = model.loadHistory(refresh: true);
        await _until(() => gateway.listCalls == 2);
        final deleteResponse = Completer<ChatDeleteSessionResult>();
        gateway.deleteResponse = deleteResponse.future;
        final delete = model.deleteSession('target');
        await _until(() => gateway.deleteCalls == 1);
        final targetRequestsBeforeRefresh = gateway.requestedSessions
            .where((id) => id == 'target')
            .length;
        await model.refreshSession('target');
        expect(
          gateway.requestedSessions.where((id) => id == 'target').length,
          targetRequestsBeforeRefresh,
        );
        deleteResponse.complete(
          const ChatDeleteSessionResult(sessionId: 'target', status: 'deleted'),
        );
        final deleted = await delete;
        expect(deleted, isTrue);
        oldListResponse.complete(['target', 'other']);
        await staleIndex;

        expect(
          model.historyConversations.any((item) => identical(item, target)),
          isFalse,
        );
        expect(model.historyConversations.map((item) => item.sessionId), [
          'other',
        ]);
      },
    );
  });
}

ChatSession _session(
  String sessionId,
  String name, {
  List<String> runIds = const [],
}) => ChatSession(sessionId: sessionId, name: name, runIds: runIds);

ChatRun _runningRun(String runId, String sessionId) => ChatRun(
  runId: runId,
  sessionId: sessionId,
  model: 'test-model',
  input: '历史输入',
  status: ChatRunStatus.running,
  output: null,
  error: null,
  createdAt: DateTime.utc(2026, 10, 17),
  finishedAt: null,
);

ChatViewModel _createModel(_RaceGateway gateway) {
  var sessionSequence = 0;
  var keySequence = 0;
  return ChatViewModel(
    gateway: gateway,
    createSessionId: () => 'local-${++sessionSequence}',
    createIdempotencyKey: () => 'key-${++keySequence}',
    retryDelay: (_) => const Duration(hours: 1),
  );
}

Future<void> _until(bool Function() condition) async {
  for (var attempt = 0; attempt < 100; attempt++) {
    if (condition()) return;
    await Future<void>.delayed(Duration.zero);
  }
  fail('在限定微任务轮询内未达到预期状态');
}

class _RaceGateway implements ChatGateway {
  _RaceGateway({this.sessionIds = const []});

  final List<String> sessionIds;
  final Map<String, ChatSession> sessions = {};
  final Map<String, ChatRun> runs = {};
  final Map<String, List<Future<ChatSession>>> _sessionResponses = {};
  final List<Future<List<String>>> _listResponses = [];
  final List<String> requestedSessions = [];
  final List<String> eventStreams = [];
  final List<String> createCalls = [];
  int listCalls = 0;
  int _activeSessionRequests = 0;
  int maxConcurrentSessionRequests = 0;
  int deleteCalls = 0;
  Future<ChatDeleteSessionResult>? deleteResponse;

  void enqueueSession(String sessionId, Future<ChatSession> response) {
    (_sessionResponses[sessionId] ??= []).add(response);
  }

  void enqueueList(Future<List<String>> response) =>
      _listResponses.add(response);

  @override
  Future<List<String>> listSessions() {
    listCalls++;
    return _listResponses.isEmpty
        ? Future<List<String>>.value(sessionIds)
        : _listResponses.removeAt(0);
  }

  @override
  Future<ChatSession> getSession(String sessionId) async {
    requestedSessions.add(sessionId);
    _activeSessionRequests++;
    if (_activeSessionRequests > maxConcurrentSessionRequests) {
      maxConcurrentSessionRequests = _activeSessionRequests;
    }
    try {
      final responses = _sessionResponses[sessionId];
      if (responses != null && responses.isNotEmpty) {
        return await responses.removeAt(0);
      }
      return sessions[sessionId] ?? _session(sessionId, '名称 $sessionId');
    } finally {
      _activeSessionRequests--;
    }
  }

  @override
  Future<ChatRun> getRun(String runId) async => runs[runId]!;

  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) {
    eventStreams.add(runId);
    return Stream<ChatRunEventSignal>.multi((controller) {});
  }

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) async {
    createCalls.add(sessionId);
    return ChatCreateRunResult('created-${createCalls.length}');
  }

  @override
  Future<ChatCancelRunResult> cancelRun(String runId) async =>
      ChatCancelRunResult(runId: runId, status: ChatRunStatus.running);

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async =>
      await _deleteSession(sessionId);

  Future<ChatDeleteSessionResult> _deleteSession(String sessionId) {
    deleteCalls++;
    return deleteResponse ??
        Future.value(
          ChatDeleteSessionResult(sessionId: sessionId, status: 'deleted'),
        );
  }
}
