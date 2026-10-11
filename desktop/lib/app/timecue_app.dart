import 'dart:async';
import 'dart:ui' show AppExitResponse;
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import '../contracts/notebook_repository.dart';
import '../features/room/room_screen.dart';
import '../features/notebook/notification_view_model.dart';
import '../features/notebook/notification_reader_dialog.dart';
import '../theme/notebook_theme.dart';
import 'windows_notification_bridge.dart';

class TimeCueApp extends StatelessWidget {
  const TimeCueApp({
    super.key,
    required this.repository,
    this.notifications,
    this.notificationViews,
    this.ownsRepository = false,
  });
  final NotebookRepository repository;
  final WindowsNotificationBridge? notifications;
  final NotificationViewModel? notificationViews;
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
      notificationViews: notificationViews,
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
    this.notificationViews,
    this.ownsRepository = false,
  });
  final NotebookRepository repository;
  final WindowsNotificationBridge? notifications;
  final NotificationViewModel? notificationViews;
  final bool ownsRepository;
  @override
  State<DesktopShell> createState() => _DesktopShellState();
}

class _DesktopShellState extends State<DesktopShell> {
  late final AppLifecycleListener _lifecycle;
  final _historyRequests = ValueNotifier<int>(0);
  bool _readerOpen = false;

  void _runtimeChanged() {
    final instance = widget.notifications?.instanceId;
    if (instance != null) widget.notificationViews?.observeRuntime(instance);
  }

  void _showReader() {
    if (!mounted || _readerOpen || widget.notificationViews?.active != true) {
      return;
    }
    _readerOpen = true;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      if (!mounted) return;
      final views = widget.notificationViews!;
      if (!views.active) {
        _readerOpen = false;
        return;
      }
      await showDialog<void>(
        context: context,
        builder: (_) => NotificationReaderDialog(
          model: views,
          onHistory: () => _historyRequests.value++,
        ),
      );
      if (!mounted) return;
      _readerOpen = false;
      views.close();
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  void _openRecovery() {
    final progress = widget.repository.recovery;
    if (progress != null) {
      unawaited(widget.notificationViews?.openRecovery(progress));
    }
  }

  @override
  void initState() {
    super.initState();
    final notifications = widget.notifications;
    widget.notificationViews?.addListener(_showReader);
    notifications?.addListener(_runtimeChanged);
    _showReader();
    if (notifications != null) unawaited(notifications.initialize());
    _lifecycle = AppLifecycleListener(
      onExitRequested: () async {
        widget.notificationViews?.close();
        await widget.notifications?.stopAndDrain(const Duration(seconds: 5));
        return AppExitResponse.exit;
      },
    );
  }

  @override
  void dispose() {
    _lifecycle.dispose();
    widget.notificationViews?.removeListener(_showReader);
    widget.notifications?.removeListener(_runtimeChanged);
    widget.notificationViews?.dispose();
    _historyRequests.dispose();
    widget.notifications?.dispose();
    if (widget.ownsRepository) widget.repository.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: Stack(
      children: [
        RoomScreen(
          repository: widget.repository,
          onRecovery: widget.notificationViews == null ? null : _openRecovery,
          historyRequests: _historyRequests,
        ),
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
