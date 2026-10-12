import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/managed_backend.dart';
import 'package:timecue_desktop/app/desktop_lifecycle.dart';
import 'package:timecue_desktop/contracts/notification_delivery.dart';
import 'desktop_lifecycle_test.dart' show FakeDesktop;

const capabilities = [
  'delivery-v1',
  'recovery-v1',
  'task-members-v1',
  'managed-lifecycle-v1',
];

class FakeProcess implements OwnedProcessPlatform {
  final controller = StreamController<OwnedProcessEvent>.broadcast(sync: true);
  int generation = 0, releases = 0, starts = 0, shutdowns = 0;
  String instance = '';
  bool living = true, autoReady = true, exitOnShutdown = true;
  Completer<int>? starting;
  Map<String, dynamic>? readyOverrides;
  @override
  Stream<OwnedProcessEvent> get events => controller.stream;
  Map<String, dynamic> get message => {
    'event': 'ready',
    'instanceId': instance,
    'pid': 77,
    'address': 'http://127.0.0.1:10086',
    'apiVersion': 'v1',
    'capabilities': capabilities,
    ...?readyOverrides,
  };
  void bytes(List<int> bytes) =>
      controller.add(OwnedProcessEvent(generation, 'stdout', bytes: bytes));
  void ready() => bytes(utf8.encode('${jsonEncode(message)}\n'));
  void exit([int code = 0]) {
    living = false;
    controller.add(OwnedProcessEvent(generation, 'exit', exitCode: code));
  }

  @override
  Future<int> start(int generation, String instanceId) async {
    this.generation = generation;
    instance = instanceId;
    starts++;
    living = true;
    if (autoReady) ready();
    return await starting?.future ?? 77;
  }

  @override
  Future<bool> alive() async => living;
  @override
  Future<void> shutdown() async {
    shutdowns++;
    if (exitOnShutdown) exit();
  }

  @override
  Future<void> release() async {
    releases++;
    living = false;
  }

  @override
  void dispose() {
    unawaited(controller.close());
  }
}

