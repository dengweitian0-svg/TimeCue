import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/operation_toast.dart';
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/settings/model_config_dialog.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

import 'fixtures/config_file_faults.dart';

void main() {
  for (final damagedFile in ['config.yaml', '.env', 'unreadable']) {
    testWidgets('$damagedFile 读取失败不展示原文，修复后从读取按钮恢复', (tester) async {
      final harness = await _DialogHarness.create(tester);
      final config = File(harness.store.configPath);
      final env = File('${harness.directory.path}/.env');
      final faults = ConfigFileFaults(harness.directory.path);
      await tester.runAsync(() async {
        if (damagedFile == 'config.yaml') {
          await config.writeAsString('private-file-content: [broken');
        } else if (damagedFile == '.env') {
          await env.writeAsString('OTHER="private-file-content');
        } else {
          await env.writeAsString('OTHER=private-file-content\n');
          faults.denyReads = true;
        }
      });
      await IOOverrides.runWithIOOverrides(() async {
        await harness.open(tester);
        expect(find.text('重试读取'), findsOneWidget);
        expect(find.textContaining('private-file-content'), findsNothing);
        expect(
          tester
              .widget<FilledButton>(find.widgetWithText(FilledButton, '保存并应用'))
              .onPressed,
          isNull,
        );
      }, faults);
      await tester.runAsync(() async {
        if (damagedFile == 'config.yaml') {
          expect(await config.readAsString(), 'private-file-content: [broken');
          await config.writeAsString('unknown: keep\n');
        } else {
          expect(await env.readAsString(), contains('private-file-content'));
          await env.writeAsString('OTHER="fixed\nmultiline"\n');
        }
      });
      faults.denyReads = false;
      await tester.tap(find.text('重试读取'));
      await _waitForDialog(tester);
      expect(find.byKey(const ValueKey('model-id')), findsOneWidget);
      expect(find.text('重试读取'), findsNothing);
      expect(tester.takeException(), isNull);
      await harness.close(tester);
    });
  }

  for (final failure in [
    'env-write',
    'config-write',
    'restore',
    'damaged-yaml',
  ]) {
    testWidgets('$failure 保存失败保留原文件与草稿，修复后按钮重试成功', (tester) async {
      final harness = await _DialogHarness.create(tester);
      final config = File(harness.store.configPath);
      final env = File('${harness.directory.path}/.env');
      await tester.runAsync(() async {
        await config.writeAsString('unknown: keep\n');
        await env.writeAsString('OTHER="keep\nquoted"\n');
      });
      await harness.open(tester);
      await tester.enterText(find.byKey(const ValueKey('model-id')), 'chat');
      await tester.enterText(
        find.byKey(const ValueKey('model-key')),
        'draft-secret',
      );
      final faults = ConfigFileFaults(harness.directory.path)
        ..denyEnvWrite = failure == 'env-write'
        ..denyConfigWrite = failure == 'config-write' || failure == 'restore'
        ..denyEnvRestore = failure == 'restore';
      if (failure == 'damaged-yaml') {
        await tester.runAsync(
          () => config.writeAsString('private-file-content: [broken'),
        );
      }
      await IOOverrides.runWithIOOverrides(() async {
        await tester.tap(find.text('保存并应用'));
        await _waitForDialog(tester);
      }, faults);
      expect(find.textContaining('private-file-content'), findsNothing);
      expect(find.textContaining('本地模型配置已可用'), findsNothing);
      expect(find.text('重试加载'), findsNothing);
      if (failure == 'restore') {
        expect(find.textContaining('密钥恢复失败'), findsWidgets);
      }
      expect(
        tester
            .widget<TextFormField>(find.byKey(const ValueKey('model-id')))
            .controller!
            .text,
        'chat',
      );
      expect(
        tester
            .widget<TextFormField>(find.byKey(const ValueKey('model-key')))
            .controller!
            .text,
        'draft-secret',
      );
      await tester.runAsync(() async {
        expect(
          await config.readAsString(),
          failure == 'damaged-yaml'
              ? 'private-file-content: [broken'
              : 'unknown: keep\n',
        );
        expect(
          await env.readAsString(),
          failure == 'restore'
              ? 'OTHER="keep\nquoted"\nDEEPSEEK_CHAT=draft-secret\n'
              : 'OTHER="keep\nquoted"\n',
        );
        if (failure == 'damaged-yaml') {
          await config.writeAsString('unknown: keep\n');
        }
      });
      await _dismissToasts(tester);
      await tester.tap(find.text('保存并应用'));
      await _waitForDialog(tester);
      expect(find.text('本地模型配置已可用'), findsOneWidget);
      expect(harness.chat.selectedModel?.model, 'chat');
      expect(
        tester
            .widget<TextFormField>(find.byKey(const ValueKey('model-key')))
            .controller!
            .text,
        isEmpty,
      );
      expect(tester.takeException(), isNull);
      await harness.close(tester);
    });
  }

  testWidgets('模型列表挂起后有界恢复，只重试加载不写文件也不自动发送', (tester) async {
    final harness = await _DialogHarness.create(tester);
    harness.hangModels = true;
    final session = harness.chat.createLocalSession();
    harness.chat.updateDraft(session.sessionId, '保留聊天草稿');
    await harness.open(tester);
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'chat');
    await tester.enterText(
      find.byKey(const ValueKey('model-key')),
      'saved-key',
    );
    await tester.tap(find.text('保存并应用'));
    await tester.pump();
    expect(
      tester
          .widget<TextButton>(find.widgetWithText(TextButton, '关闭'))
          .onPressed,
      isNull,
    );
    await _waitForDialog(tester);
    expect(harness.chat.modelsLoading, isFalse);
    expect(find.text('重试加载'), findsOneWidget);
    expect(find.textContaining('设置已保存，但服务尚未确认加载。'), findsWidgets);
    expect(find.text('本地模型配置已可用'), findsNothing);
    expect(session.draft, '保留聊天草稿');
    expect(harness.chat.activeRunCount, 0);
    final config = File(harness.store.configPath);
    final env = File('${harness.directory.path}/.env');
    final savedConfig = await tester.runAsync(config.readAsString);
    final savedEnv = await tester.runAsync(env.readAsString);
    await _dismissToasts(tester);
    // 断开文件读写权限后仍能重试加载，证明重试不再经过存储。
    harness.hangModels = false;
    final faults = ConfigFileFaults(harness.directory.path)
      ..denyReads = true
      ..denyEnvWrite = true
      ..denyConfigWrite = true;
    await IOOverrides.runWithIOOverrides(() async {
      await tester.tap(find.text('重试加载'));
      await _waitForDialog(tester);
    }, faults);
    expect(find.text('本地模型配置已可用'), findsOneWidget);
    expect(await tester.runAsync(config.readAsString), savedConfig);
    expect(await tester.runAsync(env.readAsString), savedEnv);
    expect(session.runs, isEmpty);
    expect(session.draft, '保留聊天草稿');
    await _dismissToasts(tester);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.byType(ModelConfigDialog), findsNothing);
    expect(tester.takeException(), isNull);
    await harness.close(tester);
  });

  testWidgets('运行身份失效后已保存仍可关闭，关闭不提示加载成功', (tester) async {
    final harness = await _DialogHarness.create(tester);
    await harness.open(tester);
    harness.chat.invalidateRuntime();
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'chat');
    await tester.enterText(
      find.byKey(const ValueKey('model-key')),
      'saved-key',
    );
    await tester.tap(find.text('保存并应用'));
    await _waitForDialog(tester);
    expect(find.text('重试加载'), findsOneWidget);
    expect(find.text('本地模型配置已可用'), findsNothing);
    expect(
      await tester.runAsync(() => harness.store.readDeepSeekModels()),
      hasLength(1),
    );
    await _dismissToasts(tester);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.byType(ModelConfigDialog), findsNothing);
    expect(find.text('本地模型配置已可用'), findsNothing);
    expect(tester.takeException(), isNull);
    await harness.close(tester);
  });

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
          final saved = await store.readDeepSeekModels();
          request.response.write(
            jsonEncode({
              'models': [
                for (final value in saved)
                  {
                    'provider': 'deepseek',
                    'model': value.model,
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
    await tester.ensureVisible(find.text('添加模型'));
    await tester.tap(find.text('添加模型'));
    await tester.pumpAndSettle();
    final secondId = find.byKey(const ValueKey('model-id-1'));
    await tester.ensureVisible(secondId);
    await tester.enterText(secondId, 'reasoner');
    final keyField = find.byKey(const ValueKey('model-key'));
    await tester.ensureVisible(keyField);
    await tester.enterText(keyField, 'shared-multiple');
    await tester.tap(find.text('保存并应用'));
    await flush();
    expect(chat.models.map((v) => v.model).toSet(), {'chat', 'reasoner'});
    expect(chat.selectedModel!.model, 'chat');
    final env = (await tester.runAsync(
      () => File('${directory.path}/.env').readAsString(),
    ))!;
    expect(env, contains('DEEPSEEK_CHAT=shared-multiple'));
    expect(env, contains('DEEPSEEK_REASONER=shared-multiple'));
    final remove = find.byTooltip('移除模型 1');
    await tester.ensureVisible(remove);
    await tester.tap(remove);
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<IconButton>(
            find.byWidgetPredicate(
              (v) => v is IconButton && v.tooltip == '移除模型 1',
            ),
          )
          .onPressed,
      isNull,
    );
    await tester.tap(find.text('保存并应用'));
    await flush();
    expect(chat.models.single.model, 'reasoner');
    expect(chat.selectedModel!.model, 'reasoner');
    await tester.pumpWidget(const SizedBox());
    client.close(force: true);
    await tester.pumpAndSettle();
  });
}

class _RealHttpOverrides extends HttpOverrides {}

// 等待真实文件/网络完成；虚拟时钟只推进绘制和 UI 定时器。
Future<void> _waitForDialog(WidgetTester tester) async {
  final deadline = DateTime.now().add(const Duration(seconds: 12));
  do {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 20)),
    );
    await tester.pump(const Duration(milliseconds: 100));
    if (find.byType(LinearProgressIndicator).evaluate().isEmpty) {
      await tester.pumpAndSettle();
      return;
    }
  } while (DateTime.now().isBefore(deadline));
  fail('模型配置弹窗超过十秒应用预算后仍未恢复操作');
}

