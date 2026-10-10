import 'dart:async';
import 'dart:ui' show AppExitResponse;
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import '../contracts/notebook_repository.dart';
import '../features/room/room_screen.dart';
import '../theme/notebook_theme.dart';
import 'windows_notification_bridge.dart';

class TimeCueApp extends StatelessWidget {
  const TimeCueApp({
    super.key,
    required this.repository,
    this.notifications,
    this.ownsRepository = false,
  });
  final NotebookRepository repository;
  final WindowsNotificationBridge? notifications;
  final bool ownsRepository;
  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'TimeCue',
    locale: const Locale('zh', 'CN'),
    supportedLocales: const [Locale('zh', 'CN')],
    // 保持 stable SDK 的官方本地化入口；本机 master 正在迁移 Material 包。
    // ignore: deprecated_member_use
    localizationsDelegates: GlobalMaterialLocalizations.delegates,
    debugShowCheckedModeBanner: false,
    theme: NotebookTheme.data,
    home: DesktopShell(
      repository: repository,
      notifications: notifications,
      ownsRepository: ownsRepository,
    ),
  );
}

/// 通知执行独立于房间焦点和本子开合，宿主负责停止后再释放共享仓储。
class DesktopShell extends StatefulWidget {
  const DesktopShell({
    super.key,
    required this.repository,
    this.notifications,
    this.ownsRepository = false,
  });
  final NotebookRepository repository;
  final WindowsNotificationBridge? notifications;
  final bool ownsRepository;
  @override
  State<DesktopShell> createState() => _DesktopShellState();
}

class _DesktopShellState extends State<DesktopShell> {
  late final AppLifecycleListener _lifecycle;
  @override
  void initState() {
    super.initState();
    final notifications = widget.notifications;
    if (notifications != null) unawaited(notifications.initialize());
    _lifecycle = AppLifecycleListener(
      onExitRequested: () async {
        await widget.notifications?.stopAndDrain(const Duration(seconds: 5));
        return AppExitResponse.exit;
      },
    );
  }

  @override
  void dispose() {
    _lifecycle.dispose();
    widget.notifications?.dispose();
    if (widget.ownsRepository) widget.repository.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: Stack(
      children: [
        RoomScreen(repository: widget.repository),
        if (widget.notifications case final notifications?)
          Positioned(
            left: 16,
            right: 16,
            bottom: 16,
            child: ListenableBuilder(
              listenable: notifications,
              builder: (_, _) => notifications.error == null
                  ? const SizedBox.shrink()
                  : Material(
                      elevation: 4,
                      borderRadius: BorderRadius.circular(8),
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: Row(
                          children: [
                            Expanded(child: Text(notifications.error!)),
                            TextButton(
                              onPressed: () =>
                                  unawaited(notifications.initialize()),
                              child: const Text('重试连接'),
                            ),
                          ],
                        ),
                      ),
                    ),
            ),
          ),
      ],
    ),
  );
}
