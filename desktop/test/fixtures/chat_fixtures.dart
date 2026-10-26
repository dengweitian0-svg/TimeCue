import 'dart:convert';
import 'dart:typed_data';

/// 固定接口响应和 SSE 线缆数据，供聊天协议测试与后续传输测试复用。
class ChatFixtures {
  static const _sessionId = '550e8400-e29b-41d4-a716-446655440000';
  static const _runId = 'd9428888-122b-4f6f-9b47-5ea7b2915210';

  /// 创建 run 的成功响应。
  static Map<String, Object?> createRunResponse() => {
    'run_id': _runId,
    'status': 'running',
    'output': null,
    'error': null,
  };

  /// running run 的 GET 响应。
  static Map<String, Object?> runningRun() => {
    ...succeededRun(),
    'status': 'running',
    'output': null,
    'error': null,
    'finished_at': null,
  };

  /// 活跃 run 接受取消后的响应。
  static Map<String, Object?> cancelAcceptedResponse() => {
    'run_id': _runId,
    'status': 'running',
  };

  /// 删除 session 成功响应。
  static Map<String, Object?> deleteSessionResponse() => {
    'session_id': _sessionId,
    'status': 'deleted',
  };

  /// session ID 列表响应；接口仅返回 ID，不返回名称。
  static Map<String, Object?> listSessionsResponse() => {
    'sessions': [_sessionId],
  };

  /// 标准 HTTP 聊天错误响应。
  static Map<String, Object?> httpError({
    String code = 'invalid_request',
    String message = '请求无效',
  }) => {
    'error': {'code': code, 'message': message},
  };

  /// 已终态 run 的 SSE GET 返回 204，不建立流且不带正文。
  static ({int statusCode, String body}) get noContentResponse =>
      (statusCode: 204, body: '');

  /// 会话详情中的中文名称不同于首条用户输入。
  static Map<String, Object?> session({
    String name = '中文会话名称',
    List<String> runIds = const [],
  }) => {'session_id': _sessionId, 'session_name': name, 'run_ids': runIds};

  /// 成功 run 的完整快照。
  static Map<String, Object?> succeededRun({String output = '最终回复'}) => {
    'run_id': _runId,
    'session_id': _sessionId,
    'model': 'deepseek-flash',
    'provider': 'deepseek',
    'input': '用户原始输入',
    'status': 'succeeded',
    'output': output,
    'error': null,
    'created_at': '2026-10-17T09:00:00+08:00',
    'finished_at': '2026-10-17T09:00:01+08:00',
  };

  /// failed 或 cancelled 的 run 快照。
  static Map<String, Object?> terminalRun({required String status}) => {
    ...succeededRun(),
    'status': status,
    'output': status == 'cancelled' ? '已确认检查点' : null,
    'error': status == 'failed'
        ? {'code': 'upstream_failed', 'message': '模型服务失败'}
        : null,
  };

  /// 返回一个含非空工具参数名的调用事件，供验证名称必须取 function.name。
  static Map<String, Object?> toolEvent({
    required String callId,
    required String name,
  }) => {
    'function': [
      {
        'id': callId,
        'name': name,
        'argument': {'query': '中文参数'},
      },
    ],
  };

  /// 固定且有序的 SSE 帧，含思考、正文、重复投递、多工具交错和失败结束。
  static List<String> get interleavedFailureFrames => [
    'id: 1\nevent: start\ndata: {}\n\n',
    'id: 2\nevent: thinking\ndata: {"content":"hidden reasoning"}\n\n',
    'id: 3\nevent: text\ndata: {"content":"你好，"}\n\n',
    'id: 4\nevent: tool_start\ndata: {"function":[{"id":"call-a","name":"search_schedules","argument":{"q":"日期"}}]}\n\n',
    'id: 5\nevent: tool_start\ndata: {"function":[{"id":"call-b","name":"get_calendar","argument":{}}]}\n\n',
    'id: 6\nevent: thinking\ndata: {"content":"more hidden reasoning"}\n\n',
    'id: 7\nevent: tool_start\ndata: {"function":[{"id":"call-c","name":"search_schedules","argument":{"q":"明天"}}]}\n\n',
    'id: 8\nevent: text\ndata: {"content":"世界。"}\n\n',
    'id: 9\nevent: end\ndata: {"status":"failed"}\n\n',
  ];

  /// 断线后从游标之后重放的相同事件 ID；重复片段不代表新工具调用。
  static List<String> get reconnectReplayFrames => [
    'id: 7\nevent: tool_start\ndata: {"function":[{"id":"call-c","name":"search_schedules","argument":{"q":"明天"}}]}\n\n',
    'id: 8\nevent: text\ndata: {"content":"世界。"}\n\n',
    'id: 9\nevent: end\ndata: {"status":"failed"}\n\n',
  ];

  /// 正常的 start、thinking、增量文本和成功 end 序列。
  static List<String> get normalSuccessFrames => [
    'id: 1\nevent: start\ndata: {}\n\n',
    'id: 2\nevent: thinking\ndata: {"content":"思考片段"}\n\n',
    'id: 3\nevent: text\ndata: {"content":"完整回复。"}\n\n',
    'id: 4\nevent: end\ndata: {"status":"succeeded"}\n\n',
  ];

  /// 多工具来源的线缆序列，不声明工具间的并行或先后执行关系。
  static List<String> get multipleToolFrames => [
    'id: 10\nevent: tool_start\ndata: {"function":[{"id":"call-one","name":"search_schedules","argument":{}}]}\n\n',
    'id: 11\nevent: tool_start\ndata: {"function":[{"id":"call-two","name":"get_calendar","argument":{}}]}\n\n',
    'id: 12\nevent: tool_start\ndata: {"function":[{"id":"call-three","name":"search_schedules","argument":{}}]}\n\n',
  ];

  /// 同一 SSE 帧重复投递，事件 ID 和工具调用 ID 均保持原值。
  static String get duplicateToolFrame => multipleToolFrames.first;

  /// 已成功取消的结束帧，取消结果不等于工具执行结果。
  static String get cancelledEndFrame =>
      'id: 12\nevent: end\ndata: {"status":"cancelled"}\n\n';

  /// 将固定 SSE 帧编码为 UTF-8 字节，供分片传输测试使用。
  static Uint8List utf8Bytes(String frame) =>
      Uint8List.fromList(utf8.encode(frame));

  /// 按指定字节宽度切分 UTF-8 字节，允许切点落在多字节字符中间。
  static List<Uint8List> utf8Chunks(String frame, {int chunkSize = 3}) {
    if (chunkSize < 1) throw ArgumentError.value(chunkSize, 'chunkSize');
    final bytes = utf8Bytes(frame);
    return [
      for (var offset = 0; offset < bytes.length; offset += chunkSize)
        Uint8List.fromList(
          bytes.sublist(offset, (offset + chunkSize).clamp(0, bytes.length)),
        ),
    ];
  }

  /// 将单条固定 SSE 帧拆成 ID、事件名和 JSON 数据，不提供流解析逻辑。
  static ({String id, String event, Map<String, Object?> data})
  decodeFrameForContractTest(String frame) {
    final lines = frame.trimRight().split('\n');
    final id = lines.firstWhere((line) => line.startsWith('id: ')).substring(4);
    final event = lines
        .firstWhere((line) => line.startsWith('event: '))
        .substring(7);
    final dataLine = lines
        .firstWhere((line) => line.startsWith('data: '))
        .substring(6);
    final value = jsonDecode(dataLine);
    if (value is! Map<String, Object?>) {
      throw const FormatException('SSE data must be an object');
    }
    return (id: id, event: event, data: value);
  }
}
