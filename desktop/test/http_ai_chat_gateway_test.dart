import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/ai_chat.dart';
import 'package:timecue_desktop/data/http_ai_chat_gateway.dart';

const _session = '550e8400-e29b-41d4-a716-446655440000';
const _run = 'd9428888-122b-4f6f-9b47-5ea7b2915210';

void main() {
  late HttpServer server;
  late HttpAiChatGateway gateway;
  Map<String, dynamic>? submitted;
  String? idempotencyKey;
  String? lastEventId;
  var allowRequests = true;

  setUpAll(() async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((request) async {
      final path = request.uri.path;
      if (request.method == 'GET' && path == '/v1/session') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(
            jsonEncode({
              'sessions': [_session],
            }),
          );
      } else if (request.method == 'GET' && path == '/v1/session/$_session') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(
            jsonEncode({
              'session_id': _session,
              'run_ids': [_run],
            }),
          );
      } else if (request.method == 'GET' &&
          path == '/v1/session/wrong-session') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(
            jsonEncode({
              'session_id': 'another-session',
              'run_ids': [_run],
            }),
          );
      } else if (request.method == 'GET' && path == '/v1/run/terminal/event') {
        request.response.statusCode = HttpStatus.noContent;
      } else if (request.method == 'GET' && path == '/v1/run/$_run/event') {
        lastEventId = request.headers.value('Last-Event-ID');
        request.response
          ..statusCode = HttpStatus.ok
          ..headers.contentType = ContentType(
            'text',
            'event-stream',
            charset: 'utf-8',
          );
        final frame =
            'id: 1\r\nevent: text\r\ndata: {"content":\r\n'
            'data: "你好\\n世界"}\r\n\r\n';
        final bytes = utf8.encode(frame);
        final utf8Start = bytes.indexOf(0xe4);
        request.response.add(bytes.sublist(0, utf8Start + 1));
        await request.response.flush();
        request.response.add(bytes.sublist(utf8Start + 1));
        await request.response.flush();
      } else if (request.method == 'GET' && path == '/v1/run/$_run') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(
            jsonEncode({
              'run_id': _run,
              'session_id': _session,
              'model': 'deepseek-flash',
              'input': ' 原始消息 ',
              'status': 'succeeded',
              'output': '答复',
              'error': null,
              'created_at': '2026-10-15T15:00:00+08:00',
              'finished_at': '2026-10-15T15:00:01+08:00',
            }),
          );
      } else if (request.method == 'GET' && path == '/v1/run/wrong') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(
            jsonEncode({
              'run_id': _run,
              'session_id': _session,
              'model': 'deepseek-flash',
              'input': 'input',
              'status': 'running',
              'output': null,
              'error': null,
              'created_at': '2026-10-15T15:00:00+08:00',
              'finished_at': null,
            }),
          );
      } else if (request.method == 'GET' && path == '/v1/run/non-json') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write('response-private-marker is not json');
      } else if (request.method == 'GET' && path == '/v1/run/invalid-utf8') {
        request.response
          ..statusCode = HttpStatus.ok
          ..headers.contentType = ContentType.json
          ..add([0x7b, 0x22, 0x78, 0x22, 0x3a, 0xff, 0x7d]);
      } else if (request.method == 'GET' && path == '/v1/run/non-object') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(jsonEncode(['response-private-marker']));
      } else if (request.method == 'GET' && path == '/v1/run/empty') {
        request.response.statusCode = HttpStatus.ok;
      } else if (request.method == 'GET' && path == '/v1/run/bad-date') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(
            jsonEncode({
              'run_id': 'bad-date',
              'session_id': _session,
              'model': 'deepseek-flash',
              'input': 'input',
              'status': 'running',
              'output': null,
              'error': null,
              'created_at': 'sensitive-date-marker',
              'finished_at': null,
            }),
          );
      } else if (request.method == 'POST' && path == '/v1/run') {
        submitted =
            jsonDecode(await utf8.decoder.bind(request).join())
                as Map<String, dynamic>;
        idempotencyKey = request.headers.value('Idempotency-Key');
        request.response
          ..statusCode = HttpStatus.accepted
          ..write(jsonEncode({'run_id': _run}));
      } else if (request.method == 'POST' && path == '/v1/run/$_run/cancel') {
        request.response
          ..statusCode = HttpStatus.accepted
          ..write(jsonEncode({'run_id': _run, 'status': 'running'}));
      } else if (request.method == 'DELETE' &&
          path == '/v1/session/$_session') {
        request.response
          ..statusCode = HttpStatus.ok
          ..write(jsonEncode({'session_id': _session, 'status': 'deleted'}));
      } else if (request.method == 'GET' && path == '/v1/run/error') {
        request.response
          ..statusCode = HttpStatus.conflict
          ..write(
            jsonEncode({
              'error': {'code': 'session_busy', 'message': '会话正在运行'},
            }),
          );
      } else if (request.method == 'GET' && path == '/v1/run/leaky-error') {
        request.response
          ..statusCode = HttpStatus.internalServerError
          ..write('<html>proxy secret body</html>');
      } else {
        request.response.statusCode = HttpStatus.notFound;
      }
      await request.response.close();
    });
  });

  setUp(() {
    gateway = HttpAiChatGateway(
      base: Uri.parse('http://127.0.0.1:${server.port}/ignored/base'),
      canRequest: () => allowRequests,
    );
  });

  tearDown(() {
    gateway.dispose();
    allowRequests = true;
  });

  tearDownAll(() async => server.close(force: true));

  test('七个接口使用固定 /v1 并映射直接响应', () async {
    expect(await gateway.listSessions(), [_session]);
    final runs = await gateway.getSessionRuns(_session);
    expect(runs.single.status, AiChatStatus.succeeded);
    expect(runs.single.output, '答复');
    expect((await gateway.getRun(_run)).input, ' 原始消息 ');
    expect(
      await gateway.createRun(
        _session,
        '  带空格的原文\n第二行  ',
        idempotencyKey: 'create_session:retry-key',
      ),
      _run,
    );
    expect(submitted, {
      'session_id': _session,
      'input': '  带空格的原文\n第二行  ',
      'model': 'deepseek-flash',
    });
    expect(idempotencyKey, 'create_session:retry-key');
    expect(await gateway.cancelRun(_run), AiChatStatus.running);
    await gateway.deleteSession(_session);
  });

  test('HTTP 错误保留有限错误字段并隐藏非 JSON 正文', () async {
    Object? mapped;
    try {
      await gateway.getRun('error');
    } catch (error) {
      mapped = error;
    }
    expect(mapped, isA<AiChatGatewayException>());
    expect((mapped as AiChatGatewayException).code, 'session_busy');
    expect(mapped.message, '会话正在运行');

    Object? hidden;
    try {
      await gateway.getRun('leaky-error');
    } catch (error) {
      hidden = error;
    }
    expect(hidden, isA<AiChatGatewayException>());
    expect(hidden.toString(), isNot(contains('proxy secret body')));
  });

  test('拒绝响应身份与请求不匹配的 run 或 session 历史', () async {
    await expectLater(gateway.getRun('wrong'), throwsFormatException);
    await expectLater(
      gateway.getSessionRuns('wrong-session'),
      throwsFormatException,
    );
  });

  test('成功响应格式错误只暴露有限状态和端点诊断', () async {
    for (final id in ['non-json', 'invalid-utf8', 'non-object', 'empty']) {
      Object? failure;
      try {
        await gateway.getRun(id);
      } catch (error) {
        failure = error;
      }
      expect(failure, isA<AiChatGatewayException>());
      final diagnostic = failure as AiChatGatewayException;
      expect(diagnostic.statusCode, HttpStatus.ok);
      expect(diagnostic.endpoint, '/v1/run/$id');
      expect(diagnostic.toString(), isNot(contains('response-private-marker')));
      expect(diagnostic.toString(), isNot(contains('xff')));
    }

    Object? badDate;
    try {
      await gateway.getRun('bad-date');
    } catch (error) {
      badDate = error;
    }
    expect(badDate, isA<AiChatGatewayException>());
    expect((badDate as AiChatGatewayException).statusCode, HttpStatus.ok);
    expect(badDate.endpoint, '/v1/run/bad-date');
    expect(badDate.toString(), isNot(contains('sensitive-date-marker')));
  });

  test('204 发出明确 noContent 信号', () async {
    final event = await gateway.watchRun('terminal').first;
    expect(event.noContent, isTrue);
  });

  test('SSE 正确处理分块 UTF-8、CRLF、多行 data 和 Last-Event-ID', () async {
    final event = await gateway.watchRun(_run, lastEventId: 7).first;
    expect(event.id, 1);
    expect(event.type, 'text');
    expect(event.data['content'], '你好\n世界');
    expect(lastEventId, '7');
  });

  test('取消订阅会关闭挂起的 SSE HTTP 连接', () async {
    final rawServer = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final connected = Completer<void>();
    final released = Completer<void>();
    rawServer.listen((socket) async {
      socket.add(
        utf8.encode(
          'HTTP/1.1 200 OK\r\n'
          'Content-Type: text/event-stream; charset=utf-8\r\n'
          'Connection: close\r\n\r\n'
          'id: 1\nevent: start\ndata: {}\n\n',
        ),
      );
      await socket.flush();
      connected.complete();
      socket.listen(
        (_) {},
        onDone: () {
          if (!released.isCompleted) released.complete();
        },
        onError: (_) {
          if (!released.isCompleted) released.complete();
        },
        cancelOnError: true,
      );
    });
    final rawGateway = HttpAiChatGateway(
      base: Uri.parse('http://127.0.0.1:${rawServer.port}'),
    );
    try {
      final events = rawGateway.watchRun(_run).listen((_) {});
      await connected.future;
      await events.cancel();
      await released.future.timeout(const Duration(seconds: 3));
    } finally {
      rawGateway.dispose();
      await rawServer.close();
    }
  });

  test('等待响应头时 invalidate 会结束流且不发旧事件', () async {
    final rawServer = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final requestReceived = Completer<void>();
    final peerClosed = Completer<void>();
    rawServer.listen((socket) {
      final requestBytes = <int>[];
      socket.listen(
        (chunk) {
          requestBytes.addAll(chunk);
          if (!requestReceived.isCompleted &&
              utf8
                  .decode(requestBytes, allowMalformed: true)
                  .contains('\r\n\r\n')) {
            requestReceived.complete();
          }
        },
        onDone: () {
          if (!peerClosed.isCompleted) peerClosed.complete();
        },
        onError: (_) {
          if (!peerClosed.isCompleted) peerClosed.complete();
        },
      );
    });
    final rawGateway = HttpAiChatGateway(
      base: Uri.parse('http://127.0.0.1:${rawServer.port}'),
    );
    final events = <AiChatEvent>[];
    final done = Completer<void>();
    final subscription = rawGateway
        .watchRun(_run)
        .listen(events.add, onDone: done.complete, onError: done.completeError);
    try {
      await requestReceived.future.timeout(const Duration(seconds: 3));
      rawGateway.invalidate();
      await done.future.timeout(const Duration(seconds: 3));
      await peerClosed.future.timeout(const Duration(seconds: 3));
      expect(events, isEmpty);
    } finally {
      await subscription.cancel();
      rawGateway.dispose();
      await rawServer.close();
    }
  });

  test('invalidate 已建立的 SSE 后流结束且不再交付旧事件', () async {
    final rawServer = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final firstEventSent = Completer<void>();
    final allowLaterEvent = Completer<void>();
    final peerClosed = Completer<void>();
    rawServer.listen((socket) async {
      var peerEnded = false;
      socket.listen(
        (_) {},
        onDone: () {
          peerEnded = true;
          if (!peerClosed.isCompleted) peerClosed.complete();
        },
        onError: (_) {
          peerEnded = true;
          if (!peerClosed.isCompleted) peerClosed.complete();
        },
      );
      socket.add(
        utf8.encode(
          'HTTP/1.1 200 OK\r\n'
          'Content-Type: text/event-stream; charset=utf-8\r\n'
          'Connection: close\r\n\r\n'
          'id: 1\nevent: start\ndata: {}\n\n',
        ),
      );
      await socket.flush();
      firstEventSent.complete();
      await allowLaterEvent.future;
      if (!peerEnded) {
        try {
          socket.add(
            utf8.encode('id: 2\nevent: text\ndata: {"content":"stale"}\n\n'),
          );
          await socket.flush();
        } catch (_) {
          // invalidate 已关闭连接，后续写入应失败。
        }
      }
    });
    final rawGateway = HttpAiChatGateway(
      base: Uri.parse('http://127.0.0.1:${rawServer.port}'),
    );
    final events = <AiChatEvent>[];
    final firstEvent = Completer<void>();
    final done = Completer<void>();
    final subscription = rawGateway
        .watchRun(_run)
        .listen(
          (event) {
            events.add(event);
            if (!firstEvent.isCompleted) firstEvent.complete();
          },
          onDone: done.complete,
          onError: done.completeError,
        );
    try {
      await firstEventSent.future.timeout(const Duration(seconds: 3));
      await firstEvent.future.timeout(const Duration(seconds: 3));
      rawGateway.invalidate();
      await done.future.timeout(const Duration(seconds: 3));
      await peerClosed.future.timeout(const Duration(seconds: 3));
      allowLaterEvent.complete();
      await Future<void>.delayed(Duration.zero);
      expect(events.map((event) => event.type), ['start']);
    } finally {
      if (!allowLaterEvent.isCompleted) allowLaterEvent.complete();
      await subscription.cancel();
      rawGateway.dispose();
      await rawServer.close();
    }
  });

  test('准入门禁关闭时不会发出 HTTP 请求', () async {
    allowRequests = false;
    await expectLater(
      gateway.listSessions(),
      throwsA(isA<AiChatGatewayException>()),
    );
  });
}
