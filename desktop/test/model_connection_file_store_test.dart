import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/config_file_store.dart';

void main() {
  test('旧 DeepSeek 空分组缺少地址时仍可首次配置且不覆盖其他分组', () async {
    final directory = await Directory.systemTemp.createTemp(
      'connection-empty-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final config = File('${directory.path}/config.yaml');
    await config.writeAsString('''chat_model:
  - provider: deepseek
    custom: keep
    models: []
  - provider: other
    models: []
''');
    final store = ConfigFileStore(directory.path);
    final empty = (await store.readModelConnections()).first;
    expect(empty.models, isEmpty);
    expect(empty.baseUrl, 'https://api.deepseek.com');
    await store.saveModelConnection(
      ModelConnectionSettings(
        provider: empty.provider,
        baseUrl: empty.baseUrl,
        sourceIndex: empty.sourceIndex,
        sourceProvider: empty.sourceProvider,
        models: const [ChatModelSettings(model: 'chat')],
      ),
      apiKey: 'controlled-first',
    );
    final saved = await store.readModelConnections();
    expect(saved.first.models.single.model, 'chat');
    expect(saved.last.provider, 'other');
    expect(saved.last.models, isEmpty);
    expect(await config.readAsString(), contains('custom: keep'));
  });

  test('通用连接在真实文件中保存、重开并保留旧配置和密钥', () async {
    final directory = await Directory.systemTemp.createTemp(
      'connection-store-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final config = File('${directory.path}/config.yaml');
    await config.writeAsString('''# 用户注释
log:
  level: info
chat_model:
  - provider: deepseek # 保留旧连接
    base_url: https://api.deepseek.com
    models:
      - model: chat
        context_window: 32768
        custom: keep
other: unchanged
''');
    final env = File('${directory.path}/.env');
    await env.writeAsString('DEEPSEEK_CHAT=old-secret\nOTHER=keep\n');
    final store = ConfigFileStore(directory.path);
    await store.saveModelConnection(
      const ModelConnectionSettings(
        provider: 'gateway',
        baseUrl: 'http://127.0.0.1:43210/v1',
        models: [ChatModelSettings(model: 'manual-model')],
      ),
      apiKey: 'controlled-key',
    );
    final connections = await ConfigFileStore(
      directory.path,
    ).readModelConnections();
    expect(connections.map((v) => v.provider), ['deepseek', 'gateway']);
    final saved = connections.last;
    expect(saved.baseUrl, 'http://127.0.0.1:43210/v1');
    expect(saved.models.single.model, 'manual-model');
    expect(saved.models.single.hasLocalKey, isTrue);
    expect(await config.readAsString(), contains('# 保留旧连接'));
    expect(await config.readAsString(), contains('custom: keep'));
    expect(await config.readAsString(), contains('other: unchanged'));
    expect(await env.readAsString(), contains('DEEPSEEK_CHAT=old-secret'));
    expect(
      await env.readAsString(),
      contains('GATEWAY_MANUAL_MODEL=controlled-key'),
    );
    await store.saveModelConnection(
      ModelConnectionSettings(
        provider: saved.provider,
        baseUrl: 'http://127.0.0.1:43211/proxy/v1',
        sourceIndex: saved.sourceIndex,
        sourceProvider: saved.sourceProvider,
        models: const [
          ChatModelSettings(
            model: 'manual-model',
            originalModel: 'manual-model',
            maxOutputTokens: 2048,
          ),
        ],
      ),
      apiKey: '',
    );
    final reopened = (await store.readModelConnections()).last;
    expect(reopened.baseUrl, 'http://127.0.0.1:43211/proxy/v1');
    expect(reopened.models.single.maxOutputTokens, 2048);
    expect(reopened.models.single.hasLocalKey, isTrue);
    expect(
      await env.readAsString(),
      contains('GATEWAY_MANUAL_MODEL=controlled-key'),
    );
  });

  test('非法地址、预算、身份及规范化冲突拒绝写入有效文件', () async {
    final directory = await Directory.systemTemp.createTemp(
      'connection-invalid-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final store = ConfigFileStore(directory.path);
    await store.saveModelConnection(
      const ModelConnectionSettings(
        provider: 'deepseek',
        baseUrl: 'https://api.deepseek.com',
        models: [
          ChatModelSettings(model: 'chat'),
          ChatModelSettings(model: 'chat-a'),
        ],
      ),
      apiKey: 'original-key',
    );
    final config = File(store.configPath);
    final env = File('${directory.path}/.env');
    final originalConfig = await config.readAsString();
    final originalEnv = await env.readAsString();
    for (final invalid in [
      const ModelConnectionSettings(
        provider: 'deepseek',
        baseUrl: 'https://example.test',
        models: [ChatModelSettings(model: 'other')],
      ),
      const ModelConnectionSettings(
        provider: 'deepseek_chat',
        baseUrl: 'https://example.test',
        models: [ChatModelSettings(model: 'a')],
      ),
      const ModelConnectionSettings(
        provider: '',
        baseUrl: 'https://example.test',
        models: [ChatModelSettings(model: 'a')],
      ),
      for (final url in [
        'invalid',
        'ftp://example.test',
        'https://user:password@example.test',
        'https://example.test?key=secret',
        'https://example.test#fragment',
        'http://example.test:99999',
        r'http://example.test\broken',
        'http://example.test/%nothex',
      ])
        ModelConnectionSettings(
          provider: 'gateway',
          baseUrl: url,
          models: const [ChatModelSettings(model: 'a')],
        ),
      const ModelConnectionSettings(
        provider: 'gateway',
        baseUrl: 'https://example.test',
        models: [ChatModelSettings(model: '')],
      ),
      const ModelConnectionSettings(
        provider: 'gateway',
        baseUrl: 'https://example.test',
        models: [ChatModelSettings(model: 'a', maxOutputTokens: 32768)],
      ),
      const ModelConnectionSettings(
        provider: 'gateway',
        baseUrl: 'https://example.test',
        models: [
          ChatModelSettings(model: 'a.b'),
          ChatModelSettings(model: 'a-b'),
        ],
      ),
      const ModelConnectionSettings(
        provider: 'gateway',
        baseUrl: 'https://example.test',
        sourceIndex: 0,
        sourceProvider: 'wrong-original',
        models: [ChatModelSettings(model: 'a')],
      ),
    ]) {
      await expectLater(
        store.saveModelConnection(invalid, apiKey: 'replacement'),
        throwsA(isA<ModelConfigException>()),
      );
      expect(await config.readAsString(), originalConfig);
      expect(await env.readAsString(), originalEnv);
    }
    // 密钥中的换行不能注入额外环境变量。
    await expectLater(
      store.saveModelConnection(
        const ModelConnectionSettings(
          provider: 'deepseek-',
          baseUrl: 'https://example.test',
          models: [ChatModelSettings(model: 'chat')],
        ),
        apiKey: 'bad\nOTHER=injected',
      ),
      throwsA(isA<ModelConfigException>()),
    );
    expect(await env.readAsString(), originalEnv);
  });
}
