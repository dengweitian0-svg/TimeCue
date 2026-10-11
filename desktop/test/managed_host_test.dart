import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/desktop_lifecycle.dart';
import 'package:timecue_desktop/app/managed_backend.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/app/windows_notification_bridge.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/contracts/notification_delivery.dart';
import 'package:timecue_desktop/features/room/room_screen.dart';
import 'desktop_lifecycle_test.dart' show FakeDesktop;
import 'managed_backend_test.dart' show FakeProcess, runtime;
import 'windows_notification_bridge_test.dart'
    show Gateway, TestPlatform, claim;

class CountingRepository extends DemoNotebookRepository {
  int reads = 0;
  @override
  Future<void> refresh({DateTime? month, DateTime? date}) async {
    reads++;
    await super.refresh(month: month, date: date);
  }
}

void main() {
  test('Go 退出后隔离在途平台结果，不向其他运行补交旧 attempt', () async {
    final gateway = Gateway()..queue = [claim('old')];
    final platform = TestPlatform();
    final shown = Completer<void>();
    final result = Completer<DeliveryOutcome?>();
    platform.onShow = () {
      shown.complete();
      return result.future;
    };
    final bridge = WindowsNotificationBridge(
      gateway: gateway,
      platform: platform,
    );
    addTearDown(bridge.dispose);
    await bridge.initialize(polling: false);
    final pending = bridge.runOnce();
    await shown.future;
    bridge.invalidateRuntime();
    result.complete(DeliveryOutcome.sent);
    await pending;
    expect(gateway.results, isEmpty);
    expect(platform.shown.length, 1);
    await bridge.runOnce();
    expect(platform.shown.length, 1);
  });
  testWidgets('双重握手前不挂载房间或初始化执行者，就绪后仅初始化一次', (tester) async {
    final process = FakeProcess()..autoReady = false;
    final repository = CountingRepository();
    final gateway = Gateway();
    final platform = TestPlatform();
    final bridge = WindowsNotificationBridge(
      gateway: gateway,
      platform: platform,
    );
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    backend.addListener(() {
      if (backend.ready) gateway.instance = backend.instanceId!;
    });
    final host = DesktopLifecycle(FakeDesktop());
    await tester.pumpWidget(
      TimeCueApp(
        repository: repository,
        notifications: bridge,
        backend: backend,
        desktop: host,
        ownsRepository: true,
      ),
    );
    await tester.pump();
    expect(repository.reads, 0);
    expect(platform.initializes, 0);
    expect(gateway.claims, 0);
    expect(find.byType(RoomScreen), findsNothing);
    process.ready();
    await tester.pump();
    await tester.pump();
    expect(find.byType(RoomScreen), findsOneWidget);
    expect(repository.reads, 1);
    expect(platform.initializes, 1);
    await host.hideWindow();
    await host.openWindow();
    await tester.pump();
    expect(platform.initializes, 1);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('登录隐藏意图下启动失败仍显示错误、重试和退出入口', (tester) async {
    final process = FakeProcess()..readyOverrides = {'pid': 88};
    final repository = CountingRepository();
    final platform = FakeDesktop()
      ..launches = [
        const DesktopLaunchRequest(id: 'login', login: true, startHidden: true),
      ];
    final host = DesktopLifecycle(platform);
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    await tester.pumpWidget(
      TimeCueApp(
        repository: repository,
        backend: backend,
        desktop: host,
        ownsRepository: true,
      ),
    );
    await tester.pump();
    await tester.pump();
    expect(repository.reads, 0);
    expect(find.text('重试启动'), findsOneWidget);
    expect(find.text('退出 TimeCue'), findsOneWidget);
    expect(host.state, DesktopState.visible);
    process.readyOverrides = null;
    await tester.tap(find.text('重试启动'));
    await tester.pump();
    await tester.pump();
    expect(find.byType(RoomScreen), findsOneWidget);
    expect(process.starts, 2);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  test('托管桥拒绝其他身份，不能接管已有端口服务', () async {
    final gateway = Gateway();
    final platform = TestPlatform();
    final bridge = WindowsNotificationBridge(
      gateway: gateway,
      platform: platform,
    )..expectedInstanceId = 'owned';
    addTearDown(bridge.dispose);
    await bridge.initialize(polling: false);
    await bridge.runOnce();
    expect(gateway.claims, 0);
    expect(bridge.instanceId, isNull);
    expect(bridge.error, contains('自有'));
  });
}
