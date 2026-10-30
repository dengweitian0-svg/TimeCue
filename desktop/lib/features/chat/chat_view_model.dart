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

  List<ChatModelOption> _models = const [];
  ChatModelOption? _selectedModel;
  String? _modelsError;
  bool _modelsLoading = false;

  /// 当前配置模型列表及可用性。
  List<ChatModelOption> get models => List.unmodifiable(_models);

  /// 当前明确选择的提供方和模型。
  ChatModelOption? get selectedModel => _selectedModel;

  /// 模型列表加载错误。
  String? get modelsError => _modelsError;

  /// 是否正在刷新模型配置。
  bool get modelsLoading => _modelsLoading;

  /// 刷新本地模型配置，不影响已经提交的运行身份。
  Future<void> loadModels() async {
    if (!_acceptingOperations || _modelsLoading) return;
    final gateway = _gateway;
    if (gateway is! ChatModelGateway) return;
    _modelsLoading = true;
    final generation = _runtimeGeneration;
    try {
      final values = await (gateway as ChatModelGateway).getModels();
      if (_disposed || generation != _runtimeGeneration) return;
      _models = values;
      final previous = _selectedModel?.identity;
      _selectedModel =
          values
              .where((m) => m.available && m.identity == previous)
              .firstOrNull ??
          values.where((m) => m.available).firstOrNull;
      _modelsError = null;
    } catch (error) {
      if (_disposed || generation != _runtimeGeneration) return;
      _models = const [];
      _selectedModel = null;
      _modelsError = _errorMessage(error);
    } finally {
      _modelsLoading = false;
      if (!_disposed) notifyListeners();
    }
  }

  /// 只选择本地可用的模型。
  void selectModel(String identity) {
    _selectedModel = _models
        .where((m) => m.identity == identity && m.available)
        .firstOrNull;
    _scheduleNotify();
  }

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
  final Map<String, ChatRunState> _deletedRuns = {};
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
    yield* _deletedRuns.values;
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
      // 删除响应不明的会话可能已从服务端列表消失，仍需读取详情确认。
      final uniqueIds = {
        ...ids,
        for (final conversation in _conversations.values)
          if (conversation._deleteNeedsVerification) conversation.sessionId,
      }.where((id) => !_deletedSessionIds.contains(id)).toList();
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
      existing._deleteNeedsVerification = false;
      existing._name = session.name;
      existing._error = null;
      _scheduleNotify();
      _scheduleHistoryNotify();
      // 保持服务端顺序，并复用已运行的 run，避免重置游标或连接。
      final orderedRuns = <ChatRunState>[];
      final runsToConnect = <ChatRunState>[];
      final runsToRecover = <ChatRunState>[];
      final newlyLoadedRuns = <String, ChatRunState>{};
      for (final runId in session.runIds) {
        final cached = _runsById[runId];
        if (cached != null) {
          if (cached.sessionId != sessionId) {
            throw StateError('缓存运行所属的 session ID 与当前会话不匹配');
          }
          if (!cached.isTerminal || cached._needsSnapshot) {
            final snapshot = await _gateway.getRun(runId);
            if (!_isSessionCurrent(generation, sessionId, sessionGeneration)) {
              return;
            }
            if (_hasUnconfirmedCreation(existing)) return;
            if (snapshot.runId != runId || snapshot.sessionId != sessionId) {
              throw StateError('运行快照身份与当前会话不匹配');
            }
            _mergeSnapshot(cached, snapshot);
            if (!cached.isTerminal) {
              runsToConnect.add(cached);
            } else {
              runsToRecover.add(cached);
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
        if (!state.isTerminal) {
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
      for (final run in runsToRecover) {
        final recovery = run._eventQueue.then((_) async {
          if (_isCurrent(generation, run)) {
            await _recoverEventHistory(run, generation);
          }
        });
        run._eventQueue = recovery;
        await recovery;
      }
    } catch (error) {
      if (!_isSessionCurrent(generation, sessionId, sessionGeneration)) return;
      if (_isMissingSession(error) &&
          existing._deleteNeedsVerification &&
          !existing.isLocal) {
        _removeDeletedConversation(sessionId, existing);
        return;
      }
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

  /// 删除服务端会话并立即隐藏，已有事件连接继续跟踪后台取消占用。
  Future<bool> deleteSession(String sessionId) async {
    _ensureUsable();
    final conversation = _conversations[sessionId];
    if (!acceptingOperations ||
        conversation == null ||
        conversation.isLocal ||
        (conversation.activeRun != null &&
            conversation.activeRun!.runId == null) ||
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
      _removeDeletedConversation(sessionId, conversation);
      return true;
    } catch (error) {
      if (_isRuntimeCurrent(generation)) {
        conversation._isDeleting = false;
        conversation._deleteNeedsVerification = true;
        conversation._error = _errorMessage(error);
        _scheduleNotify();
        _scheduleHistoryNotify();
      }
      return false;
    }
  }

  void _removeDeletedConversation(
    String sessionId,
    ChatConversation conversation,
  ) {
    final wasSelected = _selectedSessionId == sessionId;
    _deletedSessionIds.add(sessionId);
    _historySessionIds.remove(sessionId);
    _conversations.remove(sessionId);
    _conversationOrder.remove(sessionId);
    for (final run in conversation._runs) {
      final runId = run.runId;
      if (runId == null) continue;
      _runsById.remove(runId);
      run._retryTimer?.cancel();
      run._retryTimer = null;
      if (!run.isTerminal && run._subscription != null) {
        run._cancelPending = true;
        run._phase = ChatRunPhase.stopping;
        _deletedRuns[runId] = run;
      }
    }
    _sessionGenerations[sessionId] = (_sessionGenerations[sessionId] ?? 0) + 1;
    if (wasSelected) createLocalSession();
    _scheduleNotify();
    _scheduleHistoryNotify();
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
  Future<void> send(
    String sessionId, {
    required String model,
    String provider = 'deepseek',
  }) async {
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
      provider: provider,
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
        if (run.isTerminal) {
          // 终态概况可能早于 SSE 尾批，旧分页末尾不能作为补读完成的依据。
          final recovery = run._eventQueue.then((_) async {
            if (_isCurrent(generation, run)) {
              await _recoverEventHistory(run, generation);
            }
          });
          run._eventQueue = recovery;
          await recovery;
        }
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

  /// 当前网关是否提供持久化事件分页。
  bool get supportsEventHistory => _gateway is ChatEventHistoryGateway;

  /// 按需读取下一页，事件应用与 SSE 共用每个运行的串行队列。
  Future<void> loadMoreEvents(String runId) async {
    final run = _runsById[runId];
    if (!_acceptingOperations ||
        !supportsEventHistory ||
        run == null ||
        run._eventsLoading) {
      return;
    }
    final generation = _runtimeGeneration;
    run._eventsLoading = true;
    run._eventsError = null;
    _scheduleNotify();
    final pending = run._eventQueue.then((_) async {
      if (!_isCurrent(generation, run)) return;
      try {
        if (run._needsSnapshot) await _loadRunSnapshot(run, generation);
        await _readEvents(run, generation);
        if (run._sseFinished && run._subscription != null) {
          await _detachEvents(run);
          await _loadRunSnapshot(run, generation);
        }
      } catch (error) {
        if (_isCurrent(generation, run)) {
          run._eventsError = _errorMessage(error);
        }
      } finally {
        if (_isCurrent(generation, run)) {
          run._eventsLoading = false;
          _scheduleNotify();
        }
      }
    });
    run._eventQueue = pending;
    await pending;
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
        provider: run.provider,
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
      if (result.status != ChatRunStatus.running) {
        run._status = result.status;
        run._rawText = result.output ?? '';
        run._error = result.error?.message;
        run._phase = switch (result.status) {
          ChatRunStatus.succeeded => ChatRunPhase.succeeded,
          ChatRunStatus.failed => ChatRunPhase.failed,
          ChatRunStatus.cancelled => ChatRunPhase.stopping,
          ChatRunStatus.running => ChatRunPhase.running,
        };
        run._connectionState = ChatConnectionState.idle;
        run._terminalConfirmed = result.status != ChatRunStatus.cancelled;
        run._sseFinished = run._terminalConfirmed;
        run._cancelPending = result.status == ChatRunStatus.cancelled;
        _scheduleNotify();
        _scheduleHistoryNotify();
        unawaited(_refreshSessionName(conversation, generation));
        if (result.status == ChatRunStatus.cancelled) {
          await _loadRunSnapshot(run, generation);
        }
        if (run.isTerminal) {
          await _recoverEventHistory(run, generation);
        } else {
          await _connect(run, generation);
        }
        return;
      }
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
        run._terminalConfirmed = true;
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
        run.isTerminal ||
        run._sseFinished) {
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
        run._sseFinished) {
      return;
    }
    if (_deletedRuns[run.runId] == run) {
      // 删除后的新查询不可见；原连接只观察真正结束，不再补读历史。
      if (signal is ChatRunEventsNoContent ||
          signal is ChatRunEventFrame &&
              signal.event.type == ChatEventType.end) {
        await _releaseDeletedRun(run);
      }
      return;
    }
    if (signal is ChatRunEventsNoContent) {
      run._sseFinished = true;
      run._terminalConfirmed = true;
      run._status = null;
      run._cancelPending = false;
      run._phase = ChatRunPhase.recovering;
      run._needsSnapshot = true;
      _scheduleNotify();
      _scheduleHistoryNotify();
      await _detachEvents(run);
      await _loadRunSnapshot(run, generation);
      await _recoverEventHistory(run, generation);
      return;
    }
    final event = (signal as ChatRunEventFrame).event;
    if (event.eventId <= run.lastEventId) return;
    try {
      if (event.eventId > run.lastEventId + 1) {
        await _readEvents(
          run,
          generation,
          through: event.eventId - 1,
          connectionEpoch: connectionEpoch,
        );
      }
      if (!_isCurrent(generation, run) ||
          run._connectionEpoch != connectionEpoch) {
        return;
      }
      if (event.eventId > run.lastEventId) _applyEvent(run, event);
      if (run._sseFinished) {
        await _detachEvents(run);
        await _loadRunSnapshot(run, generation);
      }
    } catch (error) {
      await _connectionLost(run, error, generation, connectionEpoch);
    }
  }

  Future<void> _detachEvents(ChatRunState run) async {
    ++run._connectionEpoch;
    final subscription = run._subscription;
    run._subscription = null;
    run._connectionState = ChatConnectionState.idle;
    if (subscription != null) await subscription.cancel();
  }

  void _applyEvent(ChatRunState run, ChatSseEvent event) {
    if (event.eventId <= run.lastEventId) return;
    if (event.eventId != run.lastEventId + 1) throw StateError('事件序号存在缺口');
    final previousPhase = run._phase;
    final previousConnection = run._connectionState;
    switch (event.type) {
      case ChatEventType.start:
        if (!run.isTerminal && !run._cancelPending) {
          run._phase = ChatRunPhase.running;
        }
      case ChatEventType.compact:
        break;
      case ChatEventType.thinking:
        if (!run.isTerminal && !run._cancelPending) {
          run._phase = ChatRunPhase.thinking;
        }
      case ChatEventType.text:
        final data = event.data as ChatContentEventData;
        if (!run.isTerminal) {
          if (!run._cancelPending) run._phase = ChatRunPhase.replying;
          run._rawText += data.content;
        }
      case ChatEventType.error:
        if (!run.isTerminal) {
          run._error = (event.data as ChatProtocolError).message;
        }
      case ChatEventType.toolResult:
        final result = event.data as ChatToolResultData;
        final index = run._toolCalls.indexWhere(
          (call) => call.id == result.toolCallId && !call.hasResult,
        );
        if (index >= 0) {
          final call = run._toolCalls[index];
          run._toolCalls[index] = ChatToolCall(
            id: call.id,
            name: call.name,
            startSeq: call.startSeq,
            argument: call.argument,
            hasResult: true,
            output: result.output,
            error: result.error,
          );
        }
        break;
      case ChatEventType.toolStart:
        final tools = event.data is List<ChatToolEventData>
            ? event.data as List<ChatToolEventData>
            : [event.data as ChatToolEventData];
        for (final tool in tools) {
          run._toolCalls.add(
            ChatToolCall(
              id: tool.id,
              name: tool.function.name,
              startSeq: event.eventId,
              argument: tool.function.args.value,
            ),
          );
        }
        if (!run.isTerminal && !run._cancelPending) {
          run._phase = ChatRunPhase.toolCalling;
        }
      case ChatEventType.end:
        final end = event.data as ChatEndEventData;
        if (!run.isTerminal) run._needsSnapshot = true;
        run._status = end.status;
        run._terminalConfirmed = true;
        run._sseFinished = true;
        run._cancelPending = false;
        run._phase = run._statusPhase;
    }
    run._timeline.add(event);
    run._lastEventId = event.eventId;
    _scheduleNotify();
    _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
  }

  Future<void> _readEvents(
    ChatRunState run,
    int generation, {
    bool all = false,
    int? through,
    int? connectionEpoch,
  }) async {
    final history = _gateway;
    if (history is! ChatEventHistoryGateway || run.runId == null) return;
    do {
      final page = await (history as ChatEventHistoryGateway).getRunEvents(
        run.runId!,
        afterSeq: run.lastEventId,
      );
      if (!_isCurrent(generation, run) ||
          (connectionEpoch != null &&
              run._connectionEpoch != connectionEpoch)) {
        return;
      }
      if (page.events.isEmpty) {
        if (through != null && run.lastEventId < through) {
          throw StateError('事件缺口尚未保存');
        }
        if (page.nextAfterSeq != run.lastEventId || page.hasMore) {
          throw StateError('空页游标无效');
        }
        run._hasMoreEvents = false;
        return;
      }
      final before = run.lastEventId;
      for (final saved in page.events) {
        _applyEvent(run, saved);
      }
      if (run.lastEventId == before) throw StateError('事件分页未前进');
      run._hasMoreEvents = page.hasMore;
      if (through != null && run.lastEventId >= through) return;
      if (!all && through == null) return;
      if (!page.hasMore) {
        if (through != null && run.lastEventId < through) {
          throw StateError('事件缺口未补齐');
        }
        return;
      }
    } while (true);
  }

  Future<void> _recoverEventHistory(ChatRunState run, int generation) async {
    run._eventsLoading = true;
    run._eventsError = null;
    try {
      await _readEvents(run, generation, all: true);
    } catch (error) {
      if (_isCurrent(generation, run)) run._eventsError = _errorMessage(error);
    } finally {
      if (_isCurrent(generation, run)) {
        run._eventsLoading = false;
        _scheduleNotify();
      }
    }
  }

  Future<void> _loadRunSnapshot(ChatRunState run, int generation) async {
    if (run.runId == null) return;
    final previousPhase = run._phase;
    final previousConnection = run._connectionState;
    final previousText = run._rawText;
    try {
      final snapshot = await _gateway.getRun(run.runId!);
      if (!_isCurrent(generation, run)) return;
      if (snapshot.runId != run.runId || snapshot.sessionId != run.sessionId) {
        throw StateError('运行快照身份与当前会话不匹配');
      }
      _mergeSnapshot(run, snapshot);
    } catch (error) {
      if (!_isCurrent(generation, run)) return;
      run._error = _errorMessage(error);
    }
    _scheduleNotify();
    if (run._rawText != previousText) _scheduleHistoryNotify();
    _notifyHistoryOnRunStateChange(run, previousPhase, previousConnection);
  }

  void _mergeSnapshot(ChatRunState run, ChatRun snapshot) {
    if (run.isTerminal &&
        (snapshot.finishedAt == null ||
            (run.status != null && snapshot.status != run.status))) {
      return;
    }
    run._createdAt = snapshot.createdAt;
    run._status = snapshot.status;
    if (snapshot.finishedAt == null) {
      if (snapshot.status == ChatRunStatus.cancelled) {
        run._cancelPending = true;
        run._phase = ChatRunPhase.stopping;
      } else {
        run._phase = run._cancelPending
            ? ChatRunPhase.stopping
            : run._reconnectAttempts > 0
            ? ChatRunPhase.reconnecting
            : ChatRunPhase.running;
      }
      return;
    }
    run._terminalConfirmed = true;
    run._sseFinished = true;
    run._needsSnapshot = false;
    run._rawText = snapshot.output ?? '';
    run._error = snapshot.error?.message;
    run._cancelPending = false;
    run._phase = run._statusPhase;
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
    if (_deletedRuns[run.runId] == run) {
      // 隐藏运行断线后无法重查，后续准入由服务端的实际占用判定。
      await _releaseDeletedRun(run);
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
    if (_isCurrent(generation, run)) {
      if (run.isTerminal) {
        await _recoverEventHistory(run, generation);
      } else {
        _scheduleReconnect(run, generation, error);
      }
    }
  }

  void _scheduleReconnect(ChatRunState run, int generation, Object error) {
    if (!_isCurrent(generation, run) || run.isTerminal || run._sseFinished) {
      return;
    }
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
      (_deletedRuns[run.runId] == run ||
          _conversations.values.any(
            (conversation) => conversation._runs.contains(run),
          ));

  Future<void> _releaseDeletedRun(ChatRunState run) async {
    _deletedRuns.remove(run.runId);
    await _detachEvents(run);
    _scheduleNotify();
  }

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

  bool _isMissingSession(Object error) =>
      error is ChatHttpError &&
      (error.statusCode == 400 || error.statusCode == 404) &&
      error.code == 'session_not_found';

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
  bool _deleteNeedsVerification = false;
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
    required this.provider,
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
      provider: snapshot.provider,
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
    state._terminalConfirmed = snapshot.finishedAt != null;
    state._sseFinished = state._terminalConfirmed;
    if (snapshot.status == ChatRunStatus.cancelled &&
        snapshot.finishedAt == null) {
      state._cancelPending = true;
      state._phase = ChatRunPhase.stopping;
    }
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

  /// 本轮请求固定的模型提供方。
  final String provider;
  String _rawText = '';

  /// 生成期间的正文预览；终态采用概况 output，原始事件另存时间线。
  String get rawText => _rawText;

  ChatRunStatus? _status;

  /// 服务端具体结果状态；创建待确认或 204 后概况待读取时为 null。
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

  final List<ChatSseEvent> _timeline = [];

  /// 已连续应用的原始事件，与最终正文投影分别保留。
  List<ChatSseEvent> get timeline => List.unmodifiable(_timeline);

  bool _terminalConfirmed = false;
  bool _sseFinished = false;
  bool _needsSnapshot = false;

  /// 已知结束但概况读取失败时允许重新获取最终正文。
  bool get needsSnapshot => _needsSnapshot;
  bool _hasMoreEvents = true;
  bool _eventsLoading = false;
  String? _eventsError;

  /// 查询时是否还有后续已保存事件，不表示运行是否结束。
  bool get hasMoreEvents => _hasMoreEvents;

  /// 历史分页是否正在读取或等待应用。
  bool get eventsLoading => _eventsLoading;

  /// 事件历史读取错误，与模型错误分别展示。
  String? get eventsError => _eventsError;

  /// 所有事件共用的连续成功应用位置，包括不渲染的控制事件。
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
  final List<ChatToolCall> _toolCalls = [];

  /// 按开始事件 seq 与工具原 ID 区分实例，保留原始参数和已配对结果。
  List<ChatToolCall> get toolCalls => List.unmodifiable(_toolCalls);

  /// 是否仍需计入同会话及全局并发上限。
  bool get occupiesSlot => !isTerminal;

  /// 是否已有服务端确认的终态。
  bool get isTerminal => _terminalConfirmed;

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
  recovering,
  succeeded,
  failed,
  cancelled,
}

/// 当前 SSE 连接状态。
enum ChatConnectionState { idle, connected, disconnected, reconnecting }

/// 以开始事件区分的工具请求及其已配对原始结果。
class ChatToolCall {
  const ChatToolCall({
    required this.id,
    required this.name,
    this.startSeq = 0,
    this.argument,
    this.hasResult = false,
    this.output,
    this.error,
  });

  /// 服务端提供的工具调用身份，与 SSE 事件 ID 无关。
  final String id;

  /// 工具函数名称，不包含参数中的同名字段。
  final String name;

  /// 工具开始事件的位置，与原 ID 共同标识展示实例。
  final int startSeq;

  /// 模型返回的原始参数对象或 null。
  final Object? argument;

  /// 是否已经取得对应结果，避免把 null 结果当成尚未返回。
  final bool hasResult;

  /// 工具输出原值，保留对象、数组、布尔值、字符串及 null。
  final Object? output;

  /// 工具处理错误或跳过说明，与运行错误分别展示。
  final ChatProtocolError? error;

  /// 展示实例的稳定身份，允许后续回复复用原工具 ID。
  String get instanceId => '$startSeq:$id';
}

bool _isAmbiguousCreationFailure(Object error) {
  if (error is! ChatHttpError) return true;
  if (error.code == 'model_config_unavailable') return false;
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
