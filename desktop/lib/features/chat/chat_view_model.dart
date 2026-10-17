import 'dart:async';
import 'dart:math';

import 'package:flutter/foundation.dart';

import '../../contracts/chat_contracts.dart';

/// 聊天运行在 UI 生命周期之外维护的受控状态模型。
class ChatViewModel extends ChangeNotifier {
  ChatViewModel({
    required ChatGateway gateway,
    String Function()? createSessionId,
    String Function()? createIdempotencyKey,
    Duration Function(int attempt)? retryDelay,
    this.maxReconnectAttempts = 4,
  }) : _gateway = gateway,
       _createSessionId = createSessionId ?? _uuidV4,
       _createIdempotencyKey = createIdempotencyKey ?? _defaultIdempotencyKey,
       _retryDelay = retryDelay ?? _defaultRetryDelay;

  final ChatGateway _gateway;
  final String Function() _createSessionId;
  final String Function() _createIdempotencyKey;
  final Duration Function(int attempt) _retryDelay;
  final int maxReconnectAttempts;
  final Map<String, ChatConversation> _conversations = {};
  final List<String> _conversationOrder = [];
  final Set<String> _historySessionIds = {};
  final Set<String> _deletedSessionIds = {};
  final Map<String, int> _sessionGenerations = {};
  final Map<String, ChatRunState> _runsById = {};
  String? _selectedSessionId;
  int _runtimeGeneration = 0;
  bool _acceptingOperations = true;
  String? _runtimeError;
  bool _historyLoading = false;
  bool _historyLoaded = false;
  String? _historyError;
  int _historyCompletedCount = 0;
  int _historyTotalCount = 0;
  Future<void>? _historyFuture;
  bool _disposed = false;
  bool _presentationVisible = true;
  final ChangeNotifier _historyNotifier = ChangeNotifier();
  bool _notifyScheduled = false;
  Timer? _notifyTimer;

  /// 当前保留的会话；列表及其子状态均不可变。
  List<ChatConversation> get conversations => List.unmodifiable(
    _conversationOrder.map((id) => _conversations[id]!).toList(),
  );

  /// 已从服务端索引的会话，按最新 run 创建时间倒序排列。
  List<ChatConversation> get historyConversations => List.unmodifiable(
    _historySessionIds
        .map((id) => _conversations[id])
        .whereType<ChatConversation>()
        .toList()
      ..sort((a, b) => b.latestCreatedAt.compareTo(a.latestCreatedAt)),
  );

  /// 是否正在加载历史索引。
  bool get historyLoading => _historyLoading;

  /// 是否完成过一次历史索引；部分失败仍会标记为已加载。
  bool get historyLoaded => _historyLoaded;

  /// 历史会话 ID 列表整体读取失败时的错误。
  String? get historyError => _historyError;

  /// 已处理的历史会话数量。
  int get historyCompletedCount => _historyCompletedCount;

  /// 本轮历史索引需要处理的会话数量。
  int get historyTotalCount => _historyTotalCount;

  /// 是否至少有一个会话详情或运行快照读取失败。
  bool get historyHasFailures =>
      historyConversations.any((conversation) => conversation.error != null);

  /// 后端运行身份失效时用于解释禁用操作的原因。
  String? get runtimeError => _runtimeError;

  /// 当前运行身份是否仍允许发起服务端操作。
  bool get acceptingOperations => _acceptingOperations && !_disposed;

  /// 仅在历史索引、进度或列表元数据变化时通知，避免 SSE 增量重建历史树。
  Listenable get historyListenable => _historyNotifier;

  /// 当前选中的会话；未选择时返回 null。
  ChatConversation? get selectedConversation =>
      _selectedSessionId == null ? null : _conversations[_selectedSessionId];

  /// 当前选中会话的 ID。
  String? get selectedSessionId => _selectedSessionId;

  /// 当前仍占用并发名额的 run 数量。
  int get activeRunCount => _allRuns.where((run) => run.occupiesSlot).length;

  Iterable<ChatRunState> get _allRuns sync* {
    for (final conversation in _conversations.values) {
      yield* conversation.runs;
    }
  }

  /// 返回会话是否可以开始新一轮。
  bool canSend(String sessionId) {
    final conversation = _conversations[sessionId];
    if (!_acceptingOperations || conversation == null) return false;
    if (conversation.isDeleting ||
        _historyLoading ||
        _historyError != null ||
        historyHasFailures ||
        (_historySessionIds.contains(sessionId) &&
            !conversation.historyLoaded)) {
      return false;
    }
    if (conversation.activeRun != null) return false;
    return activeRunCount < 3;
  }

  /// 读取历史会话并建立名称及正式回复摘要索引；并发最多四个会话。
  Future<void> loadHistory({bool refresh = false}) async {
    _ensureUsable();
    if (!acceptingOperations) return;
    if (_historyLoading) {
      if (!refresh && _historyFuture != null) return _historyFuture;
      return;
    }
    if (_historyLoaded && !refresh) return;
    final generation = _runtimeGeneration;
    _historyLoading = true;
    _historyError = null;
    _historyCompletedCount = 0;
    _historyTotalCount = 0;
    _scheduleNotify();
    _scheduleHistoryNotify();
    final future = _loadHistory(generation);
    _historyFuture = future;
    await future;
  }

