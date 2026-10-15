import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart' show StringCharacters;

import '../../contracts/ai_chat.dart';

/// AI 对话事件流的连接状态。
enum AiChatConnectionState {
  idle,
  connecting,
  connected,
  reconnecting,
  disconnected,
}

/// AI 对话中的一轮用户输入及回复状态。
class AiChatTurn {
  const AiChatTurn({
    required this.runId,
    required this.input,
    required this.model,
    required this.status,
    required this.createdAt,
    this.thinking = '',
    this.text = '',
    this.lastProcessedEventId = 0,
    this.connectionState = AiChatConnectionState.idle,
    this.retryable = false,
    this.cancelling = false,
    this.loading = false,
    this.error,
  });

  final String runId;
  final String input;
  final String model;
  final AiChatStatus status;
  final DateTime createdAt;
  final String thinking;
  final String text;
  final int lastProcessedEventId;
  final AiChatConnectionState connectionState;
  final bool retryable;
  final bool cancelling;
  final bool loading;
  final AiChatError? error;

  /// 返回更新后的不可变轮次状态。
  AiChatTurn copyWith({
    AiChatStatus? status,
    String? thinking,
    String? text,
    int? lastProcessedEventId,
    AiChatConnectionState? connectionState,
    bool? retryable,
    bool? cancelling,
    bool? loading,
    AiChatError? error,
    bool clearError = false,
  }) => AiChatTurn(
    runId: runId,
    input: input,
    model: model,
    status: status ?? this.status,
    createdAt: createdAt,
    thinking: thinking ?? this.thinking,
    text: text ?? this.text,
    lastProcessedEventId: lastProcessedEventId ?? this.lastProcessedEventId,
    connectionState: connectionState ?? this.connectionState,
    retryable: retryable ?? this.retryable,
    cancelling: cancelling ?? this.cancelling,
    loading: loading ?? this.loading,
    error: clearError ? null : error ?? this.error,
  );
}

/// AI 对话会话及其本地输入草稿。
class AiChatSession {
  const AiChatSession({
    required this.id,
    this.title = '新对话',
    this.draft = '',
    this.turns = const [],
    this.creating = false,
    this.createResultUnknown = false,
    this.createRetryable = false,
    this.deleting = false,
    this.error,
  });

  final String id;
  final String title;
  final String draft;
  final List<AiChatTurn> turns;
  final bool creating;
  final bool createResultUnknown;
  final bool createRetryable;
  final bool deleting;
  final String? error;

  /// 会话最后一轮对话的创建时间，用于历史分组和最近活动排序。
  DateTime? get lastActivityAt => turns.isEmpty ? null : turns.last.createdAt;

  /// 返回更新后的不可变会话状态。
  AiChatSession copyWith({
    String? title,
    String? draft,
    List<AiChatTurn>? turns,
    bool? creating,
    bool? createResultUnknown,
    bool? createRetryable,
    bool? deleting,
    String? error,
    bool clearError = false,
  }) => AiChatSession(
    id: id,
    title: title ?? this.title,
    draft: draft ?? this.draft,
    turns: turns ?? this.turns,
    creating: creating ?? this.creating,
    createResultUnknown: createResultUnknown ?? this.createResultUnknown,
    createRetryable: createRetryable ?? this.createRetryable,
    deleting: deleting ?? this.deleting,
    error: clearError ? null : error ?? this.error,
  );
}

/// 管理 AI 对话状态、全局并发名额及可恢复的事件流。
class AiChatModel extends ChangeNotifier {
  AiChatModel(this._gateway, {String Function()? createId})
    : _createId = createId ?? generateAiChatUuidV4;

  static const int maxSessionCount = 3;
  static const String defaultModel = 'deepseek-flash';
  static const List<Duration> _retryDelays = [
    Duration(milliseconds: 150),
    Duration(milliseconds: 400),
    Duration(milliseconds: 900),
  ];

  final AiChatGateway _gateway;
  final String Function() _createId;
  final List<AiChatSession> _sessions = [];
  final Map<String, _PendingCreate> _pendingCreates = {};
  final Map<String, StreamSubscription<AiChatEvent>> _subscriptions = {};
  final Map<String, Future<void>> _eventQueues = {};
  final Map<String, int> _streamGenerations = {};
  final Map<String, Map<int, AiChatEvent>> _bufferedEvents = {};
  final Set<String> _deletedIds = {};
  final Set<String> _deletedActiveRuns = {};
  final Map<String, AiChatSession> _deletedSessions = {};
  final Set<String> _blockedReconnects = {};
  final Set<String> _knownRemoteIds = {};
  String? _selectedSessionId;
  bool _loading = false;
  bool _synchronized = false;
  bool _disposed = false;
  bool _invalidated = false;
  int _generation = 0;
  int _lifecycleGeneration = 0;
  String? _error;

