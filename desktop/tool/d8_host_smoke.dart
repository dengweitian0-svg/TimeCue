import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:timecue_desktop/app/desktop_lifecycle.dart';
import 'package:timecue_desktop/app/managed_backend.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/app/windows_notification_bridge.dart';
import 'package:timecue_desktop/data/http_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notification_view_model.dart';

/// 只使用显式隔离目录；真实 runner、平台插件和业务组件完成验证后共用明确退出。
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final configuration = await WindowsOwnedProcessPlatform.configuration();
  final repository = HttpNotebookRepository(managed: configuration.managed);
  final desktop = DesktopLifecycle(WindowsDesktopPlatform());
  final views = NotificationViewModel(repository);
  final bridge = WindowsNotificationBridge(
    gateway: repository,
    platform: WindowsNotificationPlatform(
      onActivated: (payload) => unawaited(desktop.activate(payload)),
    ),
  );
  final backend = configuration.managed
      ? ManagedBackend(
          platform: WindowsOwnedProcessPlatform(),
          health: repository.deliveryRuntime,
        )
      : null;
  final evidence = <String, Object?>{
    'mode': configuration.mode,
    'dataDir': configuration.dataDir,
  };
  backend?.addListener(() {
    if (backend.ready) {
      repository.enableManagedRuntime(backend.instanceId!, pid: backend.pid);
    }
  });
  if (backend != null) {
    repository.onManagedRuntimeLost = () =>
        backend.invalidateRuntime('自有进程健康身份不匹配。');
    backend.onInvalidated = () {
      repository.invalidateManagedRuntime();
      bridge.invalidateRuntime();
      views.observeRuntime(null);
    };
  }
  final evidenceFile = File('${configuration.dataDir}/d8-host-evidence.json');
  Future<void> validate() async {
    try {
      await backend?.start();
      if (backend != null && !backend.ready) {
        throw StateError(backend.error ?? '启动失败');
      }
      final health = await repository.deliveryRuntime();
      evidence['instanceId'] = health.instanceId;
      evidence['pid'] = health.pid;
      evidence['healthMatched'] =
          backend == null ||
          health.instanceId == backend.instanceId && health.pid == backend.pid;
      await File(
        '${configuration.dataDir}/d8-host-ready.json',
      ).writeAsString(jsonEncode(evidence));
      final parentDeadline = DateTime.now().add(const Duration(seconds: 8));
      while (!await File(
        '${configuration.dataDir}/d8-host-continue',
      ).exists()) {
        if (DateTime.now().isAfter(parentDeadline)) {
          throw StateError('父测试未取得子进程观察句柄。');
        }
        await Future<void>.delayed(const Duration(milliseconds: 20));
      }
      await bridge.initialize();
      final deadline = DateTime.now().add(const Duration(seconds: 8));
      while (true) {
        await bridge.runOnce();
        await repository.refresh();
        if (repository.pending.any((item) => item.delivery == 'sent')) break;
        if (DateTime.now().isAfter(deadline)) {
          throw StateError('隔离提醒没有结果回流：${bridge.error}');
        }
        await Future<void>.delayed(const Duration(milliseconds: 100));
      }
      evidence['sent'] = repository.pending
          .where((item) => item.delivery == 'sent')
          .length;
      evidence['unacknowledged'] = repository.pending
          .where((item) => item.acknowledgedAt == null)
          .length;
      evidence['readyWindowState'] = desktop.state.name;
      final watch = Stopwatch()..start();
      // 先保存执行事实，退出完成的证据由父测试使用真实句柄补充。
      evidence['passed'] = true;
      await evidenceFile.writeAsString(jsonEncode(evidence));
      await desktop.requestExit();
      evidence['gracefulMilliseconds'] = watch.elapsedMilliseconds;
    } catch (failure, stack) {
      evidence['passed'] = false;
      evidence['error'] = '$failure';
      evidence['stack'] = '$stack';
      await evidenceFile.writeAsString(jsonEncode(evidence));
      await desktop.requestExit();
    }
  }

  runApp(
    TimeCueApp(
      repository: repository,
      desktop: desktop,
      backend: backend,
      notifications: bridge,
      notificationViews: views,
      ownsRepository: true,
    ),
  );
  WidgetsBinding.instance.addPostFrameCallback((_) => unawaited(validate()));
}
