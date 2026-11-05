part of 'config_file_store.dart';

/// 一组 Chat Completions 连接设置；来源定位只用于安全编辑旧分组。
class ModelConnectionSettings {
  const ModelConnectionSettings({
    required this.provider,
    required this.baseUrl,
    required this.models,
    String? displayName,
    this.sourceIndex,
    this.sourceProvider,
  }) : displayName = displayName ?? provider;

  final String provider;
  final String displayName;
  final String baseUrl;
  final List<ChatModelSettings> models;
  final int? sourceIndex;
  final String? sourceProvider;

  /// 写入前校验服务身份、地址及每个模型的预算。
  void validate() {
    if (displayName.trim().isEmpty ||
        displayName.runes.any((c) => c < 32 || c == 127)) {
      throw const ModelConfigException('请填写有效的连接名称');
    }
    if (provider.trim().isEmpty ||
        provider != provider.trim() ||
        provider.runes.any((c) => c < 32 || c == 127)) {
      throw const ModelConfigException('请填写有效的 provider');
    }
    final uri = Uri.tryParse(baseUrl);
    if (uri == null ||
        !const {'http', 'https'}.contains(uri.scheme) ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        (uri.hasPort && (uri.port <= 0 || uri.port > 65535)) ||
        baseUrl.contains('\\') ||
        RegExp(r'%(?![0-9a-fA-F]{2})').hasMatch(baseUrl) ||
        baseUrl.runes.any((c) => c <= 32 || c == 127)) {
      throw const ModelConfigException('服务地址须为不含账号、查询或片段的 HTTP(S) 地址');
    }
    if (models.isEmpty) throw const ModelConfigException('至少保留一个模型');
    for (final model in models) {
      model.validate();
    }
  }
}

/// 通用连接与旧模型设置使用相同的文件、串行队列及密钥规则。
extension ModelConnectionConfigFileStore on ConfigFileStore {
  /// 为聊天选择器读取本地名称；不扩展服务端模型身份和响应。
  Future<Map<String, String>> readConnectionNames() async => {
    for (final connection in await readModelConnections())
      connection.provider: connection.displayName,
  };

  /// 读取已有连接和逐模型密钥状态，不返回原始 Key。
  Future<List<ModelConnectionSettings>> readModelConnections() =>
      _serialize(() async {
        final providers = _providers(
          ConfigFileStore._parse(await _readContents()),
        );
        final keys = _envAssignments(await _readModelEnv());
        return [
          for (var i = 0; i < providers.length; i++)
            _readConnection(providers[i], i, keys),
        ];
      });

  /// 保存一份连接，保留其他分组；返回热加载应确认的完整 provider 模型集合。
  Future<Set<String>> saveModelConnection(
    ModelConnectionSettings settings, {
    required String apiKey,
  }) => _serialize(() async {
    settings.validate();
    final key = apiKey.trim();
    _validateModelApiKey(apiKey);
    final editor = ConfigFileStore._parse(await _readContents());
    final providers = _providers(editor);
    final index = settings.sourceIndex ?? providers.length;
    if (settings.sourceIndex != null &&
        (index < 0 ||
            index >= providers.length ||
            _field(providers[index], 'provider') != settings.sourceProvider)) {
      throw const ModelConfigException('原连接已变化，请重新打开设置后重试');
    }
    if (settings.sourceIndex != null &&
        settings.provider != settings.sourceProvider) {
      throw const ModelConfigException('已有连接身份不可修改，请修改连接名称或新建连接');
    }
    // 新连接不能借用其他分组的身份，显示名称不参与身份校验。
    if (settings.provider != settings.sourceProvider &&
        providers.any((v) => _field(v, 'provider') == settings.provider)) {
      throw const ModelConfigException('provider 已存在，请编辑已有连接或使用其他身份');
    }
    final keys = <String>{};
    for (var i = 0; i < providers.length; i++) {
      if (i == index) continue;
      final provider = _field(providers[i], 'provider');
      for (final model in _models(providers[i])) {
        final name = _field(model, 'model');
        if (provider is! String ||
            provider.trim().isEmpty ||
            name is! String ||
            name.trim().isEmpty) {
          throw const ModelConfigException('已有模型身份无效，请先修复配置');
        }
        if (!keys.add(_modelKey(provider, name))) {
          throw const ModelConfigException('已有模型密钥名称冲突，请先修复配置');
        }
      }
    }
    final originals = <String>{};
    for (final model in settings.models) {
      if (!keys.add(_modelKey(settings.provider, model.model))) {
        throw const ModelConfigException('模型身份重复或密钥名称与已有模型冲突');
      }
      if (!originals.add(model.originalModel ?? model.model)) {
        throw const ModelConfigException('同一原模型不能重复编辑');
      }
    }
    final before = await _readModelEnv();
    var after = before;
    final existingKeys = _envAssignments(before);
    for (final model in settings.models) {
      final name = _modelKey(settings.provider, model.model);
      if (key.isNotEmpty) {
        after = _updateEnv(after, name, _encodeModelApiKey(key));
      } else if (!_hasEnvValue(existingKeys[name])) {
        throw const ModelConfigException('目标模型缺少本地密钥，请填写 API Key');
      }
    }
    final root = editor.parseAt([]).value as Map;
    final section = ConfigFileStore._key(root, 'chat_model');
    if (root[section] == null) editor.update([section], <Object?>[]);
    final original = index < providers.length
        ? providers[index]
        : <String, Object?>{};
    if (index == providers.length) {
      editor.appendToList([section], <String, Object?>{});
    }
    final path = <Object>[section, index];
    editor.update([
      ...path,
      ConfigFileStore._key(original, 'provider'),
    ], settings.provider);
    editor.update([
      ...path,
      ConfigFileStore._key(original, 'display_name'),
    ], settings.displayName.trim());
    editor.update([
      ...path,
      ConfigFileStore._key(original, 'base_url'),
    ], settings.baseUrl);
    final modelsKey = ConfigFileStore._key(original, 'models');
    final models = _models(original);
    if (_field(original, 'models') == null) {
      editor.update([...path, modelsKey], <Object?>[]);
    }
    final retained = <String>{};
    for (var i = models.length - 1; i >= 0; i--) {
      final name = _field(models[i], 'model');
      final target = settings.models
          .where((v) => (v.originalModel ?? v.model) == name)
          .firstOrNull;
      if (target == null) {
        editor.remove([...path, modelsKey, i]);
      } else {
        retained.add(target.model);
        _writeModel(editor, [...path, modelsKey, i], models[i], target);
      }
    }
    for (final model in settings.models) {
      if (retained.contains(model.model)) continue;
      editor.appendToList(
        [...path, modelsKey],
        {
          'model': model.model,
          'context_window': model.contextWindow,
          'max_output_tokens': model.maxOutputTokens,
        },
      );
    }
    await _publishModelFiles(editor, before, after, writeKey: key.isNotEmpty);
    return {
      for (final provider in _providers(editor))
        if (_field(provider, 'provider') == settings.provider)
          for (final model in _models(provider))
            _field(model, 'model') as String,
    };
  });

