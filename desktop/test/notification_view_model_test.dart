import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/contracts/notification_delivery.dart';
import 'package:timecue_desktop/contracts/notification_query.dart';
import 'package:timecue_desktop/features/notebook/notification_view_model.dart';

String activation({
  String instance = 'go-1',
  String task = 'task-1',
  String attempt = 'attempt-1',
  String type = 'minute',
}) => jsonEncode({
  'instanceId': instance,
  'deliveryTaskId': task,
  'attemptId': attempt,
  'taskType': type,
});
TriggerSummary member(
  String id, {
  String delivery = 'sent',
  DateTime? acknowledged,
}) => TriggerSummary(
  id: id,
  notificationId: id,
  scheduleId: '1',
  name: '成员 $id',
  description: '完整历史内容 $id',
  plannedAt: DateTime.utc(2026, 10, 8, 9),
  delivery: delivery,
  acknowledgedAt: acknowledged,
  editable: false,
);
NotificationMemberPage memberPage(
  List<TriggerSummary> entries, {
  String next = '',
  int? total,
  RecoveryProgress? recovery,
}) => NotificationMemberPage(
  items: entries,
  total: total ?? entries.length,
  nextCursor: next,
  asOf: DateTime.utc(2026, 10, 8, 14),
  recovery: recovery,
);
RecoveryProgress recovery({
  String state = 'running',
  int? total,
  String task = 'task-1',
}) => RecoveryProgress(
  recoveryId: 'round-$task',
  state: state,
  through: DateTime.utc(2026, 10, 8, 14),
  materializedCount: 500,
  missedTotal: total,
  displayableCount: 480,
  deliveryTaskId: task,
);

class QueryFixture implements NotificationQueryGateway {
  String instance = 'go-1';
  int healthReads = 0;
  final reads = <(String, String?, String)>[];
  final details = <String>[];
  FutureOr<NotificationMemberPage> Function(String, String?, String)? onMembers;
  FutureOr<TriggerSummary> Function(String)? onDetails;
  @override
  Future<DeliveryRuntime> deliveryRuntime() async {
    ++healthReads;
    return DeliveryRuntime(instance);
  }

  @override
  Future<NotificationMemberPage> notificationMembers(
    String task, {
    String? attemptId,
    String cursor = '',
  }) async {
    reads.add((task, attemptId, cursor));
    return await (onMembers?.call(task, attemptId, cursor) ??
        memberPage([member('1')]));
  }

  @override
  Future<TriggerSummary> notificationDetails(String id) async {
    details.add(id);
    return await (onDetails?.call(id) ?? member(id));
  }
}

