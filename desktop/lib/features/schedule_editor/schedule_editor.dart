import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../../contracts/notebook_repository.dart';
import '../../theme/notebook_theme.dart';
import 'notebook_time_picker.dart';

class ScheduleEditorViewModel extends ChangeNotifier {
  ScheduleEditorViewModel(this.repository, this.original) {
    final draft = original?.draft;
    name = TextEditingController(text: draft?.name ?? '');
    description = TextEditingController(text: draft?.description ?? '');
    kind = draft?.kind ?? ScheduleKind.single;
    time =
        draft?.time ??
        repository.demoNow
            .add(const Duration(hours: 1))
            .copyWith(second: 0, millisecond: 0, microsecond: 0);
    enabled = draft?.enabled ?? true;
  }
  final NotebookRepository repository;
  final ScheduleSummary? original;
  late final TextEditingController name, description;
  late ScheduleKind kind;
  late DateTime time;
  late bool enabled;
  bool busy = false;
  String? error;
  bool dirty = false;
  bool get readOnly => original?.deleted == true;
  bool get timeLocked =>
      readOnly ||
      (original != null &&
          kind == ScheduleKind.single &&
          !original!.draft.time.isAfter(repository.demoNow));
  void update(VoidCallback action) {
    action();
    dirty = true;
    notifyListeners();
  }

  Future<bool> save() async {
    if (busy || readOnly) return false;
    if (name.text.trim().isEmpty) {
      error = '请填写日程名称';
      notifyListeners();
      return false;
    }
    busy = true;
    error = null;
    notifyListeners();
    try {
      await repository.save(
        ScheduleDraft(
          name: name.text.trim(),
          description: description.text,
          kind: kind,
          time: time,
          enabled: enabled,
        ),
        id: original?.id,
      );
      dirty = false;
      return true;
    } catch (e) {
      error = '$e';
      return false;
    } finally {
      busy = false;
      notifyListeners();
    }
  }

  @override
  void dispose() {
    name.dispose();
    description.dispose();
    super.dispose();
  }
}

Future<bool> allowDiscard(
  BuildContext context,
  ScheduleEditorViewModel model,
) async =>
    !model.dirty ||
    await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('舍弃未保存的修改？'),
            content: const Text('这次填写的内容还没有保存。'),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('继续编辑'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('舍弃修改'),
              ),
            ],
          ),
        ) ==
        true;

class ScheduleEditor extends StatefulWidget {
  const ScheduleEditor({
    super.key,
    required this.model,
    required this.onSaved,
    required this.onCancel,
  });
  final ScheduleEditorViewModel model;
  final VoidCallback onSaved, onCancel;
  @override
  State<ScheduleEditor> createState() => _ScheduleEditorState();
}

class _ScheduleEditorState extends State<ScheduleEditor> {
  Future<void> _pickDate() async {
    final m = widget.model;
    final date = await showDatePicker(
      context: context,
      initialDate: m.time,
      firstDate: DateTime(2020),
      lastDate: DateTime(2100),
    );
    if (date != null && mounted) {
      m.update(
        () => m.time = DateTime.utc(
          date.year,
          date.month,
          date.day,
          m.time.hour,
          m.time.minute,
        ),
      );
    }
  }

