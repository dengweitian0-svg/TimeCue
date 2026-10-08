import 'dart:math' as math;
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/features/schedule_editor/notebook_time_picker.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  testWidgets('左侧直接输入校验合法范围，并实时联动转盘', (tester) async {
    TimeOfDay? result;
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () async {
                result = await showNotebookTimePicker(
                  context,
                  const TimeOfDay(hour: 15, minute: 32),
                );
              },
              child: const Text('选择'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('选择'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '24');
    await tester.tap(find.text('确定'));
    await tester.pump();
    expect(find.text('小时请输入 0–23，分钟请输入 0–59'), findsOneWidget);
    await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '23');
    await tester.pump();
    expect(
      tester.widget<NotebookTimeDial>(find.byType(NotebookTimeDial)).value,
      32,
    );
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('time-field-分钟')))
          .focusNode!
          .hasFocus,
      isTrue,
    );
    await tester.enterText(find.byKey(const ValueKey('time-field-分钟')), '58');
    await tester.pump();
    expect(
      tester.widget<NotebookTimeDial>(find.byType(NotebookTimeDial)).value,
      58,
    );
    await tester.tap(find.byKey(const ValueKey('dial-number-35')));
    await tester.pump();
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('time-field-分钟')))
          .controller!
          .text,
      '35',
    );
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    expect(result, const TimeOfDay(hour: 23, minute: 35));
  });
  testWidgets('小时输满两位后选中分钟，未完成或越界小时不跳转', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: const NotebookTimePicker(
          initialTime: TimeOfDay(hour: 15, minute: 32),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final hour = find.byKey(const ValueKey('time-field-小时'));
    final minute = find.byKey(const ValueKey('time-field-分钟'));
    await tester.enterText(hour, '1');
    await tester.pump();
    expect(tester.widget<TextField>(hour).focusNode!.hasFocus, isTrue);
    await tester.enterText(hour, '24');
    await tester.pump();
    expect(tester.widget<TextField>(hour).focusNode!.hasFocus, isTrue);
    await tester.enterText(hour, '09');
    await tester.pump();
    final minuteField = tester.widget<TextField>(minute);
    expect(minuteField.focusNode!.hasFocus, isTrue);
    expect(
      minuteField.controller!.selection,
      const TextSelection(baseOffset: 0, extentOffset: 2),
    );
    expect(
      tester.widget<NotebookTimeDial>(find.byType(NotebookTimeDial)).hours,
      isFalse,
    );
    await tester.enterText(minute, '45');
    await tester.pump();
    expect(
      tester.widget<NotebookTimeDial>(find.byType(NotebookTimeDial)).value,
      45,
    );
  });
  test('小时双圈与分钟刻度精确吸附，保留任意分钟', () {
    final hour = NotebookTimeDial(
      hours: true,
      value: 15,
      onChanged: (_) {},
      onStep: (_) {},
    );
    final minute = NotebookTimeDial(
      hours: false,
      value: 32,
      onChanged: (_) {},
      onStep: (_) {},
    );
    Offset point(double tick, int count, double radius) =>
        const Offset(150, 150) +
        Offset(
              math.sin(tick * math.pi * 2 / count),
              -math.cos(tick * math.pi * 2 / count),
            ) *
            radius;
    expect(hour.snap(point(3.2, 12, 118), 300), 3);
    expect(hour.snap(point(3.2, 12, 76), 300), 15);
    expect(hour.snap(point(11.7, 12, 118), 300), 0);
    expect(minute.snap(point(32.2, 60, 118), 300), 32);
    expect(minute.snap(point(59.8, 60, 118), 300), 0);
  });
  testWidgets('小时与分钟均高亮当前数字，滚轮逐格调整，确认返回结果', (tester) async {
    tester.view.physicalSize = const Size(1000, 700);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    TimeOfDay? result;
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () async {
                result = await showNotebookTimePicker(
                  context,
                  const TimeOfDay(hour: 15, minute: 32),
                );
              },
              child: const Text('选择'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('选择'));
    await tester.pumpAndSettle();
    Text digit(int value) => tester.widget<Text>(
      find.descendant(
        of: find.byKey(ValueKey('dial-number-$value')),
        matching: find.byType(Text),
      ),
    );
    expect(digit(15).style!.fontWeight, FontWeight.w700);
    await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
    await tester.pump();
    expect(digit(16).style!.fontWeight, FontWeight.w700);
    await tester.tap(find.byKey(const ValueKey('time-field-分钟')));
    await tester.pump();
    expect(digit(32).style!.fontWeight, FontWeight.w700);
    final dial = find.byType(NotebookTimeDial);
    await tester.sendEventToBinding(
      PointerScrollEvent(
        position: tester.getCenter(dial),
        scrollDelta: const Offset(0, 80),
      ),
    );
    await tester.pump();
    expect(digit(33).style!.fontWeight, FontWeight.w700);
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    expect(result, const TimeOfDay(hour: 16, minute: 33));
  });
  testWidgets('小窗口下两种转盘可切换，分钟从 59 固定步进到 00', (tester) async {
    tester.view.physicalSize = const Size(400, 600);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: const NotebookTimePicker(
          initialTime: TimeOfDay(hour: 23, minute: 59),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('time-field-分钟')));
    await tester.pump();
    await tester.ensureVisible(find.byTooltip('下一分钟'));
    await tester.tap(find.byTooltip('下一分钟'));
    await tester.pump();
    expect(
      tester.widget<NotebookTimeDial>(find.byType(NotebookTimeDial)).value,
      0,
    );
    expect(tester.takeException(), isNull);
  });
}
