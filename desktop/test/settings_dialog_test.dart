import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/settings/settings_dialog.dart';

void main() {
  late Directory directory;
  late ConfigFileStore store;
  late DemoNotebookRepository repository;

  setUp(() async {
    directory = await Directory.systemTemp.createTemp('timecue-settings-');
    store = ConfigFileStore(directory.path);
    repository = DemoNotebookRepository();
  });

  tearDown(() async {
    repository.dispose();
    await directory.delete(recursive: true);
  });

  // 交替推进真实 IO 与 Widget 虚拟时钟，等待弹窗结束读写。
  Future<void> flush(WidgetTester tester) async {
    await tester.pump();
    final progress = find.descendant(
      of: find.byType(SettingsDialog),
      matching: find.byType(LinearProgressIndicator),
    );
    for (
      var attempt = 0;
      attempt < 100 && progress.evaluate().isNotEmpty;
      attempt++
    ) {
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 10)),
      );
      await tester.pump(const Duration(milliseconds: 20));
    }
    expect(progress, findsNothing);
    await tester.pumpAndSettle();
  }

  Future<void> open(WidgetTester tester) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      TimeCueApp(repository: repository, configFiles: store),
    );
    await tester.tap(find.text('设置'));
    await flush(tester);
  }

  testWidgets('房间设置保存到真实文件，保留未知字段并提示路径下次启动生效', (tester) async {
    await tester.runAsync(
      () => File(store.configPath).writeAsString('''# 保留注释
desktop:
  start_on_login: true
log:
  level: info
db:
  db_path: db/original.db
'''),
    );
    await open(tester);
    await tester.tap(find.byType(DropdownButtonFormField<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('debug').last);
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('settings-database-path')),
      'db/中文 新日程.db',
    );
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    final values = await tester.runAsync(store.read);
    expect(values!.logLevel, 'debug');
    expect(values.databasePath, 'db/中文 新日程.db');
    expect(values.logPath, isNull);
    final contents = await tester.runAsync(
      () => File(store.configPath).readAsString(),
    );
    expect(contents, contains('# 保留注释'));
    expect(contents, contains('start_on_login: true'));
    expect(find.textContaining('下次启动生效，已有数据不会自动搬迁'), findsOneWidget);
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-database-path')),
          )
          .controller!
          .text,
      'db/中文 新日程.db',
    );
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('设置'));
    await flush(tester);
    expect(find.text('db/中文 新日程.db'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('缺少配置只读默认提示；取消关闭保留草稿，Escape 舍弃后不写文件', (tester) async {
    await open(tester);
    expect(find.text('使用默认配置'), findsOneWidget);
    expect(
      await tester.runAsync(() => File(store.configPath).exists()),
      isFalse,
    );
    await tester.enterText(
      find.byKey(const ValueKey('settings-log-path')),
      'new-log',
    );
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(find.text('new-log'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    await tester.tap(find.text('舍弃修改'));
    await tester.pumpAndSettle();
    expect(find.byType(SettingsDialog), findsNothing);
    expect(
      await tester.runAsync(() => File(store.configPath).exists()),
      isFalse,
    );
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('恢复默认需确认，移除三项覆盖并保留其他配置', (tester) async {
    await tester.runAsync(
      () => File(store.configPath).writeAsString('''app:
  name: TimeCue
log:
  level: warn
  log_path: custom-log
db:
  db_path: custom.db
'''),
    );
    await open(tester);
    await tester.tap(find.text('恢复默认设置'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect((await tester.runAsync(store.read))!.databasePath, 'custom.db');
    await tester.tap(find.text('恢复默认设置'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('恢复默认'));
    await flush(tester);
    final values = await tester.runAsync(store.read);
    expect(values!.logLevel, isNull);
    expect(values.logPath, isNull);
    expect(values.databasePath, isNull);
    expect(find.text('使用默认配置'), findsOneWidget);
    expect(find.text('custom.db'), findsNothing);
    final contents = await tester.runAsync(
      () => File(store.configPath).readAsString(),
    );
    expect(contents, contains('name: TimeCue'));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('配置损坏时禁止写入，修复后可重试读取', (tester) async {
    await tester.runAsync(() => File(store.configPath).writeAsString('[]'));
    await open(tester);
    expect(find.textContaining('读取设置失败'), findsOneWidget);
    expect(
      tester
          .widget<FilledButton>(find.widgetWithText(FilledButton, '保存设置'))
          .onPressed,
      isNull,
    );
    await tester.runAsync(
      () => File(store.configPath).writeAsString('log:\n  level: error\n'),
    );
    await tester.tap(find.text('重试读取'));
    await flush(tester);
    expect(find.text('error'), findsOneWidget);
    expect(find.textContaining('读取设置失败'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('已有路径不能清空；保存失败保留草稿并能再次保存', (tester) async {
    await tester.runAsync(
      () => File(store.configPath).writeAsString('log:\n  log_path: logs\n'),
    );
    await open(tester);
    await tester.enterText(find.byKey(const ValueKey('settings-log-path')), '');
    await tester.pump();
    await tester.tap(find.text('保存设置'));
    await tester.pumpAndSettle();
    expect(find.textContaining('请填写路径'), findsOneWidget);
    await tester.enterText(
      find.byKey(const ValueKey('settings-log-path')),
      'new-logs',
    );
    await tester.pump();
    await tester.runAsync(() => File(store.configPath).writeAsString('[]'));
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    expect(find.textContaining('保存设置失败'), findsOneWidget);
    expect(find.text('new-logs'), findsOneWidget);
    await tester.runAsync(
      () => File(store.configPath).writeAsString('log:\n  log_path: logs\n'),
    );
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    expect((await tester.runAsync(store.read))!.logPath, 'new-logs');
    expect(find.textContaining('保存设置失败'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('小窗口和高文本缩放可滚动设置并访问保存按钮', (tester) async {
    tester.view.physicalSize = const Size(480, 560);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(
            context,
          ).copyWith(textScaler: const TextScaler.linear(1.8)),
          child: child!,
        ),
        home: Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => showDialog<void>(
                context: context,
                builder: (_) => SettingsDialog(store: store),
              ),
              child: const Text('打开设置'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开设置'));
    await flush(tester);
    expect(tester.takeException(), isNull);
    await tester.ensureVisible(
      find.byKey(const ValueKey('settings-database-path')),
    );
    await tester.enterText(
      find.byKey(const ValueKey('settings-database-path')),
      'db/new.db',
    );
    await tester.pump();
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    expect((await tester.runAsync(store.read))!.databasePath, 'db/new.db');
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });
}
