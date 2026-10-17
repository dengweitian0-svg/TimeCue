import 'dart:async';
import 'package:flutter/material.dart';
import 'app/desktop_lifecycle.dart';
import 'app/managed_backend.dart';
import 'app/timecue_app.dart';
import 'app/windows_notification_bridge.dart';
import 'data/demo_notebook_repository.dart';
import 'data/http_chat_gateway.dart';
import 'data/http_notebook_repository.dart';
import 'features/chat/chat_view_model.dart';
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
  try {
    configuration = await WindowsOwnedProcessPlatform.configuration();
  } catch (failure) {
    runApp(_ConfigurationError(desktop: desktop, error: '$failure'));
    return;
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
  late final HttpChatGateway chatGateway;
  late final ChatViewModel chat;
  var businessOpened = false;
  var chatRuntimeLost = false;
  String? chatRuntimeInstance;
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
    backend.addListener(() {
      if (backend!.ready) {
        businessOpened = true;
        repository.enableManagedRuntime(backend.instanceId!, pid: backend.pid);
      }
    });
  }
  chatGateway = HttpChatGateway(
    beforeRequest: () async {
      if (chatRuntimeLost) throw StateError('聊天所属本地服务运行已失效。');
      if (configuration.managed && !(backend?.ready ?? false)) {
        throw StateError('自有本地服务尚未就绪。');
      }
      final runtime = await repository.deliveryRuntime();
      if (configuration.managed && !(backend?.ready ?? false)) {
        throw StateError('自有本地服务已失效。');
      }
      final expected = chatRuntimeInstance;
      if (expected == null) {
        chatRuntimeInstance = runtime.instanceId;
      } else if (expected != runtime.instanceId) {
        chatRuntimeLost = true;
        chat.invalidateRuntime();
        chatGateway.invalidateRuntime();
        throw StateError('聊天所属本地服务身份已变化，旧操作不会提交到新运行。');
      }
    },
  );
  chat = ChatViewModel(gateway: chatGateway);
  if (backend != null) {
    backend.onInvalidated = () {
      repository.invalidateManagedRuntime();
      if (businessOpened) {
        chatRuntimeLost = true;
        notifications.invalidateRuntime();
        views.observeRuntime(null);
        chat.invalidateRuntime();
        chatGateway.invalidateRuntime();
      }
    };
  }
  desktop.onActivate = views.activate;
  runApp(
    TimeCueApp(
      repository: repository,
      desktop: desktop,
      notificationViews: views,
      chat: chat,
      releaseChatTransport: chatGateway.dispose,
      notifications: notifications,
      backend: backend,
      configFiles: configFiles,
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
