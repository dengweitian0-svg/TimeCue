import 'dart:async';
import 'dart:convert';
import 'dart:io';

import '../contracts/ai_chat.dart';

/// 通过固定 `/v1` 路径访问 Go AI 对话接口。
class HttpAiChatGateway implements AiChatGateway {
  HttpAiChatGateway({
    HttpClient? client,
    Uri? base,
    bool Function()? canRequest,
    Duration requestTimeout = const Duration(seconds: 15),
  }) : _base = base ?? Uri.parse('http://127.0.0.1:10086'),
       _client =
           client ?? _createClient(base ?? Uri.parse('http://127.0.0.1:10086')),
       _canRequest = canRequest ?? _allow,
       _requestTimeout = requestTimeout;

  static bool _allow() => true;

  static HttpClient _createClient(Uri base) {
    final client = HttpClient()
      ..connectionTimeout = const Duration(seconds: 10);
    if (base.host == 'localhost' ||
        InternetAddress.tryParse(base.host)?.isLoopback == true) {
      client.findProxy = (_) => 'DIRECT';
    }
    return client;
  }

  final Uri _base;
  final HttpClient _client;
  final bool Function() _canRequest;
  final Duration _requestTimeout;
  final Set<HttpClientRequest> _requests = {};
  final Set<HttpClient> _eventClients = {};
  final Set<void Function()> _eventInvalidators = {};
  int _generation = 0;
  bool _invalid = false;

  /// 读取可见会话 ID 列表。
  @override
  Future<List<String>> listSessions() async {
    final value = await _json('GET', '/v1/session');
    if (value is! Map<String, dynamic> || value['sessions'] is! List) {
      throw const FormatException('AI 对话会话列表格式无效。');
    }
    return List.unmodifiable(
      (value['sessions'] as List).map((item) => _string(item, 'sessions[]')),
    );
  }

  /// 读取会话下的全部历史 run。
  @override
  Future<List<AiChatRun>> getSessionRuns(String sessionId) async {
    final value = await _json('GET', '/v1/session/${_segment(sessionId)}');
    if (value is! Map<String, dynamic> ||
        value['session_id'] != sessionId ||
        value['run_ids'] is! List) {
      throw const FormatException('AI 对话历史列表格式无效。');
    }
    final ids = (value['run_ids'] as List)
        .map((item) => _string(item, 'run_ids[]'))
        .toList(growable: false);
    final runs = <AiChatRun>[];
    for (final id in ids) {
      final run = await getRun(id);
      if (run.sessionId != sessionId) {
        throw const FormatException('AI 对话历史包含其他会话的 run。');
      }
      runs.add(run);
    }
    return List.unmodifiable(runs);
  }

  /// 读取单条 run 的完整状态。
  @override
  Future<AiChatRun> getRun(String runId) async {
    final endpoint = '/v1/run/${_segment(runId)}';
    final Object? payload = await _json('GET', endpoint);
    late final AiChatRun run;
    try {
      run = AiChatRun.fromJson(payload);
    } on FormatException {
      throw AiChatGatewayException(
        statusCode: HttpStatus.ok,
        endpoint: endpoint,
        message: 'AI 对话响应格式无效。',
      );
    }
    if (run.runId != runId) {
      throw const FormatException('AI 对话响应包含其他 run。');
    }
    return run;
  }

  /// 提交本轮原始输入并返回后端创建的 run ID。
  @override
  Future<String> createRun(
    String sessionId,
    String input, {
    required String idempotencyKey,
    String model = 'deepseek-flash',
  }) async {
    final value = await _json(
      'POST',
      '/v1/run',
      body: {'session_id': sessionId, 'input': input, 'model': model},
      headers: {'Idempotency-Key': idempotencyKey},
      expected: const {HttpStatus.accepted},
    );
    if (value is! Map<String, dynamic>) {
      throw const FormatException('AI 对话创建响应格式无效。');
    }
    return _string(value['run_id'], 'run_id');
  }

  /// 请求取消 run，并返回服务端返回的当前状态。
  @override
  Future<AiChatStatus> cancelRun(String runId) async {
    final value = await _json(
      'POST',
      '/v1/run/${_segment(runId)}/cancel',
      expected: const {HttpStatus.accepted, HttpStatus.ok},
    );
    if (value is! Map<String, dynamic> || value['run_id'] != runId) {
      throw const FormatException('AI 对话取消响应格式无效。');
    }
    return _status(value['status']);
  }

  /// 软删除会话并等待后端确认。
  @override
  Future<void> deleteSession(String sessionId) async {
    final value = await _json(
      'DELETE',
      '/v1/session/${_segment(sessionId)}',
      expected: const {HttpStatus.ok},
    );
    if (value is! Map<String, dynamic> ||
        value['session_id'] != sessionId ||
        value['status'] != 'deleted') {
      throw const FormatException('AI 对话删除响应格式无效。');
    }
  }

