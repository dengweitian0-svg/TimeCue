part of 'config_file_store.dart';

/// 单个 DeepSeek 模型的本地设置，不包含原始密钥。
class DeepSeekModelSettings {
  const DeepSeekModelSettings({
    required this.model,
    this.originalModel,
    this.contextWindow = 32768,
    this.maxOutputTokens = 8192,
  });

  final String model;

  /// 编辑前的原身份，用于改名时保留对应模型的未知字段。
  final String? originalModel;
  final int contextWindow;
  final int maxOutputTokens;

  /// 在任何文件写入前校验身份及可由后端表示的预算。
  void validate() {
    if (model.trim().isEmpty || model.runes.any((c) => c < 32 || c == 127)) {
      throw const FormatException('请填写有效的模型 ID');
    }
    if (contextWindow <= 0 ||
        contextWindow > 2147483647 ||
        maxOutputTokens <= 0 ||
        maxOutputTokens >= contextWindow) {
      throw const FormatException('预算必须为正整数，最大输出量须小于上下文预算（最多 2147483647）');
    }
  }
}

/// 模型设置与普通运行设置共用同一串行队列和单文件替换能力。
extension DeepSeekConfigFileStore on ConfigFileStore {
  /// 读取全部 DeepSeek 分组的模型，不返回原始密钥。
  Future<List<DeepSeekModelSettings>> readDeepSeekModels() => _serialize(
    () async {
      return _readDeepSeekModels(ConfigFileStore._parse(await _readContents()));
    },
  );

  /// 兼容单模型调用方，只读取首个已有模型。
  Future<DeepSeekModelSettings?> readDeepSeekModel() async =>
      (await readDeepSeekModels()).firstOrNull;

  /// 替换完整 DeepSeek 模型集合；保留其他提供方及移除模型的旧密钥。
  Future<void> saveDeepSeekModels(
    List<DeepSeekModelSettings> settings, {
    required String apiKey,
  }) => _serialize(() async {
    final editor = ConfigFileStore._parse(await _readContents());
    await _saveDeepSeekModels(editor, settings, apiKey);
  });

  /// 兼容单模型调用方，只编辑首个模型并保留其余模型。
  Future<void> saveDeepSeekModel(
    DeepSeekModelSettings settings, {
    required String apiKey,
  }) => _serialize(() async {
    if (apiKey.trim().isEmpty) throw const FormatException('请填写 API Key');
    final editor = ConfigFileStore._parse(await _readContents());
    final current = _readDeepSeekModels(editor);
    await _saveDeepSeekModels(editor, [
      DeepSeekModelSettings(
        model: settings.model,
        originalModel: current.firstOrNull?.model,
        contextWindow: settings.contextWindow,
        maxOutputTokens: settings.maxOutputTokens,
      ),
      ...current.skip(1),
    ], apiKey);
  });

