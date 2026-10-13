import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:timecue_desktop/app/software_update.dart';
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/room/room_screen.dart';
import 'package:timecue_desktop/features/settings/settings_dialog.dart';

class _Transport implements SoftwareUpdateTransport {
  @override
  Future<SoftwareUpdateRelease?> latestRelease() async => SoftwareUpdateRelease(
    version: '0.1.0+2',
    tag: 'v0.1.0+2',
    archiveName: 'TimeCue-0.1.0+2-windows-x64-20261013.zip',
    archiveUri: Uri.parse('https://example.test/update.zip'),
    checksumName: 'TimeCue-0.1.0+2-windows-x64-20261013.zip.sha256',
    checksumUri: Uri.parse('https://example.test/update.zip.sha256'),
    notes: '修复提醒并改进启动速度。',
  );

  @override
  Future<void> download(
    Uri uri,
    File destination, {
    required int maxBytes,
    required void Function(int received, int? total) onProgress,
  }) async {
    await destination.writeAsString(
      uri.path.endsWith('.sha256') ? 'hash' : 'zip',
    );
    onProgress(3, 3);
  }

  @override
  void close() {}
}

class _Platform implements SoftwareUpdatePlatform {
  final calls = <String>[];

  @override
  Future<bool> prepareUpdate({
    required String helperPath,
    required String planPath,
  }) async {
    calls.add('prepare');
    return true;
  }

  @override
  Future<void> launchUpdate({
    required String helperPath,
    required String planPath,
  }) async {
    calls.add('launch');
  }

  @override
  Future<void> commitUpdate({required String planPath}) async {
    calls.add('commit');
  }

  @override
  Future<void> cancelUpdate({required String planPath}) async {
    calls.add('cancel');
  }

  @override
  Future<String?> readUpdateStatus({required String statusPath}) async => null;
}

SoftwareUpdateConfiguration _configuration(
  Directory root, {
  String backend = 'managed',
}) => SoftwareUpdateConfiguration(
  backend: backend,
  dataDirectory: p.join(root.path, 'data'),
  goExecutable: backend == 'managed'
      ? p.join(root.path, 'install', 'timecue-core.exe')
      : '',
  currentVersion: '0.1.0+1',
  installDirectory: p.join(root.path, 'install'),
  applicationDirectory: p.join(root.path, 'install'),
);