Future<void> _dismissToasts(WidgetTester tester) async {
  final close = find.byIcon(Icons.close);
  while (close.evaluate().isNotEmpty) {
    await tester.tap(close.first);
    await tester.pumpAndSettle();
  }
}

class _DialogHarness {
  _DialogHarness(this.directory, this.store, this.chat, this.client);

  final Directory directory;
  final ConfigFileStore store;
  final ChatViewModel chat;
  final HttpClient client;
  bool hangModels = false;

  static Future<_DialogHarness> create(WidgetTester tester) async {
    final directory = (await tester.runAsync(
      () => Directory.systemTemp.createTemp('model-dialog-fault-'),
    ))!;
    final server = (await tester.runAsync(
      () => HttpServer.bind(InternetAddress.loopbackIPv4, 0),
    ))!;
    final client = _RealHttpOverrides().createHttpClient(null)
      ..findProxy = (_) => 'DIRECT';
    final gateway = HttpChatGateway(
      baseUri: Uri.parse('http://127.0.0.1:${server.port}'),
      client: client,
    );
    final chat = ChatViewModel(gateway: gateway);
    final harness = _DialogHarness(
      directory,
      ConfigFileStore(directory.path),
      chat,
      client,
    );
    await tester.runAsync(() async {
      server.listen((request) async {
        if (harness.hangModels) return;
        // 此夹具仅确认加载交互；真实 Go 热加载由 model_config_go_test 验证。
        request.response.headers.contentType = ContentType.json;
        request.response.write(
          '{"models":[{"provider":"deepseek","model":"chat","available":true}]}',
        );
        await request.response.close();
      });
    });
    addTearDown(() async {
      chat.dispose();
      gateway.dispose();
      client.close(force: true);
      await server.close(force: true);
      await directory.delete(recursive: true);
    });
    return harness;
  }

  Future<void> close(WidgetTester tester) async {
    client.close(force: true);
    await tester.pumpWidget(const SizedBox());
    await tester.pumpAndSettle();
  }

  Future<void> open(WidgetTester tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        builder: (context, child) => OperationToastHost(child: child!),
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () => showDialog<void>(
                context: context,
                builder: (_) => ModelConfigDialog(store: store, chat: chat),
              ),
              child: const Text('打开配置'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开配置'));
    await _waitForDialog(tester);
  }
}
