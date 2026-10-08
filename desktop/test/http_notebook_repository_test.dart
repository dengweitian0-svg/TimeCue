import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';
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
  int? openScheduleCount,
  String cursor = '',
}) => {
  'items': items,
  'total': total ?? items.length,
  'openScheduleCount':
      openScheduleCount ??
      items.map((entry) => entry['scheduleId']).toSet().length,
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
      request.response.write(
        jsonEncode(
          data is Map && data.containsKey('error') ? data : {'data': data},
        ),
      );
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
  test('启用日程总数来自服务端，包含确认记录且不随分页长度变化', () async {
    final date = DateTime.utc(2026, 10, 8);
    final nextDate = DateTime.utc(2026, 10, 9);
    var fail = false;
    final repo = await serverRepository((request) {
      if (fail) throw StateError('连接中断');
      if (request.uri.queryParameters['view'] == 'calendar') return <String>[];
      if (request.uri.queryParameters['date'] == '2026-10-09') {
        return page([], openScheduleCount: 0);
      }
      if (request.uri.queryParameters['cursor'] == 'date-next') {
        return page([occurrence(60)], total: 61, openScheduleCount: 56);
      }
      final data = snapshot();
      data['occurrences'] = page(
        [
          occurrence(1, acknowledgedAt: '2026-10-08T10:00:00+08:00'),
          occurrence(2),
        ],
        total: 61,
        openScheduleCount: 56,
        cursor: 'date-next',
      );
      return data;
    });
    expect(repo.openScheduleCountOn(date), isNull);
    await repo.refresh();
    expect(repo.today.length, 2);
    expect(repo.today.first.acknowledgedAt, isNotNull);
    expect(repo.openScheduleCountOn(date), 56);
    await repo.loadMoreOn(date);
    expect(repo.today.length, 3);
    expect(repo.openScheduleCountOn(date), 56);
    await repo.refresh(date: nextDate);
    expect(repo.openScheduleCountOn(nextDate), 0);
    fail = true;
    await repo.refresh(date: nextDate);
    expect(repo.openScheduleCountOn(nextDate), 0);
    expect(repo.refreshError, isNotNull);
  });
  test('创建和更新均拒绝秒或亚秒，不静默取整后发出请求', () async {
    var requests = 0;
    final repo = await serverRepository((request) {
      requests++;
      return {};
    });
    for (final time in [
      DateTime.utc(2099, 1, 1, 9, 30, 1),
      DateTime.utc(2099, 1, 1, 9, 30, 0, 1),
    ]) {
      final draft = ScheduleDraft(
        name: '分钟规则',
        description: '',
        kind: ScheduleKind.single,
        time: time,
      );
      await expectLater(repo.save(draft), throwsStateError);
      await expectLater(
        repo.save(draft, id: '1', expectedRevision: 1),
        throwsStateError,
      );
    }
    expect(requests, 0);
  });

  test('真实创建支持暂停和多时间，更新发送草稿版本，删除接受 204', () async {
    final writes = <Map<String, dynamic>>[];
    final methods = <String>[];
    var revision = 1;
    Map<String, dynamic> scheduleData() => {
      'id': 7,
      'name': '周期',
      'description': '说明',
      'type': 'period',
      'revision': revision,
      'cancelled': false,
      'period': {
        'open': false,
        'cron': ['30 9 * * 1,3,5', '0 18 * * 1,3,5'],
      },
    };
    final repo = await serverRepository((request) async {
      methods.add(request.method);
      if (request.method == 'POST' || request.method == 'PUT') {
        writes.add(
          jsonDecode(await utf8.decoder.bind(request).join())
              as Map<String, dynamic>,
        );
        expect(request.headers.value('Idempotency-Key'), isNotEmpty);
        if (request.method == 'POST') {
          request.response.statusCode = 201;
        } else {
          revision++;
        }
        return scheduleData();
      }
      if (request.method == 'DELETE') {
        request.response.statusCode = 204;
        return {};
      }
      if (request.uri.path.endsWith('/7')) return scheduleData();
      if (request.uri.queryParameters['view'] == 'calendar') return <String>[];
      if (request.uri.queryParameters['view'] == null) return [scheduleData()];
      return snapshot();
    });
    await repo.refresh();
    const times = ['09:30', '18:00'];
    final draft = ScheduleDraft(
      name: '周期',
      description: '说明',
      kind: ScheduleKind.weekly,
      time: DateTime.utc(2026, 10, 8, 9, 30),
      enabled: false,
      times: times,
      days: [5, 1, 3],
    );
    await repo.save(draft, operationKey: 'create-session');
    expect(writes.first['period'], {
      'open': false,
      'cron': ['30 9 * * 1,3,5', '0 18 * * 1,3,5'],
    });
    final original = await repo.loadSchedule('7');
    expect(original.revision, 1);
    expect(original.draft.times, times);
    await repo.save(
      original.draft,
      id: '7',
      expectedRevision: original.revision,
      operationKey: 'edit-session',
    );
    expect(writes.last['expectedRevision'], 1);
    expect((await repo.listSchedules()).single.draft.enabled, isFalse);
    await repo.delete('7');
    expect(methods, containsAll(['POST', 'PUT', 'DELETE']));
  });

  test('保存失败重试复用操作标识，结果不明时禁止改写请求', () async {
    final keys = <String?>[];
    var fail = true;
    final repo = await serverRepository((request) async {
      if (request.method == 'POST') {
        keys.add(request.headers.value('Idempotency-Key'));
        await utf8.decoder.bind(request).join();
        if (fail) throw StateError('提交结果不明');
        request.response.statusCode = 201;
        return {
          'id': 1,
          'name': '单次',
          'description': '',
          'revision': 1,
          'type': 'single',
          'single': {'notifyTime': '2099-01-01T09:30:00+08:00'},
        };
      }
      return request.uri.queryParameters['view'] == 'calendar'
          ? <String>[]
          : snapshot();
    });
    final draft = ScheduleDraft(
      name: '单次',
      description: '',
      kind: ScheduleKind.single,
      time: DateTime.utc(2099, 1, 1, 9, 30),
    );
    await expectLater(
      repo.save(draft, operationKey: 'session'),
      throwsA(isA<NotebookApiException>()),
    );
    final changed = ScheduleDraft(
      name: '更改',
      description: '',
      kind: draft.kind,
      time: draft.time,
    );
    await expectLater(
      repo.save(changed, operationKey: 'session'),
      throwsStateError,
    );
    fail = false;
    await repo.save(draft, operationKey: 'session');
    expect(keys.length, 2);
    expect(keys.first, keys.last);
  });

  test('编辑版本冲突保留草稿，重新加载不会随后台刷新覆盖字段', () async {
    var revision = 3;
    final repo = await serverRepository((request) async {
      if (request.method == 'PUT') {
        await utf8.decoder.bind(request).join();
        request.response.statusCode = 409;
        return {
          'error': {'code': 'revision_conflict', 'message': '日程已修改'},
        };
      }
      if (request.uri.path.endsWith('/1')) {
        return {
          'id': 1,
          'name': '原名称',
          'description': '原说明',
          'type': 'single',
          'revision': revision,
          'single': {'notifyTime': '2099-01-01T09:30:00+08:00'},
        };
      }
      return request.uri.queryParameters['view'] == 'calendar'
          ? <String>[]
          : snapshot();
    });
    await repo.refresh();
    final editor = ScheduleEditorViewModel(repo, await repo.loadSchedule('1'));
    addTearDown(editor.dispose);
    editor.name.text = '未保存草稿';
    editor.dirty = true;
    await repo.refresh();
    expect(editor.name.text, '未保存草稿');
    expect(await editor.save(), isFalse);
    expect(editor.name.text, '未保存草稿');
    expect(editor.dirty, isTrue);
    expect(editor.conflict, isTrue);
    revision = 4;
    await editor.reload();
    expect(editor.original!.revision, 4);
    expect(editor.name.text, '原名称');
    expect(editor.dirty, isFalse);
  });

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