Future<void> flush() async {
  for (var i = 0; i < 5; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

DeliveryRuntime runtime(
  FakeProcess process, {
  String? id,
  int pid = 77,
  List<String> caps = capabilities,
}) => DeliveryRuntime(id ?? process.instance, pid: pid, capabilities: caps);

void main() {
  test('启动监听同步请求退出时不再创建 Go', () async {
    final process = FakeProcess();
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    addTearDown(backend.dispose);
    backend.addListener(() {
      if (backend.state == BackendState.starting) backend.cancelStart();
    });
    await backend.start();
    expect(process.starts, 0);
    expect(backend.ready, false);
  });
  test('健康失配反馈撤销整个托管运行，后续退出事件不重复失效', () async {
    final process = FakeProcess();
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    addTearDown(backend.dispose);
    var invalidations = 0;
    backend.onInvalidated = () => invalidations++;
    await backend.start();
    backend.invalidateRuntime('端口运行身份不匹配');
    process.exit(7);
    await flush();
    expect(backend.state, BackendState.failed);
    expect(backend.ready, false);
    expect(process.living, false);
    expect(invalidations, 1);
    expect(backend.canRetry, false);
  });
  test('ready 分片后才能读取健康，PID/身份/能力全部一致才开放', () async {
    final process = FakeProcess()..autoReady = false;
    var healths = 0;
    final backend = ManagedBackend(
      platform: process,
      health: () async {
        healths++;
        return runtime(process);
      },
    );
    addTearDown(backend.dispose);
    final pending = backend.start();
    await flush();
    expect(backend.ready, false);
    expect(healths, 0);
    final ready = utf8.encode('${jsonEncode(process.message)}\n');
    process.bytes(ready.sublist(0, 23));
    await flush();
    expect(healths, 0);
    process.bytes(ready.sublist(23));
    await pending;
    expect(backend.ready, true);
    expect(healths, 1);
    expect(backend.pid, 77);
    await backend.stopOwned(const Duration(seconds: 1));
    expect(process.shutdowns, 1);
    expect(process.living, false);
  });

  for (final entry in <String, Object>{
    'event': 'log',
    'instanceId': 'other',
    'pid': 88,
    'address': 'http://localhost:10086',
    'apiVersion': 'v2',
    'capabilities': ['delivery-v1'],
  }.entries) {
    test('ready 拒绝错配 ${entry.key}，回收自有进程', () async {
      final process = FakeProcess()..readyOverrides = {entry.key: entry.value};
      final backend = ManagedBackend(
        platform: process,
        health: () async => runtime(process),
      );
      addTearDown(backend.dispose);
      await backend.start();
      expect(backend.state, BackendState.failed);
      expect(process.releases, greaterThanOrEqualTo(1));
      expect(process.living, false);
    });
  }

  test('健康身份不匹配不接受已有端口服务', () async {
    final process = FakeProcess();
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process, id: 'manual-go'),
    );
    addTearDown(backend.dispose);
    await backend.start();
    expect(backend.ready, false);
    expect(backend.state, BackendState.failed);
  });
  test('ready 后立即退出不能假就绪', () async {
    final process = FakeProcess();
    final checked = Completer<DeliveryRuntime>();
    final backend = ManagedBackend(
      platform: process,
      health: () => checked.future,
    );
    addTearDown(backend.dispose);
    final pending = backend.start();
    await flush();
    process.exit(9);
    checked.complete(runtime(process));
    await pending;
    expect(backend.state, BackendState.failed);
    expect(backend.ready, false);
    expect(process.starts, 1);
  });
  test('启动中退出隔离迟到 ready 与健康', () async {
    final process = FakeProcess()..autoReady = false;
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    addTearDown(backend.dispose);
    final pending = backend.start();
    await flush();
    backend.cancelStart();
    await backend.releaseOwned();
    process.ready();
    await pending;
    expect(backend.ready, false);
    expect(process.living, false);
  });
  test('零预算退出仍回收 Job，drain 与停止使用同一截止时刻', () async {
    final process = FakeProcess();
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    final desktop = FakeDesktop();
    var elapsed = Duration.zero;
    final host = DesktopLifecycle(desktop, elapsed: () => elapsed);
    addTearDown(backend.dispose);
    addTearDown(host.dispose);
    await backend.start();
    host.stopNewWork = backend.cancelStart;
    host.drain = (_) async {
      elapsed = const Duration(seconds: 5);
    };
    host.stopOwnedProcess = backend.stopOwned;
    host.finalizeOwnedProcess = backend.releaseOwned;
    await host.requestExit();
    expect(process.shutdowns, 0);
    expect(process.living, false);
    expect(desktop.calls, contains('destroy'));
  });
  test('Go 不协作时只等待剩余预算，之后回收', () async {
    final process = FakeProcess()..exitOnShutdown = false;
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    addTearDown(backend.dispose);
    await backend.start();
    await backend.stopOwned(const Duration(milliseconds: 15));
    expect(process.shutdowns, 1);
    expect(process.living, false);
    expect(backend.state, BackendState.stopped);
  });
  test('运行中退出一次撤销资格，无自动重启或热重试', () async {
    final process = FakeProcess();
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    addTearDown(backend.dispose);
    var invalidations = 0;
    backend.onInvalidated = () => invalidations++;
    await backend.start();
    process.exit(7);
    await flush();
    expect(backend.state, BackendState.failed);
    expect(backend.canRetry, false);
    await backend.start();
    expect(process.starts, 1);
    expect(invalidations, 1);
  });
  test('失败后显式重试生成新身份并清除旧启动代次', () async {
    final process = FakeProcess()..readyOverrides = {'pid': 2};
    var id = 0;
    final backend = ManagedBackend(
      platform: process,
      identity: () => 'launch-${++id}',
      health: () async => runtime(process),
    );
    addTearDown(backend.dispose);
    await backend.start();
    expect(backend.canRetry, true);
    final oldGeneration = process.generation;
    process.readyOverrides = null;
    await backend.start();
    process.controller.add(
      OwnedProcessEvent(oldGeneration, 'exit', exitCode: 9),
    );
    expect(backend.ready, true);
    expect(backend.instanceId, 'launch-2');
  });
  test('启动预算耗尽不能再开始健康核验', () async {
    final process = FakeProcess()..autoReady = false;
    var elapsed = Duration.zero, healths = 0;
    final backend = ManagedBackend(
      platform: process,
      elapsed: () => elapsed,
      health: () async {
        healths++;
        return runtime(process);
      },
    );
    addTearDown(backend.dispose);
    final pending = backend.start();
    await flush();
    elapsed = const Duration(seconds: 10);
    process.ready();
    await pending;
    expect(backend.state, BackendState.failed);
    expect(healths, 0);
  });
  for (final bytes in [
    utf8.encode('not-json\n'),
    [255, 10],
    List<int>.filled(16385, 97),
    utf8.encode('{}\n{}\n'),
  ]) {
    test('拒绝非法、超长或多行 ready ${bytes.length}', () async {
      final process = FakeProcess()..autoReady = false;
      final backend = ManagedBackend(
        platform: process,
        health: () async => runtime(process),
      );
      addTearDown(backend.dispose);
      final pending = backend.start();
      await flush();
      process.bytes(bytes);
      await pending;
      expect(backend.ready, false);
      expect(process.living, false);
    });
  }
  test('ready 后普通 stdout 使运行失效', () async {
    final process = FakeProcess();
    final backend = ManagedBackend(
      platform: process,
      health: () async => runtime(process),
    );
    addTearDown(backend.dispose);
    await backend.start();
    process.bytes(utf8.encode('debug\n'));
    await flush();
    expect(backend.state, BackendState.failed);
    expect(process.living, false);
  });
  test('配置缺失、外连 exe、未知模式拒绝；显式目录保持', () {
    expect(
      () => BackendConfiguration.fromNative({
        'backend': 'managed',
        'dataDir': 'C:/数据',
        'executable': '',
      }),
      throwsFormatException,
    );
    expect(
      () => BackendConfiguration.fromNative({
        'backend': 'external',
        'dataDir': 'C:/数据',
        'executable': 'C:/go.exe',
      }),
      throwsFormatException,
    );
    expect(
      () => BackendConfiguration.fromNative({
        'backend': 'unknown',
        'dataDir': 'C:/数据',
        'executable': '',
      }),
      throwsFormatException,
    );
    expect(
      BackendConfiguration.fromNative({
        'backend': 'managed',
        'dataDir': 'C:/数据',
        'executable': 'C:/go.exe',
      }).dataDir,
      'C:/数据',
    );
  });
}
