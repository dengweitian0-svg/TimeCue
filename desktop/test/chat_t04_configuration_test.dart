import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';

void main() {
  test('加载请求挂起时共用总期限，迟到结果不发布，服务恢复后可再次刷新', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final pending = <HttpRequest>[];
    var restored = false;
    const body =
        '{"models":[{"provider":"deepseek","model":"chat","available":true}]}';
    server.listen((request) async {
      if (!restored) {
        pending.add(request);
        return;
      }
      request.response.headers.contentType = ContentType.json;
      request.response.write(body);
      await request.response.close();
    });
    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}'),
    );
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() async {
      chat.dispose();
      gateway.dispose();
      await server.close(force: true);
    });
    final clock = Stopwatch()..start();
    expect(
      await chat.applySavedModel(
        provider: 'deepseek',
        model: 'chat',
        timeout: const Duration(milliseconds: 650),
      ),
      isFalse,
    );
    expect(clock.elapsed, lessThan(const Duration(seconds: 2)));
    expect(chat.modelsLoading, isFalse);
    expect(chat.selectedModel, isNull);
    for (final request in pending) {
      request.response.headers.contentType = ContentType.json;
      request.response.write(body);
      await request.response.close();
    }
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(chat.models, isEmpty, reason: '超时请求的迟到结果不能确认应用成功');
    restored = true;
    expect(
      await chat.applySavedModel(provider: 'deepseek', model: 'chat'),
      isTrue,
    );
    expect(chat.selectedModel?.model, 'chat');
    expect(chat.activeRunCount, 0);
  });

  test('完整集合加载才成功，移除旧项且新增齐全后保留选择或回退到保存模型', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var names = ['chat', 'removed'];
    var unavailableChat = false;
    server.listen((request) async {
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'models': [
            {'provider': 'other', 'model': 'untouched', 'available': true},
            for (final name in names)
              {
                'provider': 'deepseek',
                'model': name,
                'available': name != 'chat' || !unavailableChat,
              },
          ],
        }),
      );
      await request.response.close();
    });
    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}'),
    );
    final chat = ChatViewModel(gateway: gateway);
    addTearDown(() async {
      chat.dispose();
      gateway.dispose();
      await server.close(force: true);
    });
    await chat.loadModels();
    chat.selectModel(chat.models.firstWhere((v) => v.model == 'chat').identity);
    var completed = false;
    final applying = chat.applySavedModels(
      provider: 'deepseek',
      models: ['chat', 'new'],
    )..then((_) => completed = true);
    await Future<void>.delayed(const Duration(milliseconds: 750));
    expect(completed, isFalse, reason: '旧集合不能被视为成功');
    names = ['chat'];
    await Future<void>.delayed(const Duration(milliseconds: 750));
    expect(completed, isFalse, reason: '部分目标不能被视为成功');
    names = ['chat', 'new'];
    expect(await applying, isTrue);
    expect(chat.selectedModel!.model, 'chat');
    chat.selectModel(chat.models.firstWhere((v) => v.model == 'new').identity);
    names = ['chat', 'replacement'];
    unavailableChat = true;
    expect(
      await chat.applySavedModels(
        provider: 'deepseek',
        models: ['chat', 'replacement'],
        timeout: const Duration(milliseconds: 800),
      ),
      isFalse,
    );
    unavailableChat = false;
    names = ['chat', 'new'];
    await chat.loadModels();
    chat.selectModel(chat.models.firstWhere((v) => v.model == 'new').identity);
    names = ['chat', 'replacement'];
    expect(
      await chat.applySavedModels(
        provider: 'deepseek',
        models: ['chat', 'replacement'],
      ),
      isTrue,
    );
    expect(chat.selectedModel!.provider, 'deepseek');
    expect(chat.selectedModel!.model, 'chat');
    expect(chat.models.where((v) => v.model == 'new'), isEmpty);
  });

  test('每次保存后都等待并刷新模型，同名更新不跳过且保留原草稿', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var available = false;
    server.listen((request) async {
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'models': [
            {'provider': 'deepseek', 'model': 'chat', 'available': available},
          ],
        }),
      );
      await request.response.close();
    });
    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}'),
    );
    final model = ChatViewModel(gateway: gateway);
    addTearDown(() async {
      model.dispose();
      gateway.dispose();
      await server.close(force: true);
    });
    final session = model.createLocalSession();
    model.updateDraft(session.sessionId, '保留草稿');
    await model.loadModels();
    available = true;
    expect(
      await model.applySavedModel(provider: 'deepseek', model: 'chat'),
      isTrue,
    );
    expect(model.selectedModel!.model, 'chat');
    // 原列表仍可用时，当前读取结果必须重新参与确认。
    available = false;
    expect(
      await model.applySavedModel(
        provider: 'deepseek',
        model: 'chat',
        timeout: const Duration(milliseconds: 800),
      ),
      isFalse,
    );
    expect(model.selectedModel, isNull);
    available = true;
    expect(
      await model.applySavedModel(provider: 'deepseek', model: 'chat'),
      isTrue,
    );
    expect(session.draft, '保留草稿');
    expect(model.activeRunCount, 0);
  });

  test('明确的配置 503 释放占用，补齐密钥并刷新后可以发起新对话', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var keyPresent = false;
    final keys = <String>[];
    server.listen((request) async {
      request.response.headers.contentType = ContentType.json;
      Object body;
      if (request.uri.path == '/v1/model') {
        body = {
          'models': [
            {'provider': 'test', 'model': 'model', 'available': keyPresent},
          ],
        };
      } else if (request.method == 'POST') {
        await utf8.decoder.bind(request).join();
        keys.add(request.headers.value('Idempotency-Key')!);
        if (!keyPresent) {
          request.response.statusCode = 503;
          body = {
            'error': {
              'code': 'model_config_unavailable',
              'message': '模型配置不可用，请检查 .env 密钥',
            },
          };
        } else {
          body = {
            'run_id': 'repaired',
            'status': 'succeeded',
            'output': '修复后成功',
            'error': null,
          };
        }
      } else if (request.uri.path.endsWith('/events')) {
        body = {'events': [], 'next_after_seq': 0, 'has_more': false};
      } else {
        body = {
          'session_id': 'session',
          'session_name': '',
          'run_ids': ['repaired'],
        };
      }
      request.response.write(jsonEncode(body));
      await request.response.close();
    });
    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}'),
    );
    var key = 0;
    final model = ChatViewModel(
      gateway: gateway,
      createSessionId: () => 'session',
      createIdempotencyKey: () => 'create_session:${++key}',
    );
    addTearDown(() async {
      model.dispose();
      gateway.dispose();
      await server.close(force: true);
    });
    final conversation = model.createLocalSession();
    await model.loadModels();
    expect(model.models.single.available, isFalse);
    model.updateDraft('session', '配置变化前准备发送的文字');
    await model.send('session', provider: 'test', model: 'model');
    expect(model.activeRunCount, 0);
    expect(conversation.runs.single.isTerminal, isTrue);
    expect(conversation.runs.single.error, contains('.env'));
    keyPresent = true;
    await model.loadModels();
    expect(model.selectedModel?.available, isTrue);
    model.updateDraft('session', '修复后新一轮');
    await model.send('session', provider: 'test', model: 'model');
    expect(conversation.runs.last.rawText, '修复后成功');
    expect(model.activeRunCount, 0);
    expect(keys.toSet(), hasLength(2));
  });
}
