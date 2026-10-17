import 'dart:async';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import '../chat/chat_view_model.dart';
import '../../app/operation_toast.dart';
import '../../data/config_file_store.dart';
import '../notebook/notebook_overlay.dart';
import '../notebook/notebook_view_model.dart';
import '../notebook/schedule_management_dialog.dart';
import '../schedule_editor/schedule_editor.dart';
import '../settings/settings_dialog.dart';
import '../../theme/notebook_theme.dart';
import 'cat_chat_hotspot.dart';
import 'desk_notebook_hotspot.dart';
import 'room_backdrop.dart';
import 'room_clock.dart';
import 'room_geometry.dart';

class RoomScreen extends StatefulWidget {
  const RoomScreen({
    super.key,
    required this.repository,
    this.onRecovery,
    this.historyRequests,
    this.configFiles,
    this.chat,
  });
  final NotebookRepository repository;
  final VoidCallback? onRecovery;
  final ValueListenable<int>? historyRequests;
  final ConfigFileStore? configFiles;
  final ChatViewModel? chat;
  @override
  State<RoomScreen> createState() => _RoomScreenState();
}

class _RoomScreenState extends State<RoomScreen> with WidgetsBindingObserver {
  late final notebook = NotebookViewModel(
    widget.repository,
    onRecovery: widget.onRecovery,
    onOperationFeedback: (status, message) {
      if (!mounted) return;
      switch (status) {
        case NotebookOperationStatus.success:
          OperationToast.success(context, message);
        case NotebookOperationStatus.warning:
          OperationToast.warning(context, message);
        case NotebookOperationStatus.error:
          OperationToast.error(context, message);
      }
    },
  );
  Timer? _recoveryPoll;
  int _historyRequest = 0;
  String? _dismissedRecoveryId;
  late final clock = RoomClockController();
  final deskFocus = FocusNode(debugLabel: '桌面本子入口');
  final actionFocus = FocusNode(debugLabel: '固定本子入口');
  final catFocus = FocusNode(debugLabel: '猫咪 AI 对话入口');
  bool bookVisible = false;
  bool fromDesk = false;
  bool fromCat = false;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    widget.historyRequests?.addListener(openHistory);
    widget.repository.addListener(_coordinateRecoveryPolling);
    widget.repository.refresh();
  }

  void openHistory() {
    if (!bookVisible) {
      open(false);
      notebook.changeTab(NotebookTab.history);
    } else {
      setState(() => ++_historyRequest);
    }
  }

  // 房间前台继续观察整理或尚未确认的遗漏，全部确认后停止轮询。
  void _coordinateRecoveryPolling() {
    final state = WidgetsBinding.instance.lifecycleState;
    final progress = widget.repository.recovery;
    final shouldPoll =
        !bookVisible &&
        (state == null || state == AppLifecycleState.resumed) &&
        progress != null &&
        (progress.state != 'completed' || progress.unacknowledgedCount != 0);
    if (!shouldPoll) {
      _recoveryPoll?.cancel();
      _recoveryPoll = null;
      return;
    }
    _recoveryPoll ??= Timer.periodic(const Duration(seconds: 5), (_) {
      if (!widget.repository.loading) unawaited(widget.repository.refresh());
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      if (bookVisible) {
        notebook.startPolling();
      } else {
        widget.repository.refresh();
      }
    } else {
      notebook.close();
    }
    _coordinateRecoveryPolling();
  }

  void open(bool desk, {bool cat = false}) {
    if (bookVisible) return;
    notebook.open();
    setState(() {
      fromDesk = desk;
      fromCat = cat;
      bookVisible = true;
    });
    if (cat) notebook.changeTab(NotebookTab.ai);
    _coordinateRecoveryPolling();
  }

  Future<void> create() async {
    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (_) => ScheduleCreateDialog(repository: widget.repository),
    );
  }

  Future<void> manage() async {
    await showDialog<void>(
      context: context,
      builder: (_) => ScheduleManagementDialog(repository: widget.repository),
    );
  }

  Future<void> settings() async {
    final store = widget.configFiles;
    if (store == null) return;
    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (_) => SettingsDialog(store: store),
    );
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    widget.historyRequests?.removeListener(openHistory);
    widget.repository.removeListener(_coordinateRecoveryPolling);
    _recoveryPoll?.cancel();
    notebook.dispose();
    clock.dispose();
    deskFocus.dispose();
    actionFocus.dispose();
    catFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final geometry = RoomGeometry(constraints.biggest);
      final fixedOrigin = Offset(constraints.maxWidth - 100, 48);
      return Stack(
        fit: StackFit.expand,
        children: [
          // 秒级更新只驱动时钟；背景只有跨时段时才切换图片。
          ValueListenableBuilder(
            valueListenable: clock.scene,
            builder: (context, period, _) =>
                RepaintBoundary(child: RoomBackdrop(period: period)),
          ),
          Positioned(
            top: constraints.maxWidth < 650 ? 80 : 26,
            left: 0,
            right: 0,
            child: Center(child: RoomClock(clock: clock)),
          ),
          if (geometry.notebookVisible)
            Positioned.fromRect(
              rect: geometry.visibleNotebookBounds,
              child: DeskNotebookHotspot(
                onOpen: () => open(true),
                focusNode: deskFocus,
                outline: geometry.visibleNotebookPath.shift(
                  -geometry.visibleNotebookBounds.topLeft,
                ),
              ),
            ),
          if (!bookVisible && geometry.catVisible)
            Positioned.fill(
              child: CatChatHotspot(
                visibleBounds: geometry.visibleCatBounds,
                viewport: constraints.biggest,
                focusNode: catFocus,
                onOpen: () => open(false, cat: true),
              ),
            ),
          Positioned(
            top: 24,
            left: 24,
            child: Tooltip(
              message: widget.configFiles == null ? '演示模式不修改本地配置' : '应用设置',
              child: FilledButton.icon(
                onPressed: widget.configFiles == null ? null : settings,
                style: NotebookTheme.roomActionStyle(),
                icon: const Icon(Icons.settings_outlined, size: 17),
                label: const Text('设置'),
              ),
            ),
          ),
          Positioned(
            top: 24,
            right: 24,
            child: ListenableBuilder(
              listenable: notebook,
              builder: (context, _) => RoomActions(
                onOpen: () => open(false),
                pendingCount: widget.repository.pendingTotal,
                focusNode: actionFocus,
              ),
            ),
          ),
          Positioned(
            bottom: 24,
            right: 28,
            child: ListenableBuilder(
              listenable: widget.repository,
              builder: (context, _) => Wrap(
                spacing: 12,
                children: [
                  FilledButton.icon(
                    onPressed: manage,
                    style: NotebookTheme.roomActionStyle(),
                    icon: const Icon(Icons.list_alt, size: 17),
                    label: const Text('管理日程'),
                  ),
                  Tooltip(
                    message: widget.repository.canWrite
                        ? '添加日程'
                        : widget.repository.loading &&
                              !widget.repository.hasSnapshot
                        ? '正在连接本地服务，暂时不能添加日程'
                        : widget.repository.refreshError != null
                        ? '本地服务连接失败，请重试后添加日程'
                        : '提醒服务暂不可用，恢复连接后即可添加日程',
                    child: FilledButton.icon(
                      onPressed: widget.repository.canWrite ? create : null,
                      style: NotebookTheme.roomActionStyle(
                        disabledCreate: true,
                      ),
                      icon: const Icon(Icons.add, size: 17),
                      label: const Text('添加日程'),
                    ),
                  ),
                ],
              ),
            ),
          ),
          Positioned(
            top: 76,
            right: 24,
            child: ListenableBuilder(
              listenable: widget.repository,
              builder: (context, _) => widget.repository.refreshError != null
                  ? TextButton.icon(
                      onPressed: notebook.refresh,
                      style: NotebookTheme.roomActionStyle(),
                      icon: const Icon(Icons.refresh, size: 16),
                      label: Text(widget.repository.refreshError!),
                    )
                  : widget.repository.loading && !widget.repository.hasSnapshot
                  ? const Text('正在读取日程…')
                  : const SizedBox(),
            ),
          ),
          if (bookVisible)
            Positioned.fill(
              child: NotebookOverlay(
                model: notebook,
                origin: fromDesk && geometry.notebookVisible
                    ? geometry.visibleNotebookBounds.center
                    : fixedOrigin,
                historyRequest: _historyRequest,
                chat: widget.chat,
                onClosed: () {
                  notebook.close();
                  setState(() => bookVisible = false);
                  _coordinateRecoveryPolling();
                  final returnFocus = fromCat && geometry.catVisible
                      ? catFocus
                      : fromDesk && geometry.notebookVisible
                      ? deskFocus
                      : actionFocus;
                  // 猫热区可能随本子一起卸载，等入口重新挂载后再恢复焦点。
                  WidgetsBinding.instance.addPostFrameCallback((_) {
                    if (mounted) returnFocus.requestFocus();
                  });
                },
              ),
            ),
          if (!bookVisible)
            Positioned(
              left: 24,
              bottom: constraints.maxWidth < 800 ? 84 : 24,
              width: (constraints.maxWidth - 48).clamp(0.0, 380.0),
              child: ListenableBuilder(
                listenable: widget.repository,
                builder: (context, _) {
                  final progress = widget.repository.recovery;
                  if (progress == null ||
                      (progress.missedTotal ?? 0) <= 0 ||
                      (progress.unacknowledgedCount ?? 0) <= 0 ||
                      progress.recoveryId == _dismissedRecoveryId) {
                    return const SizedBox.shrink();
                  }
                  return Material(
                    color: Theme.of(context).colorScheme.surface,
                    borderRadius: BorderRadius.circular(12),
                    child: ListTile(
                      leading: const Icon(Icons.history),
                      title: Text('有 ${progress.unacknowledgedCount} 条遗漏提醒'),
                      subtitle: widget.repository.refreshError != null
                          ? const Text('上次读取结果 · 连接恢复后更新')
                          : null,
                      trailing: const Icon(Icons.chevron_right),
                      onTap: widget.onRecovery == null
                          ? null
                          : () {
                              setState(
                                () =>
                                    _dismissedRecoveryId = progress.recoveryId,
                              );
                              widget.onRecovery!();
                            },
                    ),
                  );
                },
              ),
            ),
        ],
      );
    },
  );
}

class RoomActions extends StatelessWidget {
  const RoomActions({
    super.key,
    required this.onOpen,
    required this.pendingCount,
    required this.focusNode,
  });
  final VoidCallback onOpen;
  final int pendingCount;
  final FocusNode focusNode;
  @override
  Widget build(BuildContext context) => FilledButton.icon(
    focusNode: focusNode,
    onPressed: onOpen,
    style: NotebookTheme.roomActionStyle(),
    icon: const Icon(Icons.menu_book_outlined, size: 17),
    label: Text('日程本${pendingCount > 0 ? '  $pendingCount' : ''}'),
  );
}
