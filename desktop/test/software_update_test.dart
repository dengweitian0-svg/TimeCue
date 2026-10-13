import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:timecue_desktop/app/software_update.dart';

Map<String, Object?> releaseJson({
  String version = '0.1.0+2',
  bool draft = false,
  bool prerelease = false,
}) {
  final archive = 'TimeCue-$version-windows-x64-20261013-120000-1234567.zip';
  return {
    'tag_name': 'v$version',
    'draft': draft,
    'prerelease': prerelease,
    'body': '更新说明',
    'assets': [
      {
        'name': archive,
        'browser_download_url': 'https://github.com/releases/$archive',
      },
      {
        'name': '$archive.sha256',
        'browser_download_url': 'https://github.com/releases/$archive.sha256',
      },
    ],
  };
}

class FakeTransport implements SoftwareUpdateTransport {
  FakeTransport(this.release);
  final SoftwareUpdateRelease? release;
  bool closed = false;
  @override
  Future<SoftwareUpdateRelease?> latestRelease() async => release;
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
  void close() => closed = true;
}

class FakePlatform implements SoftwareUpdatePlatform {
  final events = <String>[];
  String? status;
  String? planJson;
  Completer<String?>? statusRead;
  @override
  Future<bool> prepareUpdate({
    required String helperPath,
    required String planPath,
  }) async {
    events.add('prepare');
    expect(File(helperPath).existsSync(), isTrue);
    expect(File(planPath).existsSync(), isTrue);
    planJson = await File(planPath).readAsString();
    return true;
  }

  @override
  Future<void> launchUpdate({
    required String helperPath,
    required String planPath,
  }) async {
    events.add('launch');
    expect(File(helperPath).existsSync(), isTrue);
    expect(File(planPath).existsSync(), isTrue);
  }

  @override
  Future<void> commitUpdate({required String planPath}) async {
    events.add('commit');
  }

  @override
  Future<void> cancelUpdate({required String planPath}) async {
    events.add('cancel');
  }

  @override
  Future<String?> readUpdateStatus({required String statusPath}) =>
      statusRead?.future ?? Future<String?>.value(status);
}

SoftwareUpdateConfiguration configuration(Directory root) =>
    SoftwareUpdateConfiguration(
      backend: 'managed',
      dataDirectory: p.join(root.path, 'data'),
      goExecutable: p.join(root.path, 'install', 'timecue-core.exe'),
      currentVersion: '0.1.0+1',
      installDirectory: p.join(root.path, 'install'),
      applicationDirectory: p.join(root.path, 'install'),
    );