  /// 监听 SSE 事件；收到 204 时发送 noContent 信号。
  @override
  Stream<AiChatEvent> watchRun(String runId, {int? lastEventId}) {
    late final StreamController<AiChatEvent> controller;
    HttpClient? streamClient;
    HttpClientRequest? request;
    StreamSubscription<String>? subscription;
    final generation = _generation;
    var stopped = false;
    late void Function() cleanup;
    late void Function() invalidateStream;

    invalidateStream = () {
      if (generation == _generation && !_invalid) return;
      if (!controller.isClosed) unawaited(controller.close());
      cleanup();
    };

    cleanup = () {
      if (stopped) return;
      stopped = true;
      _eventInvalidators.remove(invalidateStream);
      final activeSubscription = subscription;
      if (activeSubscription != null) {
        unawaited(activeSubscription.cancel());
      }
      if (request != null) {
        _requests.remove(request);
        request!.abort();
      }
      if (streamClient != null) {
        _eventClients.remove(streamClient);
        streamClient!.close(force: true);
      }
    };

    controller = StreamController<AiChatEvent>(
      onListen: () async {
        try {
          _check(generation);
          streamClient = _createClient(_base);
          _eventClients.add(streamClient!);
          request = await streamClient!
              .getUrl(_uri('/v1/run/${_segment(runId)}/event'))
              .timeout(_requestTimeout);
          if (stopped) {
            request!.abort();
            return;
          }
          _check(generation);
          _requests.add(request!);
          request!.headers.set(HttpHeaders.acceptHeader, 'text/event-stream');
          if (lastEventId != null) {
            request!.headers.set('Last-Event-ID', '$lastEventId');
          }
          final response = await request!.close().timeout(_requestTimeout);
          _requests.remove(request);
          _check(generation);
          if (response.statusCode == HttpStatus.noContent) {
            controller.add(const AiChatEvent.noContent());
            cleanup();
            await controller.close();
            return;
          }
          if (response.statusCode != HttpStatus.ok) {
            final bytes = await _readBounded(response).timeout(_requestTimeout);
            _check(generation);
            throw _httpError(
              response.statusCode,
              '/v1/run/:run_id/event',
              bytes,
            );
          }
          if (!(response.headers.contentType?.mimeType ?? '').startsWith(
            'text/event-stream',
          )) {
            throw const FormatException('AI 对话事件流类型无效。');
          }
          final parser = _SseParser((event) {
            if (!stopped && generation == _generation && !_invalid) {
              controller.add(event);
            }
          });
          subscription = response
              .transform(utf8.decoder)
              .transform(const LineSplitter())
              .listen(
                (line) {
                  try {
                    parser.addLine(line);
                  } catch (error, stack) {
                    if (generation == _generation && !_invalid) {
                      controller.addError(error, stack);
                    }
                    unawaited(controller.close());
                    cleanup();
                  }
                },
                onError: (Object error, StackTrace stack) {
                  if (!stopped) {
                    if (generation == _generation && !_invalid) {
                      controller.addError(error, stack);
                    }
                    unawaited(controller.close());
                    cleanup();
                  }
                },
                onDone: () {
                  if (!stopped) {
                    try {
                      parser.finish();
                      controller.close();
                    } catch (error, stack) {
                      if (generation == _generation && !_invalid) {
                        controller.addError(error, stack);
                      }
                      controller.close();
                    }
                    cleanup();
                  }
                },
                cancelOnError: true,
              );
        } catch (error, stack) {
          if (!stopped && generation == _generation && !_invalid) {
            controller.addError(error, stack);
          }
          if (!controller.isClosed) await controller.close();
          cleanup();
        }
      },
      onCancel: cleanup,
    );
    _eventInvalidators.add(invalidateStream);
    return controller.stream.where(
      (_) => generation == _generation && !_invalid,
    );
  }

  /// 封锁后续请求，使未完成操作和事件流全部失效。
  @override
  void invalidate() {
    if (_invalid) return;
    _invalid = true;
    _generation++;
    for (final request in _requests.toList()) {
      request.abort();
    }
    _requests.clear();
    _client.close(force: true);
    for (final closeStream in _eventInvalidators.toList()) {
      closeStream();
    }
    _eventInvalidators.clear();
    for (final client in _eventClients.toList()) {
      client.close(force: true);
    }
    _eventClients.clear();
  }

  /// 释放连接池和全部事件流。
  @override
  void dispose() => invalidate();

