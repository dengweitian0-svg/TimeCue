import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/http_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';

Map<String, dynamic> occurrence(
  int id, {
  String delivery = 'pending',
  String? acknowledgedAt,
  String? time,
}) => {
  'occurrenceKey': 'occurrence-$id',
  'scheduleId': 1,
  'notificationId': id,
  'name': '提醒 $id',
  'description': '完整内容',
  'notifyTime': time ?? '2026-10-08T09:00:00+08:00',
  'source': 'persisted',
  'deliveryStatus': delivery,
  'acknowledgedAt': acknowledgedAt,
  'editable': false,
};
Map<String, dynamic> page(
  List<Map<String, dynamic>> items, {
  int? total,
  String cursor = '',
}) => {
  'items': items,
  'total': total ?? items.length,
  'nextCursor': cursor,
  'asOf': '2026-10-08T14:32:00+08:00',
};
Map<String, dynamic> snapshot({
  List<Map<String, dynamic>>? pending,
  int total = 51,
}) => {
  'date': '2026-10-08',
  'asOf': '2026-10-08T14:32:00+08:00',
  'occurrences': page([occurrence(1, delivery: 'failed'), occurrence(2)]),
  'unacknowledged': page(
    pending ?? [occurrence(1, delivery: 'failed')],
    total: total,
    cursor: 'page-two',
  ),
};

Future<HttpNotebookRepository> serverRepository(
  FutureOr<Object> Function(HttpRequest) handle, {
  String basePath = '/',
}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  server.listen((request) async {
    try {
      final data = await handle(request);
      request.response.headers.contentType = ContentType.json;
      request.response.write(jsonEncode({'data': data}));
      await request.response.close();
    } catch (_) {
      request.response.statusCode = 503;
      request.response.write(
        jsonEncode({
          'error': {'message': '服务不可用'},
        }),
      );
      await request.response.close();
    }
  });
  final repo = HttpNotebookRepository(
    base: Uri.parse('http://127.0.0.1:${server.port}$basePath'),
  );
  addTearDown(() async {
    repo.dispose();
    await server.close(force: true);
  });
  return repo;
}

