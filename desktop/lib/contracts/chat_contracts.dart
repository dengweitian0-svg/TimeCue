import 'dart:async';

/// 聊天会话详情，保留服务端名称原值并映射到 `name`。
class ChatSession {
  const ChatSession({
    required this.sessionId,
    required this.name,
    required this.runIds,
  });

  final String sessionId;
  final String name;
  final List<String> runIds;

  /// 从接口 JSON 严格解码会话详情。
  factory ChatSession.fromJson(Map<String, Object?> json) => ChatSession(
    sessionId: _requiredString(json, 'session_id'),
    name: _requiredString(json, 'session_name'),
    runIds: _requiredStringList(json, 'run_ids'),
  );
}

/// session 列表接口返回的 ID 集合；名称需通过详情接口读取。
class ChatSessionIds {
  const ChatSessionIds(this.values);

  final List<String> values;

  /// 从 `{sessions:[...]}` 接口响应严格解码 session ID 列表。
  factory ChatSessionIds.fromJson(Map<String, Object?> json) =>
      ChatSessionIds(_requiredStringList(json, 'sessions'));
}

/// 聊天 run 的公开状态；cancelled 也可能表示仍在收尾。
enum ChatRunStatus { running, succeeded, failed, cancelled }

/// HTTP run 和 SSE 事件共用的协议错误对象。
class ChatProtocolError {
  const ChatProtocolError({required this.code, required this.message});

  final String code;
  final String message;

  /// 从接口 JSON 严格解码错误对象。
  factory ChatProtocolError.fromJson(Map<String, Object?> json) =>
      ChatProtocolError(
        code: _requiredString(json, 'code'),
        message: _requiredString(json, 'message'),
      );
}

/// 聊天 run 的服务端快照。
class ChatRun {
  const ChatRun({
    required this.runId,
    required this.sessionId,
    required this.model,
    this.provider = 'deepseek',
    required this.input,
    required this.status,
    required this.output,
    required this.error,
    required this.createdAt,
    required this.finishedAt,
  });

  final String runId;
  final String sessionId;
  final String model;
  final String provider;
  final String input;
  final ChatRunStatus status;
  final String? output;
  final ChatProtocolError? error;
  final DateTime createdAt;
  final DateTime? finishedAt;

  /// 从接口 JSON 严格解码 run 快照及其 nullable 字段。
  factory ChatRun.fromJson(Map<String, Object?> json) {
    final statusText = _requiredString(json, 'status');
    final status = ChatRunStatus.values
        .where((value) => value.name == statusText)
        .firstOrNull;
    if (status == null) {
      throw ChatContractFormatException('status has unsupported value');
    }
    final output = _nullableString(json, 'output');
    final errorJson = _nullableObject(json, 'error');
    final finishedAtText = _nullableString(json, 'finished_at');
    final isRunning = status == ChatRunStatus.running;
    if (isRunning &&
        (output != null || errorJson != null || finishedAtText != null)) {
      throw const ChatContractFormatException(
        'running run must not contain terminal fields',
      );
    }
    if (!isRunning &&
        status != ChatRunStatus.cancelled &&
        finishedAtText == null) {
      throw const ChatContractFormatException(
        'terminal run must contain finished_at',
      );
    }
    if ((status == ChatRunStatus.succeeded ||
            status == ChatRunStatus.cancelled) &&
        errorJson != null) {
      throw const ChatContractFormatException('run fields do not match status');
    }
    return ChatRun(
      runId: _requiredString(json, 'run_id'),
      sessionId: _requiredString(json, 'session_id'),
      model: _requiredString(json, 'model'),
      provider: _requiredString(json, 'provider'),
      input: _requiredString(json, 'input'),
      status: status,
      output: output,
      error: errorJson == null ? null : ChatProtocolError.fromJson(errorJson),
      createdAt: _requiredDateTime(json, 'created_at'),
      finishedAt: finishedAtText == null
          ? null
          : _parseDateTime('finished_at', finishedAtText),
    );
  }
}

