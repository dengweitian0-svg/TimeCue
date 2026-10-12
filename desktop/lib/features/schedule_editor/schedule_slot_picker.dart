import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import '../../theme/notebook_theme.dart';
import 'notebook_time_picker.dart';

/// 一次确认返回一个日期和时间组合，添加或替换由外层表单决定。
Future<ScheduleTimeSlot?> showScheduleSlotPicker(
  BuildContext context, {
  required ScheduleKind kind,
  required TimeOfDay initialTime,
  required int initialDay,
}) async {
  if (kind == ScheduleKind.daily) {
    final time = await showNotebookTimePicker(context, initialTime);
    return time == null ? null : ScheduleTimeSlot(kind, time.hour, time.minute);
  }
  if (kind == ScheduleKind.single) throw ArgumentError('单次日程不使用周期时间选择');
  final weekly = kind == ScheduleKind.weekly;
  var day = initialDay.clamp(1, weekly ? 7 : 31);
  return showDialog<ScheduleTimeSlot>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, update) {
        final selector = Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              weekly ? '每周提醒日' : '每月提醒日',
              style: const TextStyle(color: NotebookTheme.muted),
            ),
            const SizedBox(height: 8),
            if (weekly)
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (var value = 1; value <= 7; value++)
                    ChoiceChip(
                      key: ValueKey('period-day-$value'),
                      showCheckmark: false,
                      label: Text(
                        '周${['一', '二', '三', '四', '五', '六', '日'][value - 1]}',
                      ),
                      selected: value == day,
                      onSelected: (_) => update(() => day = value),
                    ),
                ],
              )
            else ...[
              LayoutBuilder(
                builder: (context, constraints) => GridView(
                  shrinkWrap: true,
                  physics: const NeverScrollableScrollPhysics(),
                  gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                    crossAxisCount:
                        (constraints.maxWidth /
                                (44 *
                                        MediaQuery.textScalerOf(
                                          context,
                                        ).scale(14) /
                                        14 +
                                    4))
                            .floor()
                            .clamp(3, 7),
                    mainAxisExtent:
                        52 * MediaQuery.textScalerOf(context).scale(14) / 14,
                    mainAxisSpacing: 4,
                    crossAxisSpacing: 4,
                  ),
                  children: [
                    for (var value = 1; value <= 31; value++)
                      Center(
                        child: ChoiceChip(
                          key: ValueKey('period-day-$value'),
                          showCheckmark: false,
                          padding: EdgeInsets.zero,
                          labelPadding: const EdgeInsets.symmetric(
                            horizontal: 6,
                          ),
                          label: Text('$value'),
                          selected: value == day,
                          onSelected: (_) => update(() => day = value),
                        ),
                      ),
                  ],
                ),
              ),
              const SizedBox(height: 8),
              const Text(
                '当月不足所选日号时，按月末提醒。',
                style: TextStyle(fontSize: 12, color: NotebookTheme.muted),
              ),
            ],
          ],
        );
        return NotebookTimePicker(
          initialTime: initialTime,
          title: weekly ? '选择星期和时间' : '选择日期和时间',
          selection: weekly ? selector : null,
          leftPane: weekly ? null : selector,
          onSelected: (time) => Navigator.pop(
            context,
            ScheduleTimeSlot(kind, time.hour, time.minute, day: day),
          ),
        );
      },
    ),
  );
}