  Future<void> _pickTime() async {
    final m = widget.model;
    final time = await showNotebookTimePicker(
      context,
      TimeOfDay(hour: m.time.hour, minute: m.time.minute),
    );
    if (time != null && mounted) {
      m.update(
        () => m.time = DateTime.utc(
          m.time.year,
          m.time.month,
          m.time.day,
          time.hour,
          time.minute,
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.model,
    builder: (context, _) {
      final m = widget.model;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          TextField(
            controller: m.name,
            readOnly: m.readOnly,
            onChanged: (_) => m.dirty = true,
            decoration: const InputDecoration(labelText: '日程名称'),
            autofocus: true,
          ),
          const SizedBox(height: 18),
          TextField(
            controller: m.description,
            readOnly: m.readOnly,
            onChanged: (_) => m.dirty = true,
            maxLines: 3,
            decoration: const InputDecoration(labelText: '说明'),
          ),
          const SizedBox(height: 18),
          DropdownButtonFormField<ScheduleKind>(
            initialValue: m.kind,
            decoration: const InputDecoration(labelText: '日程类型'),
            items: const [
              DropdownMenuItem(value: ScheduleKind.single, child: Text('单次')),
              DropdownMenuItem(value: ScheduleKind.daily, child: Text('每天')),
              DropdownMenuItem(value: ScheduleKind.weekly, child: Text('每周')),
              DropdownMenuItem(value: ScheduleKind.monthly, child: Text('每月')),
            ],
            onChanged: m.original != null
                ? null
                : (value) => m.update(() => m.kind = value!),
          ),
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (m.kind != ScheduleKind.daily)
                OutlinedButton(
                  onPressed: m.timeLocked ? null : _pickDate,
                  child: Text(
                    '${m.time.year}-${m.time.month}-${m.time.day}${m.kind == ScheduleKind.weekly
                        ? ' · 星期${['一', '二', '三', '四', '五', '六', '日'][m.time.weekday - 1]}'
                        : m.kind == ScheduleKind.monthly
                        ? ' · 每月此日'
                        : ''}',
                  ),
                ),
              OutlinedButton(
                onPressed: m.timeLocked ? null : _pickTime,
                child: Text('${timeLabel(m.time)} · 北京时间'),
              ),
            ],
          ),
          if (m.timeLocked)
            const Padding(
              padding: EdgeInsets.only(top: 12),
              child: Text(
                '已到期单次日程的触发时间只读。',
                style: TextStyle(color: NotebookTheme.muted, fontSize: 12),
              ),
            ),
          if (m.kind != ScheduleKind.single)
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: Text(m.enabled ? '周期已启用' : '周期已暂停'),
              subtitle: const Text('恢复从下一次未来触发继续'),
              value: m.enabled,
              onChanged: m.readOnly
                  ? null
                  : (value) => m.update(() => m.enabled = value),
            ),
          const SizedBox(height: 16),
          const Text(
            '示例模式：修改仅保留在本次运行。历史提醒不随未来规则修改。',
            style: TextStyle(
              fontSize: 11,
              height: 1.7,
              color: NotebookTheme.muted,
            ),
          ),
          if (m.error != null)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Text(
                m.error!,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ),
          const SizedBox(height: 20),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (!m.readOnly)
                FilledButton(
                  onPressed: m.busy
                      ? null
                      : () async {
                          if (await m.save() && mounted) widget.onSaved();
                        },
                  child: Text(m.busy ? '保存中…' : '保存日程'),
                ),
              TextButton(
                onPressed: m.busy ? null : widget.onCancel,
                child: const Text('返回'),
              ),
              if (m.original != null && !m.readOnly)
                TextButton(
                  onPressed: m.busy
                      ? null
                      : () async {
                          final yes = await showDialog<bool>(
                            context: context,
                            builder: (context) => AlertDialog(
                              title: const Text('永久删除这条日程？'),
                              content: const Text('未来安排将停止，历史提醒仍保留。删除后不可恢复。'),
                              actions: [
                                TextButton(
                                  onPressed: () =>
                                      Navigator.pop(context, false),
                                  child: const Text('取消'),
                                ),
                                FilledButton(
                                  onPressed: () => Navigator.pop(context, true),
                                  child: const Text('删除'),
                                ),
                              ],
                            ),
                          );
                          if (yes == true) {
                            await m.repository.delete(m.original!.id);
                            if (mounted) widget.onSaved();
                          }
                        },
                  child: const Text('删除'),
                ),
            ],
          ),
        ],
      );
    },
  );
}

class ScheduleCreateDialog extends StatefulWidget {
  const ScheduleCreateDialog({super.key, required this.repository});
  final NotebookRepository repository;
  @override
  State<ScheduleCreateDialog> createState() => _ScheduleCreateDialogState();
}

class _ScheduleCreateDialogState extends State<ScheduleCreateDialog> {
  late final model = ScheduleEditorViewModel(widget.repository, null);
  Future<void> close() async {
    if (await allowDiscard(context, model) && mounted) Navigator.pop(context);
  }

  @override
  void dispose() {
    model.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => CallbackShortcuts(
    bindings: {const SingleActivator(LogicalKeyboardKey.escape): close},
    child: Dialog(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(28),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      '添一件小事',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                  ),
                  IconButton(
                    onPressed: close,
                    tooltip: '关闭',
                    icon: const Icon(Icons.close),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              const Text(
                '留一点时间，给日常。',
                style: TextStyle(color: NotebookTheme.muted),
              ),
              const SizedBox(height: 24),
              ScheduleEditor(
                model: model,
                onSaved: () => Navigator.pop(context),
                onCancel: close,
              ),
            ],
          ),
        ),
      ),
    ),
  );
}