/// 创建 run 成功响应。
class ChatCreateRunResult {
  const ChatCreateRunResult(
    this.runId, {
    this.status = ChatRunStatus.running,
    this.output,
    this.error,
  });
  final String runId;
  final ChatRunStatus status;
  final String? output;
  final ChatProtocolError? error;

  /// 从固定四字段回执恢复原运行结果。
  factory ChatCreateRunResult.fromJson(Map<String, Object?> json) {
    final status = _runStatus(_requiredString(json, 'status'));
    final error = _nullableObject(json, 'error');
    return ChatCreateRunResult(
      _requiredString(json, 'run_id'),
      status: status,
      output: _nullableString(json, 'output'),
      error: error == null ? null : ChatProtocolError.fromJson(error),
    );
  }
}

/// 取消 run 响应。
class ChatCancelRunResult {
  const ChatCancelRunResult({required this.runId, required this.status});

  final String runId;
  final ChatRunStatus status;

  /// 从接口 JSON 严格解码取消结果。
  factory ChatCancelRunResult.fromJson(Map<String, Object?> json) {
    final value = _requiredString(json, 'status');
    final status = ChatRunStatus.values
        .where((item) => item.name == value)
        .firstOrNull;
    if (status == null) {
      throw ChatContractFormatException('status has unsupported value');
    }
    return ChatCancelRunResult(
      runId: _requiredString(json, 'run_id'),
      status: status,
    );
  }
}

/// 删除 session 成功响应。
class ChatDeleteSessionResult {
  const ChatDeleteSessionResult({
    required this.sessionId,
    required this.status,
  });

  final String sessionId;
  final String status;

  /// 从接口 JSON 严格解码删除结果。
  factory ChatDeleteSessionResult.fromJson(Map<String, Object?> json) {
    final status = _requiredString(json, 'status');
    if (status != 'deleted') {
      throw ChatContractFormatException('status has unsupported value');
    }
    return ChatDeleteSessionResult(
      sessionId: _requiredString(json, 'session_id'),
      status: status,
    );
  }
}

/// SSE 文本或 thinking 事件数据。
class ChatContentEventData {
  const ChatContentEventData({required this.content});
  final String content;

  /// 正文和思考事件只读取 content。
  factory ChatContentEventData.fromJson(Map<String, Object?> json) =>
      ChatContentEventData(content: _requiredString(json, 'content'));
}

/// 工具参数的协议字段；本阶段仅保留原始值，不解释其含义。
class ChatToolArgument {
  const ChatToolArgument({required this.name, required this.value});

  final String name;
  final Object? value;

  /// 从接口 JSON 严格解码工具参数。
  factory ChatToolArgument.fromJson(Map<String, Object?> json) =>
      ChatToolArgument(
        name: _requiredString(json, 'name'),
        value: _required(json, 'val'),
      );
}

/// 工具调用函数信息。
class ChatToolFunction {
  const ChatToolFunction({required this.name, required this.args});

  final String name;
  final ChatToolArgument args;

  /// 从接口 JSON 严格解码工具函数。
  factory ChatToolFunction.fromJson(Map<String, Object?> json) =>
      ChatToolFunction(
        name: _requiredString(json, 'name'),
        args: ChatToolArgument.fromJson(_requiredObject(json, 'args')),
      );
}

/// 工具调用事件数据；`id` 是工具调用 ID，不是 SSE 事件 ID。
class ChatToolEventData {
  const ChatToolEventData({required this.id, required this.function});

  final String id;
  final ChatToolFunction function;

  /// 从接口 JSON 严格解码工具调用数据。
  factory ChatToolEventData.fromJson(Map<String, Object?> json) =>
      ChatToolEventData(
        id: _requiredString(json, 'id'),
        function: ChatToolFunction(
          name: _requiredString(json, 'name'),
          args: ChatToolArgument(name: '', value: _required(json, 'argument')),
        ),
      );
}