  /// 当前可见会话的只读快照。
  List<AiChatSession> get sessions => List.unmodifiable(_sessions);

  /// 当前选中的会话。
  AiChatSession? get selectedSession => _session(_selectedSessionId);

  /// 当前选中的会话 ID。
  String? get selectedSessionId => _selectedSessionId;

  /// 是否正在同步会话历史。
  bool get loading => _loading;

  /// 最近一次同步或操作错误。
  String? get error => _error;

  /// 是否已完成全局活跃会话同步，可安全提交新输入。
  bool get synchronized => _synchronized;

  /// 当前保留的并发名额数量。
  int get activeSessionCount => _activeSessionIds.length;

  /// 当前是否还可提交新的会话输入。
  bool get canSend => _synchronized && !_loading && _hasCapacityForAttempt;

  /// 当前是否可以新建一个本地空白会话。
  bool get canCreateSession => createSessionDisabledReason == null;

  /// 返回新建会话按钮不可用的原因；可用时返回 null。
  String? get createSessionDisabledReason {
    if (!_synchronized) return '正在同步会话状态，请稍后再新建。';
    if (_loading) return '正在加载会话历史。';
    final selected = _session(_selectedSessionId);
    if (selected != null && selected.turns.isEmpty) {
      return '请先完成或删除当前新会话。';
    }
    if (activeSessionCount >= maxSessionCount) {
      return '最多同时运行 $maxSessionCount 个会话。';
    }
    return null;
  }

  /// 返回指定会话发送按钮当前不可用的具体原因；可用时返回 null。
  String? sendDisabledReasonFor(String sessionId) {
    if (!_synchronized) return '正在同步会话状态，请先重试加载。';
    if (_loading) return '正在加载会话历史。';
    final session = _session(sessionId);
    if (session == null) return '请先选择一个会话。';
    if (session.createResultUnknown) return '上次提交结果未确认，请重试原始输入。';
    if (session.createRetryable) return _createRetryMessage;
    if (session.deleting) return '正在删除会话。';
    if (_sessionIsRunning(session)) return '当前会话仍有一轮对话正在运行。';
    if (!_hasCapacityForAttempt) {
      return '最多同时运行 $maxSessionCount 个会话。';
    }
    return null;
  }

  Set<String> get _activeSessionIds {
    final active = <String>{};
    for (final session in _sessions) {
      if (session.creating ||
          session.createResultUnknown ||
          session.turns.any((turn) => turn.status == AiChatStatus.running)) {
        active.add(session.id);
      }
    }
    active.addAll(_deletedActiveRuns);
    return active;
  }

  Set<String> get _visibleActiveSessionIds {
    final active = <String>{};
    for (final session in _sessions) {
      if (session.creating ||
          session.createResultUnknown ||
          session.turns.any((turn) => turn.status == AiChatStatus.running)) {
        active.add(session.id);
      }
    }
    return active;
  }

  bool get _hasCapacityForAttempt =>
      activeSessionCount < maxSessionCount ||
      (_deletedActiveRuns.isNotEmpty &&
          _visibleActiveSessionIds.length < maxSessionCount);

  static const String _createRetryMessage = '会话正在停止，请稍后重试原内容。';

  /// 判断创建拒绝状态是否可以安全重试原文和幂等键。
  bool canRetryCreate(String sessionId) {
    final session = _session(sessionId);
    return _synchronized &&
        !_loading &&
        session != null &&
        !session.creating &&
        !session.deleting &&
        _pendingCreates.containsKey(sessionId) &&
        (session.createResultUnknown ||
            (session.createRetryable && _hasCapacityForAttempt));
  }

  /// 新建本地会话并选中它；远端会话在首次发送时创建。
  AiChatSession createSession() {
    _ensureUsable();
    final reason = createSessionDisabledReason;
    if (reason != null) throw StateError(reason);
    final session = AiChatSession(id: _createId());
    _sessions.insert(0, session);
    _selectedSessionId = session.id;
    _notify();
    return session;
  }