  Future<void> _publishModelFiles(
    YamlEditor editor,
    String before,
    String after, {
    required bool writeKey,
  }) async {
    final path = p.join(p.dirname(configPath), '.env');
    final existed = await File(path).exists();
    if (writeKey) await _replace(path, after);
    try {
      await _save(editor.toString());
    } catch (_) {
      if (!writeKey) rethrow;
      try {
        if (existed) {
          await _replace(path, before);
        } else {
          await File(path).delete();
        }
      } catch (_) {
        throw const ModelConfigException('模型配置未保存，密钥恢复失败，请检查本地文件后重试');
      }
      throw const ModelConfigException('模型配置未保存，密钥已恢复，请重试');
    }
  }
}

ModelConnectionSettings _readConnection(
  Map provider,
  int index,
  Map<String, String> keys,
) {
  final name = _field(provider, 'provider');
  final displayName = _field(provider, 'display_name') ?? name;
  // 旧空分组允许省略地址，DeepSeek 沿用原首次配置的官方初值。
  final address =
      _field(provider, 'base_url') ??
      (name == 'deepseek' ? 'https://api.deepseek.com' : '');
  if (name is! String || address is! String || displayName is! String) {
    throw const ModelConfigException('连接配置格式无效，请检查本地配置');
  }
  return ModelConnectionSettings(
    provider: name,
    displayName: displayName,
    baseUrl: address,
    sourceIndex: index,
    sourceProvider: name,
    models: [
      for (final model in _models(provider)) _readChatModel(model, name, keys),
    ],
  );
}

ChatModelSettings _readChatModel(
  Map value,
  String provider,
  Map<String, String> keys,
) {
  final model = _field(value, 'model');
  final context = _field(value, 'context_window');
  final output = _field(value, 'max_output_tokens') ?? 8192;
  if (model is! String || context is! int || output is! int) {
    throw const ModelConfigException('模型配置格式无效，请检查本地配置');
  }
  return ChatModelSettings(
    model: model,
    originalModel: model,
    contextWindow: context,
    maxOutputTokens: output,
    hasLocalKey: _hasEnvValue(keys[_modelKey(provider, model)]),
  );
}

void _validateModelApiKey(String key) {
  // 与旧配置入口相同，禁止变量展开、控制字符及不能无损保存的字符。
  if (key.runes.any((c) => c < 32 || c > 126 || c == 39 || c == 92)) {
    throw const ModelConfigException('API Key 含不支持的字符，请检查后重试');
  }
}

String _encodeModelApiKey(String key) =>
    RegExp(r'^[A-Za-z0-9_.-]+$').hasMatch(key) ? key : "'$key'";
