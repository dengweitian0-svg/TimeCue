import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/settings/model_config_dialog.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  testWidgets('空态入口保存后自动可选，管理入口可重复保存且 Key 默认隐藏', (tester) async {
    final directory = (await tester.runAsync(
      () => Directory.systemTemp.createTemp('timecue-model-dialog-'),
    ))!;
    final store = ConfigFileStore(directory.path);
    final server = (await tester.runAsync(
      () => HttpServer.bind(InternetAddress.loopbackIPv4, 0),
    ))!;
    await tester.runAsync(() async {
      server.listen((request) async {
        request.response.headers.contentType = ContentType.json;
        if (request.uri.path == '/v1/model') {
          final saved = await store.readDeepSeekModel();
          request.response.write(
            jsonEncode({
              'models': [
                if (saved != null)
                  {
                    'provider': 'deepseek',
                    'model': saved.model,
                    'available': true,
                  },
              ],
            }),
          );
        } else {
          request.response.write('{"sessions":[]}');
        }
        await request.response.close();
      });
    });
    final client = _RealHttpOverrides().createHttpClient(null)
      ..findProxy = (_) => 'DIRECT';
    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}'),
      client: client,
    );
    final chat = ChatViewModel(gateway: gateway);
    final repository = DemoNotebookRepository();
    final notebook = NotebookViewModel(repository);
    addTearDown(() async {
      chat.dispose();
      gateway.dispose();
      client.close(force: true);
      notebook.dispose();
      repository.dispose();
      await server.close(force: true);
      await directory.delete(recursive: true);
    });
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: ChatSpread(model: notebook, chat: chat, configFiles: store),
        ),
      ),
    );
    Future<void> flush() async {
      for (var i = 0; i < 100; i++) {
        await tester.runAsync(
          () => Future<void>.delayed(const Duration(milliseconds: 15)),
        );
        await tester.pump(const Duration(milliseconds: 20));
        if (find.byType(LinearProgressIndicator).evaluate().isEmpty &&
            !chat.modelsLoading &&
            chat.historyLoaded) {
          break;
        }
      }
      await tester.pumpAndSettle();
    }

    await flush();
    await tester.tap(find.text('添加 DeepSeek 模型'));
    await flush();
    expect(find.byType(ModelConfigDialog), findsOneWidget);
    expect(find.text('https://api.deepseek.com'), findsOneWidget);
    expect(
      tester
          .widget<TextField>(
            find.descendant(
              of: find.byKey(const ValueKey('model-key')),
              matching: find.byType(TextField),
            ),
          )
          .obscureText,
      isTrue,
    );
    await tester.tap(find.text('保存并应用'));
    await tester.pump();
    expect(find.text('请填写模型 ID'), findsOneWidget);
    expect(
      await tester.runAsync(() => File(store.configPath).exists()),
      isFalse,
    );
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'chat');
    await tester.enterText(
      find.byKey(const ValueKey('model-key')),
      'local-dialog-first',
    );
    await tester.tap(find.text('保存并应用'));
    await tester.pump();
    expect(
      tester
          .widget<FilledButton>(find.widgetWithText(FilledButton, '保存并应用'))
          .onPressed,
      isNull,
    );
    await flush();
    expect(chat.selectedModel!.model, 'chat');
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('管理模型'));
    await flush();
    expect(
      tester
          .widget<TextFormField>(find.byKey(const ValueKey('model-key')))
          .controller!
          .text,
      isEmpty,
    );
    await tester.enterText(
      find.byKey(const ValueKey('model-key')),
      'local-dialog-second',
    );
    await tester.tap(find.text('保存并应用'));
    await flush();
    expect(
      await tester.runAsync(
        () => File('${directory.path}/.env').readAsString(),
      ),
      contains('local-dialog-second'),
    );
    expect(chat.selectedModel!.model, 'chat');
    expect(find.textContaining('本地模型已可用'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    client.close(force: true);
    await tester.pumpAndSettle();
  });
}

class _RealHttpOverrides extends HttpOverrides {}