  /// 切换当前会话，不影响已有运行或事件流。
  void selectSession(String? sessionId) {
    _ensureUsable();
    if (sessionId != null && _session(sessionId) == null) return;
    if (_selectedSessionId == sessionId) return;
    _selectedSessionId = sessionId;
    _notify();
  }

  /// 保存指定会话的输入草稿。
  void setDraft(String sessionId, String value) {
    if (_disposed || _invalidated) return;
    final session = _session(sessionId);
    if (session == null ||
        session.creating ||
        session.createResultUnknown ||
        session.createRetryable) {
      return;
    }
    _replaceSession(session.copyWith(draft: value));
    _notify();
  }

  /// 读取指定会话的输入草稿。
  String draftFor(String sessionId) => _session(sessionId)?.draft ?? '';

  /// 同步所有会话与其历史 run，确认全局活跃会话数。
  Future<void> refresh() async {
    _ensureUsable();
    final generation = ++_generation;
    _loading = true;
    _error = null;
    _notify();
    String? failedSessionId;
    try {
      final ids = await _gateway.listSessions();
      final loaded = <AiChatSession>[];
      // 顺序读取，避免启动时一次性建立大量 HTTP 请求。
      for (final id in ids) {
        if (_deletedIds.contains(id)) continue;
        List<AiChatRun> runs;
        try {
          runs = await _gateway.getSessionRuns(id);
        } catch (_) {
          failedSessionId = id;
          rethrow;
        }
        if (generation != _generation || _disposed) return;
        if (_deletedIds.contains(id)) continue;
        final current = _session(id);
        final turns = runs
            .map((run) {
              final existing = current == null ? null : _turn(id, run.runId);
              return run.status == AiChatStatus.running &&
                      existing?.status == AiChatStatus.running
                  ? existing!
                  : _turnFromRun(run);
            })
            .toList(growable: false);
        loaded.add(
          AiChatSession(
            id: id,
            title: current?.title ?? _titleFor(runs),
            draft: current?.draft ?? '',
            turns: List.unmodifiable(turns),
            creating: current?.creating ?? false,
            createResultUnknown: current?.createResultUnknown ?? false,
            createRetryable: current?.createRetryable ?? false,
            deleting: current?.deleting ?? false,
            error: current?.error,
          ),
        );
      }
      if (generation != _generation || _disposed) return;
      final remoteIds = ids.toSet();
      _knownRemoteIds.addAll(remoteIds);
      for (final current in _sessions) {
        if (!remoteIds.contains(current.id) &&
            !_deletedIds.contains(current.id) &&
            current.turns.isEmpty &&
            (!_knownRemoteIds.contains(current.id) ||
                current.creating ||
                current.createResultUnknown ||
                _pendingCreates.containsKey(current.id)) &&
            !loaded.any((session) => session.id == current.id)) {
          loaded.add(current);
        }
      }
      _sessions
        ..clear()
        ..addAll(loaded);
      if (_selectedSessionId != null && _session(_selectedSessionId) == null) {
        _selectedSessionId = _sessions.isEmpty ? null : _sessions.first.id;
      }
      _synchronized = true;
      _error = null;
      for (final session in _sessions) {
        for (final turn in session.turns) {
          if (turn.status == AiChatStatus.running) _connect(turn.runId);
        }
      }
    } catch (error) {
      if (generation != _generation || _disposed) return;
      _synchronized = false;
      _error = _messageFor(error);
      final failedSession = _session(failedSessionId);
      if (failedSession != null) {
        _replaceSession(failedSession.copyWith(error: _messageFor(error)));
      }
    } finally {
      if (generation == _generation && !_disposed) {
        _loading = false;
        _notify();
      }
    }
  }

  /// 使用保留的原文创建或继续当前会话中的一轮对话。
  Future<void> send(String sessionId, String input) async {
    _ensureUsable();
    if (!_synchronized || _loading) {
      _error = '会话状态尚未同步，请先重试加载。';
      _notify();
      return;
    }
    final disabledReason = sendDisabledReasonFor(sessionId);
    if (disabledReason != null) {
      _error = disabledReason;
      _notify();
      return;
    }
    if (input.trim().isEmpty) {
      _error = '请输入对话内容。';
      _notify();
      return;
    }
    var session = _session(sessionId);
    if (session == null) return;
    if (session.createResultUnknown || session.createRetryable) {
      _error = '上次提交结果尚未确认，请重试原始输入。';
      _notify();
      return;
    }
    if (_sessionIsRunning(session)) {
      _error = '当前会话仍有正在运行的对话。';
      _notify();
      return;
    }
    final idempotencyKey = 'create_session:${_createId()}';
    final pending = _PendingCreate(input, idempotencyKey);
    _pendingCreates[sessionId] = pending;
    session = session.copyWith(
      draft: input,
      creating: true,
      createResultUnknown: false,
      clearError: true,
    );
    _replaceSession(session);
    _error = null;
    _notify();
    await _submitCreate(sessionId, pending);
  }

