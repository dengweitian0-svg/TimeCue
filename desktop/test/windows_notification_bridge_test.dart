import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/app/windows_notification_bridge.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/contracts/notification_delivery.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';

DeliveryClaim claim(String id, {String type = 'minute'}) => DeliveryClaim(
  deliveryTaskId: 'task-$id',
  taskType: type,
  attemptId: id,
  platformId: 'platform-$id',
  notifyTime: DateTime.utc(2026, 10, 10, 1),
);

DeliveryStart decision(DeliveryClaim claim, {bool display = true}) =>
    DeliveryStart(
      claim: claim,
      shouldDisplay: display,
      memberCount: display ? 51 : 0,
      attemptNumber: display ? 1 : 0,
      title: display ? 'TimeCue' : null,
      body: display ? '有 51 项日程需要处理' : null,
    );

class Gateway implements NotificationDeliveryGateway {
  String instance = 'go-1';
  int claims = 0, healthReads = 0;
  List<DeliveryClaim> queue = [];
  final starts = <String>[];
  final results = <(String, DeliveryOutcome)>[];
  final exits = <(String, List<String>, Duration)>[];
  Future<DeliveryStart> Function(String)? onStart;
  Future<void> Function(String, DeliveryOutcome)? onResult;
  Future<void> Function(String, List<String>, Duration)? onExecutorExit;
  Future<DeliveryRuntime> Function()? onHealth;
  @override
  Future<DeliveryRuntime> deliveryRuntime() async {
    healthReads++;
    return onHealth == null ? DeliveryRuntime(instance) : await onHealth!();
  }

  @override
  Future<List<DeliveryClaim>> claimNotifications() async {
    claims++;
    return queue;
  }

  @override
  Future<DeliveryStart> startNotification(String id) async {
    starts.add(id);
    return onStart == null
        ? decision(queue.firstWhere((task) => task.attemptId == id))
        : await onStart!(id);
  }

  @override
  Future<void> reportNotification(String id, DeliveryOutcome outcome) async {
    results.add((id, outcome));
    await onResult?.call(id, outcome);
  }

  @override
  Future<void> reportExecutorExited(
    String instanceId,
    List<String> attemptIds, {
    required Duration timeout,
  }) async {
    exits.add((instanceId, List.of(attemptIds), timeout));
    await onExecutorExit?.call(instanceId, attemptIds, timeout);
  }
}

class TestPlatform implements NotificationPlatform {
  int initializes = 0;
  bool rejectInit = false;
  final shown = <(int, DeliveryStart, String)>[];
  Future<DeliveryOutcome?> Function()? onShow;
  @override
  Future<void> initialize() async {
    initializes++;
    if (rejectInit) throw StateError('初始化失败');
  }

  @override
  Future<DeliveryOutcome?> show({
    required int id,
    required DeliveryStart start,
    required String payload,
  }) async {
    shown.add((id, start, payload));
    return onShow == null ? DeliveryOutcome.sent : await onShow!();
  }

  @override
  void dispose() {}
}

