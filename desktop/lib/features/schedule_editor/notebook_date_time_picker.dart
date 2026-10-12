import 'package:flutter/material.dart';
import '../../theme/notebook_theme.dart';
import '../../contracts/notebook_repository.dart';
import 'notebook_time_picker.dart';

/// 日期与分钟级时间作为一份临时草稿，确认时一起返回北京时间墙上字段。
Future<DateTime?> showNotebookDateTimePicker(
  BuildContext context,
  DateTime initial,
) {
  var date = DateTime(initial.year, initial.month, initial.day);
  return showDialog<DateTime>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, update) => NotebookTimePicker(
        initialTime: TimeOfDay(hour: initial.hour, minute: initial.minute),
        title: '选择日期和时间',
        leftPane: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              '选择日期',
              style: TextStyle(fontSize: 16, color: NotebookTheme.muted),
            ),
            CalendarDatePicker(
              initialDate: date,
              currentDate: beijingNow(),
              firstDate: DateTime(1970),
              lastDate: DateTime(2200, 12, 31),
              onDateChanged: (value) => update(() => date = value),
            ),
            Text(
              '${date.year} 年 ${date.month} 月 ${date.day} 日',
              style: const TextStyle(color: NotebookTheme.muted),
            ),
          ],
        ),
        onSelected: (time) => Navigator.pop(
          context,
          DateTime.utc(date.year, date.month, date.day, time.hour, time.minute),
        ),
      ),
    ),
  );
}
