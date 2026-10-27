import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';

void main() {
  late HttpServer server;
  late HttpChatGateway gateway;
  late final List<String> requests = [];
  late final List<String?> cursors = [];

  test('事件分页保留原始大增量与独立下一页位置', () async {
    final page = await gateway.getRunEvents('run-1', afterSeq: 4, limit: 2);
    expect(page.events.map((e) => e.eventId), [5, 6]);
    expect(
      (page.events.first.data as ChatContentEventData).content,
      '完整思考' * 5000,
    );
    expect(page.nextAfterSeq, 6);
    expect(page.hasMore, isTrue);
  });

  setUp(() async {
    requests.clear();
    cursors.clear();
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((request) async {
      final path = request.uri.path;
      requests.add('${request.method} $path');
      cursors.add(request.headers.value('Last-Event-ID'));
      final response = request.response;
      response.headers.contentType = ContentType.json;
      switch (path) {
        case '/v1/run':
          await request.drain<void>();
          response.statusCode = HttpStatus.accepted;
          response.write(
            jsonEncode({
              'run_id': 'run-1',
              'status': 'running',
              'output': null,
              'error': null,
            }),
          );
          break;
        case '/v1/run/run-1':
          response.write(jsonEncode(_runningRun));
          break;
        case '/v1/run/run-1/events':
          expect(request.uri.queryParameters, {'after_seq': '4', 'limit': '2'});
          response.write(
            jsonEncode({
              'events': [
                {
                  'seq': 5,
                  'type': 'thinking',
                  'data': {'content': '完整思考' * 5000},
                },
                {
                  'seq': 6,
                  'type': 'text',
                  'data': {'content': '正文'},
                },
              ],
              'next_after_seq': 6,
              'has_more': true,
            }),
          );
          break;
        case '/v1/run/run-1/cancel':
          response.write(jsonEncode({'run_id': 'run-1', 'status': 'running'}));
          break;
        case '/v1/session/session-1':
          if (request.method == 'DELETE') {
            response.write(
              jsonEncode({'session_id': 'session-1', 'status': 'deleted'}),
            );
          } else {
            response.write(
              jsonEncode({
                'session_id': 'session-1',
                'session_name': '中文会话',
                'run_ids': ['run-1'],
              }),
            );
          }
          break;
        case '/v1/session':
          response.write(
            jsonEncode({
              'sessions': ['session-1'],
            }),
          );
          break;
        case '/v1/run/run-1/event':
          response.headers.contentType = ContentType('text', 'event-stream');
          response.write(': comment\r\n');
          response.write('id: 1\r\nevent: start\r\ndata: {}\r\n\r\n');
          final frame = utf8.encode(
            'id: 2\nevent: text\ndata: {"content":\ndata: "你好，猫咪","error":null}\n\n',
          );
          for (final byte in frame) {
            response.add([byte]);
            await response.flush();
          }
          // EOF 前未以空行结束的帧不得被派发。
          response.write('id: 3\nevent: end\ndata: {"status":"succeeded"}');
          break;
        case '/v1/run/slow/event':
          response.headers.contentType = ContentType('text', 'event-stream');
          response.write(': waiting\r\n\r\n');
          await response.flush();
          await request.response.done;
          break;
        case '/v1/run/run-error':
          response.statusCode = HttpStatus.conflict;
          response.write(
            jsonEncode({
              'error': {'code': 'session_busy', 'message': '运行中'},
            }),
          );
          break;
        default:
          response.statusCode = HttpStatus.notFound;
          response.write(
            jsonEncode({
              'error': {'code': 'missing', 'message': 'no'},
            }),
          );
          break;
      }
      await response.close();
    });
    gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}/'),
    );
  });

  tearDown(() async {
    gateway.dispose();
    await server.close(force: true);
  });

  test('all seven HTTP operations use direct DTOs and exact methods', () async {
    final created = await gateway.createRun(
      sessionId: 'session-1',
      input: '你好',
      model: 'deepseek-flash',
      idempotencyKey: 'create_session:abc',
    );
    expect(created.runId, 'run-1');
    expect((await gateway.getRun('run-1')).input, '原始输入');
    expect((await gateway.cancelRun('run-1')).status, ChatRunStatus.running);
    final session = await gateway.getSession('session-1');
    expect(session.name, '中文会话');
    expect(session.runIds, ['run-1']);
    expect((await gateway.deleteSession('session-1')).status, 'deleted');
    expect(await gateway.listSessions(), ['session-1']);
    expect(requests, [
      'POST /v1/run',
      'GET /v1/run/run-1',
      'POST /v1/run/run-1/cancel',
      'GET /v1/session/session-1',
      'DELETE /v1/session/session-1',
      'GET /v1/session',
    ]);
  });

  test(
    'SSE incrementally decodes UTF-8 and only dispatches complete frames',
    () async {
      final events = await gateway.runEvents('run-1', lastEventId: 7).toList();
      expect(events, hasLength(2));
      expect((events[0] as ChatRunEventFrame).event.type, ChatEventType.start);
      final text =
          (events[1] as ChatRunEventFrame).event.data as ChatContentEventData;
      expect(text.content, '你好，猫咪');
      expect(cursors.last, '7');
    },
  );

  test('204 is an explicit signal', () async {
    final noContentServer = await HttpServer.bind(
      InternetAddress.loopbackIPv4,
      0,
    );
    noContentServer.listen((request) {
      request.response.statusCode = HttpStatus.noContent;
      request.response.close();
    });
    final noContentGateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${noContentServer.port}/'),
    );
    try {
      expect(await noContentGateway.runEvents('r').toList(), [
        isA<ChatRunEventsNoContent>(),
      ]);
    } finally {
      noContentGateway.dispose();
      await noContentServer.close(force: true);
    }
  });

  test('HTTP error keeps status and server code without retry', () async {
    await expectLater(
      gateway.getRun('run-error'),
      throwsA(
        isA<ChatHttpError>()
            .having((error) => error.statusCode, 'status', 409)
            .having((error) => error.code, 'code', 'session_busy'),
      ),
    );
    expect(
      requests.where((path) => path == 'GET /v1/run/run-error'),
      hasLength(1),
    );
  });

  test('canceling an SSE subscription closes its request', () async {
    final socketClosed = Completer<void>();
    final firstEvent = Completer<void>();
    final closingServer = await ServerSocket.bind(
      InternetAddress.loopbackIPv4,
      0,
    );
    final socketFuture = closingServer.first;
    final closingGateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${closingServer.port}/'),
    );
    final subscription = closingGateway.runEvents('slow').listen((signal) {
      if (signal is ChatRunEventFrame && !firstEvent.isCompleted) {
        firstEvent.complete();
      }
    });
    final socket = await socketFuture;
    final requestBytes = BytesBuilder();
    var replied = false;
    socket.listen(
      (bytes) {
        requestBytes.add(bytes);
        if (!replied &&
            utf8.decode(requestBytes.toBytes()).contains('\r\n\r\n')) {
          replied = true;
          socket.add(
            utf8.encode(
              'HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\nid: 1\nevent: start\ndata: {}\n\n',
            ),
          );
          unawaited(socket.flush());
        }
      },
      onDone: () {
        if (!socketClosed.isCompleted) socketClosed.complete();
      },
    );
    await firstEvent.future.timeout(const Duration(seconds: 2));
    await subscription.cancel();
    await socketClosed.future.timeout(const Duration(seconds: 2));
    closingGateway.dispose();
    await closingServer.close();
  });

  test(
    'a rejected duplicate leaves the original stream intact; cancel during gate sends no request',
    () async {
      final gate = Completer<void>();
      final guarded = HttpChatGateway(
        baseUri: Uri.parse('http://127.0.0.1:${server.port}/'),
        beforeRequest: () => gate.future,
      );
      final originalEvent = Completer<void>();
      final originalDone = Completer<void>();
      final original = guarded.runEvents('run-1').listen((signal) {
        if (signal is ChatRunEventFrame && !originalEvent.isCompleted) {
          originalEvent.complete();
        }
      }, onDone: () => originalDone.complete());
      await Future<void>.delayed(Duration.zero);
      final duplicateDone = Completer<void>();
      final duplicate = guarded
          .runEvents('run-1')
          .listen(
            (_) {},
            onError: (Object error) {
              expect(error, isA<ChatHttpError>());
              if (!duplicateDone.isCompleted) duplicateDone.complete();
            },
          );
      await duplicateDone.future;
      await duplicate.cancel();
      gate.complete();
      await originalEvent.future.timeout(const Duration(seconds: 2));
      await originalDone.future.timeout(const Duration(seconds: 2));
      await original.cancel();
      expect(requests, contains('GET /v1/run/run-1/event'));
      guarded.dispose();

      final cancelGate = Completer<void>();
      final cancelGateway = HttpChatGateway(
        baseUri: Uri.parse('http://127.0.0.1:${server.port}/'),
        beforeRequest: () => cancelGate.future,
      );
      final countBeforeCancel = requests.length;
      final pending = cancelGateway.runEvents('run-1').listen((_) {});
      await Future<void>.delayed(Duration.zero);
      await pending.cancel();
      cancelGate.complete();
      await Future<void>.delayed(const Duration(milliseconds: 40));
      expect(requests.length, countBeforeCancel);
      cancelGateway.dispose();
    },
  );

  test(
    'runtime invalidation rejects future calls and ignores pending result',
    () async {
      var gateCalls = 0;
      final gate = Completer<void>();
      final guarded = HttpChatGateway(
        baseUri: Uri.parse('http://127.0.0.1:${server.port}/'),
        beforeRequest: () {
          gateCalls++;
          return gate.future;
        },
      );
      final pending = guarded.listSessions();
      expect(gateCalls, 1);
      guarded.invalidateRuntime();
      gate.complete();
      await expectLater(pending, throwsA(isA<StateError>()));
      await expectLater(guarded.listSessions(), throwsA(isA<StateError>()));
    },
  );
}

final Map<String, Object?> _runningRun = {
  'run_id': 'run-1',
  'session_id': 'session-1',
  'model': 'deepseek-flash',
  'provider': 'deepseek',
  'input': '原始输入',
  'status': 'running',
  'output': null,
  'error': null,
  'created_at': '2026-10-17T12:00:00+08:00',
  'finished_at': null,
};