  Future<void> _loadHistory(int generation) async {
    try {
      final ids = await _gateway.listSessions();
      if (!_isRuntimeCurrent(generation)) return;
      final uniqueIds = ids
          .toSet()
          .where((id) => !_deletedSessionIds.contains(id))
          .toList();
      _historyTotalCount = uniqueIds.length;
      for (final sessionId in uniqueIds) {
        _registerHistoryPlaceholder(sessionId);
      }
      _scheduleNotify();
      _scheduleHistoryNotify();
      final queue = List<String>.from(uniqueIds);
      final workers = List<Future<void>>.generate(min(4, queue.length), (
        _,
      ) async {
        while (queue.isNotEmpty && _isRuntimeCurrent(generation)) {
          final sessionId = queue.removeAt(0);
          await _loadHistorySession(sessionId, generation);
          if (!_isRuntimeCurrent(generation)) return;
          _historyCompletedCount++;
          _scheduleNotify();
          _scheduleHistoryNotify();
        }
      });
      await Future.wait(workers);
      if (!_isRuntimeCurrent(generation)) return;
      _historyLoaded = true;
      _scheduleHistoryNotify();
    } catch (error) {
      if (_isRuntimeCurrent(generation)) _historyError = _errorMessage(error);
      _scheduleHistoryNotify();
    } finally {
      if (_isRuntimeCurrent(generation)) {
        _historyLoading = false;
        _historyFuture = null;
        _scheduleNotify();
        _scheduleHistoryNotify();
      }
    }
  }

  Future<void> _loadHistorySession(String sessionId, int generation) async {
    if (_deletedSessionIds.contains(sessionId)) return;
    final sessionGeneration = (_sessionGenerations[sessionId] ?? 0) + 1;
    _sessionGenerations[sessionId] = sessionGeneration;
    final existing = _conversations.putIfAbsent(
      sessionId,
      () => ChatConversation._(sessionId: sessionId, isLocal: false),
    );
    if (existing.isDeleting) return;
    final creationInProgress = _hasUnconfirmedCreation(existing);
    if (!creationInProgress) {
      _registerHistoryPlaceholder(sessionId, existing: existing);
      existing._historyLoaded = false;
      _scheduleNotify();
      _scheduleHistoryNotify();
    }
    try {
      final session = await _gateway.getSession(sessionId);
      if (!_isSessionCurrent(generation, sessionId, sessionGeneration)) return;
      if (session.sessionId != sessionId) {
        throw StateError('会话详情响应的 session ID 与当前会话不匹配');
      }
      // 创建结果不明时，run_ids 可能已包含同一幂等请求创建的 run。
      // 契约没有幂等键可用于匹配，因此保留原占位，等待用户显式重试确认。
      if (_hasUnconfirmedCreation(existing)) return;
      existing._name = session.name;
      existing._error = null;
      _scheduleNotify();
      _scheduleHistoryNotify();
      // 保持服务端顺序，并复用已运行的 run，避免重置游标或连接。
      final orderedRuns = <ChatRunState>[];
      final runsToConnect = <ChatRunState>[];
      final newlyLoadedRuns = <String, ChatRunState>{};
      for (final runId in session.runIds) {
        final cached = _runsById[runId];
        if (cached != null) {
          if (cached.sessionId != sessionId) {
            throw StateError('缓存运行所属的 session ID 与当前会话不匹配');
          }
          if (!cached.isTerminal) {
            final snapshot = await _gateway.getRun(runId);
            if (!_isSessionCurrent(generation, sessionId, sessionGeneration)) {
              return;
            }
            if (_hasUnconfirmedCreation(existing)) return;
            if (snapshot.runId != runId || snapshot.sessionId != sessionId) {
              throw StateError('运行快照身份与当前会话不匹配');
            }
            _mergeSnapshot(cached, snapshot);
            if (!cached.isTerminal &&
                snapshot.status == ChatRunStatus.running) {
              runsToConnect.add(cached);
            }
          }
          orderedRuns.add(cached);
          continue;
        }
        final snapshot = await _gateway.getRun(runId);
        if (!_isSessionCurrent(generation, sessionId, sessionGeneration)) {
          return;
        }
        if (_hasUnconfirmedCreation(existing)) return;
        if (snapshot.runId != runId || snapshot.sessionId != sessionId) {
          throw StateError('运行快照身份与当前会话不匹配');
        }
        final state = ChatRunState._fromSnapshot(snapshot);
        newlyLoadedRuns[runId] = state;
        orderedRuns.add(state);
        if (snapshot.status == ChatRunStatus.running) {
          runsToConnect.add(state);
        }
      }
      final serverRunIds = session.runIds.toSet();
      for (final current in existing._runs) {
        if (current.runId == null || !serverRunIds.contains(current.runId)) {
          if (!orderedRuns.contains(current)) orderedRuns.add(current);
        }
      }
      if (_hasUnconfirmedCreation(existing)) return;
      _runsById.addAll(newlyLoadedRuns);
      existing._runs
        ..clear()
        ..addAll(orderedRuns);
      existing._historyLoaded = true;
      for (final run in runsToConnect) {
        unawaited(_connect(run, generation));
      }
    } catch (error) {
      if (!_isSessionCurrent(generation, sessionId, sessionGeneration)) return;
      existing._error = _errorMessage(error);
    }
    _scheduleNotify();
    _scheduleHistoryNotify();
  }