  /// 重试不明创建结果或恢复当前运行的事件流。
  Future<void> retry(String sessionId) async {
    _ensureUsable();
    final session = _session(sessionId);
    if (session == null || session.deleting) return;
    final pending = _pendingCreates[sessionId];
    if (pending != null) {
      if (session.creating ||
          (!session.createResultUnknown && !session.createRetryable)) {
        return;
      }
      if (session.createRetryable && !_hasCapacityForAttempt) return;
      _replaceSession(
        session.copyWith(
          creating: true,
          createResultUnknown: false,
          createRetryable: false,
          clearError: true,
        ),
      );
      _notify();
      await _submitCreate(sessionId, pending);
      return;
    }
    final active = session.turns.where(
      (turn) => turn.status == AiChatStatus.running,
    );
    if (active.isNotEmpty) {
      _blockedReconnects.remove(active.last.runId);
      _connect(active.last.runId, explicit: true);
      return;
    }
    await refresh();
  }

  /// 请求取消运行中的一轮对话，并等待正式状态确认。
  Future<void> cancel(String sessionId, String runId) async {
    if (_disposed || _invalidated) return;
    final lifecycle = _lifecycleGeneration;
    final turn = _turn(sessionId, runId);
    if (turn == null ||
        turn.status != AiChatStatus.running ||
        turn.cancelling) {
      return;
    }
    _updateTurn(sessionId, runId, (value) => value.copyWith(cancelling: true));
    try {
      final status = await _gateway.cancelRun(runId);
      if (_disposed || _invalidated || lifecycle != _lifecycleGeneration) {
        return;
      }
      if (status != AiChatStatus.running) {
        final run = await _gateway.getRun(runId);
        if (_disposed || _invalidated || lifecycle != _lifecycleGeneration) {
          return;
        }
        _applyRun(sessionId, run, replaceStreamText: true);
      }
    } catch (error) {
      if (_disposed || _invalidated || lifecycle != _lifecycleGeneration) {
        return;
      }
      _updateTurn(
        sessionId,
        runId,
        (value) => value.copyWith(cancelling: false, error: _errorFrom(error)),
      );
      _error = _messageFor(error);
    }
    _notify();
  }

  /// 软删除会话；成功后立即移除本地内容，保守保留活跃 run 名额。
  Future<void> deleteSession(String sessionId) async {
    if (_disposed || _invalidated) return;
    final session = _session(sessionId);
    if (session == null || session.deleting) return;
    if (session.creating || session.createResultUnknown) {
      final message = session.creating
          ? '请等待本次提交完成后再删除会话。'
          : '请先重试原始输入确认结果，再删除会话。';
      _replaceSession(session.copyWith(error: message));
      _error = message;
      _notify();
      return;
    }
    if (session.turns.isEmpty &&
        !session.creating &&
        !session.createResultUnknown &&
        (!_pendingCreates.containsKey(sessionId) || session.createRetryable)) {
      _deletedIds.add(sessionId);
      _pendingCreates.remove(sessionId);
      _sessions.removeWhere((item) => item.id == sessionId);
      if (_selectedSessionId == sessionId) {
        _selectedSessionId = _sessions.isEmpty ? null : _sessions.first.id;
      }
      _notify();
      return;
    }
    final lifecycle = _lifecycleGeneration;
    _replaceSession(session.copyWith(deleting: true));
    _notify();
    try {
      await _gateway.deleteSession(sessionId);
      if (_disposed || _invalidated || lifecycle != _lifecycleGeneration) {
        return;
      }
      final current = _session(sessionId);
      if (current == null) return;
      if (current.turns.any((turn) => turn.status == AiChatStatus.running)) {
        _deletedActiveRuns.add(sessionId);
        _deletedSessions[sessionId] = current.copyWith(deleting: false);
      }
      for (final turn in current.turns) {
        if (turn.status != AiChatStatus.running) {
          _stopSubscription(turn.runId);
        } else if (!_subscriptions.containsKey(turn.runId) &&
            !_blockedReconnects.contains(turn.runId)) {
          _connect(turn.runId, explicit: true);
        }
      }
      _deletedIds.add(sessionId);
      _sessions.removeWhere((item) => item.id == sessionId);
      _pendingCreates.remove(sessionId);
      if (_selectedSessionId == sessionId) {
        _selectedSessionId = _sessions.isEmpty ? null : _sessions.first.id;
      }
      _notify();
    } catch (error) {
      if (_disposed || _invalidated || lifecycle != _lifecycleGeneration) {
        return;
      }
      final current = _session(sessionId);
      if (current != null) {
        _replaceSession(
          current.copyWith(deleting: false, error: _messageFor(error)),
        );
      }
      _error = _messageFor(error);
      _notify();
    }
  }

