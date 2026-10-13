import 'dart:async';
import 'dart:io';
import 'package:flutter/material.dart';
import 'app/desktop_lifecycle.dart';
import 'app/managed_backend.dart';
import 'app/software_update.dart';
import 'app/timecue_app.dart';
import 'app/windows_notification_bridge.dart';
import 'data/demo_notebook_repository.dart';
import 'data/http_notebook_repository.dart';
import 'features/notebook/notification_view_model.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final desktop = DesktopLifecycle(WindowsDesktopPlatform());
  const demo = bool.fromEnvironment('TIMECUE_DEMO', defaultValue: false);
  if (demo) {
    runApp(
      TimeCueApp(
        repository: DemoNotebookRepository(),
        desktop: desktop,
        ownsRepository: true,
      ),
    );
    return;
  }
  BackendConfiguration configuration;
  SoftwareUpdateConfiguration? updateConfiguration;
  try {
    configuration = await WindowsOwnedProcessPlatform.configuration();
  } catch (failure) {
    runApp(_ConfigurationError(desktop: desktop, error: '$failure'));
    return;
  }
  try {
    final updateSnapshot = await WindowsSoftwareUpdatePlatform.channel
        .invokeMethod<Object?>('configuration');
    updateConfiguration = SoftwareUpdateConfiguration.fromNative(
      updateSnapshot,
      applicationDirectory: File(Platform.resolvedExecutable).parent.path,
    );
  } catch (_) {
    // 更新检查不可用不影响日程服务启动。
  }
  final repository = HttpNotebookRepository(managed: configuration.managed);
  final configFiles = configuration.configFiles;
  final views = NotificationViewModel(repository);
  final notifications = WindowsNotificationBridge(
    gateway: repository,
    platform: WindowsNotificationPlatform(
      onActivated: (payload) => unawaited(desktop.activate(payload)),
    ),
  );
  ManagedBackend? backend;
  var businessOpened = false;
  if (configuration.managed) {
    backend = ManagedBackend(
      platform: WindowsOwnedProcessPlatform(),
      health: repository.deliveryRuntime,
      prepareStart: () async {
        await configFiles.normalizeLegacyDatabaseDirectory();
      },
    );
    repository.onManagedRuntimeLost = () =>
        backend!.invalidateRuntime('端口上的服务身份与自有 Go 不一致。');
    backend.onInvalidated = () {
      repository.invalidateManagedRuntime();
      if (businessOpened) {
        notifications.invalidateRuntime();
        views.observeRuntime(null);
      }
    };
    backend.addListener(() {
      if (backend!.ready) {
        businessOpened = true;
        repository.enableManagedRuntime(backend.instanceId!, pid: backend.pid);
      }
    });
  }
  desktop.onActivate = views.activate;
  runApp(
    TimeCueApp(
      repository: repository,
      desktop: desktop,
      notificationViews: views,
      notifications: notifications,
      backend: backend,
      configFiles: configFiles,
      softwareUpdateConfiguration: updateConfiguration,
      ownsRepository: true,
    ),
  );
}

/// 参数错误仍有可见窗口及唯一退出入口，不创建业务仓储或执行者。
class _ConfigurationError extends StatefulWidget {
  const _ConfigurationError({required this.desktop, required this.error});
  final DesktopLifecycle desktop;
  final String error;
  @override
  State<_ConfigurationError> createState() => _ConfigurationErrorState();
}

class _ConfigurationErrorState extends State<_ConfigurationError> {
  @override
  void initState() {
    super.initState();
    unawaited(
      widget.desktop.initialize().then((_) => widget.desktop.openWindow()),
    );
  }

  @override
  void dispose() {
    widget.desktop.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    home: Scaffold(
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text('启动配置无效：${widget.error}'),
              TextButton(
                onPressed: () => unawaited(widget.desktop.requestExit()),
                child: const Text('退出 TimeCue'),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}