  void _registerHistoryPlaceholder(
    String sessionId, {
    ChatConversation? existing,
  }) {
    if (_deletedSessionIds.contains(sessionId)) return;
    final conversation =
        existing ??
        _conversations.putIfAbsent(
          sessionId,
          () => ChatConversation._(sessionId: sessionId, isLocal: false),
        );
    if (conversation.isDeleting || _hasUnconfirmedCreation(conversation)) {
      return;
    }
    conversation._isLocal = false;
    _historySessionIds.add(sessionId);
    if (!_conversationOrder.contains(sessionId)) {
      _conversationOrder.add(sessionId);
    }
  }

  /// 刷新单个会话详情及正式 run 快照，旧代次响应不会覆盖新结果。
  Future<void> refreshSession(String sessionId) async {
    _ensureUsable();
    if (!acceptingOperations ||
        _deletedSessionIds.contains(sessionId) ||
        _conversations[sessionId]?.isDeleting == true) {
      return;
    }
    await _loadHistorySession(sessionId, _runtimeGeneration);
  }

  /// 删除服务端会话；运行仍占用名额时拒绝，成功后用墓碑阻止迟到恢复。
  Future<bool> deleteSession(String sessionId) async {
    _ensureUsable();
    final conversation = _conversations[sessionId];
    if (!acceptingOperations ||
        conversation == null ||
        conversation.isLocal ||
        conversation.activeRun != null ||
        conversation.isDeleting ||
        (_historySessionIds.contains(sessionId) &&
            !conversation.historyLoaded)) {
      return false;
    }
    final generation = _runtimeGeneration;
    _sessionGenerations[sessionId] = (_sessionGenerations[sessionId] ?? 0) + 1;
    conversation._isDeleting = true;
    _scheduleNotify();
    _scheduleHistoryNotify();
    try {
      final result = await _gateway.deleteSession(sessionId);
      if (!_isRuntimeCurrent(generation)) return false;
      if (result.sessionId != sessionId) {
        throw StateError('删除响应的 session ID 与当前会话不匹配');
      }
      _deletedSessionIds.add(sessionId);
      _historySessionIds.remove(sessionId);
      _conversations.remove(sessionId);
      _conversationOrder.remove(sessionId);
      for (final runId in conversation._runs.map((run) => run.runId)) {
        if (runId != null) _runsById.remove(runId);
      }
      _sessionGenerations[sessionId] =
          (_sessionGenerations[sessionId] ?? 0) + 1;
      if (_selectedSessionId == sessionId) _selectedSessionId = null;
      _scheduleNotify();
      _scheduleHistoryNotify();
      return true;
    } catch (error) {
      if (_isRuntimeCurrent(generation)) {
        conversation._isDeleting = false;
        conversation._error = _errorMessage(error);
        _scheduleNotify();
        _scheduleHistoryNotify();
      }
      return false;
    }
  }

  /// 更新会话独立阅读位置及是否跟随最新消息。
  void updateReadingPosition(
    String sessionId, {
    required double offset,
    required bool followLatest,
  }) {
    if (_disposed) return;
    final conversation = _conversations[sessionId];
    if (conversation == null) return;
    conversation._readingOffset = offset.isFinite ? max(0, offset) : 0;
    conversation._followLatest = followLatest;
  }

  /// 新建仅保存在本地的空会话；首次发送时才请求服务端。
  ChatConversation createLocalSession() {
    _ensureUsable();
    final id = _createSessionId();
    if (_conversations.containsKey(id)) {
      throw StateError('生成了重复的聊天会话 ID');
    }
    final conversation = ChatConversation._(sessionId: id, isLocal: true);
    _conversations[id] = conversation;
    _conversationOrder.add(id);
    _selectedSessionId = id;
    _scheduleNotify();
    _scheduleHistoryNotify();
    return conversation;
  }

  /// 在本地运行期会话间切换，不会关闭或重建 run 连接。
  void selectSession(String sessionId) {
    _ensureUsable();
    if (!_conversations.containsKey(sessionId)) {
      throw ArgumentError.value(sessionId, 'sessionId', '未知会话');
    }
    if (_selectedSessionId == sessionId) return;
    _selectedSessionId = sessionId;
    _scheduleNotify();
    _scheduleHistoryNotify();
  }

  /// 更新指定会话的独立输入草稿。
  void updateDraft(String sessionId, String text) {
    final conversation = _requireConversation(sessionId);
    conversation._draft = text;
    _scheduleNotify();
  }

  /// 使用草稿开始一轮；创建中的内容和幂等键会被固定以供明确重试。
  Future<void> send(String sessionId, {required String model}) async {
    _ensureUsable();
    final conversation = _requireConversation(sessionId);
    final input = conversation._draft;
    if (input.trim().isEmpty || !canSend(sessionId)) return;

    final key = _createIdempotencyKey();
    final run = ChatRunState._(
      messageId: key,
      idempotencyKey: key,
      sessionId: conversation.sessionId,
      input: input,
      model: model,
      createdAt: DateTime.now(),
      phase: ChatRunPhase.creating,
      connectionState: ChatConnectionState.idle,
    );
    conversation._runs.add(run);
    conversation._draft = '';
    conversation._error = null;
    _scheduleNotify();
    _scheduleHistoryNotify();
    await _createRun(conversation, run);
  }

  /// 对结果不明的创建请求复用原输入、模型、session ID 和幂等键。
  Future<void> retryCreate(String sessionId) async {
    _ensureUsable();
    final conversation = _requireConversation(sessionId);
    final run = conversation._runs.lastOrNull;
    if (run == null || run.runId != null || !run._creationUncertain) return;
    if (!_acceptingOperations) return;
    run._error = null;
    run._phase = ChatRunPhase.creating;
    run._creationUncertain = false;
    _scheduleNotify();
    _scheduleHistoryNotify();
    await _createRun(conversation, run);
  }