  Future<void> _saveDeepSeekModels(
    YamlEditor editor,
    List<DeepSeekModelSettings> settings,
    String apiKey,
  ) async {
    if (settings.isEmpty) throw const FormatException('至少保留一个模型');
    for (final value in settings) {
      value.validate();
    }
    final key = apiKey.trim();
    // 使用单引号禁止变量展开；拒绝控制字符及不能无损表示的引号、反斜杠。
    if (apiKey.runes.any((c) => c < 32 || c > 126 || c == 39 || c == 92)) {
      throw const FormatException('API Key 含不支持的字符，请检查后重试');
    }
    final root = editor.parseAt([]).value as Map;
    final section = ConfigFileStore._key(root, 'chat_model');
    final providers = _providers(editor);
    final keys = <String>{};
    final originals = <String>{};
    for (var i = 0; i < providers.length; i++) {
      final identity = _field(providers[i], 'provider');
      final models = _models(providers[i]);
      for (var j = 0; j < models.length; j++) {
        final model = _field(models[j], 'model');
        if (identity is! String ||
            model is! String ||
            identity.trim().isEmpty ||
            model.trim().isEmpty) {
          throw const FormatException('已有模型身份无效，请先修复配置');
        }
        if (identity == 'deepseek') {
          if (originals.contains(model)) {
            throw const FormatException('已有 DeepSeek 模型身份重复，请先修复配置');
          }
          originals.add(model);
        } else if (!keys.add(_modelKey(identity, model))) {
          throw const FormatException('已有模型密钥名称冲突，请先修复配置');
        }
      }
    }
    final usedOriginals = <String>{};
    for (final value in settings) {
      if (!keys.add(_modelKey('deepseek', value.model))) {
        throw const FormatException('模型身份重复或密钥名称与已有模型冲突');
      }
      final original = value.originalModel ?? value.model;
      if (originals.contains(original) && !usedOriginals.add(original)) {
        throw const FormatException('同一原模型不能重复编辑');
      }
    }
    final envPath = p.join(p.dirname(configPath), '.env');
    final existed = await File(envPath).exists();
    final before = existed ? await File(envPath).readAsString() : '';
    var after = before;
    if (key.isNotEmpty) {
      final encoded = RegExp(r'^[A-Za-z0-9_.-]+$').hasMatch(key)
          ? key
          : "'$key'";
      for (final value in settings) {
        after = _updateEnv(after, _modelKey('deepseek', value.model), encoded);
      }
    } else {
      final existingKeys = _envAssignments(before);
      for (final value in settings) {
        if (!_hasEnvValue(existingKeys[_modelKey('deepseek', value.model)])) {
          throw const FormatException('目标模型缺少本地密钥，请填写 API Key');
        }
      }
    }
    if (root[section] == null) editor.update([section], <Object?>[]);
    var primary = _deepSeekProviderIndex(providers);
    if (primary < 0) {
      primary = providers.length;
      editor.appendToList(
        [section],
        {
          'provider': 'deepseek',
          'base_url': 'https://api.deepseek.com',
          'models': <Object?>[],
        },
      );
      providers.add(<String, Object?>{
        'provider': 'deepseek',
        'models': <Object?>[],
      });
    }
    // 先在原位置更新，随后逆序移除，避免下标变化损坏未知字段或注释。
    for (var i = 0; i < providers.length; i++) {
      final provider = providers[i];
      if (_field(provider, 'provider') != 'deepseek') continue;
      final path = <Object>[section, i];
      editor.update([
        ...path,
        ConfigFileStore._key(provider, 'base_url'),
      ], 'https://api.deepseek.com');
      final modelsKey = ConfigFileStore._key(provider, 'models');
      if (_field(provider, 'models') == null) {
        editor.update([...path, modelsKey], <Object?>[]);
      }
      final models = _models(provider);
      for (var j = models.length - 1; j >= 0; j--) {
        final original = _field(models[j], 'model');
        final target = settings
            .where((v) => (v.originalModel ?? v.model) == original)
            .firstOrNull;
        if (target == null) {
          editor.remove([...path, modelsKey, j]);
        } else {
          _writeModel(editor, [...path, modelsKey, j], models[j], target);
        }
      }
    }
    final modelsPath = <Object>[
      section,
      primary,
      ConfigFileStore._key(providers[primary], 'models'),
    ];
    for (final value in settings) {
      if (originals.contains(value.originalModel ?? value.model)) continue;
      editor.insertIntoList(modelsPath, 0, <String, Object?>{});
      _writeModel(editor, [...modelsPath, 0], const {}, value);
    }
    if (key.isNotEmpty) await _replace(envPath, after);
    try {
      await _save(editor.toString());
    } catch (_) {
      if (key.isEmpty) rethrow;
      try {
        if (existed) {
          await _replace(envPath, before);
        } else {
          await File(envPath).delete();
        }
      } catch (_) {
        throw const FormatException('模型配置未保存，密钥恢复失败，请检查本地文件后重试');
      }
      throw const FormatException('模型配置未保存，密钥已恢复，请重试');
    }
  }
}

List<DeepSeekModelSettings> _readDeepSeekModels(YamlEditor editor) {
  final result = <DeepSeekModelSettings>[];
  for (final provider in _providers(editor)) {
    if (_field(provider, 'provider') != 'deepseek') continue;
    for (final value in _models(provider)) {
      final model = _field(value, 'model');
      final context = _field(value, 'context_window');
      final output = _field(value, 'max_output_tokens') ?? 8192;
      if (model is! String || context is! int || output is! int) {
        throw const FormatException('模型配置格式无效，请检查本地配置');
      }
      result.add(
        DeepSeekModelSettings(
          model: model,
          originalModel: model,
          contextWindow: context,
          maxOutputTokens: output,
        ),
      );
    }
  }
  return result;
}

