@Timeout(Duration(minutes: 6))
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/settings/model_config_dialog.dart';
import 'package:yaml_edit/yaml_edit.dart';

void main() {
  Directory? buildDirectory;
  late String binary;

  setUpAll(() async {
    // 冷编译独立于各用例的超时，所有用例共用二进制但独立启动服务。
    final directory = await Directory.systemTemp.createTemp('model-go-build-');
    buildDirectory = directory;
    binary = p.join(directory.path, 'timecue-core.exe');
    final build = await Process.run('go', [
      'build',
      '-o',
      binary,
      './cmd/flutter',
    ], workingDirectory: '..');
    expect(build.exitCode, 0, reason: '${build.stderr}');
  });

  tearDownAll(() async {
    await buildDirectory?.delete(recursive: true);
  });

  testWidgets(
    '连接内多模型经真实 Go 维护预算密钥，保留在途请求并验证重启和原身份失效',
    (tester) => _customConnectionGoFlow(tester, binary, maintenance: true),
    timeout: const Timeout(Duration(minutes: 3)),
  );
  testWidgets(
    '同模型三份连接经真实 Go 隔离请求，运行中改名切换保留原身份与订阅，重启可区分',
    (tester) => _customConnectionGoFlow(tester, binary, multiple: true),
    timeout: const Timeout(Duration(minutes: 3)),
  );
  testWidgets(
    '自定义连接经正式页面保存，真实 Go 同进程使用新地址模型密钥预算并在重启后读取',
    (tester) => _customConnectionGoFlow(tester, binary),
    timeout: const Timeout(Duration(minutes: 3)),
  );
  test('真实 Go 同一身份连续保存，正式请求消费最新 Key 和预算，重启可读取', () async {
    // 固定测试端口被使用时拒绝运行，绝不接管已有实例。
    final port = await ServerSocket.bind(InternetAddress.loopbackIPv4, 1145);
    await port.close();
    final directory = await Directory.systemTemp.createTemp(
      'timecue-go-model-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final data = Directory(p.join(directory.path, '中文 数据'));
    await data.create();
    final store = ConfigFileStore(data.path);
    final observed =
        <({String? authorization, Map<String, dynamic> request})>[];
    var upstreamStatus = 200;
    final upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    upstream.listen((request) async {
      observed.add((
        authorization: request.headers.value('Authorization'),
        request:
            jsonDecode(await utf8.decoder.bind(request).join())
                as Map<String, dynamic>,
      ));
      if (upstreamStatus != 200) {
        request.response.statusCode = upstreamStatus;
        request.response.headers.contentType = ContentType.json;
        request.response.write(
          '{"error":{"message":"controlled upstream rejection"}}',
        );
        await request.response.close();
        return;
      }
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
    final healthClient = _RealHttpOverrides().createHttpClient(null)
      ..findProxy = (_) => 'DIRECT';
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
      client: healthClient,
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
    // 真实文件损坏不覆盖旧内容；修复后沿原 Go 身份重新保存并热加载。
    final config = File(store.configPath);
    final env = File(p.join(data.path, '.env'));
    final validConfig = await config.readAsString();
    final validEnv = await env.readAsString();
    await config.writeAsString('private-original: [broken', flush: true);
    await expectLater(store.readDeepSeekModels(), throwsA(anything));
    await expectLater(
      store.saveDeepSeekModels([
        const DeepSeekModelSettings(model: 'repaired'),
      ], apiKey: 'repair-key'),
      throwsA(anything),
    );
    expect(await config.readAsString(), 'private-original: [broken');
    expect(await env.readAsString(), validEnv);
    await config.writeAsString(validConfig, flush: true);
    await env.writeAsString('OTHER="private-original-unclosed', flush: true);
    expect(
      await chat.applySavedModel(
        provider: 'deepseek',
        model: 'reasoner-v2',
        timeout: const Duration(seconds: 1),
      ),
      isFalse,
    );
    expect(chat.modelsLoading, isFalse);
    await expectLater(store.readDeepSeekModels(), throwsFormatException);
    await expectLater(
      store.saveDeepSeekModels([
        const DeepSeekModelSettings(model: 'repaired'),
      ], apiKey: 'repair-key'),
      throwsFormatException,
    );
    expect(await env.readAsString(), 'OTHER="private-original-unclosed');
    await env.writeAsString(validEnv, flush: true);
    await store.saveDeepSeekModel(
      const DeepSeekModelSettings(
        model: 'reasoner-v2',
        contextWindow: 28000,
        maxOutputTokens: 1024,
      ),
      apiKey: r'literal$UNSET # suffix',
    );
    await routeUpstream();
    expect(
      await chat.applySavedModel(provider: 'deepseek', model: 'reasoner-v2'),
      isTrue,
    );
    await sendSelected(r'literal$UNSET # suffix', 1024);
    // 本地配置可用与实际上游认证/余额分别反馈，不把调用失败当成保存失败。
    for (final failure in [
      (status: 401, code: 'model_auth_failed'),
      (status: 402, code: 'model_insufficient_balance'),
    ]) {
      upstreamStatus = failure.status;
      final session = chat.createLocalSession();
      chat.updateDraft(session.sessionId, '真实上游错误映射');
      await chat.send(
        session.sessionId,
        provider: 'deepseek',
        model: 'reasoner-v2',
      );
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (!session.runs.single.isTerminal &&
          DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
      expect(session.runs.single.phase, ChatRunPhase.failed);
      expect(
        (await gateway.getRun(session.runs.single.runId!)).error?.code,
        failure.code,
      );
      expect(chat.selectedModel?.available, isTrue);
      expect(await config.readAsString(), isNot(contains('literal')));
      await checkIdentity();
    }
    upstreamStatus = 200;
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
    expect(observed, hasLength(9));
    await stop(process);
    process = await start();
    expect(process.pid, isNot(firstPid));
    expect((await store.readDeepSeekModel())!.contextWindow, 64);
    await chat.loadModels();
    expect(chat.selectedModel!.model, 'reasoner-v2');
    await stop(process);
  }, timeout: const Timeout(Duration(minutes: 3)));

  testWidgets('真实 Go 在运行中重开弹窗保留或更换 Key，移除原模型不切换运行身份', (tester) async {
    final directory = (await tester.runAsync(
      () => Directory.systemTemp.createTemp('model-maintenance-'),
    ))!;
    final client = _RealHttpOverrides().createHttpClient(null)
      ..findProxy = (_) => 'DIRECT';
    final transport = _ObservedClient(client);
    final store = ConfigFileStore(directory.path);
    late Process process;
    late HttpServer upstream;
    final observed = <({String? key, Map<String, dynamic> body})>[];
    Completer<void>? release;
    var finish = 'stop';
    await tester.runAsync(() async {
      final port = await ServerSocket.bind(InternetAddress.loopbackIPv4, 1145);
      await port.close();
      await store.saveDeepSeekModels([
        const DeepSeekModelSettings(model: 'chat'),
        const DeepSeekModelSettings(model: 'reasoner'),
      ], apiKey: 'seed');
      await File(p.join(directory.path, '.env')).writeAsString(
        'DEEPSEEK_CHAT=chat-original\nDEEPSEEK_REASONER=reasoner-original\n',
      );
      upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      upstream.listen((request) async {
        final gate = release;
        final finishReason = finish;
        observed.add((
          key: request.headers.value('Authorization'),
          body:
              jsonDecode(await utf8.decoder.bind(request).join())
                  as Map<String, dynamic>,
        ));
        await gate?.future;
        request.response.headers.contentType = ContentType(
          'text',
          'event-stream',
          charset: 'utf-8',
        );
        request.response.write(
          'data: ${jsonEncode({
            'id': 'maintenance-upstream',
            'choices': [
              {
                'delta': {'content': '保留已有回复'},
                'finish_reason': finishReason,
              },
            ],
          })}\n\ndata: [DONE]\n\n',
        );
        await request.response.close();
      });
      await _routeMaintenanceUpstream(store, upstream.port);
      process = await Process.start(binary, [
        '--data-dir',
        directory.path,
        '--instance-id',
        'maintenance-test',
        '--env',
        'test',
      ]);
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
      expect(ready['instanceId'], 'maintenance-test');
    });
    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:1145'),
      client: transport,
    );
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() async {
      if (release?.isCompleted == false) release!.complete();
      chat.dispose();
      gateway.dispose();
      client.close(force: true);
      process.kill();
      await process.exitCode;
      await upstream.close(force: true);
      await directory.delete(recursive: true);
    });
    await tester.runAsync(() async {
      await chat.loadModels();
      await chat.loadHistory();
    });
    final session = chat.createLocalSession();
    Future<void> send(String model) async {
      await tester.runAsync(() async {
        chat.updateDraft(session.sessionId, '现有会话继续对话');
        await chat.send(session.sessionId, provider: 'deepseek', model: model);
      });
    }

    Future<void> waitFor(bool Function() condition) async {
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (!condition() && DateTime.now().isBefore(deadline)) {
        await tester.runAsync(
          () => Future<void>.delayed(const Duration(milliseconds: 25)),
        );
        await tester.pump(const Duration(milliseconds: 25));
      }
      expect(condition(), isTrue);
    }

    Future<void> open() async {
      await tester.tap(find.text('管理模型'));
      await tester.pump();
      await waitFor(
        () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
      );
      await tester.pumpAndSettle();
      expect(find.text('已配置本地密钥'), findsNWidgets(2));
      expect(
        tester
            .widget<TextFormField>(find.byKey(const ValueKey('model-key')))
            .controller!
            .text,
        isEmpty,
      );
    }

    Future<void> save() async {
      await tester.tap(find.text('保存并应用'));
      await tester.pump();
      await waitFor(
        () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
      );
      expect(find.textContaining('本地模型已可用，可以继续对话'), findsOneWidget);
      await tester.runAsync(() async {
        await _routeMaintenanceUpstream(store, upstream.port);
        expect(
          await chat.applySavedModels(
            provider: 'deepseek',
            models: (await store.readDeepSeekModels()).map((v) => v.model),
          ),
          isTrue,
        );
      });
    }

    Future<void> close() async {
      await tester.tap(find.text('关闭'));
      await tester.pumpAndSettle();
      expect(find.byType(ModelConfigDialog), findsNothing);
    }

    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () => showDialog<void>(
                context: context,
                barrierDismissible: false,
                builder: (_) => ModelConfigDialog(store: store, chat: chat),
              ),
              child: const Text('管理模型'),
            ),
          ),
        ),
      ),
    );
    // 原会话已有历史，再保持第二个正式模型请求在途。
    await send('chat');
    await waitFor(() => session.runs.last.isTerminal);
    final historyRun = session.runs.single;
    release = Completer<void>();
    await send('chat');
    await waitFor(() => observed.length == 2);
    final active = session.runs.last;
    chat.updateDraft(session.sessionId, '下一条未发送草稿');
    final oldEnv = await tester.runAsync(
      () => File(p.join(directory.path, '.env')).readAsString(),
    );
    final sseBefore = transport.paths.where((v) => v.endsWith('/event')).length;
    await open();
    await close();
    await open();
    // 留空只改预算，两份不同的原 Key 都必须保留。
    final settings = (await tester.runAsync(store.readDeepSeekModels))!;
    final chatIndex = settings.indexWhere((v) => v.model == 'chat');
    final advanced = find.byKey(ValueKey('model-advanced-$chatIndex'));
    await tester.ensureVisible(advanced);
    await tester.tap(advanced);
    await tester.pumpAndSettle();
    final output = find.byKey(ValueKey('model-output-$chatIndex'));
    await tester.ensureVisible(output);
    await tester.enterText(output, '1024');
    await save();
    expect(
      await tester.runAsync(
        () => File(p.join(directory.path, '.env')).readAsString(),
      ),
      oldEnv,
    );
    await close();
    await open();
    await tester.enterText(
      find.byKey(const ValueKey('model-key')),
      'shared-replacement',
    );
    await save();
    expect(session.draft, '下一条未发送草稿');
    expect(session.runs, [historyRun, active]);
    expect(chat.selectedSessionId, session.sessionId);
    expect(chat.activeRunCount, 1);
    expect(active.isTerminal, isFalse);
    expect(active.model, 'chat');
    expect(active.provider, 'deepseek');
    expect(observed, hasLength(2));
    expect(observed.last.key, 'Bearer chat-original');
    expect(observed.last.body['max_tokens'], 8192);
    expect(
      transport.paths.where((v) => v.endsWith('/event')),
      hasLength(sseBefore),
    );
    final snapshot = (await tester.runAsync(
      () => gateway.getRun(active.runId!),
    ))!;
    expect(snapshot.model, 'chat');
    expect(snapshot.provider, 'deepseek');
    await close();
    release.complete();
    release = null;
    await waitFor(() => active.isTerminal);
    expect(active.phase, ChatRunPhase.succeeded);
    expect(chat.activeRunCount, 0);
    expect(session.draft, '下一条未发送草稿');
    await send('chat');
    await waitFor(() => session.runs.last.isTerminal);
    expect(observed.last.key, 'Bearer shared-replacement');
    expect(observed.last.body['max_tokens'], 1024);
    expect(observed.last.body['messages'].toString(), contains('保留已有回复'));
    await send('reasoner');
    await waitFor(() => session.runs.last.isTerminal);
    expect(observed.last.key, 'Bearer shared-replacement');
    expect(observed.last.body['model'], 'reasoner');
    // 移除在途模型后，已有响应保留；需要续写时按原配置不可用失败。
    release = Completer<void>();
    finish = 'length';
    await send('chat');
    await waitFor(() => observed.length == 5);
    final removedRun = session.runs.last;
    chat.updateDraft(session.sessionId, '移除模型后仍保留草稿');
    await open();
    final remove = find.byTooltip('移除模型 ${chatIndex + 1}');
    await tester.ensureVisible(remove);
    await tester.tap(remove);
    await tester.pumpAndSettle();
    await save();
    expect(chat.selectedModel!.model, 'reasoner');
    expect(removedRun.model, 'chat');
    expect(chat.activeRunCount, 1);
    await close();
    release.complete();
    release = null;
    await waitFor(() => removedRun.isTerminal);
    expect(removedRun.phase, ChatRunPhase.failed);
    final failed = (await tester.runAsync(
      () => gateway.getRun(removedRun.runId!),
    ))!;
    expect(failed.error?.code, 'model_config_unavailable');
    expect(failed.model, 'chat');
    expect(removedRun.rawText, '保留已有回复');
    expect(observed, hasLength(5));
    expect(session.runs, hasLength(5));
    expect(session.draft, '移除模型后仍保留草稿');
    expect(chat.activeRunCount, 0);
    final history = (await tester.runAsync(
      () => gateway.getSession(session.sessionId),
    ))!;
    expect(history.runIds, hasLength(5));
    final healthRequest = (await tester.runAsync(
      () => client.getUrl(Uri.parse('http://127.0.0.1:1145/health')),
    ))!;
    final health = (await tester.runAsync(
      () async =>
          jsonDecode(
                await utf8.decoder.bind(await healthRequest.close()).join(),
              )
              as Map,
    ))!;
    expect(health['data']['pid'], process.pid);
    expect(health['data']['instanceId'], 'maintenance-test');
    await tester.pumpWidget(const SizedBox());
    expect(tester.takeException(), isNull);
  }, timeout: const Timeout(Duration(minutes: 3)));
}

