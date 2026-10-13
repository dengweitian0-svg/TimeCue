import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:file_selector_platform_interface/file_selector_platform_interface.dart';
import 'package:path/path.dart' as p;
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/settings/settings_dialog.dart';

void main() {
  late Directory directory;
  late ConfigFileStore store;
  late DemoNotebookRepository repository;
  late FileSelectorPlatform originalSelector;
  late _FakeFileSelector selector;

  setUp(() async {
    directory = await Directory.systemTemp.createTemp('timecue-settings-');
    store = ConfigFileStore(directory.path);
    repository = DemoNotebookRepository();
    originalSelector = FileSelectorPlatform.instance;
    selector = _FakeFileSelector(() => directory.path);
    FileSelectorPlatform.instance = selector;
  });

  tearDown(() async {
    repository.dispose();
    FileSelectorPlatform.instance = originalSelector;
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
    final selectedDatabaseDirectory = p.join(directory.path, '数据目录');
    selector.onSelect = () => selectedDatabaseDirectory;
    await tester.tap(find.byKey(const ValueKey('settings-database-path')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    final values = await tester.runAsync(store.read);
    expect(values!.logLevel, 'debug');
    expect(
      values.databasePath,
      p.join(selectedDatabaseDirectory, 'timecue.db'),
    );
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
      selectedDatabaseDirectory,
    );
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('设置'));
    await flush(tester);
    expect(find.text(selectedDatabaseDirectory), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('缺少配置只读默认提示；取消关闭保留草稿，Escape 舍弃后不写文件', (tester) async {
    await open(tester);
    expect(find.text('使用默认配置'), findsOneWidget);
    expect(
      await tester.runAsync(() => File(store.configPath).exists()),
      isFalse,
    );
    selector.onSelect = () => p.join(directory.path, 'new-log');
    await tester.tap(find.byKey(const ValueKey('settings-log-path')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(find.text(p.join(directory.path, 'new-log')), findsOneWidget);
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

  testWidgets('选择失败保留原值；保存失败保留草稿并能再次保存', (tester) async {
    await tester.runAsync(
      () => File(store.configPath).writeAsString('log:\n  log_path: logs\n'),
    );
    await open(tester);
    selector.onSelect = () => throw StateError('dialog failed');
    await tester.tap(find.byKey(const ValueKey('settings-log-path')));
    await tester.pumpAndSettle();
    expect(find.textContaining('选择目录失败'), findsOneWidget);
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-log-path')),
          )
          .controller!
          .text,
      p.join(directory.path, 'logs'),
    );
    selector.onSelect = () => p.join(directory.path, 'new-logs');
    await tester.tap(find.byKey(const ValueKey('settings-log-path')));
    await tester.pumpAndSettle();
    await tester.runAsync(() => File(store.configPath).writeAsString('[]'));
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    expect(find.textContaining('保存设置失败'), findsOneWidget);
    expect(find.text(p.join(directory.path, 'new-logs')), findsOneWidget);
    await tester.runAsync(
      () => File(store.configPath).writeAsString('log:\n  log_path: logs\n'),
    );
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    expect(
      (await tester.runAsync(store.read))!.logPath,
      p.join(directory.path, 'new-logs'),
    );
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
    selector.onSelect = () => p.join(directory.path, 'db', 'new');
    await tester.tap(find.byKey(const ValueKey('settings-database-path')));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('保存设置'));
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    expect(
      (await tester.runAsync(store.read))!.databasePath,
      p.join(directory.path, 'db', 'new', 'timecue.db'),
    );
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('绝对目录可同时用作数据库和日志目录，重新打开不显示文件名', (tester) async {
    final target = await tester.runAsync(
      () => Directory(p.join(directory.path, '数据')).create(),
    );
    final targetPath = target!.path;
    await open(tester);
    expect(find.text('数据库目录'), findsOneWidget);
    selector.onSelect = () => targetPath;
    await tester.tap(find.byKey(const ValueKey('settings-database-path')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('settings-log-path')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存设置'));
    await flush(tester);
    final values = await tester.runAsync(store.read);
    expect(values!.databasePath, p.join(targetPath, 'timecue.db'));
    expect(values.logPath, targetPath);
    expect(
      await tester.runAsync(() => File(values.databasePath!).exists()),
      isFalse,
    );
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('设置'));
    await flush(tester);
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-database-path')),
          )
          .controller!
          .text,
      targetPath,
    );
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('旧配置把目录写入数据库路径时，设置仍显示该目录而非上一级', (tester) async {
    final target = await tester.runAsync(
      () => Directory(p.join(directory.path, '旧数据')).create(),
    );
    final original = "db:\n  db_path: '${target!.path}'\nlog:\n  level: info\n";
    await tester.runAsync(() => File(store.configPath).writeAsString(original));
    await open(tester);
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-database-path')),
          )
          .controller!
          .text,
      target.path,
    );
    expect(
      await tester.runAsync(() => File(store.configPath).readAsString()),
      original,
    );
    expect(find.text('配置文件'), findsNothing);
    expect(find.text(store.configPath), findsNothing);
    expect(find.textContaining('相对目录以当前数据目录为基准'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('相对目录以数据目录解析显示，改日志级别不会改写原路径', (tester) async {
    const original =
        'log:\n  level: info\n  log_path: logs\ndb:\n  db_path: db/custom.sqlite\n';
    await tester.runAsync(() => File(store.configPath).writeAsString(original));
    await open(tester);
    tester.view.physicalSize = const Size(1280, 1200);
    await tester.pump();
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-log-path')),
          )
          .controller!
          .text,
      p.join(directory.path, 'logs'),
    );
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-database-path')),
          )
          .controller!
          .text,
      p.join(directory.path, 'db'),
    );
    await tester.tap(find.byType(DropdownButtonFormField<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('debug').last);
    await tester.pumpAndSettle();
    tester
        .widget<FilledButton>(find.widgetWithText(FilledButton, '保存设置'))
        .onPressed!();
    await flush(tester);
    final contents = await tester.runAsync(
      () => File(store.configPath).readAsString(),
    );
    expect(contents, contains('log_path: logs'));
    expect(contents, contains('db_path: db/custom.sqlite'));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('取消选择不改草稿，选择中禁止再次打开、保存和关闭', (tester) async {
    await open(tester);
    selector.onSelect = () => null;
    await tester.tap(find.byKey(const ValueKey('settings-log-path')));
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-log-path')),
          )
          .controller!
          .text,
      '',
    );
    expect(
      tester
          .widget<FilledButton>(find.widgetWithText(FilledButton, '保存设置'))
          .onPressed,
      isNull,
    );

    final pending = Completer<String?>();
    selector.onSelect = () => pending.future;
    await tester.tap(find.byKey(const ValueKey('settings-log-path')));
    await tester.pump();
    final calls = selector.calls;
    await tester.tap(find.byKey(const ValueKey('settings-log-path')));
    await tester.tap(find.text('关闭'));
    await tester.pump();
    expect(selector.calls, calls);
    expect(find.byType(SettingsDialog), findsOneWidget);
    pending.complete(p.join(directory.path, 'selected-log'));
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextFormField>(
            find.byKey(const ValueKey('settings-log-path')),
          )
          .controller!
          .text,
      p.join(directory.path, 'selected-log'),
    );
    await tester.pumpWidget(const SizedBox());
  });
}

class _FakeFileSelector extends FileSelectorPlatform {
  _FakeFileSelector(this.onSelect);

  FutureOr<String?> Function() onSelect;
  var calls = 0;

  @override
  Future<String?> getDirectoryPathWithOptions(FileDialogOptions options) async {
    calls++;
    return await onSelect();
  }
}
