import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:yaml_edit/yaml_edit.dart';

/// 用户文件中的覆盖值；null 表示交由 Go 的内嵌默认配置决定。
class ConfigOverrides {
  const ConfigOverrides({this.logLevel, this.logPath, this.databasePath});

  final String? logLevel;
  final String? logPath;
  final String? databasePath;
}

class ConfigSaveResult {
  const ConfigSaveResult({required this.overrides, required this.pathsChanged});

  final ConfigOverrides overrides;

  /// 仅表示保存的路径字段发生变化，运行资源在下一次启动时应用。
  final bool pathsChanged;
}

/// Flutter 的配置文件写入入口，共用宿主传给 Go 的数据目录。
class ConfigFileStore {
  ConfigFileStore(String dataDir) : configPath = _configPath(dataDir);

  final String configPath;
  static final Map<String, Future<void>> _pending = {};

  static String _configPath(String dataDir) {
    if (dataDir.trim().isEmpty) {
      throw ArgumentError.value(dataDir, 'dataDir', '数据目录不能为空');
    }
    return p.join(p.normalize(Directory(dataDir).absolute.path), 'config.yaml');
  }

  Future<ConfigOverrides> read() => _serialize(() async {
    final editor = _parse(await _readContents());
    return _overrides(editor);
  });

  Future<ConfigSaveResult> update({
    String? logLevel,
    String? logPath,
    String? databasePath,
  }) => _serialize(() async {
    if (logLevel == null && logPath == null && databasePath == null) {
      throw ArgumentError('至少指定一个配置项');
    }
    if (logLevel != null &&
        !const {'debug', 'info', 'warn', 'error'}.contains(logLevel)) {
      throw ArgumentError.value(logLevel, 'logLevel', '不支持的日志级别');
    }
    for (final value in [logPath, databasePath]) {
      if (value != null && (value.trim().isEmpty || value.contains('\u0000'))) {
        throw ArgumentError('路径不能为空或包含空字符');
      }
    }

    final editor = _parse(await _readContents());
    final before = _overrides(editor);
    if (logLevel != null) _set(editor, 'log', 'level', logLevel);
    if (logPath != null) _set(editor, 'log', 'log_path', logPath);
    if (databasePath != null) _set(editor, 'db', 'db_path', databasePath);
    final after = _overrides(editor);
    await _save(editor.toString());
    return _result(before, after);
  });

  /// 移除运行配置覆盖值，保留 app、desktop、未知字段及默认配置基线。
  Future<ConfigSaveResult> restoreRuntimeDefaults() => _serialize(() async {
    final editor = _parse(await _readContents());
    final before = _overrides(editor);
    var changed = false;
    for (final field in [
      ['log', 'level'],
      ['log', 'log_path'],
      ['db', 'db_path'],
    ]) {
      final root = editor.parseAt([]).value as Map;
      final sectionKey = _key(root, field[0]);
      final section = root[sectionKey];
      if (section is Map) {
        final fieldKey = _key(section, field[1]);
        if (section.containsKey(fieldKey)) {
          editor.remove([sectionKey, fieldKey]);
          changed = true;
        }
      }
    }
    final after = _overrides(editor);
    if (changed) {
      await _save(editor.toString());
    }
    return _result(before, after);
  });

  static ConfigSaveResult _result(
    ConfigOverrides before,
    ConfigOverrides after,
  ) => ConfigSaveResult(
    overrides: after,
    pathsChanged:
        before.logPath != after.logPath ||
        before.databasePath != after.databasePath,
  );

  // 同一文件的读取与修改串行化，即使不同组件创建了多个存储实例也不会互相覆盖。
  Future<T> _serialize<T>(Future<T> Function() operation) {
    final key = Platform.isWindows ? configPath.toLowerCase() : configPath;
    final result = (_pending[key] ?? Future<void>.value()).then(
      (_) => operation(),
    );
    final tail = result.then<void>(
      (_) {},
      onError: (Object _, StackTrace _) {},
    );
    _pending[key] = tail;
    tail.then((_) {
      if (identical(_pending[key], tail)) _pending.remove(key);
    });
    return result;
  }

  Future<String> _readContents() async {
    final type = await FileSystemEntity.type(configPath);
    if (type == FileSystemEntityType.notFound) return '';
    if (type != FileSystemEntityType.file) {
      throw FileSystemException('配置路径不是文件', configPath);
    }
    return File(configPath).readAsString();
  }

  static YamlEditor _parse(String contents) {
    final editor = YamlEditor(contents);
    final root = editor.parseAt([]).value;
    if (root == null) {
      editor.update([], <String, Object?>{});
    } else if (root is! Map) {
      throw const FormatException('配置根节点必须是映射');
    }
    return editor;
  }

  static ConfigOverrides _overrides(YamlEditor editor) {
    final root = editor.parseAt([]).value as Map;
    String? value(String sectionName, String key) {
      final section = root[_key(root, sectionName)];
      if (section == null) return null;
      if (section is! Map) throw FormatException('$sectionName 必须是映射');
      final value = section[_key(section, key)];
      if (value != null && value is! String) {
        throw FormatException('$sectionName.$key 必须是字符串');
      }
      return value as String?;
    }

    return ConfigOverrides(
      logLevel: value('log', 'level'),
      logPath: value('log', 'log_path'),
      databasePath: value('db', 'db_path'),
    );
  }

  static void _set(
    YamlEditor editor,
    String section,
    String key,
    String value,
  ) {
    final root = editor.parseAt([]).value as Map;
    final sectionKey = _key(root, section);
    if (root[sectionKey] == null) {
      editor.update([sectionKey], <String, Object?>{});
    }
    final fields = editor.parseAt([sectionKey]).value as Map;
    editor.update([sectionKey, _key(fields, key)], value);
  }

  // 与 Go Viper 的大小写规则一致，保留已有键名，拒绝大小写不同的重复配置项。
  static String _key(Map values, String name) {
    String? found;
    for (final key in values.keys) {
      if (key is String && key.toLowerCase() == name) {
        if (found != null) throw FormatException('重复配置项：$name');
        found = key;
      }
    }
    return found ?? name;
  }

  Future<void> _save(String contents) async {
    final directory = Directory(p.dirname(configPath));
    await directory.create(recursive: true);
    final staging = await directory.createTemp('.config-write-');
    final temporary = File(p.join(staging.path, 'config.yaml'));
    try {
      await temporary.writeAsString(contents, flush: true);
      await temporary.rename(configPath);
    } finally {
      // 清理失败不覆盖已提交结果或原始写入错误。
      try {
        if (await temporary.exists()) await temporary.delete();
        await staging.delete();
      } on FileSystemException {
        // 下次人工清理遗留临时目录即可，不把配置保存结果改为失败。
      }
    }
  }
}
