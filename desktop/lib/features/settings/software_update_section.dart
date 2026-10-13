import 'dart:async';

import 'package:flutter/material.dart';

import '../../app/software_update.dart';

/// 展示更新状态、发行说明和受控的检查/安装操作。
class SoftwareUpdateSection extends StatefulWidget {
  const SoftwareUpdateSection({
    super.key,
    required this.controller,
    required this.onInstall,
    this.canInstall = true,
    this.disabledMessage,
  });

  final SoftwareUpdateController? controller;
  final Future<void> Function()? onInstall;
  final bool canInstall;
  final String? disabledMessage;

  @override
  State<SoftwareUpdateSection> createState() => _SoftwareUpdateSectionState();
}

class _SoftwareUpdateSectionState extends State<SoftwareUpdateSection> {
  @override
  void initState() {
    super.initState();
    unawaited(widget.controller?.restoreStatus());
  }

  String _stateText(SoftwareUpdateController controller) {
    switch (controller.state) {
      case SoftwareUpdateState.idle:
        return '尚未检查更新';
      case SoftwareUpdateState.checking:
        return '正在检查更新…';
      case SoftwareUpdateState.available:
        return '发现新版本 ${controller.latestVersion ?? ''}';
      case SoftwareUpdateState.upToDate:
        return '当前版本已是最新版本';
      case SoftwareUpdateState.unavailable:
        return controller.error ?? '暂无可用更新';
      case SoftwareUpdateState.installComplete:
        return controller.error ?? '更新已完成';
      case SoftwareUpdateState.downloading:
        final progress = controller.progress;
        return progress == null
            ? '正在下载更新包…'
            : '正在下载更新包 ${(progress * 100).round()}%';
      case SoftwareUpdateState.verifying:
        return '正在校验并准备更新包…';
      case SoftwareUpdateState.waitingExit:
        return '安装器已就绪，TimeCue 将关闭并在安装后重启';
      case SoftwareUpdateState.failed:
        return controller.error ?? '更新失败';
    }
  }

  bool _busy(SoftwareUpdateController controller) =>
      controller.state == SoftwareUpdateState.checking ||
      controller.state == SoftwareUpdateState.downloading ||
      controller.state == SoftwareUpdateState.verifying ||
      controller.state == SoftwareUpdateState.waitingExit;

  @override
  Widget build(BuildContext context) {
    final controller = widget.controller;
    final theme = Theme.of(context);
    if (controller == null) {
      return _section(
        context,
        current: '版本信息暂不可用',
        status: widget.disabledMessage ?? '当前运行环境未提供软件更新。',
        canCheck: false,
        canInstall: false,
        busy: false,
      );
    }
    return ListenableBuilder(
      listenable: controller,
      builder: (context, _) {
        final busy = _busy(controller);
        final status = controller.error ?? _stateText(controller);
        final canInstall =
            controller.state == SoftwareUpdateState.available &&
            controller.canInstall &&
            widget.canInstall &&
            widget.onInstall != null;
        return _section(
          context,
          current: '当前版本：${controller.configuration.currentVersion}',
          latest: controller.latestVersion == null
              ? null
              : '最新版本：${controller.latestVersion}',
          status: status,
          notes: controller.releaseNotes,
          progress: controller.progress,
          busy: busy,
          canCheck: controller.canCheck && !busy,
          canInstall: canInstall,
          disabledMessage: widget.disabledMessage ?? controller.supportReason,
          onCheck: () => unawaited(controller.checkForUpdates()),
          onInstall: widget.onInstall,
          error: controller.state == SoftwareUpdateState.failed,
          theme: theme,
        );
      },
    );
  }

  Widget _section(
    BuildContext context, {
    required String current,
    String? latest,
    required String status,
    String? notes,
    double? progress,
    required bool busy,
    required bool canCheck,
    required bool canInstall,
    String? disabledMessage,
    VoidCallback? onCheck,
    Future<void> Function()? onInstall,
    bool error = false,
    ThemeData? theme,
  }) => Column(
    key: const ValueKey('software-update-section'),
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text('软件更新', style: (theme ?? Theme.of(context)).textTheme.titleMedium),
      const SizedBox(height: 8),
      Text(current),
      if (latest != null) ...[const SizedBox(height: 4), Text(latest)],
      const SizedBox(height: 8),
      if (busy) const LinearProgressIndicator(),
      if (progress != null && busy && progress > 0) ...[
        const SizedBox(height: 4),
        LinearProgressIndicator(value: progress),
      ],
      Text(
        status,
        key: const ValueKey('software-update-status'),
        style: TextStyle(
          color: error ? Theme.of(context).colorScheme.error : null,
        ),
      ),
      if (notes != null && notes.trim().isNotEmpty) ...[
        const SizedBox(height: 8),
        Text('发行说明', style: Theme.of(context).textTheme.labelLarge),
        const SizedBox(height: 4),
        ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 120),
          child: SingleChildScrollView(
            child: Text(notes, key: const ValueKey('software-update-notes')),
          ),
        ),
      ],
      if (disabledMessage != null && !canInstall) ...[
        const SizedBox(height: 4),
        Text(disabledMessage, style: Theme.of(context).textTheme.bodySmall),
      ],
      const SizedBox(height: 8),
      Wrap(
        spacing: 8,
        runSpacing: 4,
        children: [
          OutlinedButton.icon(
            key: const ValueKey('software-update-check'),
            onPressed: canCheck ? onCheck : null,
            icon: const Icon(Icons.refresh),
            label: const Text('检查更新'),
          ),
          FilledButton.icon(
            key: const ValueKey('software-update-install'),
            onPressed: canInstall ? () => unawaited(onInstall!()) : null,
            icon: const Icon(Icons.system_update_alt),
            label: const Text('下载并安装'),
          ),
        ],
      ),
    ],
  );
}