// 旧 DeepSeek 存储兼容入口固定官方地址，仅在测试网络边界改接可控上游。
Future<void> _routeMaintenanceUpstream(ConfigFileStore store, int port) async {
  final editor = YamlEditor(await File(store.configPath).readAsString());
  editor.update(['chat_model', 0, 'base_url'], 'http://127.0.0.1:$port');
  final staging = File('${store.configPath}.upstream');
  await staging.writeAsString(editor.toString(), flush: true);
  await staging.rename(store.configPath);
}

class _RealHttpOverrides extends HttpOverrides {}

Future<void> _customConnectionGoFlow(
  WidgetTester tester,
  String binary, {
  bool multiple = false,
  bool maintenance = false,
}) async {
  final directory = (await tester.runAsync(
    () => Directory.systemTemp.createTemp('custom-connection-go-'),
  ))!;
  final store = ConfigFileStore(directory.path);
  final transport = (await tester.runAsync(() async {
    final client = _RealHttpOverrides().createHttpClient(null)
      ..findProxy = (_) => 'DIRECT';
    return _ObservedClient(client, ioZone: Zone.current);
  }))!;
  final client = transport.client;
  final gateway = HttpChatGateway(
    baseUri: Uri.parse('http://127.0.0.1:1145'),
    client: transport,
  );
  final chat = ChatViewModel(
    gateway: gateway,
    readConnectionNames: store.readConnectionNames,
  );
  final repository = DemoNotebookRepository();
  final notebook = NotebookViewModel(repository);
  final observed =
      <({int port, String path, String? key, Map<String, dynamic> body})>[];
  final upstreams = <HttpServer>[];
  Process? process;
  var ordinal = 0;
  Completer<void>? release;
  var finish = 'stop';
  var primaryClosed = false;
  void disposePrimary() {
    if (primaryClosed) return;
    primaryClosed = true;
    chat.dispose();
    gateway.dispose();
    client.close(force: true);
  }

  Future<void> closePrimary() async {
    disposePrimary();
    // 先取消真实连接，再让已经取消的请求超时与回调退出虚拟时钟。
    await tester.runAsync(() => Future<void>.delayed(Duration.zero));
    await tester.pumpAndSettle(const Duration(seconds: 10));
  }

  addTearDown(() async {
    if (release?.isCompleted == false) release!.complete();
    disposePrimary();
    notebook.dispose();
    repository.dispose();
    process?.kill();
    await process?.exitCode;
    for (final server in upstreams) {
      await server.close(force: true);
    }
    await directory.delete(recursive: true);
  });

  Future<void> start() async {
    process = await Process.start(binary, [
      '--data-dir',
      directory.path,
      '--env',
      'test',
      '--instance-id',
      'custom-connection-${++ordinal}',
    ]);
    process!.stderr.drain<void>();
    final ready =
        jsonDecode(
              await process!.stdout
                  .transform(utf8.decoder)
                  .transform(const LineSplitter())
                  .first
                  .timeout(const Duration(seconds: 20)),
            )
            as Map;
    expect(ready['pid'], process!.pid);
    expect(ready['instanceId'], 'custom-connection-$ordinal');
  }

  Future<void> stop() async {
    process!.stdin.writeln('{"command":"shutdown"}');
    await process!.stdin.flush();
    expect(await process!.exitCode.timeout(const Duration(seconds: 8)), 0);
  }

  await tester.runAsync(() async {
    final port = await ServerSocket.bind(InternetAddress.loopbackIPv4, 1145);
    await port.close();
    for (var i = 0; i < (multiple ? 3 : 2); i++) {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      upstreams.add(server);
      server.listen((request) async {
        final gate = release;
        final finishReason = finish;
        observed.add((
          port: server.port,
          path: request.uri.path,
          key: request.headers.value('Authorization'),
          body:
              jsonDecode(await utf8.decoder.bind(request).join())
                  as Map<String, dynamic>,
        ));
        await gate?.future;
        request.response.headers.contentType = ContentType(
          'text',
          'event-stream',
          charset: 'utf-8',
        );
        request.response.write(
          'data: ${jsonEncode({
            'id': 'custom-local',
            'choices': [
              {
                'delta': {'content': '自定义本地链路回复'},
                'finish_reason': finishReason,
              },
            ],
          })}\n\ndata: [DONE]\n\n',
        );
        await request.response.close();
      });
    }
    await start();
    await chat.loadModels();
    await chat.loadHistory();
  });
  final firstPid = process!.pid;
  expect(chat.models, isEmpty);
  expect(chat.historyLoaded, isTrue);

  // 运行中持续动画不会静止，只推进弹窗和菜单的过渡帧。
  Future<void> pumpUi() async {
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 350));
  }

  Future<void> waitFor(
    bool Function() condition, {
    String stage = '界面状态同步',
  }) async {
    final deadline = DateTime.now().add(const Duration(seconds: 30));
    while (!condition() && DateTime.now().isBefore(deadline)) {
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 25)),
      );
      await tester.pump(const Duration(milliseconds: 25));
    }
    if (!condition()) {
      final session = chat.selectedConversation;
      final run = session?.runs.lastOrNull;
      final backend = await tester.runAsync(() async {
        final inspectionClient = _RealHttpOverrides().createHttpClient(null)
          ..findProxy = (_) => 'DIRECT';
        final inspection = HttpChatGateway(
          baseUri: Uri.parse('http://127.0.0.1:1145'),
          client: inspectionClient,
        );
        try {
          if (run?.runId == null) return {'run_id': null};
          final snapshot = await inspection.getRun(run!.runId!);
          return {
            'run_id': snapshot.runId,
            'provider': snapshot.provider,
            'model': snapshot.model,
            'status': snapshot.status.name,
            'finished_at': snapshot.finishedAt?.toIso8601String(),
            'error_code': snapshot.error?.code,
          };
        } catch (error) {
          return {'snapshot_error_type': error.runtimeType.toString()};
        } finally {
          inspection.dispose();
          inspectionClient.close(force: true);
        }
      });
      // 只记录状态及请求路由，不输出认证头、请求正文或密钥。
      final diagnostics = {
        'session_id': session?.sessionId,
        'run_count': session?.runs.length,
        'run_id': run?.runId,
        'phase': run?.phase.name,
        'connection': run?.connectionState.name,
        'last_event_id': run?.lastEventId,
        'upstream_request_count': observed.length,
        'http_paths': transport.paths,
        'backend': backend,
      };
      fail('等待$stage超时：${jsonEncode(diagnostics)}');
    }
    await pumpUi();
  }

  Future<void> edit(String key, String value) async {
    final field = find.byKey(ValueKey(key));
    await tester.ensureVisible(field);
    await tester.enterText(field, value);
  }

  Future<void> save() async {
    await tester.tap(find.text('保存并应用'));
    await tester.pump();
    await waitFor(
      () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
    );
    expect(find.textContaining('本地模型已可用，可以继续对话'), findsOneWidget);
    await tester.tap(find.text('关闭'));
    await waitFor(() => find.byType(ModelConfigDialog).evaluate().isEmpty);
  }

  Future<void> send(
    int index,
    String key,
    int output,
    int upstream, {
    String provider = 'local_gateway',
    String model = 'manual-chat-id',
  }) async {
    await edit('chat-draft-input', '通过正式聊天入口验证 $index');
    await tester.pump(const Duration(milliseconds: 50));
    expect(
      tester
          .widget<IconButton>(find.byKey(const ValueKey('chat-send-button')))
          .onPressed,
      isNotNull,
    );
    await tester.tap(find.byKey(const ValueKey('chat-send-button')));
    await tester.pump();
    await waitFor(
      () =>
          chat.selectedConversation!.runs.length == index &&
          (chat.selectedConversation!.runs.last.phase ==
                  ChatRunPhase.succeeded ||
              chat.selectedConversation!.runs.last.phase ==
                  ChatRunPhase.failed),
      stage: '第 $index 次聊天 $provider/$model 的终态',
    );
    final run = chat.selectedConversation!.runs.last;
    expect(run.phase, ChatRunPhase.succeeded);
    expect(run.provider, provider);
    expect(run.model, model);
    expect(run.rawText, '自定义本地链路回复');
    expect(observed, hasLength(index));
    expect(observed.last.port, upstreams[upstream].port);
    expect(observed.last.path, '/compatible/v1/chat/completions');
    expect(observed.last.key, 'Bearer $key');
    expect(observed.last.body['model'], model);
    expect(observed.last.body['max_tokens'], output);
    await tester.runAsync(() async {
      final inspection = _RealHttpOverrides().createHttpClient(null)
        ..findProxy = (_) => 'DIRECT';
      final request = await inspection.getUrl(
        Uri.parse('http://127.0.0.1:1145/health'),
      );
      final health =
          jsonDecode(await utf8.decoder.bind(await request.close()).join())
              as Map;
      expect(health['data']['pid'], firstPid);
      expect(health['data']['instanceId'], 'custom-connection-1');
      final listRequest = await inspection.getUrl(
        Uri.parse('http://127.0.0.1:1145/v1/model'),
      );
      final body = await utf8.decoder.bind(await listRequest.close()).join();
      expect(body, isNot(contains(key)));
      inspection.close(force: true);
    });
  }

  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: ChatSpread(model: notebook, chat: chat, configFiles: store),
      ),
    ),
  );
  await waitFor(() => !chat.modelsLoading);
  await tester.tap(find.text('添加模型连接'));
  await tester.pump();
  await waitFor(() => find.byType(LinearProgressIndicator).evaluate().isEmpty);
  await tester.tap(find.text('DeepSeek'));
  await pumpUi();
  await tester.tap(find.text('自定义').last);
  await pumpUi();
  await edit('model-provider', multiple ? 'deepseek' : 'local_gateway');
  if (multiple) await edit('model-name', '官方');
  await edit(
    'model-base-url',
    'http://127.0.0.1:${upstreams[0].port}/compatible/v1',
  );
  await edit('model-id', 'manual-chat-id');
  await edit('model-key', 'controlled-custom-first');
  await save();
  await send(
    1,
    'controlled-custom-first',
    8192,
    0,
    provider: multiple ? 'deepseek' : 'local_gateway',
  );

  if (maintenance) {
    Future<void> open() async {
      await tester.tap(find.byTooltip('管理模型'));
      await tester.pump();
      await waitFor(
        () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
      );
      expect(
        tester
            .widget<TextFormField>(find.byKey(const ValueKey('model-key')))
            .controller!
            .text,
        isEmpty,
      );
    }

    Future<void> budget(int row, String context, String output) async {
      final advanced = find.byKey(ValueKey('model-advanced-$row'));
      await tester.ensureVisible(advanced);
      await tester.tap(advanced);
      await pumpUi();
      await edit('model-context-$row', context);
      await edit('model-output-$row', output);
    }

    await tester.runAsync(
      () => store.saveModelConnection(
        ModelConnectionSettings(
          provider: 'other',
          baseUrl: 'http://127.0.0.1:${upstreams[1].port}/compatible/v1',
          models: const [ChatModelSettings(model: 'manual-chat-id')],
        ),
        apiKey: 'controlled-other',
      ),
    );
    await open();
    await tester.ensureVisible(find.text('添加模型'));
    await tester.tap(find.text('添加模型'));
    await pumpUi();
    await edit('model-id-1', 'manual-reasoner');
    await budget(1, '65536', '2048');
    await edit('model-key', 'controlled-shared');
    await save();
    expect(chat.models.map((v) => v.identity).toSet(), {
      'local_gateway/manual-chat-id',
      'local_gateway/manual-reasoner',
      'other/manual-chat-id',
    });
    expect(chat.selectedModel!.identity, 'local_gateway/manual-chat-id');
    await send(2, 'controlled-shared', 8192, 0);
    chat.selectModel('local_gateway/manual-reasoner');
    await send(3, 'controlled-shared', 2048, 0, model: 'manual-reasoner');
    final session = chat.selectedConversation!;
    chat.selectModel('local_gateway/manual-chat-id');
    release = Completer<void>();
    await edit('chat-draft-input', '在途固定配置');
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.byKey(const ValueKey('chat-send-button')));
    await tester.pump();
    await waitFor(() => observed.length == 4);
    final active = session.runs.last;
    chat.updateDraft(session.sessionId, '保留下一条草稿');
    await open();
    await budget(0, '40000', '1024');
    await edit('model-key', 'controlled-rotated');
    await save();
    expect(active.isTerminal, isFalse);
    expect(chat.activeRunCount, 1);
    expect(observed.last.key, 'Bearer controlled-shared');
    expect(observed.last.body['max_tokens'], 8192);
    release.complete();
    release = null;
    await waitFor(() => active.isTerminal);
    expect(active.phase, ChatRunPhase.succeeded);
    expect(session.draft, '保留下一条草稿');
    await send(5, 'controlled-rotated', 1024, 0);
    // 仅调整预算且重复保存，保留各目标 Key；另一连接仍使用自己的 Key。
    await open();
    await budget(1, '70000', '3072');
    await save();
    await open();
    await save();
    chat.selectModel('other/manual-chat-id');
    await send(6, 'controlled-other', 8192, 1, provider: 'other');
    chat.selectModel('local_gateway/manual-reasoner');
    await send(7, 'controlled-rotated', 3072, 0, model: 'manual-reasoner');
    chat.selectModel('local_gateway/manual-chat-id');
    release = Completer<void>();
    finish = 'length';
    await edit('chat-draft-input', '移除正在请求的原身份');
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.byKey(const ValueKey('chat-send-button')));
    await tester.pump();
    await waitFor(() => observed.length == 8);
    final removed = session.runs.last;
    await open();
    await edit('model-id-1', 'renamed-reasoner');
    await tester.ensureVisible(find.byTooltip('移除模型 1'));
    await tester.tap(find.byTooltip('移除模型 1'));
    await pumpUi();
    final before = await tester.runAsync(
      () => File(store.configPath).readAsString(),
    );
    await tester.tap(find.text('保存并应用'));
    await tester.pump();
    await waitFor(
      () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
    );
    expect(find.textContaining('目标模型缺少本地密钥'), findsWidgets);
    expect(
      await tester.runAsync(() => File(store.configPath).readAsString()),
      before,
    );
    await edit('model-key', 'controlled-renamed');
    await save();
    expect(chat.selectedModel!.identity, 'local_gateway/renamed-reasoner');
    expect(removed.model, 'manual-chat-id');
    release.complete();
    release = null;
    finish = 'stop';
    await waitFor(() => removed.isTerminal);
    expect(removed.phase, ChatRunPhase.failed);
    final snapshot = await tester.runAsync(() async {
      final inspectionClient = _RealHttpOverrides().createHttpClient(null)
        ..findProxy = (_) => 'DIRECT';
      final inspection = HttpChatGateway(
        baseUri: Uri.parse('http://127.0.0.1:1145'),
        client: inspectionClient,
      );
      try {
        return await inspection.getRun(removed.runId!);
      } finally {
        inspection.dispose();
        inspectionClient.close(force: true);
      }
    });
    expect(snapshot!.error!.code, 'model_config_unavailable');
    expect(observed, hasLength(8));
    expect(removed.rawText, '自定义本地链路回复');
    await send(9, 'controlled-renamed', 3072, 0, model: 'renamed-reasoner');
    final sessionId = session.sessionId;
    await tester.pumpWidget(const SizedBox());
    await closePrimary();
    await tester.runAsync(() async {
      final reopened = await ConfigFileStore(
        directory.path,
      ).readModelConnections();
      expect(reopened.first.models.single.model, 'renamed-reasoner');
      expect(reopened.first.models.single.contextWindow, 70000);
      expect(reopened.first.models.single.maxOutputTokens, 3072);
      expect(
        reopened.every((v) => v.models.every((m) => m.hasLocalKey)),
        isTrue,
      );
      await stop();
      await start();
      final restartedClient = _RealHttpOverrides().createHttpClient(null)
        ..findProxy = (_) => 'DIRECT';
      final restartedGateway = HttpChatGateway(
        baseUri: Uri.parse('http://127.0.0.1:1145'),
        client: restartedClient,
      );
      addTearDown(() {
        restartedGateway.dispose();
        restartedClient.close(force: true);
      });
      final restarted = await restartedGateway.getModels();
      expect(restarted.map((v) => v.identity), [
        'local_gateway/renamed-reasoner',
        'other/manual-chat-id',
      ]);
      expect(restarted.first.configRevision, reopened.first.configRevision);
      expect(
        (await restartedGateway.getSession(sessionId)).runIds,
        hasLength(9),
      );
      final fresh = ChatViewModel(gateway: restartedGateway);
      final next = fresh.createLocalSession();
      fresh.updateDraft(next.sessionId, '重启后的正式请求');
      await fresh.send(
        next.sessionId,
        provider: 'local_gateway',
        model: 'renamed-reasoner',
      );
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (!next.runs.single.isTerminal &&
          DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 25));
      }
      expect(next.runs.single.phase, ChatRunPhase.succeeded);
      expect(observed.last.key, 'Bearer controlled-renamed');
      expect(observed.last.body['max_tokens'], 3072);
      fresh.dispose();
      restartedGateway.dispose();
      restartedClient.close(force: true);
      await stop();
      await for (final entity in directory.list(recursive: true)) {
        if (entity is! File || p.basename(entity.path) == '.env') {
          continue;
        }
        final contents = latin1.decode(await entity.readAsBytes());
        for (final key in [
          'controlled-custom-first',
          'controlled-other',
          'controlled-shared',
          'controlled-rotated',
          'controlled-renamed',
        ]) {
          expect(
            contents,
            isNot(contains(key)),
            reason: p.basename(entity.path),
          );
        }
      }
    });
    await pumpUi();
    expect(tester.takeException(), isNull);
    return;
  }

  if (multiple) {
    Future<void> open() async {
      await tester.tap(find.byTooltip('管理模型'));
      await tester.pump();
      await waitFor(
        () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
      );
    }

    Future<void> choose(String identity) async {
      final selector = find.byKey(const ValueKey('chat-model-selector'));
      final dropdown = tester.widget<DropdownButton<String>>(selector);
      final item = dropdown.items!.singleWhere((v) => v.value == identity);
      final label = (item.child as Text).data!;
      await tester.tap(selector);
      await pumpUi();
      await tester.tap(find.text(label).last);
      await pumpUi();
      expect(chat.selectedModel!.identity, identity);
    }

    for (var i = 1; i < 3; i++) {
      await open();
      final selector = find.byKey(const ValueKey('connection-selector'));
      await tester.ensureVisible(selector);
      await tester.tap(selector);
      await pumpUi();
      await tester.tap(find.text('新建连接').last);
      await pumpUi();
      expect(
        tester
            .widget<TextFormField>(find.byKey(const ValueKey('model-provider')))
            .controller!
            .text,
        'deepseek_${i + 1}',
      );
      await edit('model-name', '代理 $i');
      await edit(
        'model-base-url',
        'http://127.0.0.1:${upstreams[i].port}/compatible/v1',
      );
      await edit('model-id', 'manual-chat-id');
      await edit('model-key', 'controlled-proxy-$i');
      await save();
      await choose('deepseek_${i + 1}/manual-chat-id');
      await send(
        i + 1,
        'controlled-proxy-$i',
        8192,
        i,
        provider: 'deepseek_${i + 1}',
      );
    }
    expect(chat.models, hasLength(3));
    final session = chat.selectedConversation!;
    final sessionId = session.sessionId;
    release = Completer<void>();
    await edit('chat-draft-input', '挂起请求后管理连接');
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.byKey(const ValueKey('chat-send-button')));
    await tester.pump();
    await waitFor(() => observed.length == 4 && session.runs.length == 4);
    final original = session.runs.last;
    await edit('chat-draft-input', '下一条草稿保留');
    await choose('deepseek_2/manual-chat-id');
    final subscriptions = transport.paths
        .where((v) => v.endsWith('/events'))
        .length;
    final envBefore = (await tester.runAsync(
      () => File(p.join(directory.path, '.env')).readAsString(),
    ))!;
    await open();
    await edit('model-name', '改名后的代理');
    await save();
    expect(
      (await tester.runAsync(
        () => File(p.join(directory.path, '.env')).readAsString(),
      )),
      envBefore,
    );
    await open();
    expect(
      tester
          .widget<TextFormField>(find.byKey(const ValueKey('model-name')))
          .controller!
          .text,
      '改名后的代理',
    );
    await edit('model-key', 'controlled-proxy-1-new');
    await edit(
      'model-base-url',
      'http://127.0.0.1:${upstreams[1].port}/updated/v1',
    );
    await save();
    expect(chat.selectedSessionId, sessionId);
    expect(chat.selectedConversation, same(session));
    expect(session.draft, '下一条草稿保留');
    expect(session.runs.last, same(original));
    expect(original.provider, 'deepseek_3');
    expect(original.isTerminal, isFalse);
    expect(chat.activeRunCount, 1);
    expect(
      transport.paths.where((v) => v.endsWith('/events')).length,
      subscriptions,
    );
    expect(observed.last.key, 'Bearer controlled-proxy-2');
    await choose('deepseek/manual-chat-id');
    release.complete();
    release = null;
    await waitFor(
      () =>
          original.phase == ChatRunPhase.succeeded ||
          original.phase == ChatRunPhase.failed,
    );
    expect(original.phase, ChatRunPhase.succeeded);
    expect(original.provider, 'deepseek_3');
    expect(original.rawText, '自定义本地链路回复');
    expect(chat.activeRunCount, 0);
    expect(session.draft, '下一条草稿保留');
    await send(5, 'controlled-custom-first', 8192, 0, provider: 'deepseek');
    await choose('deepseek_3/manual-chat-id');
    await send(6, 'controlled-proxy-2', 8192, 2, provider: 'deepseek_3');
    await choose('deepseek_2/manual-chat-id');
    // 被修改连接使用新路径；另两份仍沿原地址和 Key。
    await edit('chat-draft-input', '修改后正式请求');
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.byKey(const ValueKey('chat-send-button')));
    await tester.pump();
    await waitFor(
      () =>
          session.runs.length == 7 &&
          (session.runs.last.phase == ChatRunPhase.succeeded ||
              session.runs.last.phase == ChatRunPhase.failed),
    );
    expect(session.runs.last.phase, ChatRunPhase.succeeded);
    expect(observed.last.port, upstreams[1].port);
    expect(observed.last.path, '/updated/v1/chat/completions');
    expect(observed.last.key, 'Bearer controlled-proxy-1-new');
    expect(observed.last.body['model'], 'manual-chat-id');
    await tester.pumpWidget(const SizedBox());
    await closePrimary();
    await tester.runAsync(() async {
      final reopened = await ConfigFileStore(
        directory.path,
      ).readModelConnections();
      expect(reopened.map((v) => v.provider), [
        'deepseek',
        'deepseek_2',
        'deepseek_3',
      ]);
      expect(reopened.map((v) => v.displayName), ['官方', '改名后的代理', '代理 2']);
      expect(reopened.every((v) => v.models.single.hasLocalKey), isTrue);
      await stop();
      await start();
      final restartedClient = _RealHttpOverrides().createHttpClient(null)
        ..findProxy = (_) => 'DIRECT';
      final restartedGateway = HttpChatGateway(
        baseUri: Uri.parse('http://127.0.0.1:1145'),
        client: restartedClient,
      );
      addTearDown(() {
        restartedGateway.dispose();
        restartedClient.close(force: true);
      });
      final restarted = ChatViewModel(
        gateway: restartedGateway,
        readConnectionNames: store.readConnectionNames,
      );
      await restarted.loadModels();
      expect(restarted.models, hasLength(3));
      expect(restarted.models.every((v) => v.available), isTrue);
      expect(restarted.models.map(restarted.modelLabel), [
        '官方 / manual-chat-id',
        '改名后的代理 / manual-chat-id',
        '代理 2 / manual-chat-id',
      ]);
      final history = await restartedGateway.getSession(sessionId);
      expect(history.runIds, hasLength(7));
      for (var i = 0; i < history.runIds.length; i++) {
        final run = await restartedGateway.getRun(history.runIds[i]);
        expect(
          run.provider,
          [
            'deepseek',
            'deepseek_2',
            'deepseek_3',
            'deepseek_3',
            'deepseek',
            'deepseek_3',
            'deepseek_2',
          ][i],
        );
      }
      final fresh = restarted.createLocalSession();
      restarted.updateDraft(fresh.sessionId, '重启后的连接请求');
      await restarted.send(
        fresh.sessionId,
        provider: 'deepseek_2',
        model: 'manual-chat-id',
      );
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (fresh.runs.single.phase != ChatRunPhase.succeeded &&
          fresh.runs.single.phase != ChatRunPhase.failed &&
          DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 25));
      }
      expect(fresh.runs.single.phase, ChatRunPhase.succeeded);
      expect(observed, hasLength(8));
      expect(observed.last.path, '/updated/v1/chat/completions');
      expect(observed.last.key, 'Bearer controlled-proxy-1-new');
      restarted.dispose();
      restartedGateway.dispose();
      restartedClient.close(force: true);
      await stop();
      await for (final entity in directory.list(recursive: true)) {
        if (entity is! File || p.basename(entity.path) == '.env') {
          continue;
        }
        final contents = latin1.decode(await entity.readAsBytes());
        for (final key in [
          'controlled-custom-first',
          'controlled-proxy-1',
          'controlled-proxy-2',
          'controlled-proxy-1-new',
        ]) {
          expect(
            contents,
            isNot(contains(key)),
            reason: p.basename(entity.path),
          );
        }
      }
      gateway.dispose();
      client.close(force: true);
    });
    await pumpUi();
    expect(tester.takeException(), isNull);
    return;
  }

  // 同一身份连续保存 Key、预算和服务地址，期间不改文件、不重启 Go。
  await tester.tap(find.byTooltip('管理模型'));
  await tester.pump();
  await waitFor(() => find.byType(LinearProgressIndicator).evaluate().isEmpty);
  expect(
    tester
        .widget<TextFormField>(find.byKey(const ValueKey('model-key')))
        .controller!
        .text,
    isEmpty,
  );
  await edit('model-key', 'controlled-custom-second');
  await save();
  await send(2, 'controlled-custom-second', 8192, 0);
  await tester.tap(find.byTooltip('管理模型'));
  await tester.pump();
  await waitFor(() => find.byType(LinearProgressIndicator).evaluate().isEmpty);
  await edit(
    'model-base-url',
    'http://127.0.0.1:${upstreams[1].port}/compatible/v1',
  );
  await tester.ensureVisible(find.text('高级设置'));
  await tester.tap(find.text('高级设置'));
  await pumpUi();
  await edit('model-output-0', '1024');
  await save();
  await send(3, 'controlled-custom-second', 1024, 1);

  final sessionId = chat.selectedSessionId!;
  await tester.pumpWidget(const SizedBox());
  await closePrimary();
  await tester.runAsync(() async {
    final saved = (await ConfigFileStore(
      directory.path,
    ).readModelConnections()).single;
    expect(saved.provider, 'local_gateway');
    expect(saved.models.single.hasLocalKey, isTrue);
    expect(saved.models.single.maxOutputTokens, 1024);
    await stop();
    await start();
    final restartedClient = _RealHttpOverrides().createHttpClient(null)
      ..findProxy = (_) => 'DIRECT';
    final restarted = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:1145'),
      client: restartedClient,
    );
    final models = await restarted.getModels();
    expect(models.single.provider, 'local_gateway');
    expect(models.single.available, isTrue);
    final history = await restarted.getSession(sessionId);
    expect(history.runIds, hasLength(3));
    restarted.dispose();
    restartedClient.close(force: true);
    await stop();
    // 可控测试 Key 不得进入配置正文、数据库或日志。
    await for (final entity in directory.list(recursive: true)) {
      if (entity is! File || p.basename(entity.path) == '.env') {
        continue;
      }
      final contents = latin1.decode(await entity.readAsBytes());
      expect(
        contents,
        isNot(contains('controlled-custom-first')),
        reason: p.basename(entity.path),
      );
      expect(
        contents,
        isNot(contains('controlled-custom-second')),
        reason: p.basename(entity.path),
      );
    }
  });
  await pumpUi();
  expect(tester.takeException(), isNull);
}

// 仅记录真实 HTTP 边界的路径，验证配置操作没有另建聊天或 SSE。
class _ObservedClient implements HttpClient {
  _ObservedClient(this.client, {Zone? ioZone}) : _ioZone = ioZone;
  final HttpClient client;
  final Zone? _ioZone;
  final paths = <String>[];

  /// JSON 请求完整转交真实客户端。
  @override
  Future<HttpClientRequest> openUrl(String method, Uri url) {
    paths.add(url.path);
    return _ioZone?.run(() => client.openUrl(method, url)) ??
        client.openUrl(method, url);
  }

  /// SSE 请求完整转交真实客户端。
  @override
  Future<HttpClientRequest> getUrl(Uri url) {
    paths.add(url.path);
    return _ioZone?.run(() => client.getUrl(url)) ?? client.getUrl(url);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