  /// 请求停止当前 run；服务端确认终态前继续占用并发名额。
  Future<void> cancel(String sessionId) async {
    _ensureUsable();
    if (!_acceptingOperations) return;
    final run = _requireConversation(sessionId).activeRun;
    if (run == null || run.runId == null || run._cancelPending) return;
    final generation = _runtimeGeneration;
    run._cancelPending = true;
    run._phase = ChatRunPhase.stopping;
    run._error = null;
    _scheduleNotify();
    _scheduleHistoryNotify();
    try {
      final result = await _gateway.cancelRun(run.runId!);
      if (!_isCurrent(generation, run)) return;
      if (run.isTerminal) return;
      if (result.runId != run.runId) {
        throw StateError('取消响应的 run ID 与当前运行不匹配');
      }
      if (result.status == ChatRunStatus.running) {
        run._cancelPending = true;
        run._phase = ChatRunPhase.stopping;
      } else {
        await _loadRunSnapshot(run, generation);
      }
    } catch (error) {
      if (!_isCurrent(generation, run)) return;
      if (run.isTerminal) return;
      run._cancelPending = false;
      run._error = _errorMessage(error);
      run._phase = run._statusPhase;
    }
    _scheduleNotify();
    _scheduleHistoryNotify();
  }

  /// 在自动重连预算耗尽后，按原游标人工恢复单个 run 的连接。
  Future<void> retryConnection(String sessionId) async {
    _ensureUsable();
    final run = _requireConversation(sessionId).activeRun;
    if (!_acceptingOperations ||
        run == null ||
        run.runId == null ||
        run._subscription != null) {
      return;
    }
    run._retryTimer?.cancel();
    run._retryTimer = null;
    run._reconnectAttempts = 0;
    await _connect(run, _runtimeGeneration);
  }

  /// 阻止新的用户操作，但保留已知状态及其占用事实。
  void stopNewWork() {
    _acceptingOperations = false;
  }

  /// 控制界面通知；隐藏期间继续处理事件，重新显示时发布一次最新状态。
  void setPresentationVisible(bool visible) {
    if (_disposed || _presentationVisible == visible) return;
    _presentationVisible = visible;
    if (!visible) {
      _notifyTimer?.cancel();
      _notifyTimer = null;
      _notifyScheduled = false;
      return;
    }
    notifyListeners();
    _historyNotifier.notifyListeners();
  }

  /// 隔离当前运行代次并释放事件连接，供宿主退出或后端失效时调用。
  void invalidateRuntime() {
    ++_runtimeGeneration;
    _acceptingOperations = false;
    _runtimeError ??= '聊天服务已失效，请重新打开应用后继续。';
    _historyLoading = false;
    _historyFuture = null;
    for (final run in _allRuns) {
      run._retryTimer?.cancel();
      run._retryTimer = null;
      final subscription = run._subscription;
      run._subscription = null;
      if (subscription != null) unawaited(subscription.cancel());
      if (!run.isTerminal && run.runId != null) {
        run._connectionState = ChatConnectionState.disconnected;
      }
    }
    _scheduleNotify();
    _scheduleHistoryNotify();
  }

  Future<void> _createRun(
    ChatConversation conversation,
    ChatRunState run,
  ) async {
    if (!acceptingOperations) return;
    final generation = _runtimeGeneration;
    try {
      final result = await _gateway.createRun(
        sessionId: conversation.sessionId,
        input: run.input,
        model: run.model,
        idempotencyKey: run.idempotencyKey,
      );
      if (!_isCurrent(generation, run)) return;
      run._runId = result.runId;
      run._creationUncertain = false;
      run._status = ChatRunStatus.running;
      run._phase = ChatRunPhase.connecting;
      conversation._isLocal = false;
      conversation._historyLoaded = true;
      _historySessionIds.add(conversation.sessionId);
      run._createdAt = DateTime.now();
      _runsById[result.runId] = run;
      _scheduleNotify();
      _scheduleHistoryNotify();
      if (!_acceptingOperations) return;
      unawaited(_refreshSessionName(conversation, generation));
      await _connect(run, generation);
    } catch (error) {
      if (!_isCurrent(generation, run)) return;
      if (_isAmbiguousCreationFailure(error)) {
        run._creationUncertain = true;
        run._phase = ChatRunPhase.creating;
        run._error = _errorMessage(error);
      } else {
        run._status = ChatRunStatus.failed;
        run._phase = ChatRunPhase.failed;
        run._error = _errorMessage(error);
      }
      _scheduleNotify();
      _scheduleHistoryNotify();
    }
  }

  Future<void> _refreshSessionName(
    ChatConversation conversation,
    int generation,
  ) async {
    final sessionGeneration =
        (_sessionGenerations[conversation.sessionId] ?? 0) + 1;
    _sessionGenerations[conversation.sessionId] = sessionGeneration;
    try {
      final session = await _gateway.getSession(conversation.sessionId);
      if (!_isSessionCurrent(
        generation,
        conversation.sessionId,
        sessionGeneration,
      )) {
        return;
      }
      if (_conversations[conversation.sessionId] != conversation) return;
      if (session.sessionId != conversation.sessionId) {
        throw StateError('会话名称响应的 session ID 与当前会话不匹配');
      }
      conversation._name = session.name;
      conversation._error = null;
    } catch (error) {
      if (!_isSessionCurrent(
        generation,
        conversation.sessionId,
        sessionGeneration,
      )) {
        return;
      }
      if (_conversations[conversation.sessionId] != conversation) return;
      conversation._error = _errorMessage(error);
    }
    _scheduleNotify();
    _scheduleHistoryNotify();
  }

