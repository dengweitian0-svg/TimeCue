import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:yaml_edit/yaml_edit.dart';

void main() {
  late Directory directory;
  late ConfigFileStore store;
  late File config;

  setUp(() async {
    directory = await Directory.systemTemp.createTemp('timecue-config-');
    store = ConfigFileStore(p.join(directory.path, '中文 空格'));
    config = File(store.configPath);
  });

  tearDown(() async {
    if (await directory.exists()) await directory.delete(recursive: true);
  });

  Future<void> seed(String contents) async {
    await config.parent.create(recursive: true);
    await config.writeAsString(contents);
  }

  test('缺少用户配置时只读覆盖值，写入只包含用户选择的字段', () async {
    final defaults = File(p.join('..', 'internal', 'config', 'default.yaml'));
    final baseline = await defaults.readAsBytes();
    final before = await store.read();
    expect(before.logLevel, isNull);
    expect(before.databasePath, isNull);
    expect(await config.exists(), isFalse);

    final saved = await store.update(databasePath: './新数据库.db');
    expect(saved.pathsChanged, isTrue);
    expect(saved.overrides.databasePath, './新数据库.db');
    final contents = await config.readAsString();
    final root = YamlEditor(contents).parseAt([]).value as Map;
    expect(root.keys, ['db']);
    expect(await defaults.readAsBytes(), baseline);
    expect(
      await config.parent
          .list()
          .map((entry) => p.basename(entry.path))
          .toList(),
      ['config.yaml'],
    );
  });

  test('修改路径和级别保留注释、未知字段、应用元数据和 desktop', () async {
    await seed('''# 用户注释
app:
  name: existing
desktop:
  start_on_login: true # 自启注释
log:
  level: info # 级别注释
  custom: keep
other:
  numbers: [1, 2]
''');
    final saved = await store.update(
      logLevel: 'debug',
      logPath: r'D:\日志 空格',
      databasePath: './db/资料.db',
    );
    expect(saved.pathsChanged, isTrue);
    final contents = await config.readAsString();
    expect(contents, contains('# 用户注释'));
    expect(contents, contains('# 自启注释'));
    expect(contents, contains('# 级别注释'));
    final root = YamlEditor(contents).parseAt([]).value as Map;
    expect(root['app']['name'], 'existing');
    expect(root['desktop']['start_on_login'], isTrue);
    expect(root['log']['custom'], 'keep');
    expect(root['other']['numbers'], [1, 2]);
    final current = await store.read();
    expect(current.logLevel, 'debug');
    expect(current.logPath, r'D:\日志 空格');
    expect(current.databasePath, './db/资料.db');
  });

  test('同一文件的多个存储实例并发更新不丢字段', () async {
    final second = ConfigFileStore(config.parent.path);
    await Future.wait([
      store.update(logLevel: 'debug'),
      second.update(databasePath: './db/new.db'),
      store.update(logPath: './new-log'),
    ]);
    final current = await store.read();
    expect(current.logLevel, 'debug');
    expect(current.databasePath, './db/new.db');
    expect(current.logPath, './new-log');
    expect((await store.update(logLevel: 'warn')).pathsChanged, isFalse);
    expect((await store.update(logPath: './new-log')).pathsChanged, isFalse);
  });

  test('恢复只移除运行覆盖值，保留其他配置及默认配置文件', () async {
    await seed('''# 保留注释
app:
  name: app-name
desktop:
  start_on_login: true
log:
  level: debug
  log_path: ./log-new
  custom: keep
db:
  db_path: ./db-new.db
  custom: 123
''');
    final saved = await store.restoreRuntimeDefaults();
    expect(saved.pathsChanged, isTrue);
    expect(saved.overrides.logLevel, isNull);
    expect(saved.overrides.logPath, isNull);
    expect(saved.overrides.databasePath, isNull);
    final contents = await config.readAsString();
    expect(contents, contains('# 保留注释'));
    final root = YamlEditor(contents).parseAt([]).value as Map;
    expect(root['app']['name'], 'app-name');
    expect(root['desktop']['start_on_login'], isTrue);
    expect(root['log'], {'custom': 'keep'});
    expect(root['db'], {'custom': 123});
  });

  test('无效输入或 YAML 保留原文件，失败后队列仍可继续使用', () async {
    const original = 'log:\n  level: info\n';
    await seed(original);
    for (final operation in [
      () => store.update(logLevel: 'trace'),
      () => store.update(logPath: ' '),
      () => store.update(databasePath: 'bad\u0000path'),
      () => store.update(),
    ]) {
      await expectLater(operation(), throwsArgumentError);
      expect(await config.readAsString(), original);
    }
    const invalidYaml = 'log: [invalid\n';
    await seed(invalidYaml);
    await expectLater(
      store.update(logLevel: 'debug'),
      throwsA(isA<Exception>()),
    );
    expect(await config.readAsString(), invalidYaml);
    await seed('log: [info]\n');
    await expectLater(store.update(logLevel: 'debug'), throwsFormatException);
    expect(await config.readAsString(), 'log: [info]\n');
    await seed(original);
    expect((await store.update(logLevel: 'debug')).overrides.logLevel, 'debug');
  });

  test('兼容已有大写键，恢复移除 null 覆盖，重复键保持原文件', () async {
    await seed('LOG:\n  LEVEL: info\nDB:\n  DB_PATH: ./old.db\n');
    expect((await store.read()).databasePath, './old.db');
    await store.update(logLevel: 'debug');
    final root =
        YamlEditor(await config.readAsString()).parseAt([]).value as Map;
    expect(root.keys, ['LOG', 'DB']);
    expect(root['LOG']['LEVEL'], 'debug');
    await seed('log:\n  level: null\n  custom: keep\n');
    await store.restoreRuntimeDefaults();
    final restored =
        YamlEditor(await config.readAsString()).parseAt([]).value as Map;
    expect(restored['log'], {'custom': 'keep'});
    const duplicate = 'log:\n  level: info\nLOG:\n  level: error\n';
    await seed(duplicate);
    await expectLater(store.update(logLevel: 'debug'), throwsFormatException);
    expect(await config.readAsString(), duplicate);
  });

  test('配置路径为目录时返回文件错误，修复后继续保存', () async {
    await Directory(config.path).create(recursive: true);
    await expectLater(
      store.update(logLevel: 'debug'),
      throwsA(isA<FileSystemException>()),
    );
    await Directory(config.path).delete();
    expect((await store.update(logLevel: 'debug')).overrides.logLevel, 'debug');
  });

  test('存储实例固定绝对目录，改变工作目录不改变写入位置', () async {
    final previous = Directory.current;
    try {
      Directory.current = directory;
      final relativeStore = ConfigFileStore('相对目录');
      Directory.current = previous;
      await relativeStore.update(logLevel: 'debug');
      expect(
        File(p.join(directory.path, '相对目录', 'config.yaml')).existsSync(),
        isTrue,
      );
      expect(p.isAbsolute(relativeStore.configPath), isTrue);
    } finally {
      Directory.current = previous;
    }
  });

  test('Windows 替换失败保留原文件并清理临时内容，随后仍可保存', () async {
    if (!Platform.isWindows) return;
    const original = '# 原始配置\nlog:\n  level: info\n';
    await seed(original);
    final protect = await Process.run('attrib', ['+R', config.path]);
    expect(protect.exitCode, 0);
    try {
      await expectLater(
        store.update(logLevel: 'debug'),
        throwsA(isA<FileSystemException>()),
      );
      expect(await config.readAsString(), original);
      expect(
        await config.parent
            .list()
            .map((entry) => p.basename(entry.path))
            .toList(),
        ['config.yaml'],
      );
    } finally {
      final unprotect = await Process.run('attrib', ['-R', config.path]);
      expect(unprotect.exitCode, 0);
    }
    expect((await store.update(logLevel: 'debug')).overrides.logLevel, 'debug');
  });
}