  /// 停止本模型持有的监听；不会取消 Go 中的运行，也不释放共享网关。
  void invalidate() {
    if (_disposed || _invalidated) return;
    _invalidated = true;
    _synchronized = false;
    _loading = false;
    _lifecycleGeneration++;
    _generation++;
    for (final subscription in _subscriptions.values) {
      unawaited(subscription.cancel());
    }
    _subscriptions.clear();
    _eventQueues.clear();
    _streamGenerations.updateAll((_, value) => value + 1);
    _notify();
  }

  @override
  void dispose() {
    if (_disposed) return;
    invalidate();
    _disposed = true;
    super.dispose();
  }

  Future<void> _submitCreate(String sessionId, _PendingCreate pending) async {
    final lifecycle = _lifecycleGeneration;
    try {
      final runId = await _gateway.createRun(
        sessionId,
        pending.input,
        idempotencyKey: pending.key,
      );
      if (_disposed ||
          _invalidated ||
          lifecycle != _lifecycleGeneration ||
          _sessionState(sessionId) == null) {
        return;
      }
      _pendingCreates.remove(sessionId);
      final session = _sessionState(sessionId)!;
      final turn = AiChatTurn(
        runId: runId,
        input: pending.input,
        model: defaultModel,
        status: AiChatStatus.running,
        createdAt: DateTime.now(),
        loading: true,
        connectionState: AiChatConnectionState.idle,
      );
      _replaceSession(
        session.copyWith(
          title: session.title == '新对话'
              ? _shortTitle(pending.input)
              : session.title,
          draft: '',
          creating: false,
          createResultUnknown: false,
          createRetryable: false,
          clearError: true,
          turns: List.unmodifiable([...session.turns, turn]),
        ),
      );
      _error = null;
      if (_sessions.any((item) => item.id == sessionId)) {
        final updated = _session(sessionId)!;
        _sessions
          ..removeWhere((item) => item.id == sessionId)
          ..insert(0, updated);
        _notify();
      }
      _connect(runId);
    } catch (error) {
      if (_disposed || _invalidated || lifecycle != _lifecycleGeneration) {
        return;
      }
      final unknown = _isUnknownCreateResult(error);
      final session = _sessionState(sessionId);
      if (session == null) return;
      if (_deletedSessions.containsKey(sessionId)) {
        if (!unknown) _finishDeletedSession(sessionId);
        return;
      }
      final retryable = _isRetryableConcurrencyRejection(error, sessionId);
      if (!unknown && !retryable) _pendingCreates.remove(sessionId);
      final message = retryable ? _createRetryMessage : _messageFor(error);
      _replaceSession(
        session.copyWith(
          creating: false,
          createResultUnknown: unknown,
          createRetryable: retryable,
          draft: unknown || retryable ? pending.input : session.draft,
          error: message,
        ),
      );
      _error = message;
      _notify();
    }
  }

