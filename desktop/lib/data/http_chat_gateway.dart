import 'dart:async';
import 'dart:convert';
import 'dart:io';

import '../contracts/chat_contracts.dart';
import 'http_notebook_repository.dart' show defaultNotebookApiBase;

/// 将聊天、模型和事件分页接口映射到本机 HTTP 与 SSE。
class HttpChatGateway
    implements ChatGateway, ChatModelGateway, ChatEventHistoryGateway {
  HttpChatGateway({
    Uri? baseUri,
    HttpClient? client,
    Future<void> Function()? beforeRequest,
  }) : _baseUri = baseUri ?? _defaultChatBase,
       _client = client ?? _createClient(baseUri ?? _defaultChatBase),
       _ownsClient = client == null,
       _beforeRequest = beforeRequest;

  static final Uri _defaultChatBase = defaultNotebookApiBase.replace(
    path: '/',
    query: null,
    fragment: null,
  );

  static HttpClient _createClient(Uri baseUri) {
    final client = HttpClient();
    if (baseUri.host == 'localhost' ||
        InternetAddress.tryParse(baseUri.host)?.isLoopback == true) {
      client.findProxy = (_) => 'DIRECT';
    }
    return client;
  }

  final Uri _baseUri;
  final HttpClient _client;
  final bool _ownsClient;
  final Future<void> Function()? _beforeRequest;
  final Set<HttpClientRequest> _requests = {};
  final Set<String> _eventRunIds = {};
  final Map<String, HttpClientRequest> _eventRequests = {};
  final Map<String, StreamSubscription<String>> _eventSubscriptions = {};
  final Map<String, StreamController<ChatRunEventSignal>> _eventControllers =
      {};
  final Map<String, _EventConnection> _eventConnections = {};
  int _generation = 0;
  bool _disposed = false;

  /// 使当前运行中的操作失效并关闭其网络请求；该门禁只能失效一次。
  void invalidateRuntime() {
    if (_disposed) return;
    _disposed = true;
    ++_generation;
    for (final request in _requests.toList()) {
      request.abort(const HttpException('chat runtime invalidated'));
    }
    for (final connection in _eventConnections.values.toList()) {
      _cancelEvent(connection, closeController: true);
    }
    _requests.clear();
    if (_ownsClient) _client.close(force: true);
  }

  /// 释放传输资源并忽略尚未完成的操作结果。
  void dispose() => invalidateRuntime();

  int _enter() {
    if (_disposed) throw StateError('聊天运行已失效。');
    return _generation;
  }

  void _check(int generation) {
    if (_disposed || generation != _generation) {
      throw StateError('聊天运行已失效，忽略迟到结果。');
    }
  }

  Future<void> _gate(int generation) async {
    await _beforeRequest?.call();
    _check(generation);
  }

  Uri _uri(String path) => _baseUri.resolve(path);

  Future<Map<String, Object?>> _jsonRequest(
    String method,
    String path, {
    Object? body,
    String? idempotencyKey,
    Set<int> accepted = const {200},
  }) async {
    final generation = _enter();
    await _gate(generation);
    final uri = _uri(path);
    HttpClientRequest? request;
    try {
      request = await _client.openUrl(method, uri).timeout(_requestTimeout);
      _requests.add(request);
      _check(generation);
      request.followRedirects = false;
      if (idempotencyKey != null) {
        request.headers.set('Idempotency-Key', idempotencyKey);
      }
      if (body != null) {
        request.headers.contentType = ContentType.json;
        request.write(jsonEncode(body));
      }
      final response = await request.close().timeout(_requestTimeout);
      _check(generation);
      final text = await response
          .transform(utf8.decoder)
          .join()
          .timeout(_requestTimeout);
      _check(generation);
      if (!accepted.contains(response.statusCode)) {
        throw _httpError(response.statusCode, text);
      }
      if (text.isEmpty) {
        throw const ChatContractFormatException('response body is empty');
      }
      final decoded = jsonDecode(text);
      return _object(decoded, 'response');
    } on TimeoutException {
      request?.abort();
      rethrow;
    } finally {
      if (request != null) _requests.remove(request);
    }
  }

  static const Duration _requestTimeout = Duration(seconds: 8);

  ChatHttpError _httpError(int status, String text) {
    try {
      final json = _object(jsonDecode(text), 'error response');
      return ChatHttpError.fromJson(status, json);
    } on ChatContractFormatException {
      rethrow;
    } on FormatException {
      throw ChatContractFormatException(
        'HTTP $status error response is not valid JSON',
      );
    }
  }

  Map<String, Object?> _object(Object? value, String label) {
    if (value is! Map) {
      throw ChatContractFormatException('$label must be an object');
    }
    final result = <String, Object?>{};
    for (final entry in value.entries) {
      if (entry.key is! String) {
        throw ChatContractFormatException('$label keys must be strings');
      }
      result[entry.key as String] = entry.value;
    }
    return result;
  }

  @override
  Future<ChatCreateRunResult> createRun({
    required String sessionId,
    required String input,
    required String model,
    String provider = 'deepseek',
    required String idempotencyKey,
  }) async => ChatCreateRunResult.fromJson(
    await _jsonRequest(
      'POST',
      '/v1/run',
      body: {
        'session_id': sessionId,
        'input': input,
        'provider': provider,
        'model': model,
      },
      idempotencyKey: idempotencyKey,
      accepted: const {200, 202},
    ),
  );

  @override
  Future<List<ChatModelOption>> getModels() async {
    final json = await _jsonRequest('GET', '/v1/model');
    final models = json['models'];
    if (models is! List) {
      throw const ChatContractFormatException('models must be an array');
    }
    return models
        .map((item) => ChatModelOption.fromJson(_object(item, 'model')))
        .toList();
  }

  @override
  Future<ChatRun> getRun(String runId) async => ChatRun.fromJson(
    await _jsonRequest('GET', '/v1/run/${Uri.encodeComponent(runId)}'),
  );

  @override
  Future<ChatEventPage> getRunEvents(
    String runId, {
    int afterSeq = 0,
    int limit = 10,
  }) async {
    if (afterSeq < 0 || limit < 1 || limit > 100) {
      throw ArgumentError('无效事件分页参数');
    }
    final page = ChatEventPage.fromJson(
      await _jsonRequest(
        'GET',
        '/v1/run/${Uri.encodeComponent(runId)}/events?after_seq=$afterSeq&limit=$limit',
      ),
    );
    if (page.events.isEmpty &&
        (page.nextAfterSeq != afterSeq || page.hasMore)) {
      throw const ChatContractFormatException(
        'empty event page must retain cursor',
      );
    }
    return page;
  }

  @override
  Future<ChatCancelRunResult> cancelRun(String runId) async =>
      ChatCancelRunResult.fromJson(
        await _jsonRequest(
          'POST',
          '/v1/run/${Uri.encodeComponent(runId)}/cancel',
          accepted: const {200, 202},
        ),
      );

  @override
  Future<ChatSession> getSession(String sessionId) async =>
      ChatSession.fromJson(
        await _jsonRequest(
          'GET',
          '/v1/session/${Uri.encodeComponent(sessionId)}',
        ),
      );

  @override
  Future<ChatDeleteSessionResult> deleteSession(String sessionId) async =>
      ChatDeleteSessionResult.fromJson(
        await _jsonRequest(
          'DELETE',
          '/v1/session/${Uri.encodeComponent(sessionId)}',
        ),
      );

  @override
  Future<List<String>> listSessions() async {
    final result = ChatSessionIds.fromJson(
      await _jsonRequest('GET', '/v1/session'),
    );
    return result.values;
  }

  @override
  Stream<ChatRunEventSignal> runEvents(String runId, {int? lastEventId}) {
    final id = Uri.encodeComponent(runId);
    return _events(id, lastEventId);
  }

  Stream<ChatRunEventSignal> _events(String runId, int? lastEventId) {
    late StreamController<ChatRunEventSignal> controller;
    _EventConnection? connection;
    controller = StreamController<ChatRunEventSignal>(
      onListen: () {
        if (!_eventRunIds.add(runId)) {
          controller.addError(
            const ChatHttpError(
              statusCode: 409,
              code: 'sse_connection_exists',
              message: 'run event stream is already connected',
            ),
          );
          unawaited(controller.close());
          return;
        }
        connection = _EventConnection(runId, controller);
        _eventConnections[runId] = connection!;
        _eventControllers[runId] = controller;
        try {
          unawaited(_openEvents(connection!, lastEventId, _enter()));
        } catch (error, stack) {
          controller.addError(error, stack);
          unawaited(controller.close());
          _finishEvent(connection!);
        }
      },
      onCancel: () {
        final active = connection;
        if (active != null) _cancelEvent(active);
      },
    );
    return controller.stream;
  }

  Future<void> _openEvents(
    _EventConnection connection,
    int? lastEventId,
    int generation,
  ) async {
    final runId = connection.runId;
    final controller = connection.controller;
    HttpClientRequest? request;
    try {
      await _gate(generation);
      if (connection.cancelled) return;
      request = await _client
          .getUrl(_uri('/v1/run/$runId/event'))
          .timeout(_requestTimeout);
      connection.request = request;
      _eventRequests[runId] = request;
      _requests.add(request);
      if (connection.cancelled ||
          !identical(_eventConnections[runId], connection)) {
        request.abort();
        _requests.remove(request);
        return;
      }
      _check(generation);
      request.followRedirects = false;
      if (lastEventId != null) {
        request.headers.set('Last-Event-ID', '$lastEventId');
      }
      final response = await request.close().timeout(_requestTimeout);
      if (connection.cancelled ||
          !identical(_eventConnections[runId], connection)) {
        request.abort();
        return;
      }
      _check(generation);
      if (response.statusCode == HttpStatus.noContent) {
        controller.add(const ChatRunEventsNoContent());
        unawaited(controller.close());
        _finishEvent(connection, request: request);
        return;
      }
      if (response.statusCode != HttpStatus.ok) {
        final text = await response
            .transform(utf8.decoder)
            .join()
            .timeout(_requestTimeout);
        throw _httpError(response.statusCode, text);
      }
      final frame = _SseFrame();
      final subscription = response
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .listen(
            (line) {
              try {
                _check(generation);
                _consumeLine(line, controller, frame);
              } catch (error, stack) {
                controller.addError(error, stack);
                unawaited(controller.close());
                _cancelEvent(connection, closeController: true);
              }
            },
            onError: (Object error, StackTrace stack) {
              controller.addError(error, stack);
              unawaited(controller.close());
              _finishEvent(connection, request: request!);
            },
            onDone: () {
              // SSE 必须以空行结束事件帧，EOF 前未完成帧不得派发。
              if (!controller.isClosed) unawaited(controller.close());
              _finishEvent(connection, request: request!);
            },
          );
      _eventSubscriptions[runId] = subscription;
      connection.subscription = subscription;
    } catch (error, stack) {
      if (!connection.cancelled && !controller.isClosed) {
        controller.addError(error, stack);
        unawaited(controller.close());
      }
      if (request != null) {
        _finishEvent(connection, request: request, abort: true);
      } else {
        _finishEvent(connection);
      }
    }
  }

  void _cancelEvent(
    _EventConnection connection, {
    bool closeController = false,
  }) {
    connection.cancelled = true;
    final request = connection.request;
    if (request != null) request.abort();
    final subscription = connection.subscription;
    if (subscription != null) unawaited(subscription.cancel());
    _finishEvent(connection, request: request);
    if (closeController && !connection.controller.isClosed) {
      unawaited(connection.controller.close());
    }
  }

  void _finishEvent(
    _EventConnection connection, {
    HttpClientRequest? request,
    bool abort = false,
  }) {
    final runId = connection.runId;
    if (!identical(_eventConnections[runId], connection)) return;
    final activeRequest = request ?? _eventRequests[runId];
    if (activeRequest != null) {
      if (abort) activeRequest.abort();
      _requests.remove(activeRequest);
    }
    _eventRequests.remove(runId);
    _eventSubscriptions.remove(runId);
    _eventControllers.remove(runId);
    _eventConnections.remove(runId);
    _eventRunIds.remove(runId);
  }

  void _consumeLine(
    String line,
    StreamController<ChatRunEventSignal> output,
    _SseFrame frame,
  ) {
    if (line.isEmpty) {
      if (frame.hasData) {
        final data = frame.data.toString();
        final decoded = _object(jsonDecode(data), 'SSE data');
        output.add(
          ChatRunEventFrame(
            ChatSseEvent.fromJson(
              id: frame.id ?? '',
              event: frame.event ?? '',
              data: decoded,
            ),
          ),
        );
      }
      frame.clear();
      return;
    }
    if (line.startsWith(':')) return;
    final colon = line.indexOf(':');
    final field = colon < 0 ? line : line.substring(0, colon);
    var value = colon < 0 ? '' : line.substring(colon + 1);
    if (value.startsWith(' ')) value = value.substring(1);
    switch (field) {
      case 'id':
        if (!value.contains('\u0000')) frame.id = value;
      case 'event':
        frame.event = value;
      case 'data':
        frame.data.writeln(value);
        frame.hasData = true;
    }
  }
}

class _SseFrame {
  String? id;
  String? event;
  final StringBuffer data = StringBuffer();
  bool hasData = false;

  void clear() {
    id = null;
    event = null;
    data.clear();
    hasData = false;
  }
}

class _EventConnection {
  _EventConnection(this.runId, this.controller);

  final String runId;
  final StreamController<ChatRunEventSignal> controller;
  HttpClientRequest? request;
  StreamSubscription<String>? subscription;
  bool cancelled = false;
}
