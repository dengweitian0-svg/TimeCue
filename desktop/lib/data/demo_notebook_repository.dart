import '../contracts/notebook_repository.dart';
import '../contracts/schedule_rules.dart';

/// 显式内存示例仓储，不连接 Go，也不写入 SQLite。
class DemoNotebookRepository extends NotebookRepository {
  DemoNotebookRepository({DateTime? now, DateTime Function()? clock})
    : _now = now ?? clock?.call() ?? DateTime.utc(2026, 10, 7, 14, 32),
      _clock = clock {
    final names = ['读一会儿书', '喝水，伸个懒腰', '整理本周计划', '整理一下桌面', '睡前放松'];
    final hours = [9, 13, 15, 16, 20];
    for (var i = 0; i < names.length; i++) {
      final at = DateTime.utc(
        demoNow.year,
        demoNow.month,
        demoNow.day,
        hours[i],
        i == 0 ? 30 : 0,
      );
      _schedules['s$i'] = ScheduleSummary(
        's$i',
        ScheduleDraft(
          name: names[i],
          description: i == 1 ? '离开屏幕，给自己一点休息。' : '一步一步，慢慢来。',
          kind: i == 1 ? ScheduleKind.daily : ScheduleKind.single,
          time: at,
        ),
      );
      _triggers.add(
        TriggerSummary(
          id: 'n$i',
          scheduleId: 's$i',
          name: names[i],
          plannedAt: at,
          acknowledgedAt: i == 0 ? at : null,
          delivery: i == 1 ? 'failed' : 'pending',
        ),
      );
    }
    for (var i = 1; i <= 5; i++) {
      _triggers.add(
        TriggerSummary(
          id: 'history$i',
          scheduleId: 's1',
          name: names[1],
          plannedAt: DateTime.utc(
            demoNow.year,
            demoNow.month,
            demoNow.day - i,
            13,
          ),
        ),
      );
    }
  }
  @override
  DateTime get demoNow => _clock?.call() ?? _now;
  final DateTime _now;
  final DateTime Function()? _clock;
  @override
  Future<void> refresh({DateTime? month, DateTime? date}) async =>
      notifyListeners();
  final Map<String, ScheduleSummary> _schedules = {};
  final List<TriggerSummary> _triggers = [];
  int _sequence = 0;
  @override
  List<TriggerSummary> get today => List.unmodifiable(entriesOn(demoNow));
  @override
  List<TriggerSummary> get pending {
    final values = _triggers
        .where(
          (entry) =>
              !entry.plannedAt.isAfter(demoNow) && entry.acknowledgedAt == null,
        )
        .toList();
    values.sort((a, b) {
      final comparison = a.plannedAt.compareTo(b.plannedAt);
      return comparison != 0 ? comparison : a.id.compareTo(b.id);
    });
    return List.unmodifiable(values);
  }

  @override
  ScheduleSummary schedule(String id) => _schedules[id]!;
  @override
  List<TriggerSummary> entriesOn(DateTime date) {
    final result = _triggers.where((e) => sameDay(e.plannedAt, date)).toList();
    // 仅展开示例预设用于视觉演示；正式日期清单由 Go 查询生成。
    for (final s in _schedules.values) {
      if (s.deleted ||
          !s.draft.enabled ||
          s.draft.kind == ScheduleKind.single) {
        continue;
      }
      final d = s.draft;
      for (final slot in ScheduleRules.slots(d)) {
        if (d.kind == ScheduleKind.weekly && date.weekday != slot.day) continue;
        final lastDay = DateTime.utc(date.year, date.month + 1, 0).day;
        if (d.kind == ScheduleKind.monthly &&
            date.day != (slot.day! > lastDay ? lastDay : slot.day!)) {
          continue;
        }
        final at = DateTime.utc(
          date.year,
          date.month,
          date.day,
          slot.hour,
          slot.minute,
        );
        if (!at.isAfter(demoNow) ||
            result.any((e) => e.scheduleId == s.id && e.plannedAt == at)) {
          continue;
        }
        result.add(
          TriggerSummary(
            id: 'preview-${s.id}-$at',
            scheduleId: s.id,
            name: d.name,
            plannedAt: at,
          ),
        );
      }
    }
    result.sort((a, b) => a.plannedAt.compareTo(b.plannedAt));
    return List.unmodifiable(result);
  }

  @override
  Future<List<ScheduleSummary>> listSchedules({int offset = 0}) async =>
      _schedules.values.where((s) => !s.deleted).skip(offset).take(50).toList();

  @override
  Future<void> confirm(Set<String> ids) async {
    final eligible = pending.map((e) => e.id).toSet();
    if (!eligible.containsAll(ids)) throw StateError('只能确认已到期且未确认的提醒');
    for (var i = 0; i < _triggers.length; i++) {
      if (ids.contains(_triggers[i].id)) {
        _triggers[i] = _triggers[i].acknowledge(demoNow);
      }
    }
    notifyListeners();
  }

  @override
  Future<void> save(
    ScheduleDraft draft, {
    String? id,
    int? expectedRevision,
    String? operationKey,
  }) async {
    final key = id ?? 'created${_sequence++}';
    final old = _schedules[key];
    if (old?.deleted == true) throw StateError('已删除的日程不可恢复');
    if (old != null &&
        (old.draft.kind == ScheduleKind.single) !=
            (draft.kind == ScheduleKind.single)) {
      throw StateError('编辑不能更改日程类型');
    }
    if (draft.time.second != 0 ||
        draft.time.millisecond != 0 ||
        draft.time.microsecond != 0) {
      throw StateError('提醒只支持分钟精度');
    }
    if (draft.kind == ScheduleKind.single &&
        !draft.time.isAfter(demoNow) &&
        (old == null || old.draft.time != draft.time)) {
      throw StateError('请选择未来时间');
    }
    if (draft.kind != ScheduleKind.single) ScheduleRules.encode(draft);
    _schedules[key] = ScheduleSummary(key, draft);
    _triggers.removeWhere(
      (e) => e.scheduleId == key && e.plannedAt.isAfter(demoNow),
    );
    if (draft.kind == ScheduleKind.single && draft.time.isAfter(demoNow)) {
      _triggers.add(
        TriggerSummary(
          id: 'trigger-$key',
          scheduleId: key,
          name: draft.name,
          plannedAt: draft.time,
        ),
      );
    }
    notifyListeners();
  }

  @override
  Future<void> delete(String id) async {
    _schedules[id] = ScheduleSummary(id, schedule(id).draft, deleted: true);
    _triggers.removeWhere(
      (e) => e.scheduleId == id && e.plannedAt.isAfter(demoNow),
    );
    notifyListeners();
  }
}