  Future<dynamic> _json(
    String method,
    String path, {
    Object? body,
    Map<String, String>? headers,
    Set<int> expected = const {HttpStatus.ok},
  }) async {
    final generation = _generation;
    _check(generation);
    HttpClientRequest? request;
    try {
      final opened = await _client
          .openUrl(method, _uri(path))
          .timeout(_requestTimeout);
      request = opened;
      _requests.add(opened);
      _check(generation);
      opened.headers.set(HttpHeaders.acceptHeader, 'application/json');
      headers?.forEach((name, value) => opened.headers.set(name, value));
      if (body != null) {
        opened.headers.contentType = ContentType.json;
        opened.write(jsonEncode(body));
      }
      final response = await opened.close().timeout(_requestTimeout);
      _requests.remove(opened);
      final bytes = await _readBounded(response).timeout(_requestTimeout);
      _check(generation);
      if (!expected.contains(response.statusCode)) {
        throw _httpError(response.statusCode, path, bytes);
      }
      if (response.statusCode == HttpStatus.noContent) return null;
      if (bytes.isEmpty) {
        throw AiChatGatewayException(
          statusCode: response.statusCode,
          endpoint: path,
          message: 'AI 对话响应为空。',
        );
      }
      final Object? value;
      try {
        value = jsonDecode(utf8.decode(bytes));
      } on FormatException {
        throw AiChatGatewayException(
          statusCode: response.statusCode,
          endpoint: path,
          message: 'AI 对话响应格式无效。',
        );
      }
      if (value is! Map<String, dynamic>) {
        throw AiChatGatewayException(
          statusCode: response.statusCode,
          endpoint: path,
          message: 'AI 对话响应格式无效。',
        );
      }
      return value;
    } catch (error) {
      request?.abort(error);
      rethrow;
    } finally {
      if (request != null) _requests.remove(request);
    }
  }

  void _check(int generation) {
    if (_invalid || generation != _generation) {
      throw const AiChatGatewayException(
        statusCode: 0,
        endpoint: '/v1',
        message: 'AI 对话服务已失效。',
      );
    }
    bool admitted;
    try {
      admitted = _canRequest();
    } catch (_) {
      admitted = false;
    }
    if (!admitted) {
      throw const AiChatGatewayException(
        statusCode: 0,
        endpoint: '/v1',
        message: 'AI 对话服务尚未就绪。',
      );
    }
  }

  Uri _uri(String path) =>
      _base.replace(path: path, query: null, fragment: null);

  static String _segment(String value) => Uri.encodeComponent(value);

  static String _string(Object? value, String field) {
    if (value is! String || value.trim().isEmpty) {
      throw FormatException('AI 对话字段无效：$field。');
    }
    return value;
  }

  static AiChatStatus _status(Object? value) => switch (value) {
    'running' => AiChatStatus.running,
    'succeeded' => AiChatStatus.succeeded,
    'failed' => AiChatStatus.failed,
    'cancelled' => AiChatStatus.cancelled,
    _ => throw const FormatException('AI 对话状态无效。'),
  };

  static AiChatGatewayException _httpError(
    int status,
    String path,
    List<int> body,
  ) {
    String? code;
    String message = 'AI 对话请求失败（HTTP $status）。';
    try {
      final decoded = jsonDecode(utf8.decode(body));
      final error = decoded is Map<String, dynamic> ? decoded['error'] : null;
      if (error is Map<String, dynamic>) {
        final rawCode = error['code'];
        final rawMessage = error['message'];
        if (rawCode is String && rawCode.length <= 96) {
          code = rawCode;
        }
        if (rawMessage is String && rawMessage.length <= 180) {
          message = rawMessage;
        }
      }
    } catch (_) {
      // 非标准错误响应只保留 HTTP 状态与固定端点，不附加正文内容。
    }
    return AiChatGatewayException(
      statusCode: status,
      endpoint: path,
      code: code,
      message: message,
    );
  }

  static Future<List<int>> _readBounded(HttpClientResponse response) async {
    const limit = 1024 * 1024;
    final bytes = <int>[];
    await for (final chunk in response) {
      if (bytes.length + chunk.length > limit) {
        throw const FormatException('AI 对话响应超过大小限制。');
      }
      bytes.addAll(chunk);
    }
    return bytes;
  }
}

class _SseParser {
  _SseParser(this.onEvent);

  final void Function(AiChatEvent event) onEvent;
  String? _id;
  String? _type;
  final List<String> _data = [];

  void addLine(String line) {
    if (line.isEmpty) {
      _dispatch();
      return;
    }
    if (line.startsWith(':')) return;
    final separator = line.indexOf(':');
    final field = separator < 0 ? line : line.substring(0, separator);
    var value = separator < 0 ? '' : line.substring(separator + 1);
    if (value.startsWith(' ')) value = value.substring(1);
    switch (field) {
      case 'id':
        if (!value.contains('\u0000')) _id = value;
        break;
      case 'event':
        _type = value;
        break;
      case 'data':
        _data.add(value);
        break;
    }
  }

  void finish() => _dispatch();

  void _dispatch() {
    if (_data.isEmpty) {
      _id = null;
      _type = null;
      return;
    }
    final eventId = int.tryParse(_id ?? '');
    if (eventId == null || eventId < 1) {
      throw const FormatException('AI 对话事件 ID 无效。');
    }
    final eventType = _type;
    if (!const {
      'start',
      'thinking',
      'text',
      'tool',
      'end',
    }.contains(eventType)) {
      throw const FormatException('AI 对话事件类型无效。');
    }
    final value = jsonDecode(_data.join('\n'));
    if (value is! Map<String, dynamic>) {
      throw const FormatException('AI 对话事件数据格式无效。');
    }
    onEvent(
      AiChatEvent(id: eventId, type: eventType, data: Map.unmodifiable(value)),
    );
    _id = null;
    _type = null;
    _data.clear();
  }
}