  Future<void> _connect(ChatRunState run, int generation) async {
    if (!_acceptingOperations ||
        !_isCurrent(generation, run) ||
        run.runId == null ||
        run.isTerminal) {
      return;
    }
    if (run._retryTimer != null) return;
    if (run._subscription != null) return;
    final previousPhase = run._phase;
    final previousConnection = run._connectionState;
    final connectionEpoch = ++run._connectionEpoch;
    run._connectionState = run._reconnectAttempts == 0
        ? ChatConnectionState.connected
        : ChatConnectionState.reconnecting;
    if (run._reconnectAttempts > 0 && !run._cancelPending) {
      run._phase = ChatRunPhase.reconnecting;
    }
    _scheduleNotify();
    _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
    try {
      final stream = _gateway.runEvents(
        run.runId!,
        lastEventId: run.lastEventId == 0 ? null : run.lastEventId,
      );
      run._subscription = stream.listen(
        (signal) {
          run._eventQueue = run._eventQueue.then((_) async {
            await _handleSignal(run, signal, generation, connectionEpoch);
          });
        },
        onError: (Object error, StackTrace stack) async {
          run._eventQueue = run._eventQueue.then((_) async {
            await _connectionLost(run, error, generation, connectionEpoch);
          });
          await run._eventQueue;
        },
        onDone: () async {
          run._eventQueue = run._eventQueue.then((_) async {
            if (_isCurrent(generation, run) &&
                run._connectionEpoch == connectionEpoch &&
                !run.isTerminal) {
              await _connectionLost(
                run,
                StateError('事件连接已关闭'),
                generation,
                connectionEpoch,
              );
            }
          });
          await run._eventQueue;
        },
        cancelOnError: true,
      );
    } catch (error) {
      await _connectionLost(run, error, generation, connectionEpoch);
    }
  }

  Future<void> _handleSignal(
    ChatRunState run,
    ChatRunEventSignal signal,
    int generation,
    int connectionEpoch,
  ) async {
    if (!_isCurrent(generation, run) ||
        run._connectionEpoch != connectionEpoch ||
        run.isTerminal) {
      return;
    }
    final previousPhase = run._phase;
    final previousConnection = run._connectionState;
    if (signal is ChatRunEventsNoContent) {
      ++run._connectionEpoch;
      final subscription = run._subscription;
      run._subscription = null;
      if (subscription != null) await subscription.cancel();
      _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
      if (!_acceptingOperations) return;
      await _loadRunSnapshot(run, generation);
      if (_isCurrent(generation, run) && !run.isTerminal) {
        _scheduleReconnect(run, generation, StateError('终态查询仍返回运行中'));
      }
      return;
    }
    final event = (signal as ChatRunEventFrame).event;
    if (event.eventId <= run.lastEventId) return;
    if (event.eventId != run.lastEventId + 1) {
      final subscription = run._subscription;
      run._subscription = null;
      ++run._connectionEpoch;
      if (subscription != null) await subscription.cancel();
      _scheduleReconnect(run, generation, StateError('事件序号存在缺口'));
      _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
      return;
    }

    switch (event.type) {
      case ChatEventType.start:
        if (!run._cancelPending) run._phase = ChatRunPhase.running;
      case ChatEventType.thinking:
        final data = event.data as ChatContentEventData;
        if (data.error != null) run._error = data.error!.message;
        if (!run._cancelPending) run._phase = ChatRunPhase.thinking;
      case ChatEventType.text:
        final data = event.data as ChatContentEventData;
        if (data.error != null) run._error = data.error!.message;
        if (!run._cancelPending) run._phase = ChatRunPhase.replying;
        run._rawText += data.content;
      case ChatEventType.tool:
        final tool = event.data as ChatToolEventData;
        if (!run._toolIds.contains(tool.id)) {
          run._toolIds.add(tool.id);
          run._toolCalls.add(
            ChatToolCall(id: tool.id, name: tool.function.name),
          );
        }
        if (!run._cancelPending) run._phase = ChatRunPhase.toolCalling;
      case ChatEventType.end:
        run._lastEventId = event.eventId;
        run._connectionState = ChatConnectionState.disconnected;
        ++run._connectionEpoch;
        final subscription = run._subscription;
        run._subscription = null;
        if (subscription != null) await subscription.cancel();
        _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
        _scheduleNotify();
        if (!_acceptingOperations) return;
        await _loadRunSnapshot(run, generation);
        if (_isCurrent(generation, run) && !run.isTerminal) {
          _scheduleReconnect(run, generation, StateError('结束事件后终态尚未可读'));
        }
        return;
    }
    run._lastEventId = event.eventId;
    run._status = ChatRunStatus.running;
    _scheduleNotify();
    _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
  }

