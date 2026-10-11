import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/data/http_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'http_notebook_repository_test.dart'
    show occurrence, page, serverRepository;

// 复用原 HTTP 夹具，按真实请求范围返回确定数据；时钟与迟到响应可控。
class _Fixture {
  _Fixture(int count) {
    final today = DateTime.utc(now.year, now.month, now.day, 9);
    for (var i = 0; i < count; i++) {
      records.add(
        occurrence(
          i + 1,
          time: stamp(today.subtract(const Duration(days: 1))),
          delivery: ['sent', 'failed', 'pending', 'loaded'][i % 4],
        ),
      );
      records.add(
        occurrence(
          i + 1001,
          time: stamp(today),
          delivery: ['sent', 'failed', 'pending', 'loaded'][i % 4],
        ),
      );
    }
  }
  DateTime now = DateTime.utc(2026, 10, 8, 23, 59);
  final records = <Map<String, dynamic>>[];
  final queries = <Map<String, String>>[];
  final submitted = <Set<int>>[];
  bool failReads = false, failConfirm = false, expireCursor = false;
  Future<void> Function(HttpRequest)? delayPending;
  Future<void> Function()? delayConfirm;
  static String stamp(DateTime time) =>
      time.toIso8601String().replaceFirst('Z', '+08:00');
  List<Map<String, dynamic>> values(String? scope) {
    final result = records.where((entry) {
      final time = HttpNotebookRepository.wallTime(
        entry['notifyTime'] as String,
      );
      return entry['acknowledgedAt'] == null &&
          !time.isAfter(now) &&
          (scope == null || sameDay(time, now) == (scope == 'today'));
    }).toList();
    result.sort((a, b) {
      final comparison = (a['notifyTime'] as String).compareTo(
        b['notifyTime'] as String,
      );
      return comparison != 0
          ? comparison
          : (a['notificationId'] as int).compareTo(b['notificationId'] as int);
    });
    return result;
  }

  Map<String, dynamic> paged(String? scope, int offset) {
    final all = values(scope);
    return {
      ...page(
        all.skip(offset).take(50).toList(),
        total: all.length,
        cursor: offset + 50 < all.length
            ? '${HttpNotebookRepository.dateKey(now)}:$scope:${offset + 50}'
            : '',
      ),
      'asOf': stamp(now),
    };
  }

  Future<Object> handle(HttpRequest request) async {
    final query = request.uri.queryParameters;
    if (request.uri.path.endsWith('/acknowledgements')) {
      final body = jsonDecode(await utf8.decoder.bind(request).join()) as Map;
      final ids = (body['notificationIds'] as List).cast<int>().toSet();
      submitted.add(ids);
      await delayConfirm?.call();
      if (failConfirm) throw StateError('确认失败');
      final time = stamp(now);
      for (final entry in records) {
        if (ids.contains(entry['notificationId'])) {
          entry['acknowledgedAt'] ??= time;
        }
      }
      return [
        for (final id in ids) {'notificationId': id, 'acknowledgedAt': time},
      ];
    }
    if (failReads) throw StateError('读取失败');
    if (request.uri.path.endsWith('/notifications')) {
      queries.add(Map.of(query));
      expect(query['state'], 'unacknowledged');
      expect(query['scope'], isIn(['today', 'history']));
      final cursor = query['cursor'];
      if (cursor != null &&
          (expireCursor ||
              !cursor.startsWith(
                '${HttpNotebookRepository.dateKey(now)}:${query['scope']}:',
              ))) {
        expireCursor = false;
        request.response.statusCode = 400;
        return {
          'error': {'code': 'invalid_request', 'message': '游标失效'},
        };
      }
      final data = paged(
        query['scope'],
        cursor == null ? 0 : int.parse(cursor.split(':').last),
      );
      await delayPending?.call(request);
      return data;
    }
    if (query['view'] == 'calendar') {
      return [HttpNotebookRepository.dateKey(now)];
    }
    final allToday = records
        .where(
          (entry) => sameDay(
            HttpNotebookRepository.wallTime(entry['notifyTime'] as String),
            now,
          ),
        )
        .toList();
    return {
      'date': HttpNotebookRepository.dateKey(now),
      'asOf': stamp(now),
      'occurrences': {
        ...page(allToday, total: allToday.length),
        'asOf': stamp(now),
      },
      'unacknowledged': paged(null, 0),
    };
  }