void _writeModel(
  YamlEditor editor,
  List<Object> path,
  Map original,
  DeepSeekModelSettings settings,
) {
  for (final entry in <String, Object>{
    'model': settings.model,
    'context_window': settings.contextWindow,
    'max_output_tokens': settings.maxOutputTokens,
  }.entries) {
    final previous = _field(original, entry.key);
    if (previous == entry.value ||
        (entry.key == 'max_output_tokens' &&
            previous == null &&
            entry.value == 8192 &&
            original.isNotEmpty)) {
      continue;
    }
    editor.update([
      ...path,
      ConfigFileStore._key(original, entry.key),
    ], entry.value);
  }
}

Object? _field(Map value, String name) =>
    value[ConfigFileStore._key(value, name)];

// 读写统一选择首个已有模型的分组；没有模型时才复用首个空分组。
int _deepSeekProviderIndex(List<Map> providers) {
  var emptyIndex = -1;
  for (var i = 0; i < providers.length; i++) {
    final provider = providers[i];
    if (_field(provider, 'provider') != 'deepseek') continue;
    if (_models(provider).isNotEmpty) return i;
    if (emptyIndex < 0) emptyIndex = i;
  }
  return emptyIndex;
}

List<Map> _providers(YamlEditor editor) {
  final root = editor.parseAt([]).value as Map;
  final value = _field(root, 'chat_model');
  if (value == null) return <Map>[];
  if (value is! List || value.any((v) => v is! Map)) {
    throw const FormatException('chat_model 必须是提供方列表');
  }
  return value.cast<Map>().toList();
}

List<Map> _models(Map provider) {
  final value = _field(provider, 'models');
  if (value == null) return <Map>[];
  if (value is! List || value.any((v) => v is! Map)) {
    throw const FormatException('models 必须是模型列表');
  }
  return value.cast<Map>().toList();
}

String _modelKey(String provider, String model) =>
    '${provider}_$model'.runes.map((c) {
      if (c >= 97 && c <= 122) return String.fromCharCode(c - 32);
      return c >= 65 && c <= 90 || c >= 48 && c <= 57
          ? String.fromCharCode(c)
          : '_';
    }).join();

// 按完整赋值记录处理，保留无关注释、引号和跨行值，不匹配引号内的伪变量。
Map<String, String> _envAssignments(String contents) {
  final result = <String, String>{};
  _updateEnv(contents, null, null, assignments: result);
  return result;
}

bool _hasEnvValue(String? value) {
  if (value == null) return false;
  final text = value.trim();
  if (text.isEmpty || text.startsWith('#')) return false;
  if (text.startsWith('"') || text.startsWith("'")) {
    for (var i = 1; i < text.length; i++) {
      if (text[i] == '\\') {
        i++;
      } else if (text[i] == text[0]) {
        return text.substring(1, i).trim().isNotEmpty;
      }
    }
    return false;
  }
  return true;
}

String _updateEnv(
  String contents,
  String? name,
  String? encodedKey, {
  Map<String, String>? assignments,
}) {
  final lines = contents.split(RegExp(r'(?<=\n)|(?<=\r)(?!\n)'));
  final result = StringBuffer();
  var replaced = false;
  final assignment = RegExp(
    r'^\s*(?:export\s+)?([A-Za-z0-9_.]+)(?:\s*=\s*|:\s+)(.*)$',
    dotAll: true,
  );
  for (var i = 0; i < lines.length; i++) {
    var record = lines[i];
    if (record.trim().isEmpty || record.trimLeft().startsWith('#')) {
      result.write(record);
      continue;
    }
    final match = assignment.firstMatch(record);
    if (match == null) throw const FormatException('密钥文件格式无效，请检查本地文件');
    final value = match.group(2)!.trimLeft();
    if (value.startsWith('"') || value.startsWith("'")) {
      final quote = value[0];
      var quoted = value.substring(1);
      bool closed() {
        for (var j = 0; j < quoted.length; j++) {
          if (quoted[j] == '\\') {
            j++;
            continue;
          }
          if (quoted[j] == quote) return true;
        }
        return false;
      }

      while (!closed() && i + 1 < lines.length) {
        final next = lines[++i];
        record += next;
        quoted += next;
      }
      if (!closed()) throw const FormatException('密钥文件引号未闭合，请检查本地文件');
    }
    assignments?[match.group(1)!] = assignment
        .firstMatch(record)!
        .group(2)!
        .trim();
    if (match.group(1) == name) {
      if (!replaced) result.writeln('$name=$encodedKey');
      replaced = true;
    } else {
      result.write(record);
    }
  }
  if (!replaced && name != null) {
    if (contents.isNotEmpty && !contents.endsWith('\n')) result.writeln();
    result.writeln('$name=$encodedKey');
  }
  return result.toString();
}