void main() {
  test('真实接口前缀分别连接日程查询和同级提醒资源', () async {
    final requests = <String>[];
    final repo = await serverRepository((request) {
      requests.add(request.uri.path);
      switch (request.uri.path) {
        case '/api/v1/scheduler/schedules':
          return request.uri.queryParameters['view'] == 'calendar'
              ? <String>[]
              : snapshot();
        case '/api/v1/notifications':
          return page([occurrence(51)], total: 51);
        case '/api/v1/notifications/acknowledgements':
          return [
            {
              'notificationId': 1,
              'acknowledgedAt': '2026-10-08T14:32:00+08:00',
            },
          ];
        default:
          throw StateError('未注册的真实请求路径');
      }
    }, basePath: '/api/v1/scheduler/');
    await repo.refresh();
    expect(repo.hasSnapshot, isTrue);
    await repo.loadPendingPage(1);
    expect(repo.pending.single.id, '51');
    await repo.confirm({'1'});
    expect(requests, contains('/api/v1/notifications/acknowledgements'));
    expect(
      requests.any(
        (path) => path.startsWith('/api/v1/scheduler/notifications'),
      ),
      isFalse,
    );
    expect(repo.refreshError, isNull);
  });
  test('单条确认使用 notificationId，单元素集合的首次时间同步两侧并保留投递结果', () async {
    var confirmed = false;
    final repo = await serverRepository((request) async {
      if (request.uri.path == '/schedules' &&
          request.uri.queryParameters['view'] == 'calendar') {
        return <String>[];
      }
      if (request.uri.path == '/notifications/acknowledgements') {
        expect(request.method, 'POST');
        expect(jsonDecode(await utf8.decoder.bind(request).join()), {
          'notificationIds': [1],
        });
        confirmed = true;
        return [
          {'notificationId': 1, 'acknowledgedAt': '2026-10-08T14:32:00+08:00'},
        ];
      }
      if (!confirmed) return snapshot();
      return {
        'date': '2026-10-08',
        'asOf': '2026-10-08T14:32:00+08:00',
        'occurrences': page([
          occurrence(
            1,
            delivery: 'failed',
            acknowledgedAt: '2026-10-08T14:32:00+08:00',
          ),
          occurrence(2),
        ]),
        'unacknowledged': page([occurrence(51)], total: 50),
      };
    });
    await repo.refresh();
    await repo.confirm({'1'});
    expect(repo.today.first.delivery, 'failed');
    expect(repo.today.first.acknowledgedAt!.hour, 14);
    expect(repo.today.last.acknowledgedAt, isNull);
    expect(repo.pendingTotal, 50);
    expect(repo.pending.single.id, '51');
  });
  test('北京时间映射、服务端总数、全选本页和跨页选择不随新增扩大', () async {
    var fresh = false;
    final repo = await serverRepository((request) {
      if (request.uri.path == '/schedules' &&
          request.uri.queryParameters['view'] == 'calendar') {
        return ['2026-10-09'];
      }
      if (request.uri.path == '/notifications') {
        return page([occurrence(51)], total: 51);
      }
      return snapshot(pending: fresh ? [occurrence(1), occurrence(3)] : null);
    });
    final vm = NotebookViewModel(repo);
    addTearDown(vm.dispose);
    await repo.refresh();
    expect(repo.demoNow.hour, 14);
    expect(repo.today.first.plannedAt.hour, 9);
    expect(repo.pendingTotal, 51);
    expect(vm.pageCount, 2);
    expect(repo.markedOn(DateTime.utc(2026, 10, 9)), isTrue);
    vm.selectPage();
    expect(vm.selected, {'1'});
    await repo.loadPendingPage(1);
    vm.toggle('51');
    expect(vm.selected, {'1', '51'});
    fresh = true;
    await repo.refresh();
    expect(vm.selected, {'1', '51'});
    expect(vm.pendingPage.map((e) => e.id), ['1', '3']);
  });

  test('批量请求失败保留两侧及跨页选择，成功后独立更新且刷新失败不伪装确认失败', () async {
    var reject = true, confirmed = false;
    Object? body;
    final repo = await serverRepository((request) async {
      if (request.uri.path == '/schedules' &&
          request.uri.queryParameters['view'] == 'calendar') {
        return <String>[];
      }
      if (request.uri.path == '/notifications/acknowledgements') {
        body = jsonDecode(await utf8.decoder.bind(request).join());
        if (reject) throw StateError('失败');
        confirmed = true;
        return [
          {'notificationId': 1, 'acknowledgedAt': '2026-10-08T14:32:00+08:00'},
          {'notificationId': 51, 'acknowledgedAt': '2026-10-08T14:32:00+08:00'},
        ];
      }
      if (confirmed) throw StateError('刷新失败');
      return snapshot();
    });
    final vm = NotebookViewModel(repo);
    addTearDown(vm.dispose);
    await repo.refresh();
    vm.toggle('1');
    vm.toggle('51');
    await vm.confirm(vm.selected);
    expect(body, {
      'notificationIds': [1, 51],
    });
    expect(vm.selected, {'1', '51'});
    expect(vm.error, isNotNull);
    expect(repo.today.first.acknowledgedAt, isNull);
    expect(repo.pending.single.id, '1');
    reject = false;
    await vm.confirm(vm.selected);
    expect(vm.selected, isEmpty);
    expect(vm.error, isNull);
    expect(repo.refreshError, isNotNull);
    expect(repo.today.first.acknowledgedAt!.hour, 14);
    expect(repo.today.first.delivery, 'failed');
    expect(repo.today.last.acknowledgedAt, isNull);
    expect(repo.pending, isEmpty);
  });

  test('迟到的旧查询不能覆盖新快照，失败刷新保留内容', () async {
    final firstArrived = Completer<void>(), release = Completer<void>();
    var reads = 0, reject = false;
    final repo = await serverRepository((request) async {
      if (request.uri.path == '/schedules' &&
          request.uri.queryParameters['view'] == 'calendar') {
        return <String>[];
      }
      if (reject) throw StateError('断线');
      reads++;
      if (reads == 1) {
        firstArrived.complete();
        await release.future;
        return snapshot(pending: [occurrence(10)]);
      }
      return snapshot(pending: [occurrence(20)]);
    });
    final first = repo.refresh();
    await firstArrived.future;
    await repo.refresh();
    release.complete();
    await first;
    expect(repo.pending.single.id, '20');
    reject = true;
    await repo.refresh();
    expect(repo.pending.single.id, '20');
    expect(repo.refreshError, isNotNull);
  });

  test('日期错误保留选中日期并提供可重试状态，初始右页保持空白', () async {
    var reject = true;
    final repo = await serverRepository((request) {
      if (request.uri.path == '/schedules' &&
          request.uri.queryParameters['view'] == 'calendar') {
        return <String>[];
      }
      if (request.uri.path == '/schedules' &&
          request.uri.queryParameters['view'] == 'occurrences' &&
          request.uri.queryParameters['include'] == null) {
        if (reject) throw StateError('失败');
        return page([occurrence(3, time: '2026-10-09T09:00:00+08:00')]);
      }
      return snapshot();
    });
    final vm = NotebookViewModel(repo);
    addTearDown(vm.dispose);
    expect(vm.right, isA<BlankPage>());
    final date = DateTime.utc(2026, 10, 9);
    await vm.selectDate(date);
    expect(vm.right, isA<ErrorDatePage>());
    expect(vm.selectedDate, date);
    reject = false;
    await vm.selectDate(date);
    expect(vm.right, isA<DateListPage>());
    expect(repo.entriesOn(date).single.name, '提醒 3');
  });
}
