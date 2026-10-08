import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../../contracts/notebook_repository.dart';
import '../../contracts/schedule_rules.dart';
import '../../theme/notebook_theme.dart';
import 'notebook_date_time_picker.dart';
import 'schedule_slot_picker.dart';
import 'schedule_time_chip.dart';

class ScheduleEditorViewModel extends ChangeNotifier {
  ScheduleEditorViewModel(this.repository, this.original) {
    repository.addListener(_repositoryChanged);
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
    _slots = draft == null ? [] : ScheduleRules.slots(draft);
    singleTimeSelected = original != null;
  }
  void _repositoryChanged() => notifyListeners();
  final NotebookRepository repository;
  ScheduleSummary? original;
  late final TextEditingController name, description;
  bool singleTimeSelected = false;
  List<ScheduleTimeSlot> _slots = [];
  List<ScheduleTimeSlot> get slots => List.unmodifiable(_slots);
  bool get kindLocked =>
      original?.draft.kind == ScheduleKind.single ||
      original?.draft.rawCron != null ||
      (kind != ScheduleKind.single && _slots.isNotEmpty);
  void changeKind(ScheduleKind value) {
    if (busy ||
        readOnly ||
        kindLocked ||
        value == kind ||
        (original != null && value == ScheduleKind.single)) {
      return;
    }
    update(() {
      kind = value;
      _slots.clear();
      singleTimeSelected = false;
      error = null;
    });
  }

  bool addSlot(ScheduleTimeSlot slot) {
    if (busy || readOnly || original?.draft.rawCron != null) return false;
    if (!slot.valid || slot.kind != kind) {
      error = '提醒时间必须使用当前周期类型';
      notifyListeners();
      return false;
    }
    if (_slots.contains(slot)) {
      error = '这个提醒时间已添加';
      notifyListeners();
      return false;
    }
    update(() {
      _slots.add(slot);
      error = null;
    });
    return true;
  }

  void removeSlot(ScheduleTimeSlot slot) {
    if (busy || readOnly || original?.draft.rawCron != null) return;
    update(() {
      _slots.remove(slot);
      error = null;
    });
  }

  void removeSingleTime() {
    if (busy || timeLocked) return;
    update(() {
      singleTimeSelected = false;
      error = null;
    });
  }

  final String operationKey = 'editor-${DateTime.now().microsecondsSinceEpoch}';
  bool conflict = false;
  late ScheduleKind kind;
  late DateTime time;
  late bool enabled;
  bool busy = false;
  String? error;
  bool dirty = false;
  bool get readOnly => original?.deleted == true;
  bool get timeLocked =>
      readOnly ||
      original?.draft.rawCron != null ||
      (original != null &&
          kind == ScheduleKind.single &&
          !original!.draft.time.isAfter(repository.demoNow));
  void update(VoidCallback action) {
    action();
    dirty = true;
    notifyListeners();
  }

  Future<bool> save() async {
    if (busy || readOnly || !repository.canWrite) return false;
    if (name.text.trim().isEmpty) {
      error = '请填写日程名称';
      notifyListeners();
      return false;
    }
    if (kind == ScheduleKind.single && !singleTimeSelected) {
      error = '请选择日期和时间';
      notifyListeners();
      return false;
    }
    if (kind == ScheduleKind.single && timeLocked && original != null) {
      time = original!.draft.time;
    }
    if (kind == ScheduleKind.single &&
        !time.isAfter(repository.demoNow) &&
        (original == null || time != original!.draft.time)) {
      error = '请选择未来时间';
      notifyListeners();
      return false;
    }
    if (kind == ScheduleKind.single &&
        (time.second != 0 || time.millisecond != 0 || time.microsecond != 0)) {
      error = timeLocked
          ? '新版本仅支持分钟级时间，这条旧日程无法按原规则保存。历史仍保留，也可以删除日程。'
          : '新版本仅支持分钟级时间，请重新选择时间。';
      notifyListeners();
      return false;
    }
    if (kind != ScheduleKind.single &&
        original?.draft.rawCron == null &&
        _slots.isEmpty) {
      error = '请至少添加一个提醒时间';
      notifyListeners();
      return false;
    }
    busy = true;
    error = null;
    conflict = false;
    notifyListeners();
    try {
      await repository.save(
        ScheduleDraft(
          name: name.text.trim(),
          description: description.text,
          kind: kind,
          time: time,
          enabled: enabled,
          slots: kind == ScheduleKind.single ? null : List.unmodifiable(_slots),
          rawCron: original?.draft.rawCron,
        ),
        id: original?.id,
        expectedRevision: original?.revision,
        operationKey: operationKey,
      );
      dirty = false;
      return true;
    } catch (e) {
      error = '$e';
      conflict = e is NotebookApiException && e.code == 'revision_conflict';
      return false;
    } finally {
      busy = false;
      notifyListeners();
    }
  }

