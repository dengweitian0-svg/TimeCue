import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/desktop_lifecycle.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notification_view_model.dart';
import 'package:timecue_desktop/features/notebook/notification_reader_dialog.dart';
import 'notification_view_model_test.dart' as fixture;

class FakeDesktop implements DesktopPlatform {
  final calls = <String>[];
  bool available = true;
  Object? trayFailure, hideFailure, showFailure;
  Completer<void>? initializing, hiding, creating;
  List<DesktopLaunchRequest> launches = [];
  Completer<List<DesktopLaunchRequest>>? readyResponse;
  @override
  Future<void> initialize(DesktopLifecycle lifecycle) async {
    calls.add('initialize');
    await initializing?.future;
  }

  @override
  Future<List<DesktopLaunchRequest>> ready() async {
    calls.add('ready');
    return await readyResponse?.future ?? launches;
  }

  @override
  Future<void> createTray() async {
    calls.add('tray');
    await creating?.future;
    if (trayFailure != null) throw trayFailure!;
  }

  @override
  Future<bool> hasTray() async => available;
  @override
  Future<void> show() async {
    calls.add('show');
    if (showFailure != null) throw showFailure!;
  }

  @override
  Future<void> hide() async {
    calls.add('hide');
    await hiding?.future;
    if (hideFailure != null) throw hideFailure!;
  }

  @override
  Future<void> beginExit() async {
    calls.add('beginExit');
  }

  @override
  Future<void> removeTray() async {
    calls.add('removeTray');
  }

  @override
  Future<void> destroy() async {
    calls.add('destroy');
  }

  @override
  void detach() {
    calls.add('detach');
  }
}