  Future<HttpNotebookRepository> repository() =>
      serverRepository(handle, now: () => now, scopedQueries: true);
}

void main() {
  for (final count in [0, 50, 51, 125]) {
    test('今日与历史各 $count 条，独立游标及完整总数', () async {
      final fixture = _Fixture(count);
      final repo = await fixture.repository();
      await repo.refresh();
      expect(repo.refreshError, isNull);
      expect(repo.pendingTotal, count * 2);
      for (final scope in PendingScope.values) {
        final ids = <String>{};
        for (var index = 0; index < (count / 50).ceil().clamp(1, 10); index++) {
          if (index > 0) await repo.loadPendingPage(index, scope: scope);
          expect(repo.pendingTotalFor(scope), count);
          for (final item in repo.pendingFor(scope)) {
            expect(ids.add(item.id), isTrue);
          }
        }
        expect(ids.length, count);
      }
      final todayPage = repo.pendingPageIndexFor(PendingScope.today);
      final historyPage = repo.pendingPageIndexFor(PendingScope.history);
      await repo.refresh();
      expect(repo.pendingPageIndexFor(PendingScope.today), todayPage);
      expect(repo.pendingPageIndexFor(PendingScope.history), historyPage);
      expect(repo.pendingTotal, count * 2);
    });
  }

  test('两类翻页可独立完成，迟到范围响应不能覆盖刷新', () async {
    final fixture = _Fixture(51);
    final repo = await fixture.repository();
    await repo.refresh();
    final arrived = Completer<void>(), release = Completer<void>();
    fixture.delayPending = (request) async {
      if (request.uri.queryParameters['scope'] == 'history' &&
          request.uri.queryParameters.containsKey('cursor')) {
        arrived.complete();
        await release.future;
      }
    };
    final old = repo.loadPendingPage(1, scope: PendingScope.history);
    await arrived.future;
    await repo.loadPendingPage(1);
    expect(repo.pendingFor(PendingScope.today).single.id, '1051');
    fixture.delayPending = null;
    await repo.refresh();
    release.complete();
    await old;
    expect(repo.pendingPageIndexFor(PendingScope.history), 0);
    expect(repo.pendingFor(PendingScope.history).first.id, '1');
    expect(repo.pendingLoadingFor(PendingScope.history), isFalse);
  });

  test('两范围选择与提交隔离，失败保留，成功读取失败保持确认事实', () async {
    final fixture = _Fixture(52);
    final repo = await fixture.repository();
    final vm = NotebookViewModel(repo);
    addTearDown(vm.dispose);
    await repo.refresh();
    vm.toggle('1001');
    vm.changeTab(NotebookTab.history);
    vm.toggle('1');
    await repo.loadPendingPage(1, scope: PendingScope.history);
    vm.toggle('51');
    fixture.failConfirm = true;
    await vm.confirm(vm.selected);
    expect(vm.selected, {'1', '51'});
    expect(vm.page, 1);
    expect(vm.error, isNotNull);
    vm.changeTab(NotebookTab.today);
    expect(vm.selected, {'1001'});
    vm.changeTab(NotebookTab.history);
    fixture.failConfirm = false;
    final arrived = Completer<void>(), release = Completer<void>();
    fixture.delayConfirm = () async {
      arrived.complete();
      await release.future;
    };
    final submit = vm.confirm(vm.selected);
    await arrived.future;
    vm.changeTab(NotebookTab.today);
    fixture.failReads = true;
    release.complete();
    await submit;
    expect(fixture.submitted.last, {1, 51});
    expect(vm.selected, {'1001'});
    expect(repo.pendingTotal, 102);
    expect(repo.pendingTotalFor(PendingScope.history), 50);
    expect(repo.pendingTotalFor(PendingScope.today), 52);
    expect(repo.pendingFor(PendingScope.history).map((entry) => entry.id), [
      '52',
    ]);
    vm.changeTab(NotebookTab.history);
    expect(vm.selected, isEmpty);
    expect(vm.error, isNull);
    expect(repo.refreshError, isNotNull);
  });

  test('跨午夜废弃两范围分页与选择，旧请求晚到不覆盖新日期', () async {
    final fixture = _Fixture(51);
    final repo = await fixture.repository();
    final vm = NotebookViewModel(repo);
    addTearDown(vm.dispose);
    await repo.refresh();
    vm.toggle('1001');
    vm.changeTab(NotebookTab.history);
    vm.toggle('1');
    await repo.loadPendingPage(1, scope: PendingScope.history);
    final arrived = Completer<void>(), release = Completer<void>();
    fixture.delayPending = (request) async {
      if (request.uri.queryParameters.containsKey('cursor') &&
          request.uri.queryParameters['scope'] == 'today') {
        arrived.complete();
        await release.future;
      }
    };
    final old = repo.loadPendingPage(1);
    await arrived.future;
    fixture.now = fixture.now.add(const Duration(minutes: 1));
    fixture.delayPending = null;
    final before = fixture.queries.length;
    await repo.refresh();
    release.complete();
    await old;
    expect(
      fixture.queries
          .skip(before)
          .every((query) => !query.containsKey('cursor')),
      isTrue,
    );
    expect(repo.pendingPageIndexFor(PendingScope.today), 0);
    expect(repo.pendingPageIndexFor(PendingScope.history), 0);
    expect(repo.pendingTotalFor(PendingScope.today), 0);
    expect(repo.pendingTotalFor(PendingScope.history), 102);
    expect(repo.today, isEmpty);
    expect(vm.selected, isEmpty);
    vm.changeTab(NotebookTab.today);
    expect(vm.selected, isEmpty);
    expect(fixture.submitted, isEmpty);
  });

  test('两个首分页跨午夜只发布同日组合，旧游标400后重新读首分页', () async {
    final fixture = _Fixture(51);
    final repo = await fixture.repository();
    var crossed = false;
    fixture.delayPending = (request) async {
      if (!crossed && request.uri.queryParameters['scope'] == 'history') {
        crossed = true;
        fixture.now = fixture.now.add(const Duration(minutes: 1));
      }
    };
    await repo.refresh();
    expect(repo.refreshError, isNull);
    expect(repo.demoNow.day, 9);
    expect(repo.pendingTotalFor(PendingScope.today), 0);
    expect(repo.pendingTotalFor(PendingScope.history), 102);
    fixture.delayPending = null;
    fixture.expireCursor = true;
    final before = fixture.queries.length;
    await repo.loadPendingPage(1, scope: PendingScope.history);
    expect(repo.pendingPageIndexFor(PendingScope.history), 0);
    expect(
      fixture.queries
          .skip(before + 1)
          .every((query) => !query.containsKey('cursor')),
      isTrue,
    );
    expect(repo.refreshError, isNull);
  });

  test('跨日读取失败保留旧内容与选择且禁止旧今日选择提交', () async {
    final fixture = _Fixture(1);
    final repo = await fixture.repository();
    final vm = NotebookViewModel(repo);
    addTearDown(vm.dispose);
    await repo.refresh();
    vm.toggle('1001');
    fixture.now = fixture.now.add(const Duration(minutes: 1));
    fixture.failReads = true;
    await repo.refresh();
    expect(repo.pendingFor(PendingScope.today).single.id, '1001');
    expect(vm.selected, {'1001'});
    expect(repo.pendingCurrent, isFalse);
    await vm.confirm(vm.selected);
    expect(fixture.submitted, isEmpty);
    fixture.failReads = false;
    vm.close();
    vm.open();
    // 打开入口的立即刷新，不等待五秒轮询。
    while (repo.loading) {
      await Future<void>.delayed(const Duration(milliseconds: 1));
    }
    expect(repo.pendingFor(PendingScope.today), isEmpty);
    expect(vm.selected, isEmpty);
    vm.close();
  });

  test('演示仓储按相同日期分范围，午夜不自动扩大选择', () async {
    var now = DateTime.utc(2026, 10, 7, 23, 59);
    final repo = DemoNotebookRepository(clock: () => now);
    final vm = NotebookViewModel(repo);
    addTearDown(() {
      vm.dispose();
      repo.dispose();
    });
    vm.selectPage();
    expect(vm.selected.every((id) => id.startsWith('n')), isTrue);
    vm.changeTab(NotebookTab.history);
    vm.selectPage();
    final selected = vm.selected;
    vm.turnPage(1);
    vm.changeTab(NotebookTab.today);
    vm.changeTab(NotebookTab.history);
    expect(vm.page, 1);
    expect(vm.selected, selected);
    now = now.add(const Duration(minutes: 1));
    await repo.refresh();
    expect(vm.page, 0);
    expect(vm.selected, isEmpty);
    expect(repo.pendingFor(PendingScope.today), isEmpty);
    expect(repo.pendingTotal, 9);
  });
}
