import 'dart:math';

/// AI 对话的稳定状态值。
enum AiChatStatus { running, succeeded, failed, cancelled }

/// AI 对话失败信息。
class AiChatError {
  const AiChatError({required this.code, required this.message});

  final String code;
  final String message;

  /// 从接口错误对象创建有限长度的错误信息。
  factory AiChatError.fromJson(Object? value) {
    if (value is! Map<String, dynamic>) {
      throw const FormatException('AI 对话错误格式无效。');
    }
    return AiChatError(
      code: _requiredString(value['code'], 'error.code'),
      message: _requiredString(value['message'], 'error.message'),
    );
  }
}

/// 一次用户输入及其完整回复。
class AiChatRun {
  const AiChatRun({
    required this.runId,
    required this.sessionId,
    required this.model,
    required this.input,
    required this.status,
    required this.createdAt,
    this.output,
    this.error,
    this.finishedAt,
  });

  final String runId;
  final String sessionId;
  final String model;
  final String input;
  final AiChatStatus status;
  final String? output;
  final AiChatError? error;
  final DateTime createdAt;
  final DateTime? finishedAt;

  /// 从 GET run 的直接 JSON 响应创建 run。
  factory AiChatRun.fromJson(Object? value) {
    if (value is! Map<String, dynamic>) {
      throw const FormatException('AI 对话响应格式无效。');
    }
    final status = switch (value['status']) {
      'running' => AiChatStatus.running,
      'succeeded' => AiChatStatus.succeeded,
      'failed' => AiChatStatus.failed,
      'cancelled' => AiChatStatus.cancelled,
      _ => throw const FormatException('AI 对话状态无效。'),
    };
    return AiChatRun(
      runId: _requiredString(value['run_id'], 'run_id'),
      sessionId: _requiredString(value['session_id'], 'session_id'),
      model: _requiredString(value['model'], 'model'),
      input: _requiredString(value['input'], 'input'),
      status: status,
      output: _nullableString(value['output'], 'output'),
      error: value['error'] == null
          ? null
          : AiChatError.fromJson(value['error']),
      createdAt: DateTime.parse(
        _requiredString(value['created_at'], 'created_at'),
      ),
      finishedAt: value['finished_at'] == null
          ? null
          : DateTime.parse(
              _requiredString(value['finished_at'], 'finished_at'),
            ),
    );
  }
}

/// AI 对话事件。`noContent` 表示 SSE 返回 204，应改用 GET run。
class AiChatEvent {
  const AiChatEvent({
    required this.id,
    required this.type,
    required this.data,
    this.noContent = false,
  });

  const AiChatEvent.noContent()
    : id = null,
      type = null,
      data = const {},
      noContent = true;

  final int? id;
  final String? type;
  final Map<String, dynamic> data;
  final bool noContent;
}

/// AI 对话请求失败；不保存或暴露未经处理的响应正文。
class AiChatGatewayException implements Exception {
  const AiChatGatewayException({
    required this.statusCode,
    required this.endpoint,
    this.code,
    this.message = 'AI 对话请求失败。',
  });

  final int statusCode;
  final String endpoint;
  final String? code;
  final String message;

  @override
  String toString() => message;
}

/// AI 对话 HTTP 能力的窄契约。
abstract interface class AiChatGateway {
  /// 读取当前可见的会话 ID。
  Future<List<String>> listSessions();

  /// 读取会话的全部历史 run，结果按后端创建顺序排列。
  Future<List<AiChatRun>> getSessionRuns(String sessionId);

  /// 读取单条 run 的状态及完整结果。
  Future<AiChatRun> getRun(String runId);

  /// 提交用户原文并创建 run；同一次重试必须复用幂等键。
  Future<String> createRun(
    String sessionId,
    String input, {
    required String idempotencyKey,
    String model = 'deepseek-flash',
  });

  /// 请求取消 run，并返回后端确认的当前状态。
  Future<AiChatStatus> cancelRun(String runId);

  /// 软删除会话。
  Future<void> deleteSession(String sessionId);

  /// 连接 run 的事件流；204 时发出 `AiChatEvent.noContent()` 后结束。
  Stream<AiChatEvent> watchRun(String runId, {int? lastEventId});

  /// 关闭网关并使所有未完成结果失效。
  void invalidate();

  /// 释放网关持有的网络资源。
  void dispose();
}

/// 生成符合 RFC 4122 的 UUID v4。
String generateAiChatUuidV4() {
  final bytes = List<int>.generate(16, (_) => _secureRandomByte());
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  final hex = bytes
      .map((byte) => byte.toRadixString(16).padLeft(2, '0'))
      .join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-'
      '${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}

final _secureRandom = Random.secure();
int _secureRandomByte() => _secureRandom.nextInt(256);

String _requiredString(Object? value, String field) {
  if (value is! String || value.trim().isEmpty) {
    throw FormatException('AI 对话字段无效：$field。');
  }
  return value;
}

String? _nullableString(Object? value, String field) {
  if (value == null) return null;
  if (value is! String) throw FormatException('AI 对话字段无效：$field。');
  return value;
}