  Future<void> _loadRunSnapshot(ChatRunState run, int generation) async {
    if (run.runId == null) return;
    final previousPhase = run._phase;
    final previousConnection = run._connectionState;
    try {
      final snapshot = await _gateway.getRun(run.runId!);
      if (!_isCurrent(generation, run)) return;
      if (run.isTerminal) return;
      if (snapshot.runId != run.runId || snapshot.sessionId != run.sessionId) {
        throw StateError('运行快照身份与当前会话不匹配');
      }
      run._status = snapshot.status;
      run._createdAt = snapshot.createdAt;
      if (snapshot.error != null) {
        run._error = snapshot.error!.message;
      } else if (snapshot.status != ChatRunStatus.running) {
        run._error = null;
      }
      if (snapshot.status == ChatRunStatus.running) {
        run._phase = run._cancelPending
            ? ChatRunPhase.stopping
            : run._reconnectAttempts > 0
            ? ChatRunPhase.reconnecting
            : ChatRunPhase.running;
      } else {
        run._rawText = snapshot.output ?? '';
        run._cancelPending = false;
        run._phase = switch (snapshot.status) {
          ChatRunStatus.succeeded => ChatRunPhase.succeeded,
          ChatRunStatus.failed => ChatRunPhase.failed,
          ChatRunStatus.cancelled => ChatRunPhase.cancelled,
          ChatRunStatus.running => ChatRunPhase.running,
        };
        run._connectionState = ChatConnectionState.idle;
        run._retryTimer?.cancel();
        run._retryTimer = null;
      }
    } catch (error) {
      if (!_isCurrent(generation, run)) return;
      run._error = _errorMessage(error);
    }
    _scheduleNotify();
    _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
  }

  void _mergeSnapshot(ChatRunState run, ChatRun snapshot) {
    if (run.isTerminal) return;
    run._createdAt = snapshot.createdAt;
    if (snapshot.status == ChatRunStatus.running) {
      run._status = ChatRunStatus.running;
      run._error = snapshot.error?.message;
      return;
    }
    run._status = snapshot.status;
    run._rawText = snapshot.output ?? '';
    run._error = snapshot.error?.message;
    run._cancelPending = false;
    run._phase = switch (snapshot.status) {
      ChatRunStatus.succeeded => ChatRunPhase.succeeded,
      ChatRunStatus.failed => ChatRunPhase.failed,
      ChatRunStatus.cancelled => ChatRunPhase.cancelled,
      ChatRunStatus.running => ChatRunPhase.running,
    };
    run._connectionState = ChatConnectionState.idle;
    run._retryTimer?.cancel();
    run._retryTimer = null;
    ++run._connectionEpoch;
    final subscription = run._subscription;
    run._subscription = null;
    if (subscription != null) unawaited(subscription.cancel());
  }

  Future<void> _connectionLost(
    ChatRunState run,
    Object error,
    int generation,
    int connectionEpoch,
  ) async {
    if (!_isCurrent(generation, run) ||
        run._connectionEpoch != connectionEpoch ||
        run.isTerminal) {
      return;
    }
    final previousPhase = run._phase;
    final previousConnection = run._connectionState;
    ++run._connectionEpoch;
    final subscription = run._subscription;
    run._subscription = null;
    if (subscription != null) await subscription.cancel();
    run._connectionState = ChatConnectionState.disconnected;
    run._error = _errorMessage(error);
    _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
    if (!_acceptingOperations) {
      _scheduleNotify();
      return;
    }
    await _loadRunSnapshot(run, generation);
    if (_isCurrent(generation, run) && !run.isTerminal) {
      _scheduleReconnect(run, generation, error);
    }
  }

  void _scheduleReconnect(ChatRunState run, int generation, Object error) {
    if (!_isCurrent(generation, run) || run.isTerminal) return;
    if (!_acceptingOperations) return;
    final previousPhase = run._phase;
    final previousConnection = run._connectionState;
    run._connectionState = ChatConnectionState.disconnected;
    run._error ??= _errorMessage(error);
    if (run._reconnectAttempts >= maxReconnectAttempts) {
      run._phase = run._cancelPending
          ? ChatRunPhase.stopping
          : ChatRunPhase.reconnecting;
      _scheduleNotify();
      _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
      return;
    }
    if (run._retryTimer != null) {
      _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
      return;
    }
    final attempt = ++run._reconnectAttempts;
    run._phase = run._cancelPending
        ? ChatRunPhase.stopping
        : ChatRunPhase.reconnecting;
    run._connectionState = ChatConnectionState.reconnecting;
    run._retryTimer?.cancel();
    run._retryTimer = Timer(_retryDelay(attempt), () {
      run._retryTimer = null;
      if (!_acceptingOperations) return;
      unawaited(_connect(run, generation));
    });
    _scheduleNotify();
    _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
  }

  void _notifyHistoryOnRunStateChange(
    ChatRunState run,
    ChatRunPhase previousPhase,
    ChatConnectionState previousConnection,
  ) {
    if (run._phase != previousPhase ||
        run._connectionState != previousConnection) {
      _scheduleHistoryNotify();
    }
  }

  bool _isCurrent(int generation, ChatRunState run) =>
      !_disposed &&
      generation == _runtimeGeneration &&
      _conversations.values.any(
        (conversation) => conversation._runs.contains(run),
      );

  bool _isRuntimeCurrent(int generation) =>
      !_disposed && generation == _runtimeGeneration && _acceptingOperations;

  bool _isSessionCurrent(
    int generation,
    String sessionId,
    int sessionGeneration,
  ) =>
      _isRuntimeCurrent(generation) &&
      !_deletedSessionIds.contains(sessionId) &&
      _conversations[sessionId]?.isDeleting != true &&
      _sessionGenerations[sessionId] == sessionGeneration;