  void _connect(String runId, {bool explicit = false, int attempt = 0}) {
    if (_disposed || _invalidated || _subscriptions.containsKey(runId)) return;
    if (_blockedReconnects.contains(runId) && !explicit) return;
    final turn = _findTurn(runId);
    if (turn == null || turn.status != AiChatStatus.running) return;
    final nextGeneration = (_streamGenerations[runId] ?? 0) + 1;
    _streamGenerations[runId] = nextGeneration;
    _setConnection(
      runId,
      explicit
          ? AiChatConnectionState.reconnecting
          : (attempt == 0
                ? AiChatConnectionState.connecting
                : AiChatConnectionState.reconnecting),
    );
    final cursor = turn.lastProcessedEventId == 0
        ? null
        : turn.lastProcessedEventId;
    late StreamSubscription<AiChatEvent> subscription;
    try {
      subscription = _gateway
          .watchRun(runId, lastEventId: cursor)
          .listen(
            (event) {
              _eventQueues[runId] =
                  (_eventQueues[runId] ?? Future<void>.value())
                      .then((_) => _handleEvent(runId, event, nextGeneration))
                      .catchError((Object error) {
                        _setTurnError(runId, error);
                        _stopSubscription(runId);
                        _reconnect(runId, nextGeneration, attempt);
                      });
            },
            onError: (Object error, StackTrace stack) {
              _stopSubscription(runId);
              _setConnection(runId, AiChatConnectionState.disconnected);
              _setTurnError(runId, error);
              if (_isAlreadyConnected(error)) {
                _blockedReconnects.add(runId);
              } else {
                _reconnect(runId, nextGeneration, attempt);
              }
            },
            onDone: () {
              unawaited(() async {
                await _eventQueues[runId];
                if (_streamGenerations[runId] != nextGeneration) return;
                _stopSubscription(runId);
                final latest = _findTurn(runId);
                if (latest?.status == AiChatStatus.running &&
                    !_blockedReconnects.contains(runId)) {
                  _setConnection(runId, AiChatConnectionState.disconnected);
                  _reconnect(runId, nextGeneration, attempt);
                }
              }());
            },
            cancelOnError: true,
          );
      _subscriptions[runId] = subscription;
      _setConnection(runId, AiChatConnectionState.connected);
    } catch (error) {
      _setTurnError(runId, error);
      _setConnection(runId, AiChatConnectionState.disconnected);
      if (_isAlreadyConnected(error)) {
        _blockedReconnects.add(runId);
      } else {
        _reconnect(runId, nextGeneration, attempt);
      }
    }
  }

  Future<void> _handleEvent(
    String runId,
    AiChatEvent event,
    int generation,
  ) async {
    if (_disposed || _streamGenerations[runId] != generation) return;
    if (event.noContent) {
      final sessionId = _sessionIdForRun(runId);
      if (sessionId != null && _deletedSessions.containsKey(sessionId)) {
        await _confirmDeletedTerminal(sessionId, runId);
        return;
      }
      await _refreshFormalRun(runId, generation);
      return;
    }
    final id = event.id;
    if (id != null) {
      final current = _findTurn(runId);
      if (current == null || id <= current.lastProcessedEventId) return;
      final buffer = _bufferedEvents.putIfAbsent(runId, () => {});
      buffer[id] = event;
      while (true) {
        final expected = (_findTurn(runId)?.lastProcessedEventId ?? 0) + 1;
        final next = buffer.remove(expected);
        if (next == null) break;
        await _processEvent(runId, next, generation);
      }
      return;
    }
    await _processEvent(runId, event, generation);
  }

  Future<void> _processEvent(
    String runId,
    AiChatEvent event,
    int generation,
  ) async {
    if (_disposed || _streamGenerations[runId] != generation) return;
    final turn = _findTurn(runId);
    if (turn == null || turn.status != AiChatStatus.running) return;
    final content = event.data['content'];
    final rawError = event.data['error'];
    final eventError = rawError == null ? null : AiChatError.fromJson(rawError);
    var updated = turn.copyWith(
      error: eventError,
      clearError: turn.retryable && eventError == null,
      retryable: false,
    );
    if (event.type == 'thinking' && content is String) {
      updated = updated.copyWith(thinking: updated.thinking + content);
    } else if (event.type == 'text' && content is String) {
      updated = updated.copyWith(text: updated.text + content);
    } else if (event.type == 'end') {
      final sessionId = _sessionIdForRun(runId);
      if (sessionId != null && _deletedSessions.containsKey(sessionId)) {
        await _confirmDeletedTerminal(sessionId, runId);
        return;
      }
      await _refreshFormalRun(runId, generation);
      final latest = _findTurn(runId);
      if (event.id != null &&
          latest != null &&
          !_disposed &&
          !_invalidated &&
          _streamGenerations[runId] == generation) {
        _replaceTurn(runId, latest.copyWith(lastProcessedEventId: event.id));
      }
      return;
    }
    if (event.id != null) {
      updated = updated.copyWith(lastProcessedEventId: event.id);
    }
    _replaceTurn(runId, updated);
    _notify();
  }

