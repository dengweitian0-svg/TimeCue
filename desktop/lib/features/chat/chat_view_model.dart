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
  String? _selectedSessionId;
  int _runtimeGeneration = 0;
  bool _acceptingOperations = true;
  bool _disposed = false;
  bool _notifyScheduled = false;
  Timer? _notifyTimer;

  /// 当前保留的会话；列表及其子状态均不可变。
  List<ChatConversation> get conversations => List.unmodifiable(
    _conversationOrder.map((id) => _conversations[id]!).toList(),
  );

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
    if (conversation.activeRun != null) return false;
    return activeRunCount < 3;
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
      phase: ChatRunPhase.creating,
      connectionState: ChatConnectionState.idle,
    );
    conversation._runs.add(run);
    conversation._draft = '';
    conversation._error = null;
    _scheduleNotify();
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

  /// 隔离当前运行代次并释放事件连接，供宿主退出或后端失效时调用。
  void invalidateRuntime() {
    ++_runtimeGeneration;
    _acceptingOperations = false;
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
  }

  Future<void> _createRun(
    ChatConversation conversation,
    ChatRunState run,
  ) async {
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
      _scheduleNotify();
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
    }
  }

  Future<void> _refreshSessionName(
    ChatConversation conversation,
    int generation,
  ) async {
    try {
      final session = await _gateway.getSession(conversation.sessionId);
      if (_disposed || generation != _runtimeGeneration) return;
      if (_conversations[conversation.sessionId] != conversation) return;
      if (session.sessionId != conversation.sessionId) {
        throw StateError('会话名称响应的 session ID 与当前会话不匹配');
      }
      conversation._name = session.name;
      conversation._error = null;
    } catch (error) {
      if (_disposed || generation != _runtimeGeneration) return;
      if (_conversations[conversation.sessionId] != conversation) return;
      conversation._error = _errorMessage(error);
    }
    _scheduleNotify();
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
    final connectionEpoch = ++run._connectionEpoch;
    run._connectionState = run._reconnectAttempts == 0
        ? ChatConnectionState.connected
        : ChatConnectionState.reconnecting;
    if (run._reconnectAttempts > 0 && !run._cancelPending) {
      run._phase = ChatRunPhase.reconnecting;
    }
    _scheduleNotify();
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
    if (signal is ChatRunEventsNoContent) {
      ++run._connectionEpoch;
      final subscription = run._subscription;
      run._subscription = null;
      if (subscription != null) await subscription.cancel();
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
  }

  Future<void> _loadRunSnapshot(ChatRunState run, int generation) async {
    if (run.runId == null) return;
    try {
      final snapshot = await _gateway.getRun(run.runId!);
      if (!_isCurrent(generation, run)) return;
      if (run.isTerminal) return;
      if (snapshot.runId != run.runId || snapshot.sessionId != run.sessionId) {
        throw StateError('运行快照身份与当前会话不匹配');
      }
      run._status = snapshot.status;
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
    ++run._connectionEpoch;
    final subscription = run._subscription;
    run._subscription = null;
    if (subscription != null) await subscription.cancel();
    run._connectionState = ChatConnectionState.disconnected;
    run._error = _errorMessage(error);
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
    run._connectionState = ChatConnectionState.disconnected;
    run._error ??= _errorMessage(error);
    if (run._reconnectAttempts >= maxReconnectAttempts) {
      run._phase = run._cancelPending
          ? ChatRunPhase.stopping
          : ChatRunPhase.reconnecting;
      _scheduleNotify();
      return;
    }
    if (run._retryTimer != null) return;
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
  }

  bool _isCurrent(int generation, ChatRunState run) =>
      !_disposed &&
      generation == _runtimeGeneration &&
      _conversations.values.any(
        (conversation) => conversation._runs.contains(run),
      );

  ChatConversation _requireConversation(String sessionId) {
    final conversation = _conversations[sessionId];
    if (conversation == null) throw ArgumentError.value(sessionId, 'sessionId');
    return conversation;
  }

  void _scheduleNotify() {
    if (_disposed || _notifyScheduled) return;
    _notifyScheduled = true;
    _notifyTimer = Timer(const Duration(milliseconds: 16), () {
      _notifyTimer = null;
      _notifyScheduled = false;
      if (!_disposed) notifyListeners();
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
  bool _isLocal;

  /// 会话是否仍是尚未首次发送的本地草稿。
  bool get isLocal => _isLocal;
  String _draft = '';

  /// 当前会话独立保存的输入草稿。
  String get draft => _draft;
  String? _error;

  /// 会话名称读取失败时显示的内联错误。
  String? get error => _error;
  final List<ChatRunState> _runs = [];

  /// 当前会话已有的运行记录。
  List<ChatRunState> get runs => List.unmodifiable(_runs);

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
    required ChatRunPhase phase,
    required ChatConnectionState connectionState,
  }) : _messageId = messageId,
       _phase = phase,
       _connectionState = connectionState;

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