  bool _hasUnconfirmedCreation(ChatConversation conversation) =>
      conversation._runs.any((run) => !run.isTerminal && run.runId == null);

  ChatConversation _requireConversation(String sessionId) {
    final conversation = _conversations[sessionId];
    if (conversation == null) throw ArgumentError.value(sessionId, 'sessionId');
    return conversation;
  }

  void _scheduleNotify() {
    if (_disposed || !_presentationVisible || _notifyScheduled) return;
    _notifyScheduled = true;
    _notifyTimer = Timer(const Duration(milliseconds: 16), () {
      _notifyTimer = null;
      _notifyScheduled = false;
      if (!_disposed && _presentationVisible) notifyListeners();
    });
  }

  void _scheduleHistoryNotify() {
    if (_disposed || !_presentationVisible) return;
    scheduleMicrotask(() {
      if (!_disposed && _presentationVisible) {
        _historyNotifier.notifyListeners();
      }
    });
  }

  void _ensureUsable() {
    if (_disposed) throw StateError('ChatViewModel 已释放');
  }

  @override
  void dispose() {
    _disposed = true;
    ++_runtimeGeneration;
    _notifyTimer?.cancel();
    _notifyTimer = null;
    for (final run in _allRuns) {
      run._retryTimer?.cancel();
      final subscription = run._subscription;
      run._subscription = null;
      if (subscription != null) unawaited(subscription.cancel());
    }
    _historyNotifier.dispose();
    super.dispose();
  }
}

/// 会话的 UI 只读状态。
class ChatConversation {
  ChatConversation._({required this.sessionId, required bool isLocal})
    : _isLocal = isLocal;

  /// 会话的本地及服务端身份。
  final String sessionId;
  String _name = '';

  /// 服务端提供的会话名称，空字符串表示服务端名称为空。
  String get name => _name;

  /// 对列表和页头使用的名称；空名称使用本地化占位，不改写服务端值。
  String get displayName => _isLocal
      ? '新会话'
      : _name.isEmpty
      ? '未命名会话'
      : _name;
  bool _isLocal;

  /// 会话是否仍是尚未首次发送的本地草稿。
  bool get isLocal => _isLocal;
  String _draft = '';

  /// 当前会话独立保存的输入草稿。
  String get draft => _draft;
  String? _error;

  /// 会话名称读取失败时显示的内联错误。
  String? get error => _error;
  bool _historyLoaded = false;

  /// 此会话详情和 run 快照是否已成功读取。
  bool get historyLoaded => _historyLoaded;
  bool _isDeleting = false;

  /// 服务端删除请求是否正在进行。
  bool get isDeleting => _isDeleting;
  double _readingOffset = 0;

  /// 会话独立保存的消息阅读位置。
  double get readingOffset => _readingOffset;
  bool _followLatest = true;

  /// 阅读位置是否仍跟随最新消息。
  bool get followLatest => _followLatest;
  final List<ChatRunState> _runs = [];

  /// 当前会话已有的运行记录。
  List<ChatRunState> get runs => List.unmodifiable(_runs);

  /// 最近正式成功回复的纯文本摘要，最多四十个字符。
  String get summary {
    final completed =
        _runs
            .where(
              (run) =>
                  (run.status == ChatRunStatus.succeeded ||
                      run.status == ChatRunStatus.cancelled) &&
                  run.rawText.trim().isNotEmpty,
            )
            .toList()
          ..sort((a, b) => b.createdAt.compareTo(a.createdAt));
    return completed.isEmpty ? '' : _plainSummary(completed.first.rawText);
  }

  /// 会话最新 run 的服务端创建时间；空会话使用本地创建时刻。
  DateTime get latestCreatedAt => _runs.isEmpty
      ? _createdAt
      : _runs
            .map((run) => run.createdAt)
            .reduce((left, right) => left.isAfter(right) ? left : right);
  final DateTime _createdAt = DateTime.now();

  /// 当前会话唯一的非终态 run。
  ChatRunState? get activeRun =>
      _runs.where((run) => !run.isTerminal).firstOrNull;
}

/// 单轮用户输入、助手正文及传输过程的 UI 只读状态。
class ChatRunState {
  ChatRunState._({
    required String messageId,
    required this.idempotencyKey,
    required this.sessionId,
    required this.input,
    required this.model,
    required DateTime createdAt,
    required ChatRunPhase phase,
    required ChatConnectionState connectionState,
  }) : _messageId = messageId,
       _createdAt = createdAt,
       _phase = phase,
       _connectionState = connectionState;

  factory ChatRunState._fromSnapshot(ChatRun snapshot) {
    final state = ChatRunState._(
      messageId: snapshot.runId,
      idempotencyKey: snapshot.runId,
      sessionId: snapshot.sessionId,
      input: snapshot.input,
      model: snapshot.model,
      createdAt: snapshot.createdAt,
      phase: switch (snapshot.status) {
        ChatRunStatus.running => ChatRunPhase.running,
        ChatRunStatus.succeeded => ChatRunPhase.succeeded,
        ChatRunStatus.failed => ChatRunPhase.failed,
        ChatRunStatus.cancelled => ChatRunPhase.cancelled,
      },
      connectionState: ChatConnectionState.idle,
    );
    state._runId = snapshot.runId;
    state._status = snapshot.status;
    state._rawText = snapshot.output ?? '';
    state._error = snapshot.error?.message;
    return state;
  }

