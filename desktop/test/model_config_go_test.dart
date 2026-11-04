import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:yaml_edit/yaml_edit.dart';

void main() {
  test('真实 Go 同一身份连续保存，正式请求消费最新 Key 和预算，重启可读取', () async {
    // 固定测试端口被使用时拒绝运行，绝不接管已有实例。
    final port = await ServerSocket.bind(InternetAddress.loopbackIPv4, 1145);
    await port.close();
    final directory = await Directory.systemTemp.createTemp(
      'timecue-go-model-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final binary = p.join(directory.path, 'timecue-core.exe');
    final build = await Process.run('go', [
      'build',
      '-o',
      binary,
      './cmd/flutter',
    ], workingDirectory: '..');
    expect(build.exitCode, 0, reason: '${build.stderr}');
    final data = Directory(p.join(directory.path, '中文 数据'));
    await data.create();
    final store = ConfigFileStore(data.path);
    final observed =
        <({String? authorization, Map<String, dynamic> request})>[];
    final upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    upstream.listen((request) async {
      observed.add((
        authorization: request.headers.value('Authorization'),
        request:
            jsonDecode(await utf8.decoder.bind(request).join())
                as Map<String, dynamic>,
      ));
      request.response.headers.contentType = ContentType(
        'text',
        'event-stream',
        charset: 'utf-8',
      );
      request.response.write(
        'data: ${jsonEncode({
          'id': 'controlled-upstream',
          'choices': [
            {
              'delta': {'content': '本地链路回复'},
              'finish_reason': 'stop',
            },
          ],
        })}\n\ndata: [DONE]\n\n',
      );
      await request.response.close();
    });
    addTearDown(() => upstream.close(force: true));
    var ordinal = 0;
    Future<Process> start() async {
      final process = await Process.start(binary, [
        '--data-dir',
        data.path,
        '--instance-id',
        'config-test-${++ordinal}',
        '--env',
        'test',
      ]);
      addTearDown(() async {
        process.kill();
        await process.exitCode;
      });
      process.stderr.drain<void>();
      final ready =
          jsonDecode(
                await process.stdout
                    .transform(utf8.decoder)
                    .transform(const LineSplitter())
                    .first
                    .timeout(const Duration(seconds: 20)),
              )
              as Map;
      expect(ready['pid'], process.pid);
      expect(ready['instanceId'], 'config-test-$ordinal');
      return process;
    }

    Future<void> stop(Process process) async {
      process.stdin.writeln('{"command":"shutdown"}');
      await process.stdin.flush();
      try {
        expect(await process.exitCode.timeout(const Duration(seconds: 8)), 0);
      } finally {
        process.kill();
        await process.exitCode;
      }
    }

    var process = await start();
    final firstPid = process.pid;
    final healthClient = HttpClient()..findProxy = (_) => 'DIRECT';
    addTearDown(() => healthClient.close(force: true));
    Future<void> checkIdentity() async {
      final request = await healthClient.getUrl(
        Uri.parse('http://127.0.0.1:1145/health'),
      );
      final response = await request.close();
      final health =
          jsonDecode(await utf8.decoder.bind(response).join()) as Map;
      expect(health['data']['pid'], firstPid);
      expect(health['data']['instanceId'], 'config-test-1');
    }

    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:1145'),
    );
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() {
      chat.dispose();
      gateway.dispose();
    });
    await chat.loadModels();
    await chat.loadHistory();
    expect(chat.models, isEmpty);
    expect(chat.historyLoaded, isTrue);
    Future<void> routeUpstream() async {
      final editor = YamlEditor(await File(store.configPath).readAsString());
      expect(
        editor.parseAt(['chat_model', 0, 'base_url']).value,
        'https://api.deepseek.com',
      );
      // 仅测试把官方网络边界转到可控上游；不证明真实 DeepSeek 认证和服务可用。
      editor.update([
        'chat_model',
        0,
        'base_url',
      ], 'http://127.0.0.1:${upstream.port}');
      final staging = File(p.join(data.path, 'test-upstream.yaml'));
      await staging.writeAsString(editor.toString(), flush: true);
      await staging.rename(store.configPath);
    }

    Future<void> sendSelected(String key, int output) async {
      final selected = chat.selectedModel!;
      final session = chat.createLocalSession();
      chat.updateDraft(session.sessionId, '多模型正式网关验证');
      await chat.send(
        session.sessionId,
        provider: selected.provider,
        model: selected.model,
      );
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (!session.runs.single.isTerminal &&
          DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
      expect(session.runs.single.phase, ChatRunPhase.succeeded);
      expect(observed.last.request['model'], selected.model);
      expect(observed.last.request['max_tokens'], output);
      expect(observed.last.authorization, 'Bearer $key');
      await checkIdentity();
    }

    for (final value in [
      (key: 'controlled-first', context: 32768, output: 8192),
      (key: 'controlled-second', context: 32768, output: 8192),
      (key: 'controlled-second', context: 36000, output: 4096),
    ]) {
      await store.saveDeepSeekModel(
        DeepSeekModelSettings(
          model: 'chat',
          contextWindow: value.context,
          maxOutputTokens: value.output,
        ),
        apiKey: value.key,
      );
      await routeUpstream();
      expect(
        await chat.applySavedModel(provider: 'deepseek', model: 'chat'),
        isTrue,
      );
      final session = chat.createLocalSession();
      chat.updateDraft(session.sessionId, '本地配置链路验证');
      await chat.send(session.sessionId, provider: 'deepseek', model: 'chat');
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (!session.runs.single.isTerminal &&
          DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
      expect(session.runs.single.phase, ChatRunPhase.succeeded);
      expect(session.runs.single.rawText, '本地链路回复');
      expect(observed.last.authorization, 'Bearer ${value.key}');
      expect(observed.last.request['max_tokens'], value.output);
      await checkIdentity();
    }
    expect(observed, hasLength(3));
    await store.saveDeepSeekModels([
      (await store.readDeepSeekModels()).single,
      const DeepSeekModelSettings(
        model: 'reasoner',
        contextWindow: 24000,
        maxOutputTokens: 2048,
      ),
    ], apiKey: 'controlled-shared');
    await routeUpstream();
    expect(
      await chat.applySavedModels(
        provider: 'deepseek',
        models: ['chat', 'reasoner'],
      ),
      isTrue,
    );
    expect(chat.selectedModel!.model, 'chat');
    await sendSelected('controlled-shared', 4096);
    chat.selectModel(
      chat.models.firstWhere((v) => v.model == 'reasoner').identity,
    );
    await sendSelected('controlled-shared', 2048);
    await store.saveDeepSeekModels([
      const DeepSeekModelSettings(
        model: 'reasoner-v2',
        originalModel: 'reasoner',
        contextWindow: 28000,
        maxOutputTokens: 1024,
      ),
    ], apiKey: 'controlled-renamed');
    await routeUpstream();
    expect(
      await chat.applySavedModels(
        provider: 'deepseek',
        models: ['reasoner-v2'],
      ),
      isTrue,
    );
    expect(chat.models.single.model, 'reasoner-v2');
    expect(chat.selectedModel!.model, 'reasoner-v2');
    await sendSelected('controlled-renamed', 1024);
    expect(
      await File(p.join(data.path, '.env')).readAsString(),
      contains('DEEPSEEK_CHAT=controlled-shared'),
    );
    expect(await gateway.listSessions(), hasLength(6));
    // 缩小上下文预算后，原本可发送的输入由正式预算门禁拒绝，不访问上游。
    await store.saveDeepSeekModel(
      const DeepSeekModelSettings(
        model: 'reasoner-v2',
        contextWindow: 64,
        maxOutputTokens: 32,
      ),
      apiKey: 'controlled-renamed',
    );
    expect(
      await chat.applySavedModel(provider: 'deepseek', model: 'reasoner-v2'),
      isTrue,
    );
    final rejected = chat.createLocalSession();
    chat.updateDraft(rejected.sessionId, '较长输入' * 2000);
    await chat.send(
      rejected.sessionId,
      provider: 'deepseek',
      model: 'reasoner-v2',
    );
    expect(rejected.runs.single.phase, ChatRunPhase.failed);
    expect(observed, hasLength(6));
    await stop(process);
    process = await start();
    expect(process.pid, isNot(firstPid));
    expect((await store.readDeepSeekModel())!.contextWindow, 64);
    await chat.loadModels();
    expect(chat.selectedModel!.model, 'reasoner-v2');
    await stop(process);
  }, timeout: const Timeout(Duration(minutes: 3)));
}