/// 工具结果保留原始 JSON 输出及独立错误。
class ChatToolResultData {
  const ChatToolResultData({
    required this.toolCallId,
    required this.output,
    required this.error,
  });

  final String toolCallId;
  final Object? output;
  final ChatProtocolError? error;

  /// 保留结果的 JSON 类型及原工具调用标识，不把错误替换为输出。
  factory ChatToolResultData.fromJson(Map<String, Object?> json) {
    final error = _nullableObject(json, 'error');
    return ChatToolResultData(
      toolCallId: _requiredString(json, 'tool_call_id'),
      output: _required(json, 'output'),
      error: error == null ? null : ChatProtocolError.fromJson(error),
    );
  }
}

/// end 事件数据，不推断工具执行结果。
class ChatEndEventData {
  const ChatEndEventData({required this.status});
  final ChatRunStatus status;

  /// end 只表达状态，具体错误由独立 error 事件提供。
  factory ChatEndEventData.fromJson(Map<String, Object?> json) =>
      ChatEndEventData(status: _runStatus(_requiredString(json, 'status')));
}

ChatRunStatus _runStatus(String value) => ChatRunStatus.values.firstWhere(
  (s) => s.name == value,
  orElse: () =>
      throw ChatContractFormatException('status has unsupported value'),
);

/// 聊天分页和 SSE 共用的八种事件类型。
enum ChatEventType {
  start,
  thinking,
  text,
  error,
  end,
  toolStart,
  toolResult,
  compact,
}

/// 解码后的单条 SSE 事件；`eventId` 与工具调用 `toolCallId` 独立。
class ChatSseEvent {
  const ChatSseEvent({
    required this.eventId,
    required this.type,
    required this.data,
    this.rawData,
  });

  final int eventId;
  final ChatEventType type;
  final Object? data;

  /// 未改写的协议 data，供原始时间线查看。
  final Map<String, Object?>? rawData;

  /// 从 SSE 的事件名、数值 ID 和 JSON data 严格解码协议事件。
  factory ChatSseEvent.fromJson({
    required String id,
    required String event,
    required Map<String, Object?> data,
  }) {
    final eventId = int.tryParse(id);
    if (eventId == null || eventId < 1) {
      throw ChatContractFormatException('SSE id must be a positive integer');
    }
    final type = ChatEventType.values
        .where(
          (value) =>
              value.name ==
              switch (event) {
                'tool_start' => 'toolStart',
                'tool_result' => 'toolResult',
                _ => event,
              },
        )
        .firstOrNull;
    if (type == null) {
      throw ChatContractFormatException('event has unsupported value');
    }
    Object? decoded;
    switch (type) {
      case ChatEventType.start:
      case ChatEventType.compact:
        decoded = data.isEmpty
            ? const <String, Object?>{}
            : _expectEmptyObject(data);
      case ChatEventType.thinking:
      case ChatEventType.text:
        decoded = ChatContentEventData.fromJson(data);
      case ChatEventType.error:
        decoded = ChatProtocolError.fromJson(data);
      case ChatEventType.toolStart:
        final functions = _required(data, 'function');
        if (functions is! List) {
          throw const ChatContractFormatException('function must be an array');
        }
        decoded = functions
            .map(
              (item) => ChatToolEventData.fromJson(
                Map<String, Object?>.from(item as Map),
              ),
            )
            .toList();
      case ChatEventType.toolResult:
        decoded = ChatToolResultData.fromJson(data);
      case ChatEventType.end:
        decoded = ChatEndEventData.fromJson(data);
    }
    return ChatSseEvent(
      eventId: eventId,
      type: type,
      data: decoded,
      rawData: Map.unmodifiable(data),
    );
  }
}

/// 原始运行事件的只读分页结果，游标不代表客户端消费位置。
class ChatEventPage {
  const ChatEventPage({
    required this.events,
    required this.nextAfterSeq,
    required this.hasMore,
  });

  final List<ChatSseEvent> events;
  final int nextAfterSeq;
  final bool hasMore;