void main() {
  late Gateway gateway;
  late TestPlatform platform;
  late WindowsNotificationBridge bridge;
  setUp(() async {
    gateway = Gateway();
    platform = TestPlatform();
    bridge = WindowsNotificationBridge(gateway: gateway, platform: platform);
    await bridge.initialize(polling: false);
  });
  tearDown(() => bridge.dispose());

  test('重复领取只展示一次，完整恢复任务文案和身份原样传递', () async {
    gateway.queue = [claim('one', type: 'recovery')];
    await bridge.runOnce();
    await bridge.runOnce();
    expect(gateway.starts, ['one']);
    expect(platform.shown, hasLength(1));
    expect(platform.shown.single.$2.body, '有 51 项日程需要处理');
    expect(jsonDecode(platform.shown.single.$3), {
      'instanceId': 'go-1',
      'deliveryTaskId': 'task-one',
      'attemptId': 'one',
      'taskType': 'recovery',
    });
    expect(gateway.results, [('one', DeliveryOutcome.sent)]);
  });

  test('start 已提交但响应丢失，队列不再返回也重传原身份', () async {
    final task = claim('one');
    gateway.queue = [task];
    gateway.onStart = (_) async {
      if (gateway.starts.length == 1) {
        gateway.queue = [];
        throw StateError('连接断开');
      }
      return decision(task);
    };
    await bridge.runOnce();
    await bridge.runOnce();
    expect(gateway.starts, ['one', 'one']);
    expect(platform.shown, hasLength(1));
    expect(gateway.results, [('one', DeliveryOutcome.sent)]);
  });

  test('回填丢响应只重传 outcome，失败的一项不丢同批其它任务', () async {
    gateway.queue = [claim('one'), claim('two')];
    gateway.onResult = (id, _) async {
      if (id == 'one' &&
          gateway.results.where((item) => item.$1 == 'one').length == 1) {
        throw TimeoutException('响应丢失');
      }
    };
    await bridge.runOnce();
    expect(platform.shown, hasLength(2));
    expect(bridge.pendingResults, 1);
    await bridge.runOnce();
    expect(platform.shown, hasLength(2));
    expect(gateway.results.where((item) => item.$1 == 'one'), [
      ('one', DeliveryOutcome.sent),
      ('one', DeliveryOutcome.sent),
    ]);
    expect(bridge.pendingResults, 0);
  });

  test('全部失效不展示不回填，明确失败只有新 attempt 才再展示', () async {
    gateway.queue = [claim('zero'), claim('one')];
    gateway.onStart = (id) async => decision(
      gateway.queue.firstWhere((item) => item.attemptId == id),
      display: id != 'zero',
    );
    platform.onShow = () async => DeliveryOutcome.failed;
    await bridge.runOnce();
    await bridge.runOnce();
    expect(platform.shown, hasLength(1));
    expect(gateway.results, [('one', DeliveryOutcome.failed)]);
    gateway.queue = [claim('retry')];
    await bridge.runOnce();
    expect(platform.shown, hasLength(2));
    expect(platform.shown.first.$1, isNot(platform.shown.last.$1));
  });

  test('平台结果未知与回填冲突均不追加展示', () async {
    gateway.queue = [claim('unknown'), claim('conflict')];
    var count = 0;
    platform.onShow = () async => ++count == 1 ? null : DeliveryOutcome.sent;
    gateway.onResult = (_, _) async => throw const NotebookApiException(
      'delivery_conflict',
      '冲突',
      status: 409,
    );
    await bridge.runOnce();
    await bridge.runOnce();
    expect(platform.shown, hasLength(2));
    expect(gateway.results, [('conflict', DeliveryOutcome.sent)]);
    expect(bridge.error, isNotNull);
  });

  test('没有断线的 Go 换进程也换代次并保留递增平台 ID', () async {
    gateway.queue = [claim('old')];
    await bridge.runOnce();
    gateway.instance = 'go-2';
    gateway.queue = [claim('new')];
    await bridge.runOnce();
    expect(platform.shown, hasLength(1));
    expect(bridge.instanceId, 'go-2');
    await bridge.runOnce();
    expect(platform.shown, hasLength(2));
    expect(platform.shown.last.$1, greaterThan(platform.shown.first.$1));
    expect(jsonDecode(platform.shown.last.$3)['instanceId'], 'go-2');
  });

  test('回填断线后换进程，原结果不移交新尝试', () async {
    gateway.queue = [claim('old')];
    gateway.onResult = (_, _) async {
      throw TimeoutException('丢响应');
    };
    await bridge.runOnce();
    gateway.instance = 'go-2';
    gateway.queue = [claim('new')];
    gateway.onResult = null;
    await bridge.runOnce();
    await bridge.runOnce();
    expect(gateway.results, [
      ('old', DeliveryOutcome.sent),
      ('new', DeliveryOutcome.sent),
    ]);
    expect(bridge.pendingResults, 0);
  });

  test('同运行 start 404 退役一项但其它任务可展示', () async {
    gateway.queue = [claim('retired'), claim('valid')];
    gateway.onStart = (id) async {
      if (id == 'retired') {
        throw const NotebookApiException(
          'delivery_attempt_not_found',
          '已转汇总',
          status: 404,
        );
      }
      return decision(gateway.queue.last);
    };
    await bridge.runOnce();
    await bridge.runOnce();
    expect(gateway.starts, ['retired', 'valid']);
    expect(platform.shown, hasLength(1));
    expect(bridge.instanceId, 'go-1');
  });

  test('重入共用在途循环，停止期间迟到 start 不展示', () async {
    gateway.queue = [claim('one')];
    final arrived = Completer<void>(), release = Completer<DeliveryStart>();
    gateway.onStart = (_) {
      arrived.complete();
      return release.future;
    };
    final first = bridge.runOnce();
    await arrived.future;
    final second = bridge.runOnce();
    final draining = bridge.stopAndDrain(
      const Duration(milliseconds: 600),
      reportExecutorExit: true,
    );
    await draining;
    expect(gateway.exits.single.$2, ['one']);
    release.complete(decision(gateway.queue.single));
    await Future.wait([first, second]);
    expect(gateway.starts, ['one']);
    expect(platform.shown, isEmpty);
  });

  test('停止等待已发起平台结果并回填，随后不领取', () async {
    gateway.queue = [claim('one')];
    final arrived = Completer<void>(), release = Completer<DeliveryOutcome?>();
    platform.onShow = () {
      arrived.complete();
      return release.future;
    };
    final cycle = bridge.runOnce();
    await arrived.future;
    final draining = bridge.stopAndDrain(const Duration(seconds: 1));
    release.complete(DeliveryOutcome.sent);
    await Future.wait([cycle, draining]);
    await bridge.runOnce();
    expect(gateway.claims, 1);
    expect(gateway.results, [('one', DeliveryOutcome.sent)]);
    expect(gateway.exits, isEmpty);
  });

  test('外连 drain 超时后在同一预算内报告活动 attempt，迟到 sent 仍回填', () async {
    gateway.queue = [claim('one')];
    final shown = Completer<void>(), release = Completer<DeliveryOutcome?>();
    platform.onShow = () {
      shown.complete();
      return release.future;
    };
    final cycle = bridge.runOnce();
    await shown.future;

    await bridge.stopAndDrain(
      const Duration(milliseconds: 600),
      reportExecutorExit: true,
    );
    expect(gateway.exits, hasLength(1));
    expect(gateway.exits.single.$1, 'go-1');
    expect(gateway.exits.single.$2, ['one']);
    expect(
      gateway.exits.single.$3,
      lessThanOrEqualTo(const Duration(milliseconds: 500)),
    );
    expect(gateway.exits.single.$3, greaterThan(Duration.zero));
    expect(gateway.results, isEmpty);

    release.complete(DeliveryOutcome.sent);
    await cycle;
    expect(gateway.results, [('one', DeliveryOutcome.sent)]);
  });

  test('结果已知但 HTTP 回填挂起时退出通知后仍重传原 sent', () async {
    gateway.queue = [claim('one')];
    final reporting = Completer<void>(), release = Completer<void>();
    gateway.onResult = (_, _) {
      reporting.complete();
      return release.future;
    };
    final cycle = bridge.runOnce();
    await reporting.future;

    await bridge.stopAndDrain(
      const Duration(milliseconds: 600),
      reportExecutorExit: true,
    );
    expect(gateway.exits.single.$2, ['one']);
    expect(gateway.results, [('one', DeliveryOutcome.sent)]);

    release.complete();
    await cycle;
    expect(gateway.results, [('one', DeliveryOutcome.sent)]);
    expect(platform.shown, hasLength(1));
  });

  test('释放后迟到 start 不展示、不回填、不发布状态', () async {
    gateway.queue = [claim('one')];
    final arrived = Completer<void>(), release = Completer<DeliveryStart>();
    gateway.onStart = (_) {
      arrived.complete();
      return release.future;
    };
    final cycle = bridge.runOnce();
    await arrived.future;
    bridge.dispose();
    release.complete(decision(gateway.queue.single));
    await cycle;
    expect(platform.shown, isEmpty);
    expect(gateway.results, isEmpty);
    // 本测试已释放，不重复释放 ChangeNotifier。
    bridge = WindowsNotificationBridge(
      gateway: gateway,
      platform: TestPlatform(),
    );
  });

  test('初始化失败不领取，显式重试可恢复', () async {
    bridge.dispose();
    platform.rejectInit = true;
    bridge = WindowsNotificationBridge(gateway: gateway, platform: platform);
    await bridge.initialize(polling: false);
    await bridge.runOnce();
    expect(gateway.claims, 0);
    expect(bridge.error, isNotNull);
    platform.rejectInit = false;
    await bridge.initialize(polling: false);
    await bridge.runOnce();
    expect(gateway.claims, 1);
    expect(bridge.error, isNull);
  });

  test('空领取不增加周期健康请求', () async {
    await bridge.runOnce();
    await bridge.runOnce();
    await bridge.runOnce();
    expect(gateway.healthReads, 1);
    expect(gateway.claims, 3);
  });

  test('结果 404 在同运行内退役，保留真实 outcome 不再重传', () async {
    gateway.queue = [claim('one')];
    gateway.onResult = (_, _) async => throw const NotebookApiException(
      'delivery_attempt_not_found',
      '尝试失效',
      status: 404,
    );
    await bridge.runOnce();
    await bridge.runOnce();
    expect(platform.shown, hasLength(1));
    expect(gateway.results, [('one', DeliveryOutcome.sent)]);
    expect(bridge.instanceId, 'go-1');
  });

  test('健康失败暂停平台调用，恢复后原 start 可继续', () async {
    gateway.queue = [claim('one')];
    gateway.onHealth = () async => throw const NotebookApiException(
      'database_unavailable',
      '数据库不可用',
      status: 503,
    );
    await bridge.runOnce();
    expect(gateway.claims, 0);
    expect(platform.shown, isEmpty);
    gateway.onHealth = null;
    await bridge.runOnce();
    expect(platform.shown, hasLength(1));
    expect(bridge.error, isNull);
  });

  test('停止接纳 503 停止领取，不能当作平台失败', () async {
    gateway.queue = [claim('one')];
    gateway.onStart = (_) async => throw const NotebookApiException(
      'delivery_stopping',
      '停止接纳',
      status: 503,
    );
    await bridge.runOnce();
    await bridge.runOnce();
    expect(gateway.claims, 1);
    expect(platform.shown, isEmpty);
    expect(gateway.results, isEmpty);
  });

  test('开始响应身份不一致时不展示且不重试该项', () async {
    gateway.queue = [claim('one')];
    gateway.onStart = (_) async => decision(claim('other'));
    await bridge.runOnce();
    await bridge.runOnce();
    expect(gateway.starts, ['one']);
    expect(platform.shown, isEmpty);
  });

  testWidgets('宿主显示初始化错误及重试入口，释放后取消通知轮询', (tester) async {
    final repository = DemoNotebookRepository();
    platform.rejectInit = true;
    bridge.dispose();
    bridge = WindowsNotificationBridge(gateway: gateway, platform: platform);
    await tester.pumpWidget(
      TimeCueApp(repository: repository, notifications: bridge),
    );
    await tester.pump();
    expect(find.text('重试连接'), findsOneWidget);
    expect(gateway.claims, 0);
    platform.rejectInit = false;
    await tester.tap(find.text('重试连接'));
    await tester.pump();
    expect(find.text('重试连接'), findsNothing);
    expect(gateway.claims, 1);
    await tester.pumpWidget(const SizedBox());
    repository.dispose();
    bridge = WindowsNotificationBridge(
      gateway: gateway,
      platform: TestPlatform(),
    );
  });
}
