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

/// 聊天 run 的终态和运行态。
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
    if (!isRunning && finishedAtText == null) {
      throw const ChatContractFormatException(
        'terminal run must contain finished_at',
      );
    }
    if (status == ChatRunStatus.succeeded &&
            (output == null || errorJson != null) ||
        status == ChatRunStatus.failed &&
            (output != null || errorJson == null) ||
        status == ChatRunStatus.cancelled &&
            (output == null || errorJson != null)) {
      throw const ChatContractFormatException('run fields do not match status');
    }
    return ChatRun(
      runId: _requiredString(json, 'run_id'),
      sessionId: _requiredString(json, 'session_id'),
      model: _requiredString(json, 'model'),
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
  const ChatCreateRunResult(this.runId);

  final String runId;

  /// 从接口 JSON 严格解码创建结果。
  factory ChatCreateRunResult.fromJson(Map<String, Object?> json) =>
      ChatCreateRunResult(_requiredString(json, 'run_id'));
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
  const ChatContentEventData({required this.content, required this.error});

  final String content;
  final ChatProtocolError? error;

  /// 从接口 JSON 严格解码文本事件。
  factory ChatContentEventData.fromJson(Map<String, Object?> json) {
    final error = _nullableObject(json, 'error');
    return ChatContentEventData(
      content: _requiredString(json, 'content'),
      error: error == null ? null : ChatProtocolError.fromJson(error),
    );
  }
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
        function: ChatToolFunction.fromJson(_requiredObject(json, 'function')),
      );
}

/// end 事件数据，不推断工具执行结果。
class ChatEndEventData {
  const ChatEndEventData({required this.stopReason, required this.error});

  final String stopReason;
  final ChatProtocolError? error;

  /// 从接口 JSON 严格解码 end 事件。
  factory ChatEndEventData.fromJson(Map<String, Object?> json) {
    const reasons = {
      'stop',
      'insufficient_system_resource',
      'aborted',
      'cancelled',
    };
    final reason = _requiredString(json, 'stop_reason');
    if (!reasons.contains(reason)) {
      throw ChatContractFormatException('stop_reason has unsupported value');
    }
    final error = _nullableObject(json, 'error');
    return ChatEndEventData(
      stopReason: reason,
      error: error == null ? null : ChatProtocolError.fromJson(error),
    );
  }
}

/// 聊天 SSE 的五种事件类型。
enum ChatEventType { start, thinking, text, tool, end }

/// 解码后的单条 SSE 事件；`eventId` 与工具调用 `toolCallId` 独立。
class ChatSseEvent {
  const ChatSseEvent({
    required this.eventId,
    required this.type,
    required this.data,
  });

  final int eventId;
  final ChatEventType type;
  final Object? data;

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
        .where((value) => value.name == event)
        .firstOrNull;
    if (type == null) {
      throw ChatContractFormatException('event has unsupported value');
    }
    Object? decoded;
    switch (type) {
      case ChatEventType.start:
        decoded = data.isEmpty
            ? const <String, Object?>{}
            : _expectEmptyObject(data);
      case ChatEventType.thinking:
      case ChatEventType.text:
        decoded = ChatContentEventData.fromJson(data);
      case ChatEventType.tool:
        decoded = ChatToolEventData.fromJson(data);
      case ChatEventType.end:
        decoded = ChatEndEventData.fromJson(data);
    }
    return ChatSseEvent(eventId: eventId, type: type, data: decoded);
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

/// 七个聊天 HTTP 操作的窄契约，不规定传输实现。
abstract interface class ChatGateway {
  /// 创建一次 run，重试时由调用方复用原幂等键。
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
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
