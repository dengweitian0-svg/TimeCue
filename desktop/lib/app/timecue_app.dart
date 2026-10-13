import 'dart:async';
import 'dart:ui' show AppExitResponse;
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import '../contracts/notebook_repository.dart';
import '../data/config_file_store.dart';
import '../features/room/room_screen.dart';
import '../features/notebook/notification_view_model.dart';
import '../features/notebook/notification_reader_dialog.dart';
import '../theme/notebook_theme.dart';
import 'windows_notification_bridge.dart';
import 'desktop_lifecycle.dart';
import 'managed_backend.dart';
import 'operation_toast.dart';

class TimeCueApp extends StatelessWidget {
  const TimeCueApp({
    super.key,
    required this.repository,
    this.notifications,
    this.backend,
    this.desktop,
    this.notificationViews,
    this.configFiles,
    this.ownsRepository = false,
  });
  final NotebookRepository repository;
  final WindowsNotificationBridge? notifications;
  final ManagedBackend? backend;
  final DesktopLifecycle? desktop;
  final NotificationViewModel? notificationViews;
  final ConfigFileStore? configFiles;
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
    builder: (context, child) =>
        OperationToastHost(child: child ?? const SizedBox.shrink()),
    home: DesktopShell(
      repository: repository,
      notifications: notifications,
      backend: backend,
      desktop: desktop,
      notificationViews: notificationViews,
      configFiles: configFiles,
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
    this.backend,
    this.desktop,
    this.notificationViews,
    this.configFiles,
    this.ownsRepository = false,
  });
  final NotebookRepository repository;
  final WindowsNotificationBridge? notifications;
  final ManagedBackend? backend;
  final DesktopLifecycle? desktop;
  final NotificationViewModel? notificationViews;
  final ConfigFileStore? configFiles;
  final bool ownsRepository;
  @override
  State<DesktopShell> createState() => _DesktopShellState();
}

class _DesktopShellState extends State<DesktopShell> {
  late final AppLifecycleListener _lifecycle;
  final _historyRequests = ValueNotifier<int>(0);
  bool _readerOpen = false;
  bool _businessOpened = false;
  String? _pendingPayload;
  bool _runtimeReleased = false;
  String? _notificationError;

  Future<void> _activate(String payload) async {
    if (_runtimeReleased || widget.desktop?.stopping == true) return;
    if (widget.backend != null && !widget.backend!.ready) {
      _pendingPayload = payload;
      return;
    }
    await widget.notificationViews?.activate(payload);
  }

  void _backendChanged() {
    final backend = widget.backend!;
    if (backend.ready && !_businessOpened) {
      _businessOpened = true;
      widget.notifications?.expectedInstanceId = backend.instanceId;
      unawaited(widget.notifications?.initialize());
      final payload = _pendingPayload;
      _pendingPayload = null;
      if (payload != null) unawaited(_activate(payload));
    }
    if (backend.state == BackendState.failed) {
      unawaited(_revealFailure());
    }
    if (mounted && !_runtimeReleased) setState(() {});
  }

  Future<void> _revealFailure() async {
    final desktop = widget.desktop;
    if (desktop == null || desktop.stopping) return;
    await desktop.initialize();
    if (!_runtimeReleased && !desktop.stopping) await desktop.openWindow();
  }

  void _runtimeChanged() {
    final error = widget.notifications?.error;
    if (mounted && error != _notificationError) {
      setState(() => _notificationError = error);
    }
    final instance = widget.notifications?.instanceId;
    if (instance != null) widget.notificationViews?.observeRuntime(instance);
  }

  void _showReader() {
    if (!mounted || _readerOpen || widget.notificationViews?.active != true) {
      return;
    }
    _readerOpen = true;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      if (!mounted || _runtimeReleased) return;
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
      if (!mounted || _runtimeReleased) return;
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
    _businessOpened = widget.backend == null;
    if (notifications != null && _businessOpened) {
      unawaited(notifications.initialize());
    }
    final desktop = widget.desktop;
    if (desktop != null) {
      desktop.onActivate = _activate;
      desktop.stopNewWork = () {
        widget.backend?.cancelStart();
        widget.notifications?.stopClaiming();
      };
      desktop.stopOwnedProcess = widget.backend?.stopOwned;
      desktop.finalizeOwnedProcess = widget.backend?.releaseOwned;
      desktop.drain = widget.notifications == null
          ? null
          : (budget) => widget.notifications!.stopAndDrain(
              budget,
              reportExecutorExit: widget.backend == null,
            );
      desktop.releaseResources = _releaseRuntime;
      unawaited(desktop.initialize());
    }
    widget.backend?.addListener(_backendChanged);
    unawaited(widget.backend?.start());
    _lifecycle = AppLifecycleListener(
      onExitRequested: () async {
        if (widget.desktop case final desktop?) {
          await desktop.requestExit();
          return AppExitResponse.cancel;
        }
        widget.backend?.cancelStart();
        widget.notificationViews?.close();
        await widget.notifications?.stopAndDrain(
          const Duration(seconds: 5),
          reportExecutorExit: widget.backend == null,
        );
        await widget.backend?.releaseOwned();
        return AppExitResponse.exit;
      },
    );
  }

