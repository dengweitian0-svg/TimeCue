import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import '../../contracts/notification_query.dart';

String plannedTimeLabel(DateTime value) =>
    '${value.year} 年 ${value.month} 月 ${value.day} 日 ${timeLabel(value)}';

String deliveryLabel(TriggerSummary entry) =>
    entry.source == 'projected' || entry.delivery.isEmpty
    ? '未来安排'
    : switch (entry.delivery) {
        'pending' || 'loaded' => '待投递',
        'sent' => '已提醒（平台已接受展示）',
        'failed' => '投递失败',
        _ => '投递状态未知',
      };

class NotificationDetailsContent extends StatelessWidget {
  const NotificationDetailsContent({super.key, required this.entry});
  final TriggerSummary entry;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    mainAxisSize: MainAxisSize.min,
    children: [
      SelectableText('原计划时间 · ${plannedTimeLabel(entry.plannedAt)} · 北京时间'),
      const SizedBox(height: 16),
      SelectableText(entry.description.isEmpty ? '暂无说明' : entry.description),
      const SizedBox(height: 24),
      Text('投递状态 · ${deliveryLabel(entry)}'),
      const SizedBox(height: 8),
      Text(
        entry.acknowledgedAt == null
            ? '确认状态 · 等待你的确认'
            : '确认状态 · 已完成\n${plannedTimeLabel(entry.acknowledgedAt!)} · 北京时间',
      ),
    ],
  );
}

class NotificationDetailsDialog extends StatefulWidget {
  const NotificationDetailsDialog({
    super.key,
    required this.entry,
    this.gateway,
  });
  final TriggerSummary entry;
  final NotificationQueryGateway? gateway;
  @override
  State<NotificationDetailsDialog> createState() =>
      _NotificationDetailsDialogState();
}

class _NotificationDetailsDialogState extends State<NotificationDetailsDialog> {
  late TriggerSummary entry = widget.entry;
  bool loading = false;
  String? error;
  @override
  void initState() {
    super.initState();
    if (widget.gateway != null && entry.notificationId != null) refresh();
  }

  Future<void> refresh() async {
    if (loading) return;
    setState(() {
      loading = true;
      error = null;
    });
    try {
      final value = await widget.gateway!.notificationDetails(
        entry.notificationId!,
      );
      if (mounted) setState(() => entry = value);
    } catch (failure) {
      if (mounted) setState(() => error = '详情未更新：$failure');
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: SelectableText(entry.name),
    content: SizedBox(
      width: 520,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (loading) const LinearProgressIndicator(),
            if (error != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 16),
                child: Text(error!),
              ),
            NotificationDetailsContent(entry: entry),
          ],
        ),
      ),
    ),
    actions: [
      if (widget.gateway != null && entry.notificationId != null)
        TextButton(
          onPressed: loading ? null : refresh,
          child: const Text('刷新详情'),
        ),
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('关闭'),
      ),
    ],
  );
}
