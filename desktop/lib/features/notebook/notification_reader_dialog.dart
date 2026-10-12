import 'dart:async';
import 'package:flutter/material.dart';
import 'notification_details.dart';
import 'notification_view_model.dart';
import 'recovery_status.dart';

/// 系统激活和恢复入口共用只读窗口，不替换本子及其编辑会话。
class NotificationReaderDialog extends StatefulWidget {
  const NotificationReaderDialog({
    super.key,
    required this.model,
    required this.onHistory,
  });
  final NotificationViewModel model;
  final VoidCallback onHistory;
  @override
  State<NotificationReaderDialog> createState() =>
      _NotificationReaderDialogState();
}

class _NotificationReaderDialogState extends State<NotificationReaderDialog>
    with WidgetsBindingObserver {
  Timer? _poll;
  bool _foreground = true;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _foreground =
        WidgetsBinding.instance.lifecycleState == null ||
        WidgetsBinding.instance.lifecycleState == AppLifecycleState.resumed;
    startPolling();
  }

  void startPolling() {
    _poll?.cancel();
    if (!_foreground) return;
    _poll = Timer.periodic(const Duration(seconds: 5), (_) {
      final model = widget.model;
      if (!model.loading &&
          !model.showsDetail &&
          model.error == null &&
          !model.expired) {
        unawaited(model.refreshCurrent());
      }
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    _poll?.cancel();
    if (_foreground) {
      final model = widget.model;
      if (model.detail != null) {
        unawaited(model.readDetail(model.detail!));
      } else {
        unawaited(model.refresh());
      }
      startPolling();
    }
  }

  @override
  void dispose() {
    _poll?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.model,
    builder: (context, _) {
      final model = widget.model;
      final detail = model.detail;
      final members = model.members;
      return AlertDialog(
        title: Text(
          model.target == null
              ? '通知定位'
              : model.showsDetail && detail != null
              ? detail.name
              : model.target?.isActivation == true
              ? '本次通知成员'
              : '本轮恢复记录',
        ),
        content: SizedBox(
          width: 600,
          height: (MediaQuery.sizeOf(context).height * .62).clamp(120.0, 560.0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (model.loading || model.detailLoading)
                const LinearProgressIndicator(),
              if (model.error != null)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: Text(
                    '${model.error!}${members != null ? '\n以下保留上次读取结果。' : ''}',
                  ),
                ),
              if (model.notice != null)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(model.notice!),
                ),
              if (!model.showsDetail && members != null)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: Text(
                    '已保存成员 ${members.total} 条 · 第 ${model.pageIndex + 1} 页',
                  ),
                ),
              Expanded(
                child: model.showsDetail && detail != null
                    ? SingleChildScrollView(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            if (model.detailError != null)
                              Padding(
                                padding: const EdgeInsets.only(bottom: 16),
                                child: Text(model.detailError!),
                              ),
                            NotificationDetailsContent(entry: detail),
                          ],
                        ),
                      )
                    : ListView(
                        children: [
                          if (model.progress != null)
                            Padding(
                              padding: const EdgeInsets.only(bottom: 16),
                              child: RecoveryStatus(
                                progress: model.progress!,
                                stale: model.error != null,
                              ),
                            ),
                          if (members != null && members.items.isEmpty)
                            Text(
                              model.progress?.state == 'completed'
                                  ? '本轮没有已保存的成员记录。'
                                  : '当前尚无成员记录，历史整理可能仍在进行。',
                            ),
                          for (final entry in members?.items ?? [])
                            ListTile(
                              contentPadding: EdgeInsets.zero,
                              title: Text(entry.name),
                              subtitle: Text(
                                '${plannedTimeLabel(entry.plannedAt)}\n${deliveryLabel(entry)} · ${entry.acknowledgedAt == null ? '待确认' : '已完成'}',
                              ),
                              trailing: const Icon(Icons.chevron_right),
                              onTap: () => unawaited(model.readDetail(entry)),
                            ),
                        ],
                      ),
              ),
            ],
          ),
        ),
        actions: [
          if (model.expired)
            TextButton(
              onPressed: () {
                Navigator.pop(context);
                widget.onHistory();
              },
              child: const Text('打开历史待确认'),
            ),
          if (model.showsDetail && !model.singleMinute)
            TextButton(
              onPressed: model.backToMembers,
              child: const Text('返回成员'),
            ),
          if ((!model.expired && model.target != null) ||
              (model.showsDetail && model.detail?.notificationId != null))
            TextButton(
              onPressed: model.loading || model.detailLoading
                  ? null
                  : () {
                      if (model.showsDetail && model.detail != null) {
                        unawaited(model.readDetail(model.detail!));
                      } else {
                        unawaited(
                          model.error != null ? model.retry() : model.refresh(),
                        );
                      }
                    },
              child: Text(
                model.error != null || model.detailError != null
                    ? '重试读取'
                    : '刷新',
              ),
            ),
          if (!model.showsDetail && members != null && !model.expired) ...[
            TextButton(
              onPressed: model.loading || !model.canPrevious
                  ? null
                  : model.previousPage,
              child: const Text('上一页'),
            ),
            TextButton(
              onPressed: model.loading || !model.hasNext
                  ? null
                  : model.nextPage,
              child: const Text('下一页'),
            ),
          ],
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('关闭'),
          ),
        ],
      );
    },
  );
}
