import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';

void main() {
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