  /// 解码分页原始事件，分页位置不代表客户端已经应用的位置。
  factory ChatEventPage.fromJson(Map<String, Object?> json) {
    final entries = _required(json, 'events');
    final next = _required(json, 'next_after_seq');
    final more = _required(json, 'has_more');
    if (entries is! List || next is! int || next < 0 || more is! bool) {
      throw const ChatContractFormatException('invalid event page');
    }
    final events = <ChatSseEvent>[];
    for (final entry in entries) {
      if (entry is! Map<String, Object?> || entry['seq'] is! int) {
        throw const ChatContractFormatException('invalid paged event');
      }
      events.add(
        ChatSseEvent.fromJson(
          id: '${entry['seq']}',
          event: _requiredString(entry, 'type'),
          data: _requiredObject(entry, 'data'),
        ),
      );
    }
    if (events.isNotEmpty && next != events.last.eventId) {
      throw const ChatContractFormatException(
        'event page cursor differs from last event',
      );
    }
    return ChatEventPage(
      events: List.unmodifiable(events),
      nextAfterSeq: next,
      hasMore: more,
    );
  }
}

/// run 事件流内传递的数据帧或 HTTP 204 终态查询信号。
sealed class ChatRunEventSignal {
  const ChatRunEventSignal();
}

/// 收到的单条 SSE 事件。
class ChatRunEventFrame extends ChatRunEventSignal {
  const ChatRunEventFrame(this.event);

  final ChatSseEvent event;
}

/// 服务端以 HTTP 204 表明 run 已终态且不再提供事件回放。
class ChatRunEventsNoContent extends ChatRunEventSignal {
  const ChatRunEventsNoContent();
}

/// 统一保留聊天 HTTP 错误码和服务端消息。
class ChatHttpError {
  const ChatHttpError({
    required this.statusCode,
    required this.code,
    required this.message,
  });

  final int statusCode;
  final String code;
  final String message;

  /// 从 HTTP 错误响应严格解码 `{error:{code,message}}`。
  factory ChatHttpError.fromJson(int statusCode, Map<String, Object?> json) {
    final error = ChatProtocolError.fromJson(_requiredObject(json, 'error'));
    return ChatHttpError(
      statusCode: statusCode,
      code: error.code,
      message: error.message,
    );
  }

  /// 返回稳定的界面错误类别，同时保留原始服务端错误信息。
  ChatHttpFailureKind get kind => switch (code) {
    'invalid_request' ||
    'session_deleted' ||
    'session_not_found' => ChatHttpFailureKind.invalidRequest,
    'session_busy' ||
    'session_concurrency_limit' ||
    'idempotency_key_conflict' ||
    'sse_connection_exists' => ChatHttpFailureKind.conflict,
    'run_terminal' => ChatHttpFailureKind.runTerminal,
    'event_buffer_full' => ChatHttpFailureKind.eventBufferFull,
    'run_not_found' => ChatHttpFailureKind.notFound,
    'internal_error' || 'internal' => ChatHttpFailureKind.internal,
    _ => ChatHttpFailureKind.server,
  };
}

/// 聊天 HTTP 错误的稳定分类。
enum ChatHttpFailureKind {
  invalidRequest,
  conflict,
  runTerminal,
  notFound,
  eventBufferFull,
  internal,
  server,
}

/// 聊天 JSON 形状不符合协议时抛出的格式错误。
class ChatContractFormatException implements Exception {
  const ChatContractFormatException(this.message);

  final String message;

  @override
  String toString() => 'ChatContractFormatException: $message';
}

/// 本地配置中可选择的提供方与模型身份。
class ChatModelOption {
  const ChatModelOption({
    required this.provider,
    required this.model,
    required this.available,
  });
  final String provider;
  final String model;
  final bool available;
  String get identity => '$provider/$model';

  /// 读取服务端声明的本地可用性。
  factory ChatModelOption.fromJson(Map<String, Object?> json) {
    final available = _required(json, 'available');
    if (available is! bool) {
      throw const ChatContractFormatException('available must be boolean');
    }
    return ChatModelOption(
      provider: _requiredString(json, 'provider'),
      model: _requiredString(json, 'model'),
      available: available,
    );
  }
}

