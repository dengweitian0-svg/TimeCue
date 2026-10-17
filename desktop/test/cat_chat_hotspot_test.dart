import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/room/cat_chat_hotspot.dart';
import 'package:timecue_desktop/features/room/room_backdrop.dart';
import 'package:timecue_desktop/features/room/room_geometry.dart';
import 'package:timecue_desktop/features/room/room_screen.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  test('三时段共用猫咪原图区域的 cover 投影并正确裁切', () {
    for (final viewport in [
      const Size(1280, 720),
      const Size(1000, 800),
      const Size(1600, 900),
    ]) {
      final geometry = RoomGeometry(viewport);
      final imageBounds = Rect.fromPoints(
        geometry.project(
          Offset(
            RoomGeometry.catSourceBounds.left / RoomGeometry.source.width,
            RoomGeometry.catSourceBounds.top / RoomGeometry.source.height,
          ),
        ),
        geometry.project(
          Offset(
            RoomGeometry.catSourceBounds.right / RoomGeometry.source.width,
            RoomGeometry.catSourceBounds.bottom / RoomGeometry.source.height,
          ),
        ),
      );
      expect(
        geometry.visibleCatBounds.left,
        closeTo(imageBounds.left.clamp(0.0, viewport.width), .001),
      );
      expect(
        geometry.visibleCatBounds.top,
        closeTo(imageBounds.top.clamp(0.0, viewport.height), .001),
      );
      expect(
        geometry.visibleCatBounds.right,
        closeTo(imageBounds.right.clamp(0.0, viewport.width), .001),
      );
      expect(
        geometry.visibleCatBounds.bottom,
        closeTo(imageBounds.bottom.clamp(0.0, viewport.height), .001),
      );
      expect(geometry.catVisible, isTrue);
    }

    final partial = RoomGeometry(const Size(400, 1000));
    expect(partial.catVisible, isTrue);
    expect(partial.visibleCatBounds.right, 400);
    final cropped = RoomGeometry(const Size(400, 2000));
    expect(cropped.catVisible, isFalse);
    expect(cropped.visibleCatBounds.isEmpty, isTrue);
  });

  testWidgets('猫咪入口支持鼠标悬停、键盘聚焦和回车打开', (tester) async {
    final focus = FocusNode(debugLabel: '猫咪测试入口');
    addTearDown(focus.dispose);
    var opened = false;
    const bounds = Rect.fromLTWH(945, 438, 181, 73);
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CatChatHotspot(
            visibleBounds: bounds,
            viewport: const Size(1280, 720),
            focusNode: focus,
            onOpen: () => opened = true,
          ),
        ),
      ),
    );
    final button = find.byKey(const ValueKey('cat-chat-hotspot-button'));
    final center = tester.getCenter(button);
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(center);
    await tester.pump();
    expect(find.text('和我聊聊'), findsOneWidget);
    expect(find.text('点击猫咪，打开 AI 对话'), findsOneWidget);
    final hint = tester.getRect(find.text('和我聊聊'));
    expect(hint.overlaps(tester.getRect(button)), isFalse);

    focus.requestFocus();
    await tester.pump();
    expect(find.text('和我聊聊'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();
    expect(opened, isTrue);
  });

  testWidgets('提示气泡按大字模式增长并保持在视口内且不遮挡猫咪', (tester) async {
    final focus = FocusNode();
    addTearDown(focus.dispose);
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        home: MediaQuery(
          data: const MediaQueryData(textScaler: TextScaler.linear(2)),
          child: Scaffold(
            body: CatChatHotspot(
              visibleBounds: const Rect.fromLTWH(945, 438, 181, 73),
              viewport: const Size(1280, 720),
              focusNode: focus,
              onOpen: () {},
            ),
          ),
        ),
      ),
    );
    focus.requestFocus();
    await tester.pump();
    expect(tester.takeException(), isNull);
    final target = tester.getRect(
      find.byKey(const ValueKey('cat-chat-hotspot-button')),
    );
    final subtitle = tester.getRect(find.text('点击猫咪，打开 AI 对话'));
    final title = tester.getRect(find.text('和我聊聊'));
    expect(title.overlaps(target), isFalse);
    expect(subtitle.overlaps(target), isFalse);
    expect(subtitle.height, greaterThan(18));
    expect(subtitle.right, lessThanOrEqualTo(1280));
    expect(subtitle.bottom, lessThanOrEqualTo(720));
  });

  testWidgets('点击猫咪打开 AI 标签，关闭后焦点回猫；裁切时回固定本子入口', (tester) async {
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = DemoNotebookRepository();
    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(disableAnimations: true),
        child: MaterialApp(
          theme: NotebookTheme.data,
          home: Scaffold(body: RoomScreen(repository: repository)),
        ),
      ),
    );
    final geometry = RoomGeometry(const Size(1280, 720));
    await tester.tapAt(geometry.visibleCatBounds.center);
    await tester.pumpAndSettle();
    expect(find.byType(ChatSpread), findsOneWidget);
    expect(find.text('AI 对话'), findsAtLeastNWidgets(1));
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    final catButton = tester.widget<TextButton>(
      find.byKey(const ValueKey('cat-chat-hotspot-button')),
    );
    expect(catButton.focusNode!.hasFocus, isTrue);

    await tester.tapAt(
      RoomGeometry(const Size(1280, 720)).visibleCatBounds.center,
    );
    await tester.pumpAndSettle();
    tester.view.physicalSize = const Size(400, 2000);
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('cat-chat-hotspot-button')), findsNothing);
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<RoomActions>(find.byType(RoomActions)).focusNode.hasFocus,
      isTrue,
    );
    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });

  testWidgets('统一入口样式保持文字对比度、禁用创建门禁和管理入口', (tester) async {
    final style = NotebookTheme.roomActionStyle();
    final foreground = style.foregroundColor!.resolve({})!;
    final background = style.backgroundColor!.resolve({})!;
    final lighter =
        foreground.computeLuminance() > background.computeLuminance()
        ? foreground.computeLuminance()
        : background.computeLuminance();
    final darker = foreground.computeLuminance() > background.computeLuminance()
        ? background.computeLuminance()
        : foreground.computeLuminance();
    expect((lighter + .05) / (darker + .05), greaterThanOrEqualTo(4.5));
    expect(style.minimumSize!.resolve({})!.height, 44);
    expect(
      style.shape!.resolve({}),
      isA<RoundedRectangleBorder>().having(
        (shape) => shape.borderRadius,
        '圆角',
        BorderRadius.circular(12),
      ),
    );

    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = _ReadOnlyRepository();
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(body: RoomScreen(repository: repository)),
      ),
    );
    final create = find.widgetWithText(FilledButton, '添加日程');
    final manage = find.widgetWithText(FilledButton, '管理日程');
    expect(tester.widget<FilledButton>(create).onPressed, isNull);
    expect(find.byTooltip('提醒服务暂不可用，恢复连接后即可添加日程'), findsOneWidget);
    expect(tester.widget<FilledButton>(manage).onPressed, isNotNull);
    expect(
      tester.widget<FilledButton>(create).style!.backgroundColor!.resolve({
        WidgetState.disabled,
      }),
      isNot(style.backgroundColor!.resolve({})),
    );
    await tester.pumpWidget(const SizedBox());
    repository.dispose();
  });

  testWidgets('显式环境变量开启时保存三时段真实房间与猫咪悬停入口截图', (tester) async {
    if (Platform.environment['TIMECUE_CAPTURE_CHAT'] != 'true') return;
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.runAsync(() async {
      final yahei = await File('C:/Windows/Fonts/msyh.ttc').readAsBytes();
      final simsun = await File('C:/Windows/Fonts/simsun.ttc').readAsBytes();
      for (final family in ['Microsoft YaHei', 'Roboto']) {
        await (FontLoader(
          family,
        )..addFont(Future<ByteData>.value(ByteData.sublistView(yahei)))).load();
      }
      await (FontLoader(
        'SimSun',
      )..addFont(Future<ByteData>.value(ByteData.sublistView(simsun)))).load();
      await (FontLoader(
        'MaterialIcons',
      )..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
    });
    const screenshotLabelStyle = TextStyle(
      fontFamily: 'Microsoft YaHei',
      fontSize: 12,
    );
    for (final period in ['morning', 'afternoon', 'night']) {
      final geometry = RoomGeometry(const Size(1280, 720));
      final focus = FocusNode();
      final boundaryKey = GlobalKey();
      await tester.pumpWidget(
        RepaintBoundary(
          key: boundaryKey,
          child: MaterialApp(
            theme: NotebookTheme.data,
            home: Scaffold(
              body: Stack(
                fit: StackFit.expand,
                children: [
                  RoomBackdrop(period: period),
                  Positioned.fill(
                    child: CatChatHotspot(
                      visibleBounds: geometry.visibleCatBounds,
                      viewport: const Size(1280, 720),
                      focusNode: focus,
                      onOpen: () {},
                    ),
                  ),
                  Positioned(
                    top: 24,
                    left: 24,
                    child: FilledButton.icon(
                      onPressed: () {},
                      style: NotebookTheme.roomActionStyle(),
                      icon: const Icon(Icons.settings_outlined, size: 17),
                      label: const Text('设置', style: screenshotLabelStyle),
                    ),
                  ),
                  Positioned(
                    top: 24,
                    right: 24,
                    child: FilledButton.icon(
                      onPressed: () {},
                      style: NotebookTheme.roomActionStyle(),
                      icon: const Icon(Icons.menu_book_outlined, size: 17),
                      label: const Text('日程本', style: screenshotLabelStyle),
                    ),
                  ),
                  Positioned(
                    bottom: 24,
                    right: 28,
                    child: Row(
                      children: [
                        FilledButton.icon(
                          onPressed: () {},
                          style: NotebookTheme.roomActionStyle(),
                          icon: const Icon(Icons.list_alt, size: 17),
                          label: const Text(
                            '管理日程',
                            style: screenshotLabelStyle,
                          ),
                        ),
                        const SizedBox(width: 12),
                        FilledButton.icon(
                          onPressed: () {},
                          style: NotebookTheme.roomActionStyle(),
                          icon: const Icon(Icons.add, size: 17),
                          label: const Text(
                            '添加日程',
                            style: screenshotLabelStyle,
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
      final context = tester.element(find.byType(Scaffold));
      await tester.runAsync(() async {
        await precacheImage(
          AssetImage('assets/room/b-anime-$period.png'),
          context,
        );
      });
      await tester.pumpAndSettle();
      final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
      await mouse.addPointer(location: Offset.zero);
      await mouse.moveTo(geometry.visibleCatBounds.center);
      await tester.pump();
      await tester.runAsync(() async {
        final boundary =
            boundaryKey.currentContext!.findRenderObject()!
                as RenderRepaintBoundary;
        final image = await boundary.toImage();
        final data = await image.toByteData(format: ui.ImageByteFormat.png);
        final output = File('build/visuals/chat-room-$period.png');
        await output.parent.create(recursive: true);
        await output.writeAsBytes(data!.buffer.asUint8List());
        image.dispose();
      });
      await mouse.removePointer();
      await tester.pump();
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      focus.dispose();
    }
  });
}

class _ReadOnlyRepository extends DemoNotebookRepository {
  @override
  bool get supportsRuleChanges => false;
}