  Future<void> _refreshFormalRun(String runId, int generation) async {
    final turn = _findTurn(runId);
    if (turn == null) return;
    try {
      final run = await _gateway.getRun(runId);
      if (_disposed || _streamGenerations[runId] != generation) return;
      final sessionId = _sessionIdForRun(runId);
      if (sessionId == null) {
        return;
      }
      _applyRun(sessionId, run, replaceStreamText: true);
      if (run.status == AiChatStatus.running) {
        _stopSubscription(runId);
        _setConnection(runId, AiChatConnectionState.disconnected);
      }
    } catch (error) {
      _setTurnError(runId, error);
      rethrow;
    }
  }

  Future<void> _confirmDeletedTerminal(String sessionId, String runId) async {
    final generation = _streamGenerations[runId];
    try {
      final run = await _gateway.getRun(runId);
      if (_disposed ||
          _invalidated ||
          generation != _streamGenerations[runId]) {
        return;
      }
      if (run.status != AiChatStatus.running) {
        _finishDeletedSession(sessionId);
      }
    } catch (_) {
      if (_disposed ||
          _invalidated ||
          generation != _streamGenerations[runId]) {
        return;
      }
      // 删除后的 404 不能单独证明终态；当前调用已经收到明确终止事件。
      _finishDeletedSession(sessionId);
    }
  }

  void _finishDeletedSession(String sessionId) {
    final deleted = _deletedSessions.remove(sessionId);
    if (deleted == null) return;
    _deletedActiveRuns.remove(sessionId);
    _pendingCreates.remove(sessionId);
    for (final turn in deleted.turns) {
      _streamGenerations[turn.runId] =
          (_streamGenerations[turn.runId] ?? 0) + 1;
      _stopSubscription(turn.runId);
      _blockedReconnects.remove(turn.runId);
      _bufferedEvents.remove(turn.runId);
    }
    _notify();
  }

  void _reconnect(String runId, int generation, int attempt) {
    if (_disposed || _streamGenerations[runId] != generation) return;
    final turn = _findTurn(runId);
    if (turn == null || turn.status != AiChatStatus.running) return;
    final nextAttempt = attempt + 1;
    if (nextAttempt > _retryDelays.length) {
      _updateTurnByRun(runId, (value) => value.copyWith(retryable: true));
      _notify();
      return;
    }
    final delay = _retryDelays[attempt];
    Timer(delay, () {
      if (_disposed || _streamGenerations[runId] != generation) return;
      if (_subscriptions.containsKey(runId)) return;
      _connect(runId, attempt: nextAttempt);
    });
  }

  void _applyRun(
    String sessionId,
    AiChatRun run, {
    required bool replaceStreamText,
  }) {
    final turn = _turn(sessionId, run.runId);
    if (turn == null) return;
    final terminal = run.status != AiChatStatus.running;
    final updated = turn.copyWith(
      status: run.status,
      text: replaceStreamText && run.status == AiChatStatus.cancelled
          ? run.output ?? ''
          : run.status == AiChatStatus.succeeded
          ? run.output ?? ''
          : turn.text,
      error: run.error,
      clearError: run.error == null,
      loading: false,
      retryable: false,
      cancelling: false,
      connectionState: terminal
          ? AiChatConnectionState.idle
          : turn.connectionState,
    );
    _replaceTurn(run.runId, updated);
    if (terminal) {
      _stopSubscription(run.runId);
      _blockedReconnects.remove(run.runId);
      if (_deletedSessions.containsKey(sessionId)) {
        _finishDeletedSession(sessionId);
      }
    }
    _notify();
  }

  AiChatTurn _turnFromRun(AiChatRun run) => AiChatTurn(
    runId: run.runId,
    input: run.input,
    model: run.model,
    status: run.status,
    createdAt: run.createdAt,
    text:
        run.status == AiChatStatus.succeeded ||
            run.status == AiChatStatus.cancelled
        ? run.output ?? ''
        : '',
    error: run.error,
    loading: run.status == AiChatStatus.running,
    connectionState: run.status == AiChatStatus.running
        ? AiChatConnectionState.connecting
        : AiChatConnectionState.idle,
  );

  void _setConnection(String runId, AiChatConnectionState state) {
    _updateTurnByRun(runId, (turn) => turn.copyWith(connectionState: state));
    _notify();
  }

  void _setTurnError(String runId, Object error) {
    _updateTurnByRun(
      runId,
      (turn) => turn.copyWith(error: _errorFrom(error), retryable: true),
    );
    _error = _messageFor(error);
    _notify();
  }