void main() {
  test('只接受精确标签、完整 Windows ZIP 和匹配 sidecar', () {
    final parsed = parseLatestRelease(releaseJson())!;
    expect(parsed.version, '0.1.0+2');
    expect(parsed.notes, '更新说明');
    expect(parsed.archiveName, startsWith('TimeCue-0.1.0+2-windows-x64-'));
    expect(parseLatestRelease(releaseJson(draft: true)), isNull);
    final noWindowsPackage = releaseJson();
    (noWindowsPackage['assets'] as List).clear();
    expect(parseLatestRelease(noWindowsPackage), isNull);
    final missingSidecar = releaseJson();
    (missingSidecar['assets'] as List).removeLast();
    expect(() => parseLatestRelease(missingSidecar), throwsFormatException);
  });

  test('external 配置可读取但不能安装', () {
    final parsed = SoftwareUpdateConfiguration.fromNative({
      'backend': 'external',
      'dataDir': r'C:\Users\user\AppData\TimeCue',
      'executable': '',
      'version': '0.1.0+1',
      'installDirectory': r'C:\Program Files\TimeCue',
    }, applicationDirectory: r'C:\Program Files\TimeCue');
    expect(parsed.goExecutable, isEmpty);
    expect(parsed.canCheckOn(true), isTrue);
    expect(parsed.isSupportedOn(true), isFalse);
  });

  test('应用目录与安装目录不同时禁止安装', () {
    final root = Directory.systemTemp;
    final original = configuration(root);
    final mismatched = SoftwareUpdateConfiguration(
      backend: 'managed',
      dataDirectory: original.dataDirectory,
      goExecutable: original.goExecutable,
      currentVersion: original.currentVersion,
      installDirectory: original.installDirectory,
      applicationDirectory: p.join(root.path, 'another-install'),
    );
    expect(mismatched.canCheckOn(true), isTrue);
    expect(mismatched.isSupportedOn(true), isFalse);
  });

  test('下载后由 helper 准备，提交退出握手并保留工作计划', () async {
    final root = await Directory.systemTemp.createTemp('timecue-update-test-');
    addTearDown(() => root.delete(recursive: true));
    final config = configuration(root);
    await Directory(config.dataDirectory).create(recursive: true);
    await Directory(config.installDirectory).create(recursive: true);
    await File(p.join(config.dataDirectory, 'config.yaml')).writeAsString('''
db:
  db_path: user-data/app.db
log:
  log_path: user-log
''');
    await File(config.helperPath).writeAsString('helper');
    final transport = FakeTransport(parseLatestRelease(releaseJson()));
    final platform = FakePlatform();
    final controller = SoftwareUpdateController(
      configuration: config,
      platform: platform,
      transport: transport,
      windows: true,
    );
    addTearDown(controller.dispose);

    await controller.checkForUpdates();
    expect(controller.state, SoftwareUpdateState.available);
    var exited = false;
    await controller.downloadAndInstall(
      requestExit: () async {
        exited = true;
        platform.events.add('exit');
      },
    );

    expect(exited, isTrue);
    expect(controller.state, SoftwareUpdateState.waitingExit);
    expect(platform.events, ['prepare', 'launch', 'commit', 'exit']);
    final plan = jsonDecode(platform.planJson!) as Map<String, dynamic>;
    expect(plan['schemaVersion'], 1);
    expect(plan['currentVersion'], '0.1.0+1');
    expect(plan['targetVersion'], '0.1.0+2');
    expect(
      plan['protectedPaths'],
      contains(p.join(config.dataDirectory, 'config.yaml')),
    );
    expect(
      plan['protectedPaths'],
      contains(p.join(config.dataDirectory, 'user-data', 'app.db-wal')),
    );
    expect(
      plan['protectedPaths'],
      contains(p.join(config.dataDirectory, 'user-log')),
    );
    expect(
      plan['protectedPaths'],
      isNot(contains(p.join(config.dataDirectory, 'user-data'))),
    );
    expect(
      plan['statusPath'],
      p.join(config.dataDirectory, 'software-update-status.json'),
    );
  });

  test('退出请求失败会取消 helper 且保留工作目录', () async {
    final root = await Directory.systemTemp.createTemp(
      'timecue-update-cancel-',
    );
    addTearDown(() => root.delete(recursive: true));
    final config = configuration(root);
    await Directory(config.dataDirectory).create(recursive: true);
    await Directory(config.installDirectory).create(recursive: true);
    await File(config.helperPath).writeAsString('helper');
    final platform = FakePlatform();
    final controller = SoftwareUpdateController(
      configuration: config,
      platform: platform,
      transport: FakeTransport(parseLatestRelease(releaseJson())),
      windows: true,
      temporaryDirectory: await Directory(p.join(root.path, 'temp')).create(),
    );
    addTearDown(controller.dispose);
    await controller.checkForUpdates();
    await controller.downloadAndInstall(
      requestExit: () async => throw StateError('exit failed'),
    );
    expect(controller.state, SoftwareUpdateState.failed);
    expect(platform.events, ['prepare', 'launch', 'commit', 'cancel']);
    final work = Directory(p.join(root.path, 'temp'))
        .listSync()
        .whereType<Directory>()
        .where(
          (directory) =>
              p.basename(directory.path).startsWith('timecue-update-'),
        );
    expect(work, isNotEmpty);
  });

  test('无法准确保护的 SQLite file: 数据源拒绝在线安装', () async {
    final root = await Directory.systemTemp.createTemp('timecue-update-uri-');
    addTearDown(() => root.delete(recursive: true));
    final config = configuration(root);
    await Directory(config.dataDirectory).create(recursive: true);
    await Directory(config.installDirectory).create(recursive: true);
    await File(p.join(config.dataDirectory, 'config.yaml')).writeAsString('''
db:
  db_path: file:custom.db?mode=rwc
''');
    await File(config.helperPath).writeAsString('helper');
    final platform = FakePlatform();
    final controller = SoftwareUpdateController(
      configuration: config,
      platform: platform,
      transport: FakeTransport(parseLatestRelease(releaseJson())),
      windows: true,
    );
    addTearDown(controller.dispose);
    await controller.checkForUpdates();
    await controller.downloadAndInstall(requestExit: () async {});
    expect(controller.state, SoftwareUpdateState.failed);
    expect(controller.error, contains('file: 数据源'));
    expect(platform.events, isEmpty);
  });

  test('重启后恢复已完成的安装状态', () async {
    final root = await Directory.systemTemp.createTemp(
      'timecue-update-status-',
    );
    addTearDown(() => root.delete(recursive: true));
    final platform = FakePlatform()
      ..status = jsonEncode({
        'state': 'installed',
        'targetVersion': '0.1.0+1',
        'message': '版本已更新',
      });
    final controller = SoftwareUpdateController(
      configuration: configuration(root),
      platform: platform,
      transport: FakeTransport(null),
      windows: true,
    );
    addTearDown(controller.dispose);
    await controller.restoreStatus();
    expect(controller.state, SoftwareUpdateState.installComplete);
    expect(controller.error, '版本已更新');
  });

  test('迟到的状态读取不会覆盖较新的更新检查', () async {
    final root = await Directory.systemTemp.createTemp('timecue-update-race-');
    addTearDown(() => root.delete(recursive: true));
    final platform = FakePlatform()..statusRead = Completer<String?>();
    final controller = SoftwareUpdateController(
      configuration: configuration(root),
      platform: platform,
      transport: FakeTransport(parseLatestRelease(releaseJson())),
      windows: true,
    );
    addTearDown(controller.dispose);
    final restoring = controller.restoreStatus();
    await controller.checkForUpdates();
    platform.statusRead!.complete(
      jsonEncode({'state': 'failed', 'message': '旧的安装失败状态'}),
    );
    await restoring;
    expect(controller.state, SoftwareUpdateState.available);
    expect(controller.error, isNull);
  });
}
