import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import '../../contracts/schedule_rules.dart';
import '../schedule_editor/schedule_editor.dart';

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
  bool editing = false;
  String? error;

  String details(ScheduleDraft draft) {
    final status = draft.kind == ScheduleKind.single
        ? '单次日程'
        : draft.enabled
        ? '周期已启用'
        : '周期已暂停 · 可重新开启';
    final rule = draft.kind == ScheduleKind.single
        ? '${draft.time.year} 年 ${draft.time.month} 月 ${draft.time.day} 日 ${timeLabel(draft.time)} · 北京时间'
        : draft.rawCron?.join('；') ??
              ScheduleRules.slots(draft)
                  .map(
                    (slot) => draft.kind == ScheduleKind.daily
                        ? '每天 ${slot.label}'
                        : slot.label,
                  )
                  .join('；');
    return [
      status,
      if (draft.description.isNotEmpty) draft.description,
      rule,
    ].join('\n');
  }

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

  Future<void> edit(String id) async {
    if (loading || editing) return;
    setState(() {
      editing = true;
      loading = true;
      error = null;
    });
    try {
      final original = await widget.repository.loadSchedule(id);
      if (!mounted) return;
      setState(() => loading = false);
      final changed = await showDialog<bool>(
        context: context,
        barrierDismissible: false,
        builder: (_) => ScheduleEditDialog(
          repository: widget.repository,
          original: original,
        ),
      );
      if (mounted && changed == true) await load(offset);
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) {
        setState(() {
          editing = false;
          loading = false;
        });
      }
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
              onPressed: loading || editing ? null : () => load(offset),
              child: Text('$error · 重试'),
            ),
          const Text('包含已暂停的日程，点击查看详情或重新开启。'),
          const SizedBox(height: 8),
          Expanded(
            child: ListView(
              children: [
                if (items.isEmpty && !loading && error == null)
                  const Text('还没有日程。'),
                for (final item in items)
                  ListTile(
                    title: Text(item.draft.name),
                    subtitle: Text(details(item.draft)),
                    trailing: const Tooltip(
                      message: '查看日程详情',
                      child: Icon(Icons.chevron_right),
                    ),
                    onTap: loading || editing ? null : () => edit(item.id),
                  ),
              ],
            ),
          ),
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              TextButton(
                onPressed: loading || editing || offset == 0
                    ? null
                    : () => load(offset - 50),
                child: const Text('上一页'),
              ),
              TextButton(
                onPressed: loading || editing || items.length < 50
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