  void _releaseRuntime() {
    if (_runtimeReleased) return;
    _runtimeReleased = true;
    widget.backend?.removeListener(_backendChanged);
    widget.backend?.dispose();
    widget.notificationViews?.removeListener(_showReader);
    widget.notifications?.removeListener(_runtimeChanged);
    widget.notificationViews?.dispose();
    widget.notifications?.dispose();
    if (widget.ownsRepository) widget.repository.dispose();
  }

  @override
  void dispose() {
    _lifecycle.dispose();
    widget.desktop?.dispose();
    _releaseRuntime();
    _historyRequests.dispose();
    super.dispose();
  }

  Widget _errorCard(String message, List<Widget> actions) => Material(
    elevation: 4,
    borderRadius: BorderRadius.circular(8),
    child: Padding(
      padding: const EdgeInsets.all(12),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(message),
          Align(
            alignment: Alignment.centerRight,
            child: Wrap(children: actions),
          ),
        ],
      ),
    ),
  );

  @override
  Widget build(BuildContext context) => Scaffold(
    body: Stack(
      children: [
        if (_businessOpened)
          AbsorbPointer(
            absorbing: widget.backend != null && !widget.backend!.ready,
            child: RoomScreen(
              repository: widget.repository,
              configFiles: widget.configFiles,
              onRecovery: widget.notificationViews == null
                  ? null
                  : _openRecovery,
              historyRequests: _historyRequests,
            ),
          ),
        if (widget.backend case final backend?)
          if (!backend.ready)
            Positioned.fill(
              child: ColoredBox(
                color: const Color(0xEEFAF7F0),
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(32),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (backend.state == BackendState.starting)
                          const CircularProgressIndicator(),
                        const SizedBox(height: 16),
                        ConstrainedBox(
                          constraints: const BoxConstraints(maxHeight: 280),
                          child: SingleChildScrollView(
                            child: Text(backend.error ?? '正在启动本地服务…'),
                          ),
                        ),
                        const SizedBox(height: 16),
                        Wrap(
                          children: [
                            if (backend.canRetry)
                              TextButton(
                                onPressed: () => unawaited(backend.start()),
                                child: const Text('重试启动'),
                              ),
                            if (widget.desktop case final desktop?)
                              TextButton(
                                onPressed: desktop.stopping
                                    ? null
                                    : () => unawaited(desktop.requestExit()),
                                child: const Text('退出 TimeCue'),
                              ),
                          ],
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
      ],
    ),
    bottomNavigationBar: ListenableBuilder(
      listenable: Listenable.merge([?widget.desktop, ?widget.notifications]),
      builder: (context, _) {
        final banners = <Widget>[];
        if (widget.desktop case final desktop?) {
          if (desktop.error case final error?) {
            banners.add(
              _errorCard(error, [
                TextButton(
                  onPressed: desktop.stopping
                      ? null
                      : () => unawaited(
                          desktop.windowFailed
                              ? desktop.openWindow()
                              : desktop.retryTray(),
                        ),
                  child: Text(desktop.windowFailed ? '重试窗口' : '重试托盘'),
                ),
                TextButton(
                  onPressed: desktop.stopping
                      ? null
                      : () => unawaited(desktop.requestExit()),
                  child: const Text('退出 TimeCue'),
                ),
              ]),
            );
          }
        }
        if (widget.notifications case final notifications?) {
          if (notifications.error case final error?) {
            banners.add(
              _errorCard(error, [
                TextButton(
                  onPressed: () => unawaited(notifications.initialize()),
                  child: const Text('重试连接'),
                ),
              ]),
            );
          }
        }
        if (banners.isEmpty) return const SizedBox.shrink();
        return Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (var i = 0; i < banners.length; i++) ...[
                if (i > 0) const SizedBox(height: 8),
                banners[i],
              ],
            ],
          ),
        );
      },
    ),
  );
}
