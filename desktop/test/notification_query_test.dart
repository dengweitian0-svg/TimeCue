import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'http_notebook_repository_test.dart' as fixture;

Map<String, Object?> recoveryJson({String state = 'running', int? missed}) => {
  'recoveryId': 'round-1',
  'state': state,
  'through': '2026-10-08T06:00:00Z',
  'materializedCount': 500,
  'missedTotal': missed,
  'displayableCount': 480,
  'deliveryTaskId': 'task-1',
};

void main() {
  test('任务/attempt 查询保持原身份和游标，详情读取真实双状态且不写业务', () async {
    final requests = <String>[];
    final repo = await fixture.serverRepository((request) {
      requests.add('${request.method} ${request.uri}');
      expect(request.method, 'GET');
      if (request.uri.path.endsWith('/notifications/51')) {
        return fixture.occurrence(
          51,
          delivery: 'failed',
          acknowledgedAt: '2026-10-08T10:00:00+08:00',
        );
      }
      expect(request.uri.path, '/api/v1/notifications');
      expect(request.uri.queryParameters.containsKey('state'), isFalse);
      expect(request.uri.queryParameters.containsKey('scope'), isFalse);
      expect(request.uri.queryParameters['deliveryTaskId'], 'task/a');
      expect(request.uri.queryParameters['attemptId'], 'attempt/a');
      final second = request.uri.queryParameters['cursor'] == 'opaque/+=cursor';
      return fixture.page(
        second
            ? [fixture.occurrence(51)]
            : [for (var i = 1; i <= 50; i++) fixture.occurrence(i)],
        total: 51,
        cursor: second ? '' : 'opaque/+=cursor',
      )..['recovery'] = recoveryJson();
    }, basePath: '/api/v1/scheduler/');
    var publications = 0;
    repo.addListener(() => publications++);
    final first = await repo.notificationMembers(
      'task/a',
      attemptId: 'attempt/a',
    );
    final last = await repo.notificationMembers(
      'task/a',
      attemptId: 'attempt/a',
      cursor: first.nextCursor,
    );
    final detail = await repo.notificationDetails('51');
    expect(first.items.length, 50);
    expect(last.items.single.notificationId, '51');
    expect(last.total, 51);
    expect(first.recovery!.missedTotal, isNull);
    expect(first.recovery!.through.hour, 14);
    expect(detail.delivery, 'failed');
    expect(detail.acknowledgedAt!.hour, 10);
    expect(publications, 0);
    expect(repo.hasSnapshot, isFalse);
    expect(repo.loading, isFalse);
    expect(requests.length, 3);
  });

  test('组合快照发布恢复摘要，null/缺省及失败均不伪造完成', () async {
    Object? progress = recoveryJson();
    var failing = false;
    final repo = await fixture.serverRepository((request) {
      if (failing) throw StateError('断线');
      if (request.uri.queryParameters['view'] == 'calendar') return <String>[];
      return fixture.snapshot()..['recovery'] = progress;
    });
    await repo.refresh();
    expect(repo.recovery!.state, 'running');
    expect(repo.recovery!.missedTotal, isNull);
    expect(repo.pendingTotal, 51);
    failing = true;
    await repo.refresh();
    expect(repo.recovery!.state, 'running');
    expect(repo.refreshError, isNotNull);
    failing = false;
    progress = recoveryJson(state: 'completed', missed: 0);
    await repo.refresh();
    expect(repo.recovery!.missedTotal, 0);
    progress = null;
    await repo.refresh();
    expect(repo.recovery, isNull);
  });

  test('历史成员不附 attempt，HTTP 错误原样保留并拒绝错配详情', () async {
    var status = 409;
    final repo = await fixture.serverRepository((request) {
      if (request.uri.path.endsWith('/notifications/7')) {
        return fixture.occurrence(8);
      }
      expect(request.uri.queryParameters, {'deliveryTaskId': 'task'});
      request.response.statusCode = status;
      return {
        'error': {'code': 'members_changed', 'message': '成员改变'},
      };
    });
    for (final code in [400, 404, 409]) {
      status = code;
      await expectLater(
        repo.notificationMembers('task'),
        throwsA(
          isA<NotebookApiException>().having(
            (error) => error.status,
            'status',
            code,
          ),
        ),
      );
    }
    await expectLater(repo.notificationDetails('7'), throwsFormatException);
  });

  test('恢复摘要校验状态与数量，保留未知总数', () {
    expect(RecoveryProgress.fromJson(null), isNull);
    expect(
      RecoveryProgress.fromJson(recoveryJson(state: 'failed'))!.state,
      'failed',
    );
    expect(
      () => RecoveryProgress.fromJson(recoveryJson(state: 'sent')),
      throwsFormatException,
    );
    expect(
      () =>
          RecoveryProgress.fromJson(recoveryJson()..['materializedCount'] = -1),
      throwsFormatException,
    );
  });
}
