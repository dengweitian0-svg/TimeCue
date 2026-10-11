import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/http_notebook_repository.dart';

// 只在自有隔离 Go 进程上手动运行；先核对身份，避免确认用户业务库。
void main() {
  test('实际 Go 与 Flutter 范围分页、确认及总数联调', () async {
    final expectedInstance =
        Platform.environment['TIMECUE_HISTORY_SMOKE_INSTANCE'];
    expect(expectedInstance, 'history-flutter-smoke');
    final repository = HttpNotebookRepository();
    addTearDown(repository.dispose);
    expect((await repository.deliveryRuntime()).instanceId, expectedInstance);
    await repository.refresh();
    expect(repository.refreshError, isNull);
    expect(repository.pendingTotal, 103);
    for (final scope in PendingScope.values) {
      final expected = scope == PendingScope.history ? 51 : 52;
      final ids = <String>{};
      for (var index = 0; index < 2; index++) {
        if (index > 0) await repository.loadPendingPage(index, scope: scope);
        expect(repository.pendingErrorFor(scope), isNull);
        expect(repository.pendingTotalFor(scope), expected);
        for (final entry in repository.pendingFor(scope)) {
          expect(ids.add(entry.id), isTrue);
          expect(
            sameDay(entry.plannedAt, repository.demoNow),
            scope == PendingScope.today,
          );
          expect(entry.acknowledgedAt, isNull);
        }
      }
      expect(ids.length, expected);
      expect(ids.contains('105'), isFalse);
      expect(ids.contains('106'), isFalse);
      expect(ids.contains('107'), isFalse);
    }
    await repository.refresh();
    expect(repository.pendingPageIndexFor(PendingScope.today), 1);
    expect(repository.pendingPageIndexFor(PendingScope.history), 1);
    // 夹具中的 2 为暂停父日程历史，53 为同日到期记录。
    await repository.confirm({'2'});
    expect(repository.refreshError, isNull);
    expect(repository.pendingTotal, 102);
    expect(repository.pendingTotalFor(PendingScope.history), 50);
    expect(repository.pendingTotalFor(PendingScope.today), 52);
    await expectLater(
      repository.confirm({'53', '105'}),
      throwsA(
        isA<NotebookApiException>().having(
          (error) => error.status,
          'status',
          409,
        ),
      ),
    );
    expect(repository.pendingTotal, 102);
    expect(
      repository
          .pendingFor(PendingScope.today)
          .every((entry) => entry.acknowledgedAt == null),
      isTrue,
    );
  });
}
