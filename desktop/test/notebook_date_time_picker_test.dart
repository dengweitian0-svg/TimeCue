import 'package:flutter/material.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';
import 'package:timecue_desktop/features/schedule_editor/notebook_date_time_picker.dart';
import 'package:timecue_desktop/features/schedule_editor/notebook_time_picker.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  testWidgets('单次通过＋打开合并弹层，左日历右时间，确认生成一个时间项', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    final model = ScheduleEditorViewModel(repo, null);
    final initial = DateTime.utc(2026, 10, 8, 17, 48);
    model.time = initial;
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: Center(
            child: SizedBox(
              width: 480,
              child: SingleChildScrollView(
                child: ScheduleEditor(
                  model: model,
                  onSaved: () {},
                  onCancel: () {},
                ),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await tester.pumpAndSettle();
    expect(find.text('选择日期和时间'), findsOneWidget);
    expect(find.byType(CalendarDatePicker), findsOneWidget);
    expect(find.byType(NotebookTimeDial), findsOneWidget);
    expect(
      tester.getRect(find.byType(CalendarDatePicker)).right,
      lessThan(
        tester.getRect(find.byKey(const ValueKey('time-field-小时'))).left,
      ),
    );
    expect(find.textContaining('每格'), findsNothing);
    await tester.tap(
      find.descendant(
        of: find.byType(CalendarDatePicker),
        matching: find.text('9'),
      ),
    );
    await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '20');
    await tester.enterText(find.byKey(const ValueKey('time-field-分钟')), '11');
    expect(model.time, initial);
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    expect(model.time, DateTime.utc(2026, 10, 9, 20, 11));
    expect(find.text('2026-10-9 20:11 · 北京时间'), findsOneWidget);
    expect(find.byKey(const ValueKey('add-single-time')), findsNothing);
    await tester.tap(find.byKey(const ValueKey('single-selected-time')));
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<CalendarDatePicker>(find.byType(CalendarDatePicker))
          .initialDate,
      DateTime(2026, 10, 9),
    );
    expect(
      tester
          .widget<NotebookTimePicker>(find.byType(NotebookTimePicker))
          .initialTime,
      const TimeOfDay(hour: 20, minute: 11),
    );
    await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '22');
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(model.time, DateTime.utc(2026, 10, 9, 20, 11));
    await tester.tap(find.byKey(const ValueKey('single-selected-time')));
    await tester.pumpAndSettle();
    await tester.tap(
      find.descendant(
        of: find.byType(CalendarDatePicker),
        matching: find.text('10'),
      ),
    );
    await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '21');
    await tester.enterText(find.byKey(const ValueKey('time-field-分钟')), '32');
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    expect(model.time, DateTime.utc(2026, 10, 10, 21, 32));
    expect(find.byKey(const ValueKey('add-single-time')), findsNothing);
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(
      tester.getCenter(find.byKey(const ValueKey('single-selected-time'))),
    );
    await tester.pump();
    await tester.tap(find.byTooltip('删除2026-10-10 21:32 · 北京时间'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('single-selected-time')), findsNothing);
    expect(find.byKey(const ValueKey('add-single-time')), findsOneWidget);
    expect(find.byType(AlertDialog), findsNothing);
    await mouse.removePointer();
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await tester.pumpAndSettle();
    expect(find.byType(CalendarDatePicker), findsOneWidget);
    expect(
      tester
          .widget<NotebookTimePicker>(find.byType(NotebookTimePicker))
          .initialTime,
      const TimeOfDay(hour: 21, minute: 32),
    );
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    await tester.pumpWidget(const SizedBox());
    model.dispose();
    repo.dispose();
  });
  testWidgets('取消合并弹层不修改日期或时间', (tester) async {
    DateTime? result;
    final initial = DateTime.utc(2026, 10, 8, 17, 48);
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () async {
                result = await showNotebookDateTimePicker(context, initial);
              },
              child: const Text('打开'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    await tester.tap(
      find.descendant(
        of: find.byType(CalendarDatePicker),
        matching: find.text('9'),
      ),
    );
    await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '19');
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(result, isNull);
    expect(find.byType(CalendarDatePicker), findsNothing);
  });
  testWidgets('小窗口保持同一弹层并允许横向访问日期和时间', (tester) async {
    tester.view.physicalSize = const Size(400, 650);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () => showNotebookDateTimePicker(
                context,
                DateTime.utc(2026, 10, 8, 17, 48),
              ),
              child: const Text('打开'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    final horizontal = find.byWidgetPredicate(
      (widget) =>
          widget is SingleChildScrollView &&
          widget.scrollDirection == Axis.horizontal,
    );
    expect(horizontal, findsOneWidget);
    await tester.drag(horizontal, const Offset(-500, 0));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });
}
