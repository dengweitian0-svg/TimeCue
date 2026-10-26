import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';

void main() {
  group('ChatViewModel 会话管理', () {
    test('历史保留完整名称、按 run 时间排序并只索引正式回复摘要', () async {
      final gateway = _HistoryGateway(
        sessionIds: ['older', 'newer'],
        sessions: {
          'older': const ChatSession(
            sessionId: 'older',
            name: '完整的旧会话名称',
            runIds: ['run-old'],
          ),
          'newer': const ChatSession(
            sessionId: 'newer',
            name: '',
            runIds: ['run-new'],
          ),
        },
        runs: {
          'run-old': _run(
            'run-old',
            'older',
            '## 正式 **回复** [链接](https://example.test)\n思考内容不在此处',
            DateTime.utc(2026, 10, 10),
          ),
          'run-new': _run(
            'run-new',
            'newer',
            '更新的正式回复',
            DateTime.utc(2026, 10, 16),
          ),
        },
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final emptyDraft = model.createLocalSession();

      await model.loadHistory();

      expect(model.historyConversations.map((item) => item.sessionId), [
        'newer',
        'older',
      ]);
      expect(model.historyConversations.first.displayName, '未命名会话');
      expect(model.historyConversations.last.displayName, '完整的旧会话名称');
      expect(model.historyConversations.last.summary, '正式 回复 链接 思考内容不在此处');
      expect(
        model.historyConversations.first.latestCreatedAt,
        DateTime.utc(2026, 10, 16),
      );
      expect(model.historyConversations, isNot(contains(emptyDraft)));
      expect(model.historyLoaded, isTrue);
      expect(model.historyCompletedCount, 2);
      expect(model.historyHasFailures, isFalse);
    });

    test('刷新详情复用运行对象和连接，阅读状态按会话独立保存', () async {
      final gateway = _HistoryGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-live')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final first = model.createLocalSession();
      model.updateDraft(first.sessionId, '输入');
      await model.send(first.sessionId, model: 'model');
      final run = first.runs.single;
      final connectionCount = gateway.eventStreams;
      gateway.runs['run-live'] = ChatRun(
        runId: 'run-live',
        sessionId: first.sessionId,
        model: 'model',
        input: '输入',
        status: ChatRunStatus.running,
        output: null,
        error: null,
        createdAt: DateTime.now(),
        finishedAt: null,
      );
      gateway.sessions[first.sessionId] = ChatSession(
        sessionId: first.sessionId,
        name: '刷新后的名称',
        runIds: [run.runId!],
      );

      model.updateReadingPosition(
        first.sessionId,
        offset: 128,
        followLatest: false,
      );
      final second = model.createLocalSession();
      model.updateReadingPosition(
        second.sessionId,
        offset: 12,
        followLatest: true,
      );
      await model.refreshSession(first.sessionId);

      expect(first.name, '刷新后的名称');
      expect(identical(first.runs.single, run), isTrue);
      expect(gateway.eventStreams, connectionCount);
      expect(first.readingOffset, 128);
      expect(first.followLatest, isFalse);
      expect(second.readingOffset, 12);
      expect(second.followLatest, isTrue);
    });

    test('创建结果不明时历史刷新保留原 run，显式重试只建立一轮和一条连接', () async {
      final gateway = _HistoryGateway();
      final lostResponse = Completer<ChatCreateRunResult>();
      gateway.createResponses.add(lostResponse.future);
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-X')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '同一条输入');
      lostResponse.completeError(StateError('unknown create result'));
      await model.send(conversation.sessionId, model: 'model');
      final originalRun = conversation.runs.single;

      gateway.sessionIds.add(conversation.sessionId);
      gateway.sessions[conversation.sessionId] = ChatSession(
        sessionId: conversation.sessionId,
        name: '已接受的会话',
        runIds: ['run-X'],
      );
      gateway.runs['run-X'] = ChatRun(
        runId: 'run-X',
        sessionId: conversation.sessionId,
        model: 'model',
        input: '同一条输入',
        status: ChatRunStatus.running,
        output: null,
        error: null,
        createdAt: DateTime.utc(2026, 10, 17),
        finishedAt: null,
      );

      await model.loadHistory(refresh: true);

      expect(conversation.isLocal, isTrue);
      expect(model.historyConversations, isEmpty);
      expect(conversation.runs, [same(originalRun)]);
      expect(originalRun.runId, isNull);
      expect(model.activeRunCount, 1);
      expect(gateway.getRunCalls, 0);

      await model.retryCreate(conversation.sessionId);
      await model.refreshSession(conversation.sessionId);

      expect(gateway.createKeys, hasLength(2));
      expect(gateway.createKeys[0], gateway.createKeys[1]);
      expect(conversation.runs, [same(originalRun)]);
      expect(originalRun.runId, 'run-X');
      expect(model.activeRunCount, 1);
      expect(gateway.eventStreams, 1);
      expect(model.historyConversations, [same(conversation)]);
    });

    test('详情失败显示进度和重试，成功后空白搜索状态才可视为完整', () async {
      final gateway = _HistoryGateway(sessionIds: ['one'])
        ..sessionFailures.add(StateError('暂不可用'));
      final model = _model(gateway);
      addTearDown(model.dispose);

      await model.loadHistory();

      expect(model.historyLoaded, isTrue);
      expect(model.historyCompletedCount, 1);
      expect(model.historyHasFailures, isTrue);
      expect(model.historyConversations.single.error, '暂不可用');

      gateway.sessions['one'] = const ChatSession(
        sessionId: 'one',
        name: '恢复成功',
        runIds: [],
      );
      await model.loadHistory(refresh: true);

      expect(model.historyHasFailures, isFalse);
      expect(model.historyConversations.single.displayName, '恢复成功');
      expect(gateway.listCalls, 2);
    });

    test('运行会话拒绝删除，删除失败保留，成功后迟到索引不能恢复', () async {
      final gateway = _HistoryGateway(sessionIds: ['session-1'])
        ..sessions['session-1'] = const ChatSession(
          sessionId: 'session-1',
          name: '待删除',
          runIds: [],
        );
      final model = _model(gateway);
      addTearDown(model.dispose);
      await model.loadHistory();
      final conversation = model.historyConversations.single;
      gateway.deleteError = StateError('删除失败');

      expect(await model.deleteSession('session-1'), isFalse);
      expect(model.historyConversations.single, same(conversation));
      expect(conversation.isDeleting, isFalse);
      expect(conversation.error, '删除失败');

      gateway.deleteError = null;
      final delayedList = Completer<List<String>>();
      gateway.listResponse = delayedList.future;
      final refresh = model.loadHistory(refresh: true);
      await Future<void>.delayed(Duration.zero);
      expect(await model.deleteSession('session-1'), isTrue);
      delayedList.complete(['session-1']);
      await refresh;
      expect(model.historyConversations, isEmpty);
    });

    test('删除响应丢失后仅由确认的 session_not_found 清理会话并解除发送门禁', () async {
      final gateway = _HistoryGateway(
        sessionIds: ['delete-uncertain', 'other'],
        sessions: {
          'delete-uncertain': const ChatSession(
            sessionId: 'delete-uncertain',
            name: '删除响应丢失',
            runIds: [],
          ),
          'other': const ChatSession(
            sessionId: 'other',
            name: '保留会话',
            runIds: [],
          ),
        },
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      await model.loadHistory();
      final conversation = model.historyConversations.firstWhere(
        (item) => item.sessionId == 'delete-uncertain',
      );
      final other = model.historyConversations.firstWhere(
        (item) => item.sessionId == 'other',
      );
      model.selectSession(conversation.sessionId);
      gateway.deleteError = StateError('DELETE response lost');

      expect(await model.deleteSession(conversation.sessionId), isFalse);
      expect(model.historyConversations, contains(same(conversation)));
      expect(conversation.error, 'DELETE response lost');
      expect(model.selectedSessionId, conversation.sessionId);
      expect(model.historyHasFailures, isTrue);

      gateway.sessionFailures.add(
        const ChatHttpError(
          statusCode: 503,
          code: 'temporary_failure',
          message: '暂时不可用',
        ),
      );
      await model.refreshSession(conversation.sessionId);
      expect(model.historyConversations, contains(same(conversation)));
      expect(conversation.error, '暂时不可用');
      expect(model.selectedSessionId, conversation.sessionId);

      gateway.sessionFailures.add(
        const ChatHttpError(
          statusCode: 404,
          code: 'session_not_found',
          message: '会话不存在',
        ),
      );
      await model.refreshSession(conversation.sessionId);
      expect(
        model.historyConversations.any(
          (item) => item.sessionId == conversation.sessionId,
        ),
        isFalse,
      );
      expect(model.selectedConversation, isNotNull);
      expect(model.selectedConversation!.isLocal, isTrue);
      expect(model.selectedConversation!.draft, isEmpty);
      expect(model.historyHasFailures, isFalse);

      final local = model.createLocalSession();
      model.updateDraft(local.sessionId, '未发送草稿不受影响');
      expect(model.canSend(local.sessionId), isTrue);
      await model.loadHistory(refresh: true);
      expect(model.historyConversations.map((item) => item.sessionId), [
        'other',
      ]);
      expect(model.conversations, contains(same(local)));
      expect(local.draft, '未发送草稿不受影响');
      expect(model.canSend(local.sessionId), isTrue);
      expect(model.historyHasFailures, isFalse);
      expect(other.name, '保留会话');
    });

    test('历史刷新会核验列表外的待确认删除，暂时失败保留，明确缺失后恢复发送', () async {
      final gateway = _HistoryGateway(
        sessionIds: ['delete-uncertain', 'other'],
        sessions: {
          'delete-uncertain': const ChatSession(
            sessionId: 'delete-uncertain',
            name: '删除响应丢失',
            runIds: [],
          ),
          'other': const ChatSession(
            sessionId: 'other',
            name: '保留会话',
            runIds: [],
          ),
        },
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      await model.loadHistory();
      final local = model.createLocalSession();
      model.updateDraft(local.sessionId, '保留本地草稿');
      model.selectSession('delete-uncertain');
      gateway.deleteError = StateError('DELETE response lost');
      expect(await model.deleteSession('delete-uncertain'), isFalse);
      gateway.sessionIds.remove('delete-uncertain');
      gateway.sessionErrors['delete-uncertain'] = const ChatHttpError(
        statusCode: 503,
        code: 'temporary_failure',
        message: '暂时不可用',
      );

      await model.loadHistory(refresh: true);

      expect(model.selectedConversation!.error, '暂时不可用');
      expect(model.historyConversations, hasLength(2));
      expect(model.historyTotalCount, 2);
      expect(model.historyCompletedCount, 2);
      expect(model.canSend('other'), isFalse);
      gateway.sessionErrors['delete-uncertain'] = const ChatHttpError(
        statusCode: 404,
        code: 'session_not_found',
        message: '会话不存在',
      );
      final requestsBeforeRetry = gateway.getSessionCalls;

      await model.loadHistory(refresh: true);

      expect(gateway.getSessionCalls - requestsBeforeRetry, 2);
      expect(model.historyConversations.map((item) => item.sessionId), [
        'other',
      ]);
      expect(model.historyHasFailures, isFalse);
      expect(model.historyTotalCount, 2);
      expect(model.historyCompletedCount, 2);
      expect(model.selectedConversation!.isLocal, isTrue);
      expect(model.canSend('other'), isTrue);
      expect(model.canSend(local.sessionId), isTrue);
      expect(local.draft, '保留本地草稿');

      await model.loadHistory(refresh: true);

      expect(model.historyTotalCount, 1);
      expect(model.historyCompletedCount, 1);
      expect(model.historyConversations.map((item) => item.sessionId), [
        'other',
      ]);
    });

    test('历史刷新对仍在列表中的待确认删除只核验一次并恢复现存会话', () async {
      final gateway = _HistoryGateway(sessionIds: ['target']);
      final model = _model(gateway);
      addTearDown(model.dispose);
      await model.loadHistory();
      gateway.deleteError = StateError('DELETE response lost');
      expect(await model.deleteSession('target'), isFalse);
      gateway.sessionIds.add('target');
      final requestsBeforeRefresh = gateway.getSessionCalls;

      await model.loadHistory(refresh: true);

      expect(gateway.getSessionCalls - requestsBeforeRefresh, 1);
      expect(model.historyTotalCount, 1);
      expect(model.historyCompletedCount, 1);
      expect(model.historyConversations.single.sessionId, 'target');
      expect(model.historyHasFailures, isFalse);
      expect(model.canSend('target'), isTrue);
    });

    test('删除非当前会话不抢选中会话，也保留其草稿和运行', () async {
      final gateway = _HistoryGateway(
        sessionIds: ['delete-target'],
        sessions: {
          'delete-target': const ChatSession(
            sessionId: 'delete-target',
            name: '待删除',
            runIds: [],
          ),
        },
      );
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('current-live-run')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      await model.loadHistory();
      final current = model.createLocalSession();
      model.updateDraft(current.sessionId, '运行中的下一条草稿');
      await model.send(current.sessionId, model: 'model');
      model.updateDraft(current.sessionId, '运行期间保留的草稿');
      expect(current.activeRun, isNotNull);
      expect(model.selectedSessionId, current.sessionId);

      expect(await model.deleteSession('delete-target'), isTrue);

      expect(model.selectedConversation, same(current));
      expect(current.draft, '运行期间保留的草稿');
      expect(current.activeRun, isNotNull);
      expect(model.activeRunCount, 1);
    });

    test('session_not_found 不会清理创建结果不明的本地会话或运行名额', () async {
      final gateway = _HistoryGateway();
      gateway.createResponses.add(
        Future.error(StateError('create response lost')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final local = model.createLocalSession();
      model.updateDraft(local.sessionId, '原始输入');
      await model.send(local.sessionId, model: 'model');
      final run = local.runs.single;
      expect(run.runId, isNull);
      expect(model.activeRunCount, 1);

      gateway.sessionFailures.add(
        const ChatHttpError(
          statusCode: 404,
          code: 'session_not_found',
          message: '会话不存在',
        ),
      );
      await model.refreshSession(local.sessionId);

      expect(model.conversations, contains(same(local)));
      expect(local.isLocal, isTrue);
      expect(local.runs, [same(run)]);
      expect(model.activeRunCount, 1);
    });

    test('历史索引不确定时暂停新发送，运行中的会话不能删除', () async {
      final gateway = _HistoryGateway(sessionIds: ['occupied'])
        ..sessions['occupied'] = const ChatSession(
          sessionId: 'occupied',
          name: '占用名额',
          runIds: [],
        );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final delayedSession = Completer<ChatSession>();
      gateway.sessionOverrides.add(delayedSession.future);
      final history = model.loadHistory();
      final local = model.createLocalSession();
      model.updateDraft(local.sessionId, '等待中的输入');
      await Future<void>.delayed(Duration.zero);
      await model.send(local.sessionId, model: 'model');
      expect(gateway.createCalls, isEmpty);
      expect(local.draft, '等待中的输入');

      delayedSession.complete(
        const ChatSession(sessionId: 'occupied', name: '占用名额', runIds: []),
      );
      await history;
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('live-run')),
      );
      await model.send(local.sessionId, model: 'model');
      expect(await model.deleteSession(local.sessionId), isFalse);
      expect(gateway.deleteCalls, 0);
    });

    test('运行失效后历史、新建及删除操作不会再访问远端', () async {
      final gateway = _HistoryGateway(sessionIds: ['session-1'])
        ..sessions['session-1'] = const ChatSession(
          sessionId: 'session-1',
          name: '会话',
          runIds: [],
        );
      final model = _model(gateway);
      addTearDown(model.dispose);
      await model.loadHistory();
      final listsBefore = gateway.listCalls;
      final sessionsBefore = gateway.getSessionCalls;
      final deletesBefore = gateway.deleteCalls;
      model.invalidateRuntime();

      await model.loadHistory(refresh: true);
      await model.refreshSession('session-1');
      expect(await model.deleteSession('session-1'), isFalse);
      final local = model.createLocalSession();
      model.updateDraft(local.sessionId, '仍可保留的本地草稿');
      await model.send(local.sessionId, model: 'model');

      expect(model.acceptingOperations, isFalse);
      expect(model.runtimeError, isNotEmpty);
      expect(gateway.listCalls, listsBefore);
      expect(gateway.getSessionCalls, sessionsBefore);
      expect(gateway.deleteCalls, deletesBefore);
      expect(gateway.createCalls, isEmpty);
      expect(local.draft, '仍可保留的本地草稿');
    });

    test('历史通知随阶段变化触发，但不随同阶段文本增量触发', () async {
      final gateway = _HistoryGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-live')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      await Future<void>.delayed(Duration.zero);
      var notifications = 0;
      model.historyListenable.addListener(() => notifications++);
      final before = notifications;
      gateway.controllers.single.add(
        const ChatRunEventFrame(
          ChatSseEvent(
            eventId: 1,
            type: ChatEventType.text,
            data: ChatContentEventData(content: '增量', error: null),
          ),
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 25));
      expect(conversation.activeRun?.phase, ChatRunPhase.replying);
      expect(notifications, before + 1);
      final afterPhaseChange = notifications;

      gateway.controllers.single.add(
        const ChatRunEventFrame(
          ChatSseEvent(
            eventId: 2,
            type: ChatEventType.text,
            data: ChatContentEventData(content: '后续片段', error: null),
          ),
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 25));
      expect(conversation.activeRun?.phase, ChatRunPhase.replying);
      expect(notifications, afterPhaseChange);
    });

    test('展示隐藏时状态持续推进但不通知，恢复时各发布一次累计状态', () async {
      final gateway = _HistoryGateway();
      gateway.createResponses.add(
        Future.value(const ChatCreateRunResult('run-live')),
      );
      final model = _model(gateway);
      addTearDown(model.dispose);
      final conversation = model.createLocalSession();
      model.updateDraft(conversation.sessionId, '输入');
      await model.send(conversation.sessionId, model: 'model');
      await Future<void>.delayed(const Duration(milliseconds: 20));
      var modelNotifications = 0;
      var historyNotifications = 0;
      model.addListener(() => modelNotifications++);
      model.historyListenable.addListener(() => historyNotifications++);
      model.setPresentationVisible(false);
      final modelBefore = modelNotifications;
      final historyBefore = historyNotifications;

      gateway.controllers.single.add(
        const ChatRunEventFrame(
          ChatSseEvent(
            eventId: 1,
            type: ChatEventType.text,
            data: ChatContentEventData(content: '隐藏期间收到', error: null),
          ),
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 25));

      expect(conversation.runs.single.rawText, '隐藏期间收到');
      expect(modelNotifications, modelBefore);
      expect(historyNotifications, historyBefore);

      model.setPresentationVisible(true);
      expect(modelNotifications, modelBefore + 1);
      expect(historyNotifications, historyBefore + 1);
    });
  });
}

ChatRun _run(String id, String sessionId, String output, DateTime createdAt) =>
    ChatRun(
      runId: id,
      sessionId: sessionId,
      model: 'model',
      input: '输入',
      status: ChatRunStatus.succeeded,
      output: output,
      error: null,
      createdAt: createdAt,
      finishedAt: createdAt.add(const Duration(minutes: 1)),
    );

ChatViewModel _model(_HistoryGateway gateway) {
  var session = 0;
  return ChatViewModel(
    gateway: gateway,
    createSessionId: () => 'local-${++session}',
    createIdempotencyKey: () => 'key-$session',
    retryDelay: (_) => Duration.zero,
  );
}

class _HistoryGateway implements ChatGateway {
  _HistoryGateway({
    List<String> sessionIds = const [],
    Map<String, ChatSession> sessions = const {},
    Map<String, ChatRun> runs = const {},
  }) : sessionIds = List.of(sessionIds),
       sessions = Map.from(sessions),
       runs = Map.from(runs);

  final List<String> sessionIds;
  final Map<String, ChatSession> sessions;
  final Map<String, ChatRun> runs;
  final List<Future<ChatSession>> sessionOverrides = [];
  final List<Object> sessionFailures = [];
  final Map<String, Object> sessionErrors = {};
  final List<Future<ChatCreateRunResult>> createResponses = [];
  final List<String> createCalls = [];
  final List<String> createKeys = [];
  final List<StreamController<ChatRunEventSignal>> controllers = [];
  int listCalls = 0;
  int getSessionCalls = 0;
  int getRunCalls = 0;
  int eventStreams = 0;
  int deleteCalls = 0;
  Object? deleteError;
  Future<List<String>>? listResponse;

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    required String idempotencyKey,
  }) {
    createCalls.add(sessionId);
    createKeys.add(idempotencyKey);
    return createResponses.isEmpty
        ? Future.error(StateError('unexpected create'))
        : createResponses.removeAt(0);
  }

  @override
  Future<ChatRun> getRun(String runId) {
    getRunCalls++;
    return Future.value(runs[runId]!);
  }

  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) {
    eventStreams++;
    final controller = StreamController<ChatRunEventSignal>();
    controllers.add(controller);
    return controller.stream;
  }

  @override
  Future<ChatCancelRunResult> cancelRun(String runId) => Future.value(
    ChatCancelRunResult(runId: runId, status: ChatRunStatus.running),
  );

  @override
  Future<ChatSession> getSession(String sessionId) async {
    getSessionCalls++;
    if (sessionOverrides.isNotEmpty) return sessionOverrides.removeAt(0);
    if (sessionFailures.isNotEmpty) throw sessionFailures.removeAt(0);
    if (sessionErrors[sessionId] case final error?) throw error;
    return sessions[sessionId] ??
        ChatSession(sessionId: sessionId, name: '名称 $sessionId', runIds: []);
  }

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async {
    deleteCalls++;
    if (deleteError case final error?) throw error;
    return ChatDeleteSessionResult(sessionId: sessionId, status: 'deleted');
  }

  @override
  Future<List<String>> listSessions() async {
    listCalls++;
    if (listResponse case final response?) return response;
    return sessionIds;
  }
}