/// 模型配置列表读取端口。
abstract interface class ChatModelGateway {
  /// 查询全部模型及本地可用性。
  Future<List<ChatModelOption>> getModels();
}

/// 运行事件历史的只读分页端口。
abstract interface class ChatEventHistoryGateway {
  /// 按 seq 分页读取完整原始事件，不保存消费位置。
  Future<ChatEventPage> getRunEvents(
    String runId, {
    int afterSeq = 0,
    int limit = 10,
  });
}

/// 七个聊天 HTTP 操作的窄契约，不规定传输实现。
abstract interface class ChatGateway {
  /// 创建一次 run，重试时由调用方复用原幂等键。
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    String provider = 'deepseek',
    required String idempotencyKey,
  });

  /// 查询 run 的服务端快照。
  Future<ChatRun> getRun(String runId);

  /// 订阅 run 的事件流，可选使用已成功处理的事件游标恢复。
  /// 204 必须以 [ChatRunEventsNoContent] 明确发出；无 end 的流关闭表示连接中断。
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId});

  /// 请求取消 run；返回运行态并不代表取消已经完成。
  Future<ChatCancelRunResult> cancelRun(String runId);

  /// 查询 session 名称和所属 run。
  Future<ChatSession> getSession(String sessionId);

  /// 删除 session。
  Future<ChatDeleteSessionResult> deleteSession(String sessionId);

  /// 查询 session ID 列表。
  Future<List<String>> listSessions();
}

Object? _required(Map<String, Object?> json, String key) {
  if (!json.containsKey(key)) {
    throw ChatContractFormatException('$key is required');
  }
  return json[key];
}

String _requiredString(Map<String, Object?> json, String key) {
  final value = _required(json, key);
  if (value is! String) {
    throw ChatContractFormatException('$key must be a string');
  }
  return value;
}

String? _nullableString(Map<String, Object?> json, String key) {
  final value = _required(json, key);
  if (value == null) return null;
  if (value is! String) {
    throw ChatContractFormatException('$key must be a string or null');
  }
  return value;
}

Map<String, Object?> _requiredObject(Map<String, Object?> json, String key) {
  final value = _required(json, key);
  if (value is! Map) {
    throw ChatContractFormatException('$key must be an object');
  }
  return value.map((key, value) {
    if (key is! String) {
      throw ChatContractFormatException('$key keys must be strings');
    }
    return MapEntry(key, value);
  });
}

Map<String, Object?>? _nullableObject(Map<String, Object?> json, String key) {
  final value = _required(json, key);
  if (value == null) return null;
  if (value is! Map) {
    throw ChatContractFormatException('$key must be an object or null');
  }
  return value.map((key, value) {
    if (key is! String) {
      throw ChatContractFormatException('$key keys must be strings');
    }
    return MapEntry(key, value);
  });
}

List<String> _requiredStringList(Map<String, Object?> json, String key) {
  final value = _required(json, key);
  if (value is! List || value.any((item) => item is! String)) {
    throw ChatContractFormatException('$key must be a string array');
  }
  return List<String>.unmodifiable(value.cast<String>());
}

DateTime _requiredDateTime(Map<String, Object?> json, String key) =>
    _parseDateTime(key, _requiredString(json, key));

DateTime _parseDateTime(String key, String value) {
  final dateTime = DateTime.tryParse(value);
  if (dateTime == null ||
      !RegExp(r'^\d{4}-\d\d-\d\dT.*(?:Z|[+-]\d\d:\d\d)$').hasMatch(value)) {
    throw ChatContractFormatException('$key must be an RFC3339 timestamp');
  }
  return dateTime;
}

Object _expectEmptyObject(Map<String, Object?> data) {
  if (data.isNotEmpty) {
    throw const ChatContractFormatException('start data must be empty');
  }
  return const <String, Object?>{};
}