void main() {
  testWidgets('设置中的真实按钮检查更新并在安装前显示重启确认', (tester) async {
    final root = Directory.systemTemp.createTempSync('timecue-update-widget-');
    addTearDown(() => root.deleteSync(recursive: true));
    final configuration = _configuration(root);
    final platform = _Platform();
    final controller = SoftwareUpdateController(
      configuration: configuration,
      platform: platform,
      transport: _Transport(),
      windows: true,
    );
    var exitRequests = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => showDialog<void>(
                context: context,
                barrierDismissible: false,
                builder: (_) => SettingsDialog(
                  store: ConfigFileStore(configuration.dataDirectory),
                  updateController: controller,
                  requestExit: () async => exitRequests++,
                ),
              ),
              child: const Text('打开设置'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开设置'));
    await _waitForSettingsLoad(tester);
    expect(find.byKey(const ValueKey('software-update-check')), findsOneWidget);
    expect(
      find.byKey(const ValueKey('software-update-install')),
      findsOneWidget,
    );
    expect(
      tester
          .widget<OutlinedButton>(
            find.byKey(const ValueKey('software-update-check')),
          )
          .onPressed,
      isNotNull,
    );
    expect(
      tester
          .widget<FilledButton>(
            find.byKey(const ValueKey('software-update-install')),
          )
          .onPressed,
      isNull,
    );

    await tester.ensureVisible(
      find.byKey(const ValueKey('software-update-check')),
    );
    await tester.tap(find.byKey(const ValueKey('software-update-check')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.text('发现新版本 0.1.0+2'), findsOneWidget);
    expect(
      tester
          .widget<FilledButton>(
            find.byKey(const ValueKey('software-update-install')),
          )
          .onPressed,
      isNotNull,
    );

    await tester.ensureVisible(
      find.byKey(const ValueKey('software-update-install')),
    );
    await tester.tap(find.byKey(const ValueKey('software-update-install')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 250));
    expect(find.text('下载并安装更新？'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pump(const Duration(milliseconds: 250));
    expect(exitRequests, 0);
    expect(platform.calls, isEmpty);
  });

  testWidgets('外连模式允许版本检查但禁用安装按钮', (tester) async {
    final root = Directory.systemTemp.createTempSync(
      'timecue-update-external-',
    );
    addTearDown(() => root.deleteSync(recursive: true));
    final configuration = _configuration(root, backend: 'external');
    final controller = SoftwareUpdateController(
      configuration: configuration,
      platform: _Platform(),
      transport: _Transport(),
      windows: true,
    );
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SettingsDialog(
            store: ConfigFileStore(configuration.dataDirectory),
            updateController: controller,
            requestExit: () async {},
          ),
        ),
      ),
    );
    await _waitForSettingsLoad(tester);
    expect(
      tester
          .widget<OutlinedButton>(
            find.byKey(const ValueKey('software-update-check')),
          )
          .onPressed,
      isNotNull,
    );
    expect(
      tester
          .widget<FilledButton>(
            find.byKey(const ValueKey('software-update-install')),
          )
          .onPressed,
      isNull,
    );
    expect(find.textContaining('托管运行模式'), findsOneWidget);
  });

  testWidgets('更新前沿用设置草稿舍弃守卫', (tester) async {
    final root = Directory.systemTemp.createTempSync('timecue-update-draft-');
    addTearDown(() => root.deleteSync(recursive: true));
    final configuration = _configuration(root);
    final controller = SoftwareUpdateController(
      configuration: configuration,
      platform: _Platform(),
      transport: _Transport(),
      windows: true,
    );
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SettingsDialog(
            store: ConfigFileStore(configuration.dataDirectory),
            updateController: controller,
            requestExit: () async {},
          ),
        ),
      ),
    );
    await _waitForSettingsLoad(tester);
    await tester.ensureVisible(
      find.byKey(const ValueKey('software-update-check')),
    );
    await tester.tap(find.byKey(const ValueKey('software-update-check')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    await tester.ensureVisible(find.byKey(const ValueKey('settings-log-path')));
    await tester.enterText(
      find.byKey(const ValueKey('settings-log-path')),
      'new-log-directory',
    );
    await tester.ensureVisible(
      find.byKey(const ValueKey('software-update-install')),
    );
    await tester.tap(find.byKey(const ValueKey('software-update-install')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 250));
    expect(find.text('舍弃未保存的设置？'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pump(const Duration(milliseconds: 250));
    expect(find.text('下载并安装更新？'), findsNothing);
  });

  testWidgets('房间打开设置后重建路由仍复用同一个更新控制器', (tester) async {
    final root = Directory.systemTemp.createTempSync('timecue-update-room-');
    addTearDown(() => root.deleteSync(recursive: true));
    final repository = DemoNotebookRepository();
    addTearDown(repository.dispose);
    final configuration = _configuration(root);
    final controller = SoftwareUpdateController(
      configuration: configuration,
      platform: _Platform(),
      transport: _Transport(),
      windows: true,
    );
    var factoryCalls = 0;
    tester.view.physicalSize = const Size(900, 700);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      MaterialApp(
        home: RoomScreen(
          repository: repository,
          configFiles: ConfigFileStore(configuration.dataDirectory),
          createSoftwareUpdateController: () {
            factoryCalls++;
            return controller;
          },
        ),
      ),
    );
    await tester.tap(find.text('设置'));
    await _waitForSettingsLoad(tester);
    expect(factoryCalls, 1);
    await tester.ensureVisible(
      find.byKey(const ValueKey('software-update-check')),
    );
    await tester.tap(find.byKey(const ValueKey('software-update-check')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.text('发现新版本 0.1.0+2'), findsOneWidget);

    tester.view.physicalSize = const Size(1100, 760);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 250));
    expect(factoryCalls, 1);
    expect(find.text('发现新版本 0.1.0+2'), findsOneWidget);
  });
}

Future<void> _waitForSettingsLoad(WidgetTester tester) async {
  for (var attempt = 0; attempt < 100; attempt++) {
    await tester.pump();
    final busy = find.descendant(
      of: find.byType(SettingsDialog),
      matching: find.byType(LinearProgressIndicator),
    );
    if (busy.evaluate().isEmpty) break;
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
  }
  await tester.pump();
  expect(
    find.descendant(
      of: find.byType(SettingsDialog),
      matching: find.byType(LinearProgressIndicator),
    ),
    findsNothing,
  );
}
