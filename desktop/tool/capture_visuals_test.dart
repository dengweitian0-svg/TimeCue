import 'dart:io';
import 'dart:ui' as ui;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/room/desk_notebook_hotspot.dart';
import 'package:timecue_desktop/features/room/room_backdrop.dart';
import 'package:timecue_desktop/features/room/room_geometry.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

// 手动运行 flutter test tool/capture_visuals_test.dart，生成真实 Widget 绘制截图。
// 使用本机 Windows 字体，截图不作为跨系统像素断言。
void main() {
  testWidgets('生成房间、双页、空白日历和编辑截图', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.runAsync(() async {
      for (final entry in {
        'Microsoft YaHei': 'C:/Windows/Fonts/msyh.ttc',
        'SimSun': 'C:/Windows/Fonts/simsun.ttc',
        'Georgia': 'C:/Windows/Fonts/georgia.ttf',
      }.entries) {
        final bytes = await File(entry.value).readAsBytes();
        await (FontLoader(
          entry.key,
        )..addFont(Future.value(ByteData.sublistView(bytes)))).load();
      }
      await (FontLoader(
        'MaterialIcons',
      )..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
    });
    final repo = DemoNotebookRepository();
    final boundaryKey = GlobalKey();
    await tester.pumpWidget(
      RepaintBoundary(
        key: boundaryKey,
        child: TimeCueApp(repository: repo),
      ),
    );
    Future<void> capture(String name) async {
      await tester.runAsync(() async {
        await Future<void>.delayed(const Duration(milliseconds: 150));
      });
      await tester.pumpAndSettle();
      await tester.runAsync(() async {
        final boundary =
            boundaryKey.currentContext!.findRenderObject()
                as RenderRepaintBoundary;
        final image = await boundary.toImage();
        final data = await image.toByteData(format: ui.ImageByteFormat.png);
        await Directory('build/visuals').create(recursive: true);
        await File(
          'build/visuals/$name.png',
        ).writeAsBytes(data!.buffer.asUint8List());
        image.dispose();
      });
    }

    tester.view.physicalSize = const Size(1440, 810);
    await tester.pumpAndSettle();
    await capture('01-room');
    // 原图本子仍是可操作入口，透明热区必须能打开和合上真正的双页。
    await tester.tap(find.byType(DeskNotebookHotspot));
    await tester.pumpAndSettle();
    expect(find.text('今日安排'), findsOneWidget);
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    tester.view.physicalSize = const Size(1280, 800);
    await tester.pumpAndSettle();
    await tester.tap(find.text('日程本  6'));
    await capture('02-today');
    await tester.tap(find.text('日历'));
    await capture('03-calendar-blank');
    await tester.tap(find.text('7').first);
    await tester.pumpAndSettle();
    await tester.tap(find.text('喝水，伸个懒腰'));
    await capture('04-edit');
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('添加日程'));
    await capture('05-create');
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await capture('06-hour-dial');
    await tester.tap(find.byKey(const ValueKey('time-field-分钟')));
    await capture('07-minute-dial');
    await tester.tap(find.text('取消').last);
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await capture('08-date-picker');
    await tester.tap(find.text('取消').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('单次'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('每月').last);
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('add-period-time')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('period-day-31')));
    await tester.enterText(find.byKey(const ValueKey('time-field-小时')), '22');
    await tester.enterText(find.byKey(const ValueKey('time-field-分钟')), '09');
    await capture('10-monthly-date-time');
    await tester.tap(find.text('取消').last);
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, '一件小事');
    await tester.tap(find.byTooltip('关闭'));
    await capture('09-discard-dialog');
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
    tester.view.physicalSize = const Size(1440, 810);
    final deskFocus = FocusNode();
    final geometry = RoomGeometry(const Size(1440, 810));
    // 分别固定三个时段，确认透明入口保留原图本子的完整外观。
    for (final period in ['morning', 'afternoon', 'night']) {
      await tester.pumpWidget(
        RepaintBoundary(
          key: boundaryKey,
          child: MaterialApp(
            debugShowCheckedModeBanner: false,
            theme: NotebookTheme.data,
            home: Scaffold(
              body: Stack(
                fit: StackFit.expand,
                children: [
                  RoomBackdrop(period: period),
                  Positioned.fromRect(
                    rect: geometry.visibleNotebookBounds,
                    child: DeskNotebookHotspot(
                      onOpen: () {},
                      focusNode: deskFocus,
                      outline: geometry.visibleNotebookPath.shift(
                        -geometry.visibleNotebookBounds.topLeft,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
      await capture('desk-$period');
    }
    await tester.pumpWidget(const SizedBox());
    deskFocus.dispose();
  });
}