  DateTime _createdAt;

  /// run 的服务端创建时间，用于历史排序和日期分组。
  DateTime get createdAt => _createdAt;

  final String _messageId;

  /// UI 消息列表的稳定身份；从创建请求到终态都保持原幂等键。
  String get messageId => _messageId;

  /// 创建请求使用的幂等键，在结果不明时供原样重试。
  final String idempotencyKey;

  /// 所属会话的身份，用于校验服务端 run 快照。
  final String sessionId;
  String? _runId;

  /// 服务端确认创建后的 run ID；创建结果不明时为 null。
  String? get runId => _runId;

  /// 本轮提交时固定的原始用户输入。
  final String input;

  /// 本轮请求使用的模型标识。
  final String model;
  String _rawText = '';

  /// 当前已累计的原始助手正文；事件重放只拼接尚未处理的部分。
  String get rawText => _rawText;

  ChatRunStatus? _status;

  /// 服务端 run 状态；尚未确认创建时为 null。
  ChatRunStatus? get status => _status;
  ChatRunPhase _phase;

  /// 当前创建、生成、停止或终态阶段。
  ChatRunPhase get phase => _phase;
  ChatConnectionState _connectionState;

  /// 当前 SSE 连接状态，与服务端 run 状态分别维护。
  ChatConnectionState get connectionState => _connectionState;
  String? _error;

  /// 当前 run 的内联创建、传输或服务端错误。
  String? get error => _error;
  int _lastEventId = 0;

  /// 五类事件共用的最后成功处理游标。
  int get lastEventId => _lastEventId;

  bool _cancelPending = false;

  /// 取消已请求或受理，但服务端尚未确认终态。
  bool get cancelPending => _cancelPending;
  bool _creationUncertain = false;
  int _reconnectAttempts = 0;
  Timer? _retryTimer;
  StreamSubscription<ChatRunEventSignal>? _subscription;
  Future<void> _eventQueue = Future<void>.value();
  int _connectionEpoch = 0;
  final List<String> _toolIds = [];
  final List<ChatToolCall> _toolCalls = [];

  /// 按工具调用 ID 去重后，依首次事件顺序排列的调用。
  List<ChatToolCall> get toolCalls => List.unmodifiable(_toolCalls);

  /// 是否仍需计入同会话及全局并发上限。
  bool get occupiesSlot => !isTerminal;

  /// 是否已有服务端确认的终态。
  bool get isTerminal => _status != null && _status != ChatRunStatus.running;

  ChatRunPhase get _statusPhase => switch (_status) {
    ChatRunStatus.succeeded => ChatRunPhase.succeeded,
    ChatRunStatus.failed => ChatRunPhase.failed,
    ChatRunStatus.cancelled => ChatRunPhase.cancelled,
    ChatRunStatus.running || null => ChatRunPhase.running,
  };
}

/// 助手回复的阶段提示。
enum ChatRunPhase {
  creating,
  connecting,
  running,
  thinking,
  toolCalling,
  replying,
  reconnecting,
  stopping,
  succeeded,
  failed,
  cancelled,
}

/// 当前 SSE 连接状态。
enum ChatConnectionState { idle, connected, disconnected, reconnecting }

/// 已发起的工具调用名称和服务端调用 ID。
class ChatToolCall {
  const ChatToolCall({required this.id, required this.name});

  /// 服务端提供的工具调用身份，与 SSE 事件 ID 无关。
  final String id;

  /// 工具函数名称，不包含参数中的同名字段。
  final String name;
}

bool _isAmbiguousCreationFailure(Object error) {
  if (error is! ChatHttpError) return true;
  return error.statusCode >= 500 ||
      error.statusCode == 408 ||
      error.statusCode == 429;
}

String _errorMessage(Object error) => error is ChatHttpError
    ? error.message
    : error is StateError
    ? error.message.toString()
    : error.toString().replaceFirst('Exception: ', '');

String _plainSummary(String markdown) {
  final text = markdown
      .replaceAll(RegExp(r'```[^\n]*\n?|~~~[^\n]*\n?'), '')
      .replaceAllMapped(
        RegExp(r'!\[([^\]]*)\]\([^)]*\)'),
        (match) => match.group(1) ?? '',
      )
      .replaceAllMapped(
        RegExp(r'\[([^\]]+)\]\([^)]*\)'),
        (match) => match.group(1) ?? '',
      )
      .replaceAll(RegExp(r'<[^>]+>'), '')
      .replaceAllMapped(
        RegExp(r'(^|\s)#{1,6}\s*'),
        (match) => match.group(1) ?? '',
      )
      .replaceAll(RegExp(r'[*_~`>|]'), '')
      .replaceAll(RegExp(r'\s+'), ' ')
      .trim();
  final characters = text.runes.toList(growable: false);
  if (characters.length <= 40) return text;
  return '${String.fromCharCodes(characters.take(39))}…';
}

Duration _defaultRetryDelay(int attempt) =>
    Duration(milliseconds: min(250 * (1 << (attempt - 1)), 4000));

String _defaultIdempotencyKey() => 'create_session:${_uuidV4()}';

String _uuidV4() {
  final random = Random.secure();
  final bytes = List<int>.generate(16, (_) => random.nextInt(256));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  final hex = bytes
      .map((byte) => byte.toRadixString(16).padLeft(2, '0'))
      .join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-'
      '${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}