void main() {
  late QueryFixture gateway;
  late NotificationViewModel model;
  setUp(() {
    gateway = QueryFixture();
    model = NotificationViewModel(gateway);
  });
  tearDown(() => model.dispose());

  test('单条点击读取原 attempt 和持久详情，重复点击不重读', () async {
    await model.activate(activation());
    expect(gateway.reads, [('task-1', 'attempt-1', '')]);
    expect(gateway.details, ['1']);
    expect(model.singleMinute, isTrue);
    expect(model.detail!.description, '完整历史内容 1');
    await model.activate(activation());
    expect(gateway.reads.length, 1);
  });
  test('未知/不完整 payload 不查询，也不拼造成员', () async {
    for (final value in [
      '{',
      '{}',
      activation(type: 'unknown'),
      activation(instance: ''),
    ]) {
      await model.activate(value);
      expect(model.error, contains('无法定位'));
      expect(gateway.healthReads, 0);
      expect(gateway.reads, isEmpty);
    }
  });
  test('旧运行拒绝定位，不退化为待确认或当前 task 查询', () async {
    gateway.instance = 'go-2';
    await model.activate(activation());
    expect(model.expired, isTrue);
    expect(gateway.reads, isEmpty);
  });
  test('本次恢复通知与任务历史分开查询，51 条冻结页保留旧 attempt', () async {
    gateway.onMembers = (_, attempt, cursor) => memberPage(
      cursor.isEmpty
          ? [for (var i = 1; i <= 50; i++) member('$i')]
          : [member('51')],
      total: 51,
      next: cursor.isEmpty ? 'next' : '',
      recovery: recovery(),
    );
    await model.activate(activation(type: 'recovery'));
    expect(model.showsDetail, isFalse);
    expect(model.progress, isNull);
    await model.nextPage();
    expect(model.members!.items.single.id, '51');
    expect(model.pageIndex, 1);
    await model.previousPage();
    expect(model.pageIndex, 0);
    expect(gateway.reads.every((read) => read.$2 == 'attempt-1'), isTrue);
    await model.openRecovery(recovery());
    expect(gateway.reads.last.$2, isNull);
    expect(model.progress!.missedTotal, isNull);
  });
  test('成员版本 409 只重读一次首页，不丢原身份', () async {
    var changed = false;
    gateway.onMembers = (_, _, cursor) {
      if (cursor.isNotEmpty) {
        changed = true;
        throw const NotebookApiException('members_changed', '变化', status: 409);
      }
      return memberPage(
        [member(changed ? '0' : '1')],
        total: 51,
        next: 'next',
        recovery: recovery(),
      );
    };
    await model.openRecovery(recovery());
    await model.nextPage();
    expect(gateway.reads, [
      ('task-1', null, ''),
      ('task-1', null, 'next'),
      ('task-1', null, ''),
    ]);
    expect(model.pageIndex, 0);
    expect(model.members!.items.single.id, '0');
    expect(model.notice, isNotNull);
  });
  test('attempt 首页 409 保留原 attempt 手动重试，404 明确失效', () async {
    gateway.onMembers = (_, _, _) =>
        throw const NotebookApiException('not_started', '未开始', status: 409);
    await model.activate(activation());
    expect(model.error, contains('该次展示成员'));
    await model.retry();
    expect(gateway.reads.every((read) => read.$2 == 'attempt-1'), isTrue);
    gateway.onMembers = (_, _, _) =>
        throw const NotebookApiException('not_found', '失效', status: 404);
    await model.retry();
    expect(model.expired, isTrue);
  });
  test('下一页断线保留已读页，重试同一游标', () async {
    var fail = true;
    gateway.onMembers = (_, _, cursor) {
      if (cursor.isNotEmpty && fail) throw StateError('断线');
      return memberPage(
        [member(cursor.isEmpty ? '1' : '51')],
        total: 51,
        next: cursor.isEmpty ? 'next' : '',
      );
    };
    await model.activate(activation(type: 'recovery'));
    await model.nextPage();
    expect(model.members!.items.single.id, '1');
    fail = false;
    await model.retry();
    expect(gateway.reads.last.$3, 'next');
    expect(model.pageIndex, 1);
    expect(model.error, isNull);
  });
  test('切换目标或关闭后，旧成员请求不覆盖新轮次', () async {
    final old = Completer<NotificationMemberPage>();
    gateway.onMembers = (task, _, _) =>
        task == 'task-1' ? old.future : memberPage([member('2')]);
    final pending = model.activate(activation(type: 'recovery'));
    await Future<void>.delayed(Duration.zero);
    await model.activate(
      activation(task: 'task-2', attempt: 'attempt-2', type: 'recovery'),
    );
    old.complete(memberPage([member('1')]));
    await pending;
    expect(model.members!.items.single.id, '2');
    expect(model.target!.deliveryTaskId, 'task-2');
    model.close();
    expect(model.active, isFalse);
  });
  test('迟到详情不能覆盖新目标，返回列表使详情失效', () async {
    await model.activate(activation(type: 'recovery'));
    final old = Completer<TriggerSummary>();
    gateway.onDetails = (_) => old.future;
    final pending = model.readDetail(member('1'));
    model.backToMembers();
    old.complete(member('99'));
    await pending;
    expect(model.detail, isNull);
    expect(model.detailLoading, isFalse);
  });
  test('运行失效后仅可用已知持久 ID 更新详情，不重建 task', () async {
    await model.activate(activation());
    model.observeRuntime('go-2');
    gateway.onDetails = (id) => member(
      id,
      delivery: 'failed',
      acknowledged: DateTime.utc(2026, 10, 8, 15),
    );
    await model.readDetail(model.detail!);
    model.observeRuntime('go-2');
    expect(model.detail!.delivery, 'failed');
    expect(model.detail!.acknowledgedAt, isNotNull);
    await model.refresh();
    expect(gateway.reads.length, 1);
    expect(model.expired, isTrue);
  });
  test('失效续页清理游标后禁用翻页，重试从第一页恢复', () async {
    var invalid = false;
    gateway.onMembers = (_, _, cursor) {
      if (invalid && cursor.isNotEmpty) {
        throw const NotebookApiException('invalid_request', '旧游标', status: 400);
      }
      return memberPage(
        [member(cursor.isEmpty ? '1' : '51')],
        total: 151,
        next: 'next',
      );
    };
    await model.activate(activation(type: 'recovery'));
    await model.nextPage();
    invalid = true;
    await model.nextPage();
    expect(model.members!.items.single.id, '51');
    expect(model.canPrevious, isFalse);
    expect(model.hasNext, isFalse);
    await model.previousPage();
    await model.retry();
    expect(model.pageIndex, 0);
    expect(model.members!.items.single.id, '1');
  });
  test('不拼接其他恢复轮次的摘要，关闭使在途成员请求失效', () async {
    gateway.onMembers = (_, _, _) =>
        memberPage([member('1')], recovery: recovery(task: 'other'));
    await model.openRecovery(recovery());
    expect(model.error, contains('恢复摘要与查询任务不一致'));
    expect(model.members, isNull);
    final pending = Completer<NotificationMemberPage>();
    gateway.onMembers = (_, _, _) => pending.future;
    final reading = model.refresh();
    await Future<void>.delayed(Duration.zero);
    model.close();
    pending.complete(memberPage([member('2')]));
    await reading;
    expect(model.members, isNull);
    expect(model.active, isFalse);
  });
}
