part of 'config_file_store.dart';

/// 单个 DeepSeek 模型的本地设置，不包含原始密钥。
class DeepSeekModelSettings {
  const DeepSeekModelSettings({
    required this.model,
    this.contextWindow = 32768,
    this.maxOutputTokens = 8192,
  });

  final String model;
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
  /// 只读取第一个 DeepSeek 模型；原始密钥从不返回页面。
  Future<DeepSeekModelSettings?> readDeepSeekModel() => _serialize(() async {
    final editor = ConfigFileStore._parse(await _readContents());
    final providers = _providers(editor);
    for (final provider in providers) {
      if (_field(provider, 'provider') != 'deepseek') continue;
      final models = _models(provider);
      if (models.isEmpty) continue;
      final value = models.first;
      final model = _field(value, 'model');
      final context = _field(value, 'context_window');
      final output = _field(value, 'max_output_tokens') ?? 8192;
      if (model is! String || context is! int || output is! int) {
        throw const FormatException('模型配置格式无效，请检查本地配置');
      }
      return DeepSeekModelSettings(
        model: model,
        contextWindow: context,
        maxOutputTokens: output,
      );
    }
    return null;
  });

  /// 先保存本机密钥，再发布模型配置；不覆盖其他模型、提供方或运行设置。
  Future<void> saveDeepSeekModel(
    DeepSeekModelSettings settings, {
    required String apiKey,
  }) => _serialize(() async {
    settings.validate();
    final key = apiKey.trim();
    if (key.isEmpty) throw const FormatException('请填写 API Key');
    // 使用单引号禁止变量展开；拒绝控制字符及无法无损表示的引号、反斜杠。
    if (apiKey.runes.any((c) => c < 32 || c > 126 || c == 39 || c == 92)) {
      throw const FormatException('API Key 含不支持的字符，请检查后重试');
    }
    final editor = ConfigFileStore._parse(await _readContents());
    final root = editor.parseAt([]).value as Map;
    final section = ConfigFileStore._key(root, 'chat_model');
    final providers = _providers(editor);
    var providerIndex = providers.indexWhere(
      (v) => _field(v, 'provider') == 'deepseek',
    );
    if (providerIndex < 0) {
      providerIndex = providers.length;
      providers.add(<String, Object?>{
        'provider': 'deepseek',
        'models': <Object?>[],
      });
    }
    final provider = providers[providerIndex];
    final models = _models(provider);
    final keys = <String>{};
    for (var i = 0; i < providers.length; i++) {
      final identity = _field(providers[i], 'provider');
      for (var j = 0; j < _models(providers[i]).length; j++) {
        if (i == providerIndex && j == 0) continue;
        final model = _field(_models(providers[i])[j], 'model');
        if (identity is! String ||
            model is! String ||
            identity.trim().isEmpty ||
            model.trim().isEmpty ||
            !keys.add(_modelKey(identity, model))) {
          throw const FormatException('已有模型身份或密钥名称冲突，请先修复配置');
        }
      }
    }
    final keyName = _modelKey('deepseek', settings.model);
    if (!keys.add(keyName)) throw const FormatException('模型 ID 与已有模型的密钥名称冲突');
    if (!root.containsKey(section)) editor.update([section], <Object?>[]);
    if (providerIndex == (editor.parseAt([section]).value as List).length) {
      editor.appendToList(
        [section],
        {
          'provider': 'deepseek',
          'base_url': 'https://api.deepseek.com',
          'models': <Object?>[],
        },
      );
    }
    final path = <Object>[section, providerIndex];
    editor.update([
      ...path,
      ConfigFileStore._key(provider, 'base_url'),
    ], 'https://api.deepseek.com');
    final modelsKey = ConfigFileStore._key(provider, 'models');
    if (_field(provider, 'models') == null) {
      editor.update([...path, modelsKey], <Object?>[]);
    }
    if (models.isEmpty) {
      editor.appendToList([...path, modelsKey], <String, Object?>{});
    }
    final original = models.isEmpty ? <String, Object?>{} : models.first;
    for (final entry in <String, Object>{
      'model': settings.model,
      'context_window': settings.contextWindow,
      'max_output_tokens': settings.maxOutputTokens,
    }.entries) {
      editor.update([
        ...path,
        modelsKey,
        0,
        ConfigFileStore._key(original, entry.key),
      ], entry.value);
    }
    final envPath = p.join(p.dirname(configPath), '.env');
    final existed = await File(envPath).exists();
    final before = existed ? await File(envPath).readAsString() : '';
    final encodedKey = RegExp(r'^[A-Za-z0-9_.-]+$').hasMatch(key)
        ? key
        : "'$key'";
    final after = _updateEnv(before, keyName, encodedKey);
    await _replace(envPath, after);
    try {
      await _save(editor.toString());
    } catch (_) {
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
  });
}

Object? _field(Map value, String name) =>
    value[ConfigFileStore._key(value, name)];

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
String _updateEnv(String contents, String name, String encodedKey) {
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
    if (match.group(1) == name) {
      if (!replaced) result.writeln('$name=$encodedKey');
      replaced = true;
    } else {
      result.write(record);
    }
  }
  if (!replaced) {
    if (contents.isNotEmpty && !contents.endsWith('\n')) result.writeln();
    result.writeln('$name=$encodedKey');
  }
  return result.toString();
}