void main() {
  test('关闭隐藏不停止通知或释放资源，打开复用状态', () async {
    final platform = FakeDesktop();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    host.stopNewWork = () => platform.calls.add('stop');
    host.releaseResources = () => platform.calls.add('release');
    await host.initialize();
    await host.hideWindow();
    expect(host.state, DesktopState.hidden);
    expect(platform.calls, isNot(contains('stop')));
    expect(platform.calls, isNot(contains('release')));
    await host.openWindow();
    expect(host.state, DesktopState.visible);
    expect(platform.calls.where((call) => call == 'initialize').length, 1);
  });

  test('插件注册未被系统接受时保持窗口并可重试', () async {
    final platform = FakeDesktop()..available = false;
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    await host.hideWindow();
    expect(host.state, DesktopState.visible);
    expect(host.trayReady, isFalse);
    expect(host.error, contains('托盘'));
    expect(platform.calls, isNot(contains('hide')));
    platform.available = true;
    await host.retryTray();
    expect(host.trayReady, isTrue);
    expect(host.error, isNull);
  });

  test('隐藏前托盘丢失与 Explorer 恢复不重建宿主', () async {
    final platform = FakeDesktop();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    platform.available = false;
    await host.hideWindow();
    expect(host.state, DesktopState.visible);
    platform.available = true;
    await host.trayLost();
    expect(host.trayReady, isTrue);
    expect(platform.calls.where((call) => call == 'initialize').length, 1);
  });

  test('hide 结果未知不能报告隐藏成功', () async {
    final platform = FakeDesktop()..hideFailure = StateError('未知结果');
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    await host.hideWindow();
    expect(host.state, DesktopState.visible);
    expect(host.error, contains('窗口操作失败'));
    expect(platform.calls.last, 'show');
  });

  test('窗口显示失败后的成功唤回清除旧提示', () async {
    final platform = FakeDesktop()..showFailure = StateError('显示失败');
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    expect(host.error, contains('窗口操作失败'));
    expect(host.trayReady, isTrue);
    platform.showFailure = null;
    await host.openWindow();
    expect(host.state, DesktopState.visible);
    expect(host.error, isNull);
  });

  test('成功显示不能清除托盘不可用错误', () async {
    final platform = FakeDesktop()..trayFailure = StateError('托盘失败');
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    await host.openWindow();
    expect(host.state, DesktopState.visible);
    expect(host.error, contains('托盘不可用'));
    expect(host.trayReady, isFalse);
  });
  test('关闭与唤回竞争以最新意图收敛', () async {
    final platform = FakeDesktop()..hiding = Completer<void>();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    final hide = host.hideWindow();
    await Future<void>.delayed(Duration.zero);
    final open = host.openWindow();
    platform.hiding!.complete();
    await Future.wait([hide, open]);
    expect(host.state, DesktopState.visible);
    expect(platform.calls.last, 'show');
  });

  test('就绪前保存最新通知，普通打开不清空目标', () async {
    final platform = FakeDesktop()..initializing = Completer<void>();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    final activations = <String>[];
    host.onActivate = (payload) async => activations.add(payload);
    final startup = host.initialize();
    await host.accept(const DesktopLaunchRequest(id: '1', payload: '旧目标'));
    await host.accept(const DesktopLaunchRequest(id: '2', payload: '中文 "目标"'));
    await host.accept(const DesktopLaunchRequest(id: '3'));
    platform.initializing!.complete();
    await startup;
    expect(activations, ['中文 "目标"']);
    expect(host.state, DesktopState.visible);
    await host.accept(const DesktopLaunchRequest(id: '2', payload: '中文 "目标"'));
    expect(activations, hasLength(1));
  });

  test('ready 旧回应不能覆盖等待期间的新激活', () async {
    final platform = FakeDesktop()
      ..readyResponse = Completer<List<DesktopLaunchRequest>>();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    final activations = <String>[];
    host.onActivate = (payload) async => activations.add(payload);
    final startup = host.initialize();
    await Future<void>.delayed(Duration.zero);
    await host.activate('新回调');
    platform.readyResponse!.complete([
      const DesktopLaunchRequest(id: 'old', payload: '旧启动'),
    ]);
    await startup;
    expect(activations, ['新回调']);
  });
  test('初次登录隐藏，重复登录不隐藏已有可见窗口', () async {
    final platform = FakeDesktop()
      ..launches = [const DesktopLaunchRequest(id: 'initial', login: true)];
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    expect(host.state, DesktopState.hidden);
    await host.openWindow();
    await host.accept(
      const DesktopLaunchRequest(id: 'login-again', login: true),
    );
    expect(host.state, DesktopState.visible);
  });

  test('通知 callback 唤回后才调用 D6 目标查询', () async {
    final platform = FakeDesktop();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    await host.hideWindow();
    host.onActivate = (payload) async {
      expect(host.state, DesktopState.visible);
      platform.calls.add('activate:$payload');
    };
    await host.activate('原 payload');
    expect(platform.calls.takeLast(2), ['show', 'activate:原 payload']);
  });

  test('重复明确退出共用 Future，退出后激活/关闭不产生工作', () async {
    final platform = FakeDesktop();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    platform.calls.clear();
    final draining = Completer<void>();
    host.stopNewWork = () => platform.calls.add('stop');
    host.drain = (_) async {
      platform.calls.add('drain');
      await draining.future;
    };
    host.stopOwnedProcess = (_) async => platform.calls.add('owned');
    host.releaseResources = () => platform.calls.add('release');
    host.onActivate = (_) async => platform.calls.add('activate');
    final first = host.requestExit();
    final second = host.requestExit();
    expect(identical(first, second), isTrue);
    await host.accept(const DesktopLaunchRequest(id: 'late', payload: 'late'));
    await host.hideWindow();
    draining.complete();
    await first;
    expect(platform.calls, [
      'stop',
      'beginExit',
      'drain',
      'owned',
      'release',
      'detach',
      'removeTray',
      'destroy',
    ]);
  });

  test('退出监听重入不会启动第二次清理', () async {
    final platform = FakeDesktop();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    await host.initialize();
    var reentered = false;
    host.addListener(() {
      if (host.stopping && !reentered) {
        reentered = true;
        unawaited(host.requestExit());
      }
    });
    await host.requestExit();
    expect(platform.calls.where((call) => call == 'destroy').length, 1);
  });

  testWidgets('清理超时不会另开一个停止预算或伪造结果', (tester) async {
    final platform = FakeDesktop();
    var spent = Duration.zero;
    final host = DesktopLifecycle(
      platform,
      exitBudget: const Duration(seconds: 5),
      elapsed: () => spent,
    );
    await host.initialize();
    host.drain = (_) => Completer<void>().future;
    host.stopOwnedProcess = (_) async => platform.calls.add('owned');
    var completed = false;
    unawaited(host.requestExit().then((_) => completed = true));
    await tester.pump();
    spent = const Duration(seconds: 5);
    await tester.pump(const Duration(seconds: 5));
    expect(completed, isTrue);
    expect(platform.calls, isNot(contains('owned')));
    expect(platform.calls.last, 'destroy');
    host.dispose();
  });

  test('退出期间未完成托盘初始化不发布 ready 或隐藏', () async {
    final platform = FakeDesktop()..creating = Completer<void>();
    final host = DesktopLifecycle(platform);
    addTearDown(host.dispose);
    final startup = host.initialize();
    await Future<void>.delayed(Duration.zero);
    final exiting = host.requestExit();
    platform.creating!.complete();
    await Future.wait([startup, exiting]);
    expect(host.trayReady, isFalse);
    expect(host.state, DesktopState.exiting);
    expect(platform.calls, isNot(contains('ready')));
    expect(platform.calls, isNot(contains('hide')));
  });

  test('就绪参数保留 Unicode 和 JSON，拒绝连接切换及协议错误', () {
    final payload = '{"名称":"中文 空格 \\"引号\\""}';
    final value = {
      'version': 1,
      'id': '1',
      'source': 'notification',
      'arguments': ['--activation-payload=$payload'],
    };
    expect(DesktopLaunchRequest.fromMap(value).payload, payload);
    expect(
      () => DesktopLaunchRequest.fromMap({...value, 'version': 2}),
      throwsFormatException,
    );
    expect(
      () => DesktopLaunchRequest.fromMap({...value, 'source': 'open'}),
      throwsFormatException,
    );
    expect(
      () => DesktopLaunchRequest.fromMap({
        ...value,
        'arguments': ['--data-dir=其他目录'],
      }),
      throwsFormatException,
    );
  });

  testWidgets('桌面激活唤回后打开 D6 冻结成员，重复目标复用弹窗', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final platform = FakeDesktop();
    final host = DesktopLifecycle(platform);
    final gateway = fixture.QueryFixture();
    final model = NotificationViewModel(gateway);
    final repository = DemoNotebookRepository();
    host.onActivate = model.activate;
    await tester.pumpWidget(
      TimeCueApp(
        repository: repository,
        desktop: host,
        notificationViews: model,
      ),
    );
    await tester.pumpAndSettle();
    await host.hideWindow();
    await host.activate(fixture.activation());
    await tester.pumpAndSettle();
    expect(host.state, DesktopState.visible);
    expect(find.byType(NotificationReaderDialog), findsOneWidget);
    expect(gateway.reads.single.$2, 'attempt-1');
    await host.activate(fixture.activation());
    await tester.pumpAndSettle();
    expect(find.byType(NotificationReaderDialog), findsOneWidget);
    expect(gateway.reads, hasLength(1));
    await tester.pumpWidget(const SizedBox.shrink());
    repository.dispose();
  });
  testWidgets('窗口错误提供重试窗口，成功后移除提示而不重建托盘', (tester) async {
    final platform = FakeDesktop()..showFailure = StateError('显示失败');
    final host = DesktopLifecycle(platform);
    await tester.pumpWidget(
      TimeCueApp(repository: DemoNotebookRepository(), desktop: host),
    );
    await tester.pumpAndSettle();
    expect(find.text('重试窗口'), findsOneWidget);
    expect(find.text('重试托盘'), findsNothing);
    platform.showFailure = null;
    await tester.tap(find.text('重试窗口'));
    await tester.pumpAndSettle();
    expect(host.error, isNull);
    expect(find.text('重试窗口'), findsNothing);
    expect(platform.calls.where((call) => call == 'tray'), hasLength(1));
    await tester.pumpWidget(const SizedBox.shrink());
  });
  testWidgets('托盘失败显示重试与明确退出，关闭不重建房间', (tester) async {
    final platform = FakeDesktop()..trayFailure = StateError('图标加载失败');
    final host = DesktopLifecycle(platform);
    await tester.pumpWidget(
      TimeCueApp(repository: DemoNotebookRepository(), desktop: host),
    );
    await tester.pumpAndSettle();
    expect(find.text('重试托盘'), findsOneWidget);
    expect(find.text('退出 TimeCue'), findsOneWidget);
    platform.trayFailure = null;
    await tester.tap(find.text('重试托盘'));
    await tester.pumpAndSettle();
    expect(find.text('退出 TimeCue'), findsNothing);
    expect(host.trayReady, isTrue);
    await host.hideWindow();
    await host.openWindow();
    expect(platform.calls.where((call) => call == 'initialize').length, 1);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}

extension on List<String> {
  Iterable<String> takeLast(int count) => skip(length - count);
}
