import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/chat/chat_message_list.dart';

void main() {
  for (final failRecovery in [false, true]) {
    test(
      failRecovery
          ? '取消终态补读失败后仍可分页重试，不重新订阅或占用名额'
          : '活跃分页读到末尾后取消先确认终态，仍补齐未送达的尾批',
      () async {
        final fixture = await _HistoryServer.open();
        addTearDown(fixture.close);
        fixture.events.add(_event(1, 'start', {}));
        final conversation = fixture.model.createLocalSession();
        fixture.model.updateDraft('s', '问题');
        await fixture.model.send('s', model: 'model', provider: 'test');
        await _until(() => fixture.connections.isNotEmpty);
        await fixture.send(fixture.events.first);
        final run = conversation.runs.single;
        await _until(() => run.lastEventId == 1);
        await fixture.model.loadMoreEvents('r');
        expect(run.hasMoreEvents, isFalse);
        expect(run.isTerminal, isFalse);
        fixture.events.addAll([
          _event(2, 'text', {'content': '取消前的尾部正文'}),
          _event(3, 'end', {'status': 'cancelled'}),
        ]);
        fixture
          ..finished = true
          ..status = 'cancelled'
          ..output = '最终正文投影'
          ..pageSize = 1
          ..pageFailures = failRecovery ? 1 : 0;

        await fixture.model.cancel('s');

        expect(run.isTerminal, isTrue);
        expect(fixture.model.activeRunCount, 0);
        if (failRecovery) {
          expect(run.eventsError, contains('历史稍后重试'));
          expect(run.hasMoreEvents, isFalse);
          expect(run.eventsLoading, isFalse);
          await fixture.model.loadMoreEvents('r');
          expect(run.hasMoreEvents, isTrue);
          await fixture.model.loadMoreEvents('r');
        }
        await _until(
          () => run.lastEventId == 3,
          diagnostic: () =>
              'seq=${run.lastEventId} hasMore=${run.hasMoreEvents} pages=${fixture.pageCursors}',
        );
        expect(run.timeline.map((e) => e.eventId), [1, 2, 3]);
        expect(run.rawText, '最终正文投影');
        expect(run.eventsError, isNull);
        expect(run.hasMoreEvents, isFalse);
        expect(fixture.model.activeRunCount, 0);
        expect(fixture.pageCursors, [1, 1, 2]);
        expect(fixture.cursors, [null]);
      },
    );
  }

  test('取消确认终态时分页尚未返回，尾批等待旧页应用后继续补齐', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture.events.add(_event(1, 'start', {}));
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    await _until(() => fixture.connections.isNotEmpty);
    await fixture.send(fixture.events.first);
    final run = conversation.runs.single;
    await _until(() => run.lastEventId == 1);
    final oldPageGate = Completer<void>();
    fixture.pageGate = oldPageGate;
    addTearDown(() {
      if (!oldPageGate.isCompleted) oldPageGate.complete();
    });
    final oldPage = fixture.model.loadMoreEvents('r');
    await _until(() => fixture.pageCursors.isNotEmpty);
    fixture.events.addAll([
      _event(2, 'text', {'content': '尾部正文'}),
      _event(3, 'end', {'status': 'cancelled'}),
    ]);
    fixture
      ..finished = true
      ..status = 'cancelled'
      ..output = '最终正文投影';
    final cancellation = fixture.model.cancel('s');
    await _until(() => run.isTerminal);
    expect(fixture.model.activeRunCount, 0);
    expect(fixture.pageCursors, [1]);

    oldPageGate.complete();
    await Future.wait([oldPage, cancellation]);

    expect(run.timeline.map((e) => e.eventId), [1, 2, 3]);
    expect(run.lastEventId, 3);
    expect(run.rawText, '最终正文投影');
    expect(run.hasMoreEvents, isFalse);
    expect(run.eventsLoading, isFalse);
    expect(run.eventsError, isNull);
    expect(fixture.pageCursors, [1, 1]);
    expect(fixture.cursors, [null]);
  });

  test('活跃分页读到末尾后刷新先取得终态，仍补齐未送达的尾批', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture.events.add(_event(1, 'start', {}));
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    await _until(() => fixture.connections.isNotEmpty);
    await fixture.send(fixture.events.first);
    final run = conversation.runs.single;
    await _until(() => run.lastEventId == 1);
    await fixture.model.loadMoreEvents('r');
    expect(run.hasMoreEvents, isFalse);
    expect(run.isTerminal, isFalse);
    fixture.events.addAll([
      _event(2, 'thinking', {'content': '未送达思考'}),
      _event(3, 'text', {'content': '未送达正文'}),
      _event(4, 'end', {'status': 'succeeded'}),
    ]);
    fixture
      ..finished = true
      ..status = 'succeeded'
      ..output = '未送达正文';
    await fixture.model.refreshSession('s');
    await _until(() => run.lastEventId == 4);
    expect(run.timeline.map((e) => e.eventId), [1, 2, 3, 4]);
    expect(run.rawText, '未送达正文');
    expect(fixture.cursors, [null]);
  });

  test('204 已确认结束，概况与历史同时失败仍释放运行占用', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture
      ..noContent = true
      ..finished = true
      ..status = 'succeeded'
      ..output = '最终投影'
      ..snapshotFailures = 1
      ..pageFailures = 1;
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'text', {'content': '旧尝试'}),
      _event(3, 'end', {'status': 'succeeded'}),
    ]);
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    final run = conversation.runs.single;
    await _until(() => run.eventsError != null);
    expect(run.isTerminal, isTrue);
    expect(fixture.model.activeRunCount, 0);
    expect(fixture.model.canSend('s'), isTrue);
    expect(run.phase.name, 'recovering');
    await fixture.model.loadMoreEvents('r');
    expect(run.rawText, '最终投影');
    expect(fixture.cursors, [null]);
  });

  test('分页应用失败只保留连续前缀，重连不用已下载页面的高位游标', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture.events.addAll([
      _event(2, 'text', {'content': '甲'}),
      _event(4, 'text', {'content': '丁'}),
    ]);
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    await _until(() => fixture.connections.isNotEmpty);
    await fixture.send(_event(1, 'start', {}));
    await fixture.send(_event(5, 'text', {'content': '末'}));
    await _until(() => fixture.cursors.length == 2);
    final run = conversation.runs.single;
    expect(run.lastEventId, 2);
    expect(run.rawText, '甲');
    expect(run.timeline.map((e) => e.eventId), [1, 2]);
    expect(fixture.cursors, [null, '2']);
    fixture.events
      ..clear()
      ..addAll([
        _event(3, 'text', {'content': '乙'}),
        _event(4, 'text', {'content': '丁'}),
        _event(5, 'text', {'content': '末'}),
      ]);
    await _until(() => fixture.connections.length == 2);
    await fixture.send(_event(5, 'text', {'content': '末'}));
    await _until(() => run.lastEventId == 5);
    expect(run.rawText, '甲乙丁末');
  });

  test('204 后概况暂时失败可重读最终正文且不会重新订阅', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture
      ..noContent = true
      ..finished = true
      ..status = 'succeeded'
      ..output = '最终投影'
      ..snapshotFailures = 1;
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'text', {'content': '旧尝试'}),
      _event(3, 'end', {'status': 'succeeded'}),
    ]);
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    await _until(() => conversation.runs.single.lastEventId == 3);
    await fixture.model.loadMoreEvents('r');
    expect(conversation.runs.single.rawText, '最终投影');
    expect(conversation.runs.single.timeline, hasLength(3));
    expect(fixture.cursors, [null]);
  });

  test('断线时概况已终态仍补齐未收到的尾部事件', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'text', {'content': '部分正文'}),
      _event(3, 'error', {'code': 'model_network_error', 'message': '中断'}),
      _event(4, 'end', {'status': 'failed'}),
    ]);
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    await _until(() => fixture.connections.isNotEmpty);
    await fixture.send(fixture.events[0]);
    await fixture.send(fixture.events[1]);
    await _until(() => conversation.runs.single.lastEventId == 2);
    fixture
      ..finished = true
      ..status = 'failed'
      ..output = '部分正文'
      ..error = {'code': 'model_network_error', 'message': '中断'};
    await fixture.connections.last.close();
    await _until(() => conversation.runs.single.lastEventId == 4);
    expect(conversation.runs.single.timeline, hasLength(4));
    expect(conversation.runs.single.rawText, '部分正文');
    expect(fixture.cursors, [null]);
  });

  test('取消投影的创建重放必须查询概况后判定终态', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture
      ..status = 'cancelled'
      ..receiptStatus = 'cancelled';
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    expect(conversation.runs.single.isTerminal, isFalse);
    expect(conversation.runs.single.phase, ChatRunPhase.stopping);
    expect(fixture.model.activeRunCount, 1);
    await _until(() => fixture.connections.isNotEmpty);
  });

  testWidgets('原始记录与正文分开，工具参数结果和思考可折叠查看', (tester) async {
    final fixture = (await tester.runAsync(_HistoryServer.open))!;
    addTearDown(fixture.close);
    fixture
      ..finished = true
      ..status = 'failed'
      ..output = '保留正文'
      ..pageSize = 100;
    fixture.error = {'code': 'model_network_error', 'message': '模型中断'};
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'thinking', {'content': '可恢复的思考'}),
      _event(3, 'tool_start', {
        'function': [
          {
            'id': 'original-id',
            'name': '查询日程',
            'argument': {'date': '今天'},
          },
        ],
      }),
      _event(4, 'tool_result', {
        'tool_call_id': 'original-id',
        'output': ['原始结果', 7],
        'error': null,
      }),
      _event(5, 'text', {'content': '保留正文'}),
      _event(6, 'error', {'code': 'model_network_error', 'message': '模型中断'}),
      _event(7, 'end', {'status': 'failed'}),
    ]);
    await tester.runAsync(() async {
      await fixture.model.loadHistory();
      await fixture.model.loadMoreEvents('r');
    });
    final run = fixture.model.conversations.single.runs.single;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ChatMessageList(
            sessionId: 's',
            runs: [run],
            initialOffset: 0,
            initiallyFollowLatest: false,
            onReadingPosition: (_, _) {},
            onRetryCreate: (_) {},
            onRetryConnection: (_) {},
          ),
        ),
      ),
    );
    expect(find.text('运行记录'), findsOneWidget);
    expect(find.text('可恢复的思考'), findsNothing);
    await tester.tap(find.byKey(const ValueKey('chat-tool-3:original-id')));
    await tester.pumpAndSettle();
    expect(find.textContaining('original-id'), findsWidgets);
    expect(find.textContaining('原始结果'), findsOneWidget);
    expect(find.textContaining('今天'), findsOneWidget);
    await tester.ensureVisible(find.text('运行记录'));
    await tester.tap(find.text('运行记录'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('#2 · 思考'));
    await tester.tap(find.text('#2 · 思考'));
    await tester.pumpAndSettle();
    expect(find.text('可恢复的思考'), findsOneWidget);
    expect(find.textContaining('模型中断'), findsWidgets);
  });

  test('工具跨回复复用 ID 时按开始 seq 建立实例并保留参数和原始 JSON 结果', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture
      ..finished = true
      ..status = 'succeeded'
      ..output = '结果'
      ..pageSize = 100;
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'tool_start', {
        'function': [
          {
            'id': 'same',
            'name': '查询日程',
            'argument': {'date': '今天'},
          },
        ],
      }),
      _event(3, 'tool_result', {
        'tool_call_id': 'same',
        'output': ['原始结果', 7],
        'error': null,
      }),
      _event(4, 'tool_start', {
        'function': [
          {'id': 'same', 'name': '更新日程', 'argument': null},
        ],
      }),
      _event(5, 'tool_result', {
        'tool_call_id': 'same',
        'output': false,
        'error': {'code': 'tool_argument_parse_error', 'message': '参数截断'},
      }),
      _event(6, 'end', {'status': 'succeeded'}),
    ]);
    await fixture.model.loadHistory();
    await fixture.model.loadMoreEvents('r');
    final calls = fixture.model.conversations.single.runs.single.toolCalls;
    expect(calls, hasLength(2));
    expect(calls.map((c) => c.id), ['same', 'same']);
    expect(calls.map((c) => c.startSeq), [2, 4]);
    expect(calls.first.argument, {'date': '今天'});
    expect(calls.first.output, ['原始结果', 7]);
    expect(calls.first.hasResult, isTrue);
    expect(calls.last.argument, isNull);
    expect(calls.last.output, isFalse);
    expect(calls.last.error?.code, 'tool_argument_parse_error');
  });

  test('重新打开取消收尾中的运行继续订阅，error 不提前释放名额', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture.status = 'cancelled';
    await fixture.model.loadHistory();
    final run = fixture.model.conversations.single.runs.single;
    expect(run.isTerminal, isFalse);
    expect(run.phase, ChatRunPhase.stopping);
    expect(fixture.model.activeRunCount, 1);
    await _until(() => fixture.connections.isNotEmpty);
    await fixture.send(_event(1, 'start', {}));
    await fixture.send(
      _event(2, 'error', {'code': 'cleanup_error', 'message': '继续收尾'}),
    );
    await _until(() => run.lastEventId == 2);
    expect(run.isTerminal, isFalse);
    expect(fixture.model.activeRunCount, 1);
    fixture
      ..finished = true
      ..status = 'cancelled';
    await fixture.send(_event(3, 'end', {'status': 'cancelled'}));
    await _until(() => run.isTerminal);
    expect(fixture.model.activeRunCount, 0);
  });

  test('历史按需翻页且重建模型不会复用没有展示状态的孤立游标', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture
      ..finished = true
      ..status = 'succeeded'
      ..output = '最终正文';
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'thinking', {'content': '只在事件中保留'}),
      _event(3, 'text', {'content': '旧尝试'}),
      _event(4, 'compact', {}),
      _event(5, 'text', {'content': '最终正文'}),
      _event(6, 'end', {'status': 'succeeded'}),
    ]);
    await fixture.model.loadHistory();
    final run = fixture.model.conversations.single.runs.single;
    expect(run.lastEventId, 0);
    await fixture.model.loadMoreEvents('r');
    expect(run.lastEventId, 2);
    expect(run.hasMoreEvents, isTrue);
    expect(run.timeline, hasLength(2));
    expect(run.rawText, '最终正文');
    await fixture.model.loadMoreEvents('r');
    expect(run.lastEventId, 4);
    final reopened = ChatViewModel(gateway: fixture.gateway);
    addTearDown(reopened.dispose);
    await reopened.loadHistory();
    final fresh = reopened.conversations.single.runs.single;
    expect(fresh.lastEventId, 0);
    expect(fresh.timeline, isEmpty);
    await reopened.loadMoreEvents('r');
    expect(fixture.pageCursors, [0, 2, 0]);
    expect(fresh.lastEventId, 2);
  });

  test('204 补齐完整原始时间线，重发事件不混入最终正文且不再重连', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture
      ..noContent = true
      ..finished = true
      ..status = 'failed'
      ..output = '本次正文'
      ..error = {'code': 'model_network_error', 'message': '中断'};
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'thinking', {'content': '旧思考'}),
      _event(3, 'text', {'content': '旧尝试正文'}),
      _event(4, 'compact', {}),
      _event(5, 'text', {'content': '本次正文'}),
      _event(6, 'error', {'code': 'model_network_error', 'message': '中断'}),
      _event(7, 'end', {'status': 'failed'}),
    ]);
    final conversation = fixture.model.createLocalSession();
    fixture.model.updateDraft('s', '问题');
    await fixture.model.send('s', model: 'model', provider: 'test');
    await _until(() => conversation.runs.single.lastEventId == 7);
    final run = conversation.runs.single;
    expect(run.timeline.map((e) => e.eventId), [1, 2, 3, 4, 5, 6, 7]);
    expect(run.rawText, '本次正文');
    expect(run.error, '中断');
    expect(run.isTerminal, isTrue);
    expect(fixture.pageCursors, [0, 2, 4, 6]);
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(fixture.cursors, [null]);
  });

  test('SSE 缺口先从分页补齐，再连续应用重叠事件', () async {
    final fixture = await _HistoryServer.open();
    addTearDown(fixture.close);
    fixture.events.addAll([
      _event(1, 'start', {}),
      _event(2, 'text', {'content': '甲'}),
      _event(3, 'text', {'content': '乙'}),
      _event(4, 'text', {'content': '丙'}),
      _event(5, 'text', {'content': '丁'}),
    ]);
    final model = fixture.model;
    final conversation = model.createLocalSession();
    model.updateDraft('s', '问题');
    await model.send('s', model: 'model', provider: 'test');
    await _until(() => fixture.connections.isNotEmpty);
    await fixture.send(fixture.events[0]);
    await fixture.send(fixture.events[4]);
    await _until(
      () => conversation.runs.single.lastEventId == 5,
      diagnostic: () =>
          'seq=${conversation.runs.single.lastEventId} error=${conversation.runs.single.error} pages=${fixture.pageCursors} connections=${fixture.cursors}',
    );
    expect(conversation.runs.single.rawText, '甲乙丙丁');
    expect(fixture.pageCursors, [1, 3]);
    expect(fixture.cursors, [null]);
  });
}

