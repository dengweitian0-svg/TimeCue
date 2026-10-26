import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  testWidgets('AI 页面在窗口与文字缩放矩阵中保持导航和输入可达', (tester) async {
    final repository = DemoNotebookRepository();
    final model = NotebookViewModel(repository)..changeTab(NotebookTab.ai);
    Widget app(double scale) => MaterialApp(
      theme: NotebookTheme.data,
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(scale)),
        child: child!,
      ),
      home: Scaffold(
        body: Center(
          child: SizedBox(
            width: (tester.view.physicalSize.width - 64)
                .clamp(260.0, 1120.0)
                .toDouble(),
            height: (tester.view.physicalSize.height - 108)
                .clamp(160.0, 720.0)
                .toDouble(),
            child: RepaintBoundary(
              key: const ValueKey('chat-capture-boundary'),
              child: ListenableBuilder(
                listenable: model,
                builder: (context, _) => NotebookFrame(
                  model: model,
                  onClose: () {},
                  onManage: () {},
                  onTab: model.changeTab,
                  spread: ChatSpread(model: model),
                ),
              ),
            ),
          ),
        ),
      ),
    );
    addTearDown(() {
      model.dispose();
      repository.dispose();
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    const cases = [
      (640, 480, 1.0),
      (640, 480, 1.6),
      (640, 480, 2.0),
      (1280, 800, 1.0),
      (1280, 800, 1.6),
      (1280, 800, 2.0),
      (1440, 900, 1.0),
      (1440, 900, 1.6),
      (1440, 900, 2.0),
    ];
    final capture = Platform.environment['TIMECUE_CAPTURE_CHAT'] == 'true';
    if (capture) {
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
    }

    for (final (width, height, scale) in cases) {
      tester.view.physicalSize = Size(width.toDouble(), height.toDouble());
      tester.view.devicePixelRatio = 1;
      await tester.pumpWidget(app(scale));
      await tester.pump();

      expect(find.byKey(const ValueKey('chat-draft-input')), findsOneWidget);
      expect(find.byTooltip('合上日程本'), findsOneWidget);
      expect(find.text('AI 对话'), findsWidgets);
      expect(tester.takeException(), isNull);
      final sendButton = tester.widget<IconButton>(
        find.widgetWithIcon(IconButton, Icons.send),
      );
      expect(sendButton.onPressed, isNull);

      if (width < 840 || scale >= 1.6) {
        expect(find.widgetWithText(TextButton, '历史会话'), findsOneWidget);
        await tester.tap(find.widgetWithText(TextButton, '历史会话'));
        await tester.pump();
        expect(find.text('历史会话暂不可用'), findsOneWidget);
        expect(find.widgetWithText(TextButton, '返回对话'), findsOneWidget);
        await tester.tap(find.widgetWithText(TextButton, '返回对话'));
        await tester.pump();
        expect(find.byKey(const ValueKey('chat-draft-input')), findsOneWidget);
      } else {
        expect(find.text('搜索暂未开放'), findsOneWidget);
      }

      if (capture &&
          ((width == 1280 && scale == 1.0) ||
              (width == 640 && scale == 1.0) ||
              (width == 1440 && scale == 2.0))) {
        await _capture(tester, 'build/visuals/chat-stage01-$width-$scale.png');
      }
    }

    await tester.enterText(
      find.byKey(const ValueKey('chat-draft-input')),
      '保留的本地草稿',
    );
    await tester.pumpWidget(const SizedBox());
    await tester.pumpWidget(app(1));
    expect(model.chatDraft, '保留的本地草稿');
    expect(
      tester
          .widget<TextField>(find.byKey(const ValueKey('chat-draft-input')))
          .controller!
          .text,
      '保留的本地草稿',
    );
    await tester.tap(find.byTooltip('新会话'));
    await tester.pumpAndSettle();
    expect(model.chatDraft, '保留的本地草稿');
    await tester.tap(find.text('继续编辑'));
    await tester.pumpAndSettle();
    expect(model.chatDraft, '保留的本地草稿');
    await tester.tap(find.byTooltip('新会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('清空草稿'));
    await tester.pumpAndSettle();
    expect(model.chatDraft, isEmpty);
    expect(tester.takeException(), isNull);
  });
}

Future<void> _capture(WidgetTester tester, String path) async {
  await tester.runAsync(() async {
    final boundary = tester.renderObject<RenderRepaintBoundary>(
      find.byKey(const ValueKey('chat-capture-boundary')),
    );
    final image = await boundary.toImage(pixelRatio: 1);
    final data = await image.toByteData(format: ui.ImageByteFormat.png);
    image.dispose();
    if (data == null) return;
    final file = File(path);
    await file.parent.create(recursive: true);
    await file.writeAsBytes(data.buffer.asUint8List());
  });
}
