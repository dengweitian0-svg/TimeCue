import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/contracts/schedule_rules.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_time_chip.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  test('同一周期逐项添加，重复及混合类型拒绝，空列表不能保存', () async {
    final repo = DemoNotebookRepository();
    final model = ScheduleEditorViewModel(repo, null);
    addTearDown(() {
      model.dispose();
      repo.dispose();
    });
    model.name.text = '独立星期时间';
    model.changeKind(ScheduleKind.weekly);
    expect(await model.save(), isFalse);
    const first = ScheduleTimeSlot(ScheduleKind.weekly, 9, 0, day: 1);
    const second = ScheduleTimeSlot(ScheduleKind.weekly, 18, 0, day: 3);
    expect(model.addSlot(first), isTrue);
    expect(model.addSlot(second), isTrue);
    expect(model.addSlot(first), isFalse);
    expect(
      model.addSlot(const ScheduleTimeSlot(ScheduleKind.monthly, 9, 0, day: 1)),
      isFalse,
    );
    model.changeKind(ScheduleKind.monthly);
    expect(model.kind, ScheduleKind.weekly);
    expect(await model.save(), isTrue);
    final saved = (await repo.listSchedules()).singleWhere(
      (value) => value.draft.name == '独立星期时间',
    );
    expect(saved.draft.slots, [first, second]);
    expect(ScheduleRules.encode(saved.draft), ['0 9 * * 1', '0 18 * * 3']);
    model.removeSlot(first);
    model.removeSlot(second);
    expect(model.dirty, isTrue);
    model.changeKind(ScheduleKind.monthly);
    expect(model.kind, ScheduleKind.monthly);
  });

  for (final kind in [
    ScheduleKind.daily,
    ScheduleKind.weekly,
    ScheduleKind.monthly,
  ]) {
    testWidgets('$kind 点击＋选择并生成圆角框，悬停删除不弹确认', (tester) async {
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final repo = DemoNotebookRepository();
      final model = ScheduleEditorViewModel(repo, null)..changeKind(kind);
      await tester.pumpWidget(
        MaterialApp(
          theme: NotebookTheme.data,
          home: Scaffold(
            body: SingleChildScrollView(
              child: Center(
                child: SizedBox(
                  width: 480,
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
      expect(find.byType(TextField), findsNWidgets(2));
      await tester.tap(find.byKey(const ValueKey('add-period-time')));
      await tester.pumpAndSettle();
      expect(find.textContaining('每格'), findsNothing);
      expect(find.byTooltip('上一小时'), findsNothing);
      expect(find.byTooltip('下一小时'), findsNothing);
      if (kind == ScheduleKind.monthly) {
        expect(
          tester.getRect(find.byKey(const ValueKey('period-day-1'))).right,
          lessThan(
            tester.getRect(find.byKey(const ValueKey('time-field-小时'))).left,
          ),
        );
      }
      if (kind != ScheduleKind.daily) {
        final choice = find.byKey(
          ValueKey('period-day-${kind == ScheduleKind.weekly ? 3 : 31}'),
        );
        await tester.ensureVisible(choice);
        await tester.tap(choice);
        await tester.pumpAndSettle();
      }
      await tester.ensureVisible(find.byKey(const ValueKey('time-field-小时')));
      await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '18');
      await tester.enterText(find.byKey(const ValueKey('time-field-分钟')), '05');
      await tester.ensureVisible(find.text('确定'));
      await tester.tap(find.text('确定'));
      await tester.pumpAndSettle();
      expect(model.slots.length, 1);
      expect(model.slots.single.clockLabel, '18:05');
      expect(
        model.slots.single.day,
        kind == ScheduleKind.daily
            ? null
            : kind == ScheduleKind.weekly
            ? 3
            : 31,
      );
      expect(find.byType(ScheduleTimeChip), findsOneWidget);
      await tester.ensureVisible(find.byType(ScheduleTimeChip));
      await tester.pumpAndSettle();
      final chip = find.byType(ScheduleTimeChip);
      final rect = tester.getRect(chip);
      final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
      await mouse.addPointer(location: Offset.zero);
      await mouse.moveTo(rect.center);
      await tester.pump();
      expect(tester.getRect(chip), rect);
      await tester.tap(find.byTooltip('删除${model.slots.single.label}'));
      await tester.pumpAndSettle();
      expect(model.slots, isEmpty);
      expect(find.byType(AlertDialog), findsNothing);
      await mouse.removePointer();
      await tester.pumpWidget(const SizedBox());
      model.dispose();
      repo.dispose();
    });
  }
}