Map<String, Object?> _event(int seq, String type, Map<String, Object?> data) =>
    {'seq': seq, 'type': type, 'data': data};

Future<void> _until(
  bool Function() predicate, {
  String Function()? diagnostic,
}) async {
  final deadline = DateTime.now().add(const Duration(seconds: 3));
  while (!predicate()) {
    if (DateTime.now().isAfter(deadline)) {
      fail('等待公开展示状态超时 ${diagnostic?.call() ?? ''}');
    }
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
}

class _HistoryServer {
  _HistoryServer(this.server) {
    client = _RealHttpOverrides().createHttpClient(null)
      ..findProxy = (_) => 'DIRECT';
    gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}'),
      client: client,
    );
    model = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 's',
      createIdempotencyKey: () => 'key',
      retryDelay: (_) => const Duration(milliseconds: 10),
    );
    server.listen((request) => unawaited(_handle(request)));
  }

  static Future<_HistoryServer> open() async =>
      _HistoryServer(await HttpServer.bind(InternetAddress.loopbackIPv4, 0));

  final HttpServer server;
  late final HttpClient client;
  late final HttpChatGateway gateway;
  late final ChatViewModel model;
  final events = <Map<String, Object?>>[];
  final connections = <HttpResponse>[];
  final cursors = <String?>[];
  final pageCursors = <int>[];
  bool noContent = false;
  bool finished = false;
  String status = 'running';
  String receiptStatus = 'running';
  String? output;
  Map<String, Object?>? error;
  Completer<void>? pageGate;
  int pageSize = 2;
  int snapshotFailures = 0;
  int pageFailures = 0;

  Future<void> _handle(HttpRequest request) async {
    final response = request.response;
    try {
      response.headers.contentType = ContentType.json;
      switch (request.uri.path) {
        case '/v1/run':
          await request.drain<void>();
          response.statusCode = 202;
          response.write(
            jsonEncode({
              'run_id': 'r',
              'status': receiptStatus,
              'output': null,
              'error': null,
            }),
          );
        case '/v1/run/r':
          if (snapshotFailures > 0) {
            snapshotFailures--;
            response.statusCode = 500;
            response.write(
              jsonEncode({
                'error': {'code': 'internal_error', 'message': '稍后重试'},
              }),
            );
            break;
          }
          response.write(
            jsonEncode({
              'run_id': 'r',
              'session_id': 's',
              'provider': 'test',
              'model': 'model',
              'input': '问题',
              'status': status,
              'output': output,
              'error': error,
              'created_at': '2026-10-27T10:00:00+08:00',
              'finished_at': finished ? '2026-10-27T10:01:00+08:00' : null,
            }),
          );
        case '/v1/run/r/cancel':
          await request.drain<void>();
          response.write(jsonEncode({'run_id': 'r', 'status': status}));
        case '/v1/session':
          response.write(
            jsonEncode({
              'sessions': ['s'],
            }),
          );
        case '/v1/session/s':
          response.write(
            jsonEncode({
              'session_id': 's',
              'session_name': '会话',
              'run_ids': ['r'],
            }),
          );
        case '/v1/run/r/events':
          if (pageFailures > 0) {
            pageFailures--;
            response.statusCode = 500;
            response.write(
              jsonEncode({
                'error': {'code': 'internal_error', 'message': '历史稍后重试'},
              }),
            );
            break;
          }
          final after = int.parse(
            request.uri.queryParameters['after_seq'] ?? '0',
          );
          pageCursors.add(after);
          final selected = events
              .where((e) => (e['seq']! as int) > after)
              .toList();
          final page = selected.take(pageSize).toList();
          await pageGate?.future;
          response.write(
            jsonEncode({
              'events': page,
              'next_after_seq': page.isEmpty ? after : page.last['seq'],
              'has_more': selected.length > page.length,
            }),
          );
        case '/v1/run/r/event':
          cursors.add(request.headers.value('Last-Event-ID'));
          if (noContent) {
            response.statusCode = 204;
            break;
          }
          response.headers.contentType = ContentType('text', 'event-stream');
          response.bufferOutput = false;
          response.write(': ready\n\n');
          await response.flush();
          connections.add(response);
          return;
        default:
          response.statusCode = 404;
      }
      await response.close();
    } catch (_) {
      // 页面释放时允许传输被主动关闭，不修改夹具中的运行状态。
    }
  }

  Future<void> send(Map<String, Object?> event) async {
    connections.last.add(
      utf8.encode(
        'id: ${event['seq']}\nevent: ${event['type']}\ndata: ${jsonEncode(event['data'])}\n\n',
      ),
    );
    await connections.last.flush();
  }

  Future<void> close() async {
    model.dispose();
    gateway.dispose();
    client.close(force: true);
    await server.close(force: true);
  }
}

class _RealHttpOverrides extends HttpOverrides {}
