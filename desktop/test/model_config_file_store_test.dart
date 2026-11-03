import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:yaml_edit/yaml_edit.dart';

void main() {
  late Directory directory;
  late ConfigFileStore store;
  setUp(() async {
    directory = await Directory.systemTemp.createTemp('timecue-model-config-');
    store = ConfigFileStore(directory.path);
  });
  tearDown(() => directory.delete(recursive: true));

  for (final model in ['chat', 'renamed-chat']) {
    test('前置空分组时保存 $model 更新当前显示模型，保留其他分组及模型', () async {
      await File(store.configPath).writeAsString('''# 保留分组
chat_model:
  - provider: deepseek
    base_url: https://api.deepseek.com
    models: []
    placeholder: keep
  - provider: other
    base_url: https://example.com
    models:
      - model: untouched-other
        context_window: 16000
  - provider: deepseek
    base_url: https://api.deepseek.com
    custom: keep
    models:
      - model: chat
        context_window: 24000
        max_output_tokens: 4000
        custom_model: keep
      - model: untouched
        context_window: 16000
''');
      final before =
          YamlEditor(
                await File(store.configPath).readAsString(),
              ).parseAt([]).value
              as Map;
      expect((await store.readDeepSeekModel())!.model, 'chat');

      await store.saveDeepSeekModel(
        DeepSeekModelSettings(
          model: model,
          contextWindow: 30000,
          maxOutputTokens: 5000,
        ),
        apiKey: 'local-group-update',
      );

      final after =
          YamlEditor(
                await File(store.configPath).readAsString(),
              ).parseAt([]).value
              as Map;
      final providers = after['chat_model'] as List;
      expect(providers, hasLength(3));
      expect(providers[0], before['chat_model'][0]);
      expect(providers[1], before['chat_model'][1]);
      expect(providers[2]['custom'], 'keep');
      expect(providers[2]['models'], hasLength(2));
      expect(providers[2]['models'][1], before['chat_model'][2]['models'][1]);
      expect(providers[2]['models'][0]['model'], model);
      expect(providers[2]['models'][0]['custom_model'], 'keep');
      final reopened = await ConfigFileStore(
        directory.path,
      ).readDeepSeekModel();
      expect(reopened!.model, model);
      expect(reopened.contextWindow, 30000);
      expect(reopened.maxOutputTokens, 5000);
    });
  }

  test('所有 DeepSeek 分组均为空时复用第一个空分组', () async {
    await File(store.configPath).writeAsString('''chat_model:
  - provider: deepseek
    models: []
    placeholder: first
  - provider: deepseek
    models: []
    placeholder: second
''');
    expect(await store.readDeepSeekModel(), isNull);
    await store.saveDeepSeekModel(
      const DeepSeekModelSettings(model: 'chat'),
      apiKey: 'local-empty-group',
    );
    final providers =
        YamlEditor(
              await File(store.configPath).readAsString(),
            ).parseAt(['chat_model']).value
            as List;
    expect(providers, hasLength(2));
    expect(providers[0]['placeholder'], 'first');
    expect(providers[0]['models'][0]['model'], 'chat');
    expect(providers[1]['models'], isEmpty);
    expect(providers[1]['placeholder'], 'second');
    expect((await store.readDeepSeekModel())!.model, 'chat');
  });

  for (final value in [
    (label: '空字段', key: 'chat_model', yaml: 'chat_model:\n'),
    (label: '显式 null', key: 'chat_model', yaml: 'chat_model: null\n'),
    (label: '大写字段与 ~', key: 'CHAT_MODEL', yaml: 'CHAT_MODEL: ~\n'),
  ]) {
    test('${value.label}可读取为空并首次保存模型，保留无关配置及原字段名', () async {
      await File(store.configPath).writeAsString('''# 保留用户注释
log:
  level: warn
unknown: keep
${value.yaml}''');
      expect(await store.readDeepSeekModel(), isNull);

      await store.saveDeepSeekModel(
        const DeepSeekModelSettings(model: 'chat'),
        apiKey: 'local-null-list',
      );

      final reopened = await ConfigFileStore(
        directory.path,
      ).readDeepSeekModel();
      expect(reopened!.model, 'chat');
      expect(reopened.contextWindow, 32768);
      expect(reopened.maxOutputTokens, 8192);
      final contents = await File(store.configPath).readAsString();
      final root = YamlEditor(contents).parseAt([]).value as Map;
      expect(root.keys, ['log', 'unknown', value.key]);
      expect(root['log']['level'], 'warn');
      expect(root['unknown'], 'keep');
      expect(root[value.key][0]['provider'], 'deepseek');
      expect(contents, contains('# 保留用户注释'));
      expect(
        await File(p.join(directory.path, '.env')).readAsString(),
        'DEEPSEEK_CHAT=local-null-list\n',
      );
    });
  }

  test('更换 Key 保留 CR 和混合换行环境文件的其他变量', () async {
    final env = File(p.join(directory.path, '.env'));
    await env.writeAsString(
      'DEEPSEEK_CHAT=old\rOTHER=keep\rQUOTED="one\rtwo"\r\nLAST=end\n',
    );
    await store.saveDeepSeekModel(
      const DeepSeekModelSettings(model: 'chat'),
      apiKey: 'new-key',
    );
    expect(
      await env.readAsString(),
      'DEEPSEEK_CHAT=new-key\nOTHER=keep\rQUOTED="one\rtwo"\r\nLAST=end\n',
    );
  });

  test('非法输入不改写任一文件，模型密钥规范化冲突也拒绝保存', () async {
    const original = '''chat_model:
  - provider: deepseek
    base_url: https://api.deepseek.com
    models:
      - model: chat
        context_window: 32768
      - model: other.chat
        context_window: 32768
''';
    await File(store.configPath).writeAsString(original);
    final env = File(p.join(directory.path, '.env'));
    await env.writeAsString('DEEPSEEK_CHAT=original\n');
    for (final value in [
      (settings: const DeepSeekModelSettings(model: ''), key: 'new-key'),
      (settings: const DeepSeekModelSettings(model: 'chat'), key: ''),
      (
        settings: const DeepSeekModelSettings(model: 'chat', contextWindow: 0),
        key: 'new-key',
      ),
      (
        settings: const DeepSeekModelSettings(
          model: 'chat',
          maxOutputTokens: 32768,
        ),
        key: 'new-key',
      ),
      (
        settings: const DeepSeekModelSettings(model: 'chat'),
        key: 'new\nINJECTED=value',
      ),
      (
        settings: const DeepSeekModelSettings(model: 'other-chat'),
        key: 'new-key',
      ),
    ]) {
      await expectLater(
        store.saveDeepSeekModel(value.settings, apiKey: value.key),
        throwsFormatException,
      );
      expect(await File(store.configPath).readAsString(), original);
      expect(await env.readAsString(), 'DEEPSEEK_CHAT=original\n');
    }
  });

  test('首次及连续保存单模型，重开读取预算且密钥只在本机 env', () async {
    expect(await store.readDeepSeekModel(), isNull);
    await File(store.configPath).writeAsString('''# 保留运行设置
log:
  level: warn
unknown: [1, 2]
chat_model:
  - provider: other
    base_url: https://example.com
    models:
      - model: preserved
        context_window: 10000
''');
    final env = File(p.join(directory.path, '.env'));
    const unrelated =
        'OTHER="first\nDEEPSEEK_CHAT=inside-quoted-value\nlast" # keep\n';
    await env.writeAsString(unrelated);
    await store.saveDeepSeekModel(
      const DeepSeekModelSettings(model: 'chat'),
      apiKey: 'local-first',
    );
    await Future.wait([
      store.saveDeepSeekModel(
        const DeepSeekModelSettings(
          model: 'chat',
          contextWindow: 24000,
          maxOutputTokens: 4000,
        ),
        apiKey: 'local-second',
      ),
      ConfigFileStore(directory.path).update(logLevel: 'debug'),
    ]);
    final reopened = await ConfigFileStore(directory.path).readDeepSeekModel();
    expect(reopened!.model, 'chat');
    expect(reopened.contextWindow, 24000);
    expect(reopened.maxOutputTokens, 4000);
    final configText = await File(store.configPath).readAsString();
    expect(configText, contains('# 保留运行设置'));
    expect(configText, isNot(contains('local-')));
    final root = YamlEditor(configText).parseAt([]).value as Map;
    expect(root['log']['level'], 'debug');
    expect(root['unknown'], [1, 2]);
    expect(root['chat_model'][0]['provider'], 'other');
    expect(root['chat_model'][1]['base_url'], 'https://api.deepseek.com');
    expect(
      await env.readAsString(),
      '$unrelated'
      'DEEPSEEK_CHAT=local-second\n',
    );
    await store.restoreRuntimeDefaults();
    expect((await store.readDeepSeekModel())!.model, 'chat');
  });
}
