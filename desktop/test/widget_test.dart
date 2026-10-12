import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_overlay.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';
import 'package:timecue_desktop/features/room/desk_notebook_hotspot.dart';
import 'package:timecue_desktop/features/room/room_geometry.dart';

void main() {
  testWidgets('只有星期四有安排时，同一行日期数字仍水平对齐', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = _ThursdayMarkedRepository();
    final model = NotebookViewModel(repo);
    for (final month in [10, 11]) {
      model.month = DateTime.utc(2026, month);
      final selected = month == 10 ? 8 : 5;
      await model.selectDate(DateTime.utc(2026, month, selected));
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Align(
              alignment: Alignment.topLeft,
              child: SizedBox(
                width: 600,
                child: MonthGrid(model: model, onSelect: (_) {}),
              ),
            ),
          ),
        ),
      );
      final thursdays = month == 10 ? [8, 15, 22, 29] : [5, 12, 19, 26];
      for (final day in thursdays) {
        final markedY = tester.getCenter(find.text('$day')).dy;
        final unmarkedY = tester.getCenter(find.text('${day + 1}')).dy;
        expect(
          markedY,
          closeTo(unmarkedY, .01),
          reason: '$month 月 $day 日与下一日的数字应对齐',
        );
      }
    }
    await tester.pumpWidget(const SizedBox());
    model.dispose();
    repo.dispose();
  });
  testWidgets('日历底部显示选中日期数量和无日程状态，不再显示固定图例', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    final model = NotebookViewModel(repo);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ListenableBuilder(
            listenable: model,
            builder: (_, _) => CalendarSpread(
              model: model,
              onNavigate: (action) async => action(),
              onManage: () {},
              editor: null,
            ),
          ),
        ),
      ),
    );
    expect(find.text('当日共 5 个日程'), findsOneWidget);
    expect(find.textContaining('日历标记'), findsNothing);
    await repo.confirm({'n1'});
    await tester.pump();
    expect(find.text('当日共 5 个日程'), findsOneWidget);
    await model.selectDate(DateTime.utc(2026, 10, 1));
    await tester.pump();
    expect(find.text('无日程'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    model.dispose();
    repo.dispose();
  });
  testWidgets('缩窄窗口后本子露出的部分仍可点击，完全裁掉才隐藏', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(1280, 800);
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    await tester.pumpWidget(TimeCueApp(repository: repo));
    for (final width in [1280.0, 1100.0, 960.0]) {
      tester.view.physicalSize = Size(width, 800);
      await tester.pumpAndSettle();
      final geometry = RoomGeometry(Size(width, 800));
      final point = geometry.project(const Offset(275 / 1672, 542 / 941));
      expect(find.byType(DeskNotebookHotspot), findsOneWidget);
      await tester.tapAt(point);
      await tester.pumpAndSettle();
      expect(find.text('今日安排'), findsOneWidget);
      await tester.tap(find.byTooltip('合上日程本'));
      await tester.pumpAndSettle();
    }
    tester.view.physicalSize = const Size(500, 800);
    await tester.pumpAndSettle();
    expect(find.byType(DeskNotebookHotspot), findsNothing);
    expect(find.text('日程本  6'), findsOneWidget);
    tester.view.physicalSize = const Size(1440, 810);
    await tester.pumpAndSettle();
    expect(find.byType(DeskNotebookHotspot), findsOneWidget);
    await tester.tap(find.byType(DeskNotebookHotspot));
    await tester.pumpAndSettle();
    expect(find.text('今日安排'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('添加草稿通过 Escape 舍弃守卫，保存后刷新示例清单', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    await tester.pumpWidget(TimeCueApp(repository: repo));
    await tester.tap(find.text('添加日程'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, '新安排');
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('继续编辑'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byKey(const ValueKey('add-single-time')));
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('保存日程'));
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleCreateDialog), findsNothing);
    expect(repo.today.any((e) => e.name == '新安排'), isTrue);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('房间到双页、空白右页、右页编辑和 Escape 合上', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    await tester.pumpWidget(TimeCueApp(repository: repo));
    await tester.tap(find.text('日程本  6'));
    await tester.pumpAndSettle();
    expect(find.text('今日安排'), findsOneWidget);
    await tester.tap(find.text('日历'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('calendar-blank')), findsOneWidget);
    await tester.tap(find.text('7').first);
    await tester.pumpAndSettle();
    await tester.tap(find.text('喝水，伸个懒腰'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditor), findsOneWidget);
    expect(find.text('2026 年 10 月'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.byType(NotebookOverlay), findsNothing);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('小窗口和高文本缩放支持横向双页，减少动效立即到位', (tester) async {
    tester.view.physicalSize = const Size(640, 480);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    final vm = NotebookViewModel(repo);
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: MediaQuery(
          data: const MediaQueryData(
            size: Size(640, 480),
            disableAnimations: true,
            textScaler: TextScaler.linear(1.6),
          ),
          child: Scaffold(
            body: NotebookOverlay(
              model: vm,
              origin: const Offset(500, 40),
              onClosed: () {},
            ),
          ),
        ),
      ),
    );
    await tester.pump();
    expect(find.text('今日安排'), findsOneWidget);
    expect(tester.takeException(), isNull);
    final horizontal = find.byWidgetPredicate(
      (w) => w is SingleChildScrollView && w.scrollDirection == Axis.horizontal,
    );
    await tester.drag(horizontal, const Offset(-600, 0));
    await tester.pumpAndSettle();
    expect(find.text('待确认'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    vm.dispose();
    repo.dispose();
  });
}

class _ThursdayMarkedRepository extends DemoNotebookRepository {
  @override
  bool markedOn(DateTime date) => date.weekday == DateTime.thursday;
}