  Future<bool> delete() async {
    if (busy || original == null || !repository.canWrite) return false;
    busy = true;
    error = null;
    notifyListeners();
    try {
      await repository.delete(original!.id);
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

  Future<void> reload() async {
    if (busy || original == null) return;
    busy = true;
    notifyListeners();
    try {
      final latest = await repository.loadSchedule(original!.id);
      original = latest;
      name.text = latest.draft.name;
      description.text = latest.draft.description;
      kind = latest.draft.kind;
      time = latest.draft.time;
      singleTimeSelected = true;
      enabled = latest.draft.enabled;
      _slots = ScheduleRules.slots(latest.draft);
      dirty = false;
      conflict = false;
      error = null;
    } catch (e) {
      error = '$e';
    } finally {
      busy = false;
      notifyListeners();
    }
  }

  @override
  void dispose() {
    repository.removeListener(_repositoryChanged);
    name.dispose();
    description.dispose();
    super.dispose();
  }
}

Future<bool> allowDiscard(
  BuildContext context,
  ScheduleEditorViewModel model,
) async =>
    !model.busy &&
    (!model.dirty ||
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
            true);

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
  Future<void> _pickDateTime() async {
    final m = widget.model;
    final selected = await showNotebookDateTimePicker(context, m.time);
    if (selected != null && mounted) {
      m.update(() {
        m.time = selected;
        m.singleTimeSelected = true;
        m.error = null;
      });
    }
  }

  Future<void> _addTime() async {
    final m = widget.model;
    final slot = await showScheduleSlotPicker(
      context,
      kind: m.kind,
      initialTime: TimeOfDay(hour: m.time.hour, minute: m.time.minute),
      initialDay: m.kind == ScheduleKind.weekly ? m.time.weekday : m.time.day,
    );
    if (slot != null && mounted) m.addSlot(slot);
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
            readOnly: m.readOnly || m.busy,
            onChanged: (_) => m.dirty = true,
            decoration: const InputDecoration(labelText: '日程名称'),
            autofocus: true,
          ),
          const SizedBox(height: 18),
          TextField(
            controller: m.description,
            readOnly: m.readOnly || m.busy,
            onChanged: (_) => m.dirty = true,
            maxLines: 3,
            decoration: const InputDecoration(labelText: '说明'),
          ),
          const SizedBox(height: 18),
          DropdownButtonFormField<ScheduleKind>(
            key: ValueKey('${m.original?.revision}-${m.kind}'),
            initialValue: m.kind,
            decoration: const InputDecoration(labelText: '日程类型'),
            items: [
              if (m.original == null ||
                  m.original?.draft.kind == ScheduleKind.single)
                const DropdownMenuItem(
                  value: ScheduleKind.single,
                  child: Text('单次'),
                ),
              DropdownMenuItem(
                value: ScheduleKind.daily,
                child: Text(
                  m.original?.draft.rawCron == null ? '每天' : '复杂周期（保留原规则）',
                ),
              ),
              DropdownMenuItem(value: ScheduleKind.weekly, child: Text('每周')),
              DropdownMenuItem(value: ScheduleKind.monthly, child: Text('每月')),
            ],
            onChanged: m.readOnly || m.busy || m.kindLocked
                ? null
                : (value) => m.changeKind(value!),
          ),
          const SizedBox(height: 16),
          if (m.original?.draft.rawCron != null)
            Text(
              '当前规则暂不能在此修改，保留原规则：\n${m.original!.draft.rawCron!.join("\n")}',
              style: const TextStyle(color: NotebookTheme.muted),
            )
          else if (m.kind == ScheduleKind.single) ...[
            Wrap(
              spacing: 8,
              runSpacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                if (m.singleTimeSelected)
                  ScheduleTimeChip.single(
                    key: const ValueKey('single-selected-time'),
                    date: m.time,
                    onRemoved: m.timeLocked || m.busy
                        ? null
                        : m.removeSingleTime,
                  ),
                if (!m.singleTimeSelected)
                  Tooltip(
                    message: '选择日期和时间',
                    child: OutlinedButton(
                      key: const ValueKey('add-single-time'),
                      style: OutlinedButton.styleFrom(
                        minimumSize: const Size(44, 44),
                        padding: const EdgeInsets.all(10),
                        shape: const StadiumBorder(),
                      ),
                      onPressed: m.timeLocked || m.busy ? null : _pickDateTime,
                      child: const Icon(Icons.add, size: 20),
                    ),
                  ),
              ],
            ),
          ] else ...[
            Wrap(
              spacing: 8,
              runSpacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                for (final slot in m.slots)
                  ScheduleTimeChip(
                    key: ValueKey(slot),
                    slot: slot,
                    onRemoved: m.readOnly || m.busy
                        ? null
                        : () => m.removeSlot(slot),
                  ),
                Tooltip(
                  message: '添加提醒时间',
                  child: OutlinedButton(
                    key: const ValueKey('add-period-time'),
                    style: OutlinedButton.styleFrom(
                      minimumSize: const Size(44, 44),
                      padding: const EdgeInsets.all(10),
                      shape: const StadiumBorder(),
                    ),
                    onPressed: m.readOnly || m.busy ? null : _addTime,
                    child: const Icon(Icons.add, size: 20),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              m.slots.isEmpty ? '点击＋添加提醒时间。' : '删除全部提醒时间后，可切换周期类型。',
              style: const TextStyle(fontSize: 12, color: NotebookTheme.muted),
            ),
          ],
          if (m.timeLocked && m.kind == ScheduleKind.single)
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
              onChanged: m.readOnly || m.busy
                  ? null
                  : (value) => m.update(() => m.enabled = value),
            ),
          const SizedBox(height: 16),
          Text(
            m.repository.serverPagination
                ? '保存后更新未来安排，历史提醒保留。'
                : '示例模式：修改仅保留在本次运行。历史提醒不随未来规则修改。',
            style: TextStyle(
              fontSize: 11,
              height: 1.7,
              color: NotebookTheme.muted,
            ),
          ),
          if (!m.repository.canWrite) const Text('提醒服务暂不可用，请恢复连接后保存。'),
          if (m.error != null)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Text(
                m.error!,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ),
          if (m.conflict)
            TextButton(
              onPressed: m.busy
                  ? null
                  : () async {
                      if (await allowDiscard(context, m)) await m.reload();
                    },
              child: const Text('重新加载最新版本'),
            ),
          const SizedBox(height: 20),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (!m.readOnly)
                FilledButton(
                  onPressed: m.busy || !m.repository.canWrite
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
                  onPressed: m.busy || !m.repository.canWrite
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
                            if (await m.delete() && mounted) widget.onSaved();
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
                    onPressed: model.busy ? null : close,
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
