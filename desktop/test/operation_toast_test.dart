import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/operation_toast.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

Widget _app({required Widget home, TextScaler? textScaler}) => MaterialApp(
  theme: NotebookTheme.data,
  builder: (context, child) => MediaQuery(
    data: MediaQuery.of(context).copyWith(textScaler: textScaler),
    child: OperationToastHost(child: child ?? const SizedBox.shrink()),
  ),
  home: home,
);

void main() {
  testWidgets('宿主绘制在导航路由之上，关闭弹窗后提醒仍可见', (tester) async {
    await tester.pumpWidget(
      _app(
        home: Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => showDialog<void>(
                context: context,
                builder: (dialogContext) => AlertDialog(
                  title: const Text('编辑日程'),
                  actions: [
                    TextButton(
                      onPressed: () {
                        OperationToast.success(dialogContext, '日程保存成功');
                        Navigator.pop(dialogContext);
                      },
                      child: const Text('保存并关闭'),
                    ),
                  ],
                ),
              ),
              child: const Text('打开编辑'),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('打开编辑'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存并关闭'));
    await tester.pumpAndSettle();

    expect(find.byType(AlertDialog), findsNothing);
    expect(find.text('日程保存成功'), findsOneWidget);
    expect(
      find.byWidgetPredicate(
        (widget) => widget is Semantics && widget.properties.liveRegion == true,
      ),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('连续结果最多保留三条且可以独立关闭', (tester) async {
    await tester.pumpWidget(
      _app(
        home: Scaffold(
          body: Builder(
            builder: (context) => Wrap(
              children: [
                TextButton(
                  onPressed: () => OperationToast.success(context, '操作一成功'),
                  child: const Text('成功一'),
                ),
                TextButton(
                  onPressed: () => OperationToast.error(context, '操作二失败'),
                  child: const Text('失败二'),
                ),
                TextButton(
                  onPressed: () => OperationToast.warning(context, '操作三需留意'),
                  child: const Text('警告三'),
                ),
                TextButton(
                  onPressed: () => OperationToast.success(context, '操作四成功'),
                  child: const Text('成功四'),
                ),
              ],
            ),
          ),
        ),
      ),
    );

    for (final button in ['成功一', '失败二', '警告三', '成功四']) {
      await tester.tap(find.text(button));
      await tester.pump();
    }
    expect(find.text('操作一成功'), findsNothing);
    expect(find.text('操作二失败'), findsOneWidget);
    expect(find.text('操作三需留意'), findsOneWidget);
    expect(find.text('操作四成功'), findsOneWidget);
    expect(find.byIcon(Icons.close), findsNWidgets(3));

    await tester.tap(find.byIcon(Icons.close).at(1));
    await tester.pump();
    expect(find.text('操作三需留意'), findsNothing);
    expect(find.text('操作二失败'), findsOneWidget);
    expect(find.text('操作四成功'), findsOneWidget);
  });

  testWidgets('成功提醒四秒、失败和警告提醒六秒后自动消失', (tester) async {
    await tester.pumpWidget(
      _app(
        home: Scaffold(
          body: Builder(
            builder: (context) => Column(
              children: [
                TextButton(
                  onPressed: () => OperationToast.success(context, '完成'),
                  child: const Text('显示成功'),
                ),
                TextButton(
                  onPressed: () => OperationToast.error(context, '失败'),
                  child: const Text('显示失败'),
                ),
                TextButton(
                  onPressed: () => OperationToast.warning(context, '注意'),
                  child: const Text('显示警告'),
                ),
              ],
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('显示成功'));
    await tester.tap(find.text('显示失败'));
    await tester.tap(find.text('显示警告'));
    await tester.pump();
    await tester.pump(const Duration(seconds: 4));
    expect(find.text('完成'), findsNothing);
    expect(find.text('失败'), findsOneWidget);
    expect(find.text('注意'), findsOneWidget);
    await tester.pump(const Duration(seconds: 2));
    expect(find.text('失败'), findsNothing);
    expect(find.text('注意'), findsNothing);
  });

  testWidgets('无宿主时调用安全忽略，卸载宿主会释放提醒计时器', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => OperationToast.success(context, '无宿主'),
              child: const Text('发送无宿主提醒'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('发送无宿主提醒'));
    await tester.pump();
    expect(find.text('无宿主'), findsNothing);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(
      _app(
        home: Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => OperationToast.error(context, '即将释放'),
              child: const Text('显示并释放'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('显示并释放'));
    await tester.pump();
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(seconds: 7));
    expect(tester.takeException(), isNull);
  });

  testWidgets('窄窗口和高文本缩放下长文案换行且不溢出', (tester) async {
    tester.view.physicalSize = const Size(360, 600);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    const message =
        '设置保存失败：配置文件无法写入。请检查目标目录权限后重试；当前编辑内容仍会保留，'
        '你可以继续修改路径或关闭此窗口。';
    await tester.pumpWidget(
      _app(
        textScaler: const TextScaler.linear(2.2),
        home: Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => OperationToast.error(context, message),
              child: const Text('显示长失败提醒'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('显示长失败提醒'));
    await tester.pump();
    expect(find.text(message), findsOneWidget);
    expect(tester.takeException(), isNull);
    final rect = tester.getRect(find.text(message));
    expect(rect.left, greaterThanOrEqualTo(0));
    expect(rect.right, lessThanOrEqualTo(360));
    expect(rect.bottom, lessThanOrEqualTo(600));
  });

  testWidgets('提醒以外的空白区域仍可点击底层界面', (tester) async {
    var tapped = false;
    await tester.pumpWidget(
      _app(
        home: Scaffold(
          body: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: () => tapped = true,
            child: const SizedBox.expand(),
          ),
        ),
      ),
    );
    final context = tester.element(find.byType(Scaffold));
    OperationToast.success(context, '可点击区域测试');
    await tester.pump();
    await tester.tapAt(const Offset(20, 20));
    expect(tapped, isTrue);
  });

  testWidgets('显式环境变量开启时保存真实 Widget 绘制截图', (tester) async {
    if (Platform.environment['TIMECUE_CAPTURE_OPERATION_TOAST'] != 'true') {
      return;
    }
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
        )..addFont(Future<ByteData>.value(ByteData.sublistView(bytes)))).load();
      }
      await (FontLoader(
        'MaterialIcons',
      )..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
    });
    final boundaryKey = GlobalKey();
    await tester.pumpWidget(
      RepaintBoundary(
        key: boundaryKey,
        child: _app(
          home: Scaffold(
            body: Builder(
              builder: (context) => Center(
                child: FilledButton(
                  onPressed: () => OperationToast.success(context, '日程已创建'),
                  child: const Text('触发截图提醒'),
                ),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('触发截图提醒'));
    await tester.pump();
    await tester.runAsync(() async {
      final boundary =
          boundaryKey.currentContext!.findRenderObject()
              as RenderRepaintBoundary;
      final image = await boundary.toImage();
      final data = await image.toByteData(format: ui.ImageByteFormat.png);
      await Directory('build/visuals').create(recursive: true);
      await File(
        'build/visuals/operation-toast.png',
      ).writeAsBytes(data!.buffer.asUint8List());
      image.dispose();
    });
    expect(File('build/visuals/operation-toast.png').existsSync(), isTrue);
  });
}
