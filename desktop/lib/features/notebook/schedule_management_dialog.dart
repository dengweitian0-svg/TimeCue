import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';

/// 管理列表独立于日历投影，暂停规则仍有恢复入口。
class ScheduleManagementDialog extends StatefulWidget {
  const ScheduleManagementDialog({super.key, required this.repository});
  final NotebookRepository repository;
  @override
  State<ScheduleManagementDialog> createState() =>
      _ScheduleManagementDialogState();
}

class _ScheduleManagementDialogState extends State<ScheduleManagementDialog> {
  List<ScheduleSummary> items = const [];
  int offset = 0;
  bool loading = true;
  String? error;
  @override
  void initState() {
    super.initState();
    load(0);
  }

  Future<void> load(int target) async {
    setState(() {
      loading = true;
      error = null;
    });
    try {
      final values = await widget.repository.listSchedules(offset: target);
      if (mounted) {
        setState(() {
          items = values;
          offset = target;
        });
      }
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('管理日程'),
    content: SizedBox(
      width: 440,
      height: 350,
      child: Column(
        children: [
          if (loading) const LinearProgressIndicator(),
          if (error != null)
            TextButton(
              onPressed: () => load(offset),
              child: Text('$error · 重试'),
            ),
          Expanded(
            child: ListView(
              children: [
                if (items.isEmpty && !loading && error == null)
                  const Text('还没有日程。'),
                for (final item in items)
                  ListTile(
                    title: Text(item.draft.name),
                    subtitle: Text(
                      item.draft.kind == ScheduleKind.single
                          ? '单次日程'
                          : item.draft.enabled
                          ? '周期已启用'
                          : '周期已暂停 · 可恢复',
                    ),
                    trailing: const Icon(Icons.chevron_right),
                    onTap: loading
                        ? null
                        : () => Navigator.pop(context, item.id),
                  ),
              ],
            ),
          ),
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              TextButton(
                onPressed: loading || offset == 0
                    ? null
                    : () => load(offset - 50),
                child: const Text('上一页'),
              ),
              TextButton(
                onPressed: loading || items.length < 50
                    ? null
                    : () => load(offset + 50),
                child: const Text('下一页'),
              ),
            ],
          ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('关闭'),
      ),
    ],
  );
}
