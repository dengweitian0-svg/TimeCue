import 'notebook_repository.dart';

/// 输入项与五字段 cron 互转；不计算实际触发日期或短月投影。
class ScheduleRules {
  // 兼容已保存的旧草稿数据，界面不再暴露逗号输入。
  static List<String> times(String text) {
    final values = <String>{};
    for (final token in text.split(RegExp('[,，]'))) {
      final match = RegExp(r'^(\d{1,2}):(\d{2})$').firstMatch(token.trim());
      if (match == null) throw const FormatException('无效的分钟级时间。');
      final hour = int.parse(match[1]!), minute = int.parse(match[2]!);
      if (hour > 23 || minute > 59) {
        throw const FormatException('时间范围为 00:00–23:59。');
      }
      values.add(
        '${hour.toString().padLeft(2, '0')}:${minute.toString().padLeft(2, '0')}',
      );
    }
    return values.toList()..sort();
  }

  static List<int> days(String text, int max) {
    final values = <int>{};
    for (final token in text.split(RegExp('[,，]'))) {
      final value = int.tryParse(token.trim());
      if (value == null || value < 1 || value > max) {
        throw FormatException('日期必须是 1–$max 的整数。');
      }
      values.add(value);
    }
    return values.toList()..sort();
  }

  static List<ScheduleTimeSlot> slots(ScheduleDraft draft) {
    if (draft.slots != null) return List.of(draft.slots!);
    if (draft.kind == ScheduleKind.single || draft.rawCron != null) return [];
    final clock = times((draft.times ?? [timeLabel(draft.time)]).join(','));
    final selected = draft.kind == ScheduleKind.daily
        ? <int?>[null]
        : days(
            (draft.days ??
                    [
                      draft.kind == ScheduleKind.weekly
                          ? draft.time.weekday
                          : draft.time.day,
                    ])
                .join(','),
            draft.kind == ScheduleKind.weekly ? 7 : 31,
          );
    return [
      for (final value in clock)
        for (final day in selected)
          ScheduleTimeSlot(
            draft.kind,
            int.parse(value.split(':')[0]),
            int.parse(value.split(':')[1]),
            day: day,
          ),
    ];
  }

  static List<String> encode(ScheduleDraft draft) {
    if (draft.rawCron != null) return List.of(draft.rawCron!);
    final values = slots(draft);
    if (values.isEmpty) throw const FormatException('请至少添加一个提醒时间。');
    final grouped = <String, Set<int>>{};
    for (final slot in values) {
      if (!slot.valid || slot.kind != draft.kind) {
        throw const FormatException('提醒时间必须使用同一种周期类型。');
      }
      final selected = grouped.putIfAbsent(slot.clockLabel, () => <int>{});
      if (slot.day != null) selected.add(slot.day!);
    }
    final clock = grouped.keys.toList()..sort();
    return [
      for (final value in clock)
        _cron(draft.kind, value, grouped[value]!.toList()..sort()),
    ];
  }

  static String _cron(ScheduleKind kind, String value, List<int> selected) {
    final clock = value.split(':').map(int.parse).toList();
    final suffix = switch (kind) {
      ScheduleKind.weekly => '* * ${selected.join(',')}',
      ScheduleKind.monthly => '${selected.join(',')} * *',
      _ => '* * *',
    };
    return '${clock[1]} ${clock[0]} $suffix';
  }

  static ScheduleDraft decode({
    required String name,
    required String description,
    required List<String> cron,
    required bool enabled,
    required DateTime anchor,
  }) {
    ScheduleKind? kind;
    final values = <ScheduleTimeSlot>{};
    try {
      for (final expression in cron) {
        final fields = expression.trim().split(RegExp(r'\s+'));
        if (fields.length != 5 || fields[3] != '*') {
          throw const FormatException();
        }
        final clock = times(
          '${fields[1]}:${fields[0].padLeft(2, '0')}',
        ).single.split(':').map(int.parse).toList();
        final current = fields[2] == '*'
            ? (fields[4] == '*' ? ScheduleKind.daily : ScheduleKind.weekly)
            : ScheduleKind.monthly;
        if ((current == ScheduleKind.monthly && fields[4] != '*') ||
            (kind != null && kind != current)) {
          throw const FormatException();
        }
        final selected = current == ScheduleKind.daily
            ? <int?>[null]
            : days(
                fields[current == ScheduleKind.weekly ? 4 : 2],
                current == ScheduleKind.weekly ? 7 : 31,
              );
        for (final day in selected) {
          values.add(ScheduleTimeSlot(current, clock[0], clock[1], day: day));
        }
        kind = current;
      }
      if (kind == null || values.isEmpty) throw const FormatException();
      final sorted = values.toList()
        ..sort((a, b) {
          final date = (a.day ?? 0).compareTo(b.day ?? 0);
          return date != 0 ? date : a.clockLabel.compareTo(b.clockLabel);
        });
      final clock = values.map((slot) => slot.clockLabel).toSet().toList()
        ..sort();
      final selected =
          values.map((slot) => slot.day).whereType<int>().toSet().toList()
            ..sort();
      final first = sorted.first;
      return ScheduleDraft(
        name: name,
        description: description,
        kind: kind,
        time: DateTime.utc(
          anchor.year,
          anchor.month,
          anchor.day,
          first.hour,
          first.minute,
        ),
        enabled: enabled,
        slots: List.unmodifiable(sorted),
        times: List.unmodifiable(clock),
        days: List.unmodifiable(selected),
      );
    } on FormatException {
      return ScheduleDraft(
        name: name,
        description: description,
        kind: ScheduleKind.daily,
        time: anchor,
        enabled: enabled,
        rawCron: List.unmodifiable(cron),
      );
    }
  }
}