  void _updateTurn(
    String sessionId,
    String runId,
    AiChatTurn Function(AiChatTurn) update,
  ) {
    final turn = _turn(sessionId, runId);
    if (turn != null) _replaceTurn(runId, update(turn));
  }

  void _updateTurnByRun(String runId, AiChatTurn Function(AiChatTurn) update) {
    final sessionId = _sessionIdForRun(runId);
    if (sessionId != null) _updateTurn(sessionId, runId, update);
  }

  void _replaceTurn(String runId, AiChatTurn updated) {
    final sessionId = _sessionIdForRun(runId);
    if (sessionId == null) return;
    final session = _sessionState(sessionId)!;
    final turns = session.turns
        .map((turn) => turn.runId == runId ? updated : turn)
        .toList(growable: false);
    _replaceSession(session.copyWith(turns: List.unmodifiable(turns)));
  }

  AiChatSession? _session(String? id) {
    if (id == null) return null;
    for (final session in _sessions) {
      if (session.id == id) return session;
    }
    return null;
  }

  AiChatTurn? _turn(String sessionId, String runId) {
    for (final turn
        in _sessionState(sessionId)?.turns ?? const <AiChatTurn>[]) {
      if (turn.runId == runId) return turn;
    }
    return null;
  }

  AiChatTurn? _findTurn(String runId) {
    for (final session in _sessions) {
      for (final turn in session.turns) {
        if (turn.runId == runId) return turn;
      }
    }
    for (final session in _deletedSessions.values) {
      for (final turn in session.turns) {
        if (turn.runId == runId) return turn;
      }
    }
    return null;
  }

  String? _sessionIdForRun(String runId) {
    for (final session in _sessions) {
      if (session.turns.any((turn) => turn.runId == runId)) return session.id;
    }
    for (final session in _deletedSessions.values) {
      if (session.turns.any((turn) => turn.runId == runId)) return session.id;
    }
    return null;
  }

  void _replaceSession(AiChatSession updated) {
    final index = _sessions.indexWhere((session) => session.id == updated.id);
    if (index == -1) {
      if (_deletedSessions.containsKey(updated.id)) {
        _deletedSessions[updated.id] = updated;
      }
      return;
    }
    _sessions[index] = updated;
  }

  AiChatSession? _sessionState(String id) =>
      _session(id) ?? _deletedSessions[id];

  bool _sessionIsRunning(AiChatSession session) =>
      session.creating ||
      session.turns.any((turn) => turn.status == AiChatStatus.running);

  void _stopSubscription(String runId) {
    final subscription = _subscriptions.remove(runId);
    if (subscription != null) unawaited(subscription.cancel());
  }

  String _titleFor(List<AiChatRun> runs) =>
      runs.isEmpty ? '新对话' : _shortTitle(runs.first.input);

  String _shortTitle(String input) {
    final normalized = input.trim().replaceAll(RegExp(r'\s+'), ' ');
    final characters = normalized.characters;
    return characters.length <= 28 ? normalized : '${characters.take(28)}…';
  }

  String _messageFor(Object error) =>
      error is AiChatGatewayException ? error.message : 'AI 对话请求失败，请检查连接后重试。';

  AiChatError _errorFrom(Object error) => error is AiChatGatewayException
      ? AiChatError(
          code: error.code ?? 'request_failed',
          message: error.message,
        )
      : const AiChatError(code: 'connection_failed', message: '连接失败。');

  bool _isUnknownCreateResult(Object error) =>
      error is! AiChatGatewayException ||
      (error.statusCode >= 200 && error.statusCode < 300) ||
      error.statusCode == 408 ||
      error.statusCode >= 500;

  bool _isRetryableConcurrencyRejection(Object error, String sessionId) =>
      error is AiChatGatewayException &&
      error.statusCode == 409 &&
      error.code == 'session_concurrency_limit' &&
      _deletedActiveRuns.isNotEmpty &&
      _visibleActiveSessionIds.where((id) => id != sessionId).length <
          maxSessionCount;

  bool _isAlreadyConnected(Object error) =>
      error is AiChatGatewayException &&
      error.statusCode == 409 &&
      error.code == 'sse_connection_exists';

  void _ensureUsable() {
    if (_disposed || _invalidated) throw StateError('AI 对话状态已失效。');
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }
}

class _PendingCreate {
  const _PendingCreate(this.input, this.key);

  final String input;
  final String key;
}
