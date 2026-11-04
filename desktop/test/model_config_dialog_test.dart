import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
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
  testWidgets('预算、Key 和增删操作均有草稿守卫，撤回修改可直接关闭', (tester) async {
    final harness = await _DialogHarness.create(tester);
    await tester.runAsync(
      () => harness.store.saveDeepSeekModels([
        const DeepSeekModelSettings(model: 'chat'),
      ], apiKey: 'original'),
    );
    for (final edit in ['key', 'budget', 'add', 'remove']) {
      await harness.open(tester);
      if (edit == 'key') {
        await tester.enterText(find.byKey(const ValueKey('model-key')), 'new');
      } else if (edit == 'budget') {
        await tester.tap(find.text('高级设置'));
        await tester.pumpAndSettle();
        await tester.enterText(
          find.byKey(const ValueKey('model-output-0')),
          '4096',
        );
      } else {
        await tester.ensureVisible(find.text('添加模型'));
        await tester.tap(find.text('添加模型'));
        await tester.pumpAndSettle();
        if (edit == 'remove') {
          await tester.ensureVisible(find.byTooltip('移除模型 1'));
          await tester.tap(find.byTooltip('移除模型 1'));
          await tester.pumpAndSettle();
        }
      }
      await tester.tap(find.text('关闭'));
      await tester.pumpAndSettle();
      expect(find.text('舍弃未保存的修改？'), findsOneWidget, reason: edit);
      await tester.tap(find.text('舍弃修改'));
      await tester.pumpAndSettle();
    }
    await harness.open(tester);
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'changed');
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'chat');
    await tester.enterText(find.byKey(const ValueKey('model-key')), 'new');
    await tester.enterText(find.byKey(const ValueKey('model-key')), '');
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.byType(ModelConfigDialog), findsNothing);
    expect(find.text('舍弃未保存的修改？'), findsNothing);
    await harness.close(tester);
  });

  testWidgets('已有模型缺少 Key 时明确提示，空 Key 保存不覆盖原文件', (tester) async {
    final harness = await _DialogHarness.create(tester);
    await tester.runAsync(
      () => harness.store.saveDeepSeekModels([
        const DeepSeekModelSettings(model: 'chat'),
      ], apiKey: 'original'),
    );
    final env = File('${harness.directory.path}/.env');
    await tester.runAsync(() => env.writeAsString('OTHER=keep\n'));
    final config = File(harness.store.configPath);
    final original = await tester.runAsync(config.readAsString);
    await harness.open(tester);
    expect(find.text('未配置本地密钥，请填写新 Key。'), findsOneWidget);
    await tester.tap(find.text('保存并应用'));
    await _waitForDialog(tester);
    expect(find.textContaining('目标模型缺少本地密钥'), findsWidgets);
    expect(await tester.runAsync(config.readAsString), original);
    expect(await tester.runAsync(env.readAsString), 'OTHER=keep\n');
    await harness.close(tester);
  });

  testWidgets('小窗口大字模式可滚动增删、预算、错误重试、保存和关闭', (tester) async {
    tester.view.physicalSize = const Size(480, 400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final harness = await _DialogHarness.create(tester);
    await harness.open(tester, textScale: 2);
    await tester.ensureVisible(find.byKey(const ValueKey('model-id')));
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'chat');
    await tester.ensureVisible(find.text('高级设置'));
    await tester.tap(find.text('高级设置'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byKey(const ValueKey('model-output-0')));
    await tester.enterText(
      find.byKey(const ValueKey('model-output-0')),
      '1024',
    );
    await tester.ensureVisible(find.text('添加模型'));
    await tester.tap(find.text('添加模型'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byTooltip('移除模型 2'));
    await tester.tap(find.byTooltip('移除模型 2'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('保存并应用'));
    await tester.tap(find.text('保存并应用'));
    await _waitForDialog(tester);
    await _dismissToasts(tester);
    await tester.ensureVisible(find.text('目标模型缺少本地密钥，请填写 API Key'));
    await tester.ensureVisible(find.byKey(const ValueKey('model-key')));
    await tester.enterText(find.byKey(const ValueKey('model-key')), 'new-key');
    await tester.ensureVisible(find.text('保存并应用'));
    await tester.tap(find.text('保存并应用'));
    await _waitForDialog(tester);
    await _dismissToasts(tester);
    expect(
      (await tester.runAsync(
        harness.store.readDeepSeekModels,
      ))!.single.maxOutputTokens,
      1024,
    );
    await tester.ensureVisible(find.text('关闭'));
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.byType(ModelConfigDialog), findsNothing);
    expect(tester.takeException(), isNull);
    await harness.close(tester);
  });

  for (final phase in ['read', 'save', 'apply']) {
    testWidgets('$phase 等待期间禁止编辑和关闭，页面卸载后忽略迟到结果', (tester) async {
      final harness = await _DialogHarness.create(tester);
      final gate = Completer<void>();
      final faults = ConfigFileFaults(harness.directory.path);
      if (phase == 'read') faults.beforeRead = gate.future;
      if (phase == 'save') faults.beforeConfigReplace = gate.future;
      if (phase == 'apply') harness.modelsGate = gate.future;
      await IOOverrides.runWithIOOverrides(() async {
        await harness.open(tester, wait: phase != 'read');
        if (phase != 'read') {
          await tester.enterText(
            find.byKey(const ValueKey('model-id')),
            'chat',
          );
          await tester.enterText(
            find.byKey(const ValueKey('model-key')),
            'new-key',
          );
          await tester.tap(find.text('保存并应用'));
          for (var i = 0; i < 20; i++) {
            await tester.runAsync(
              () => Future<void>.delayed(const Duration(milliseconds: 20)),
            );
            await tester.pump(const Duration(milliseconds: 50));
          }
        }
        expect(find.byType(LinearProgressIndicator), findsOneWidget);
        for (final field in tester.widgetList<TextFormField>(
          find.byType(TextFormField),
        )) {
          expect(field.enabled, isFalse);
        }
        expect(
          tester
              .widget<TextButton>(find.widgetWithText(TextButton, '关闭'))
              .onPressed,
          isNull,
        );
        await tester.sendKeyEvent(LogicalKeyboardKey.escape);
        await tester.pump();
        expect(find.text('舍弃未保存的修改？'), findsNothing);
        expect(find.byType(ModelConfigDialog), findsOneWidget);
        await tester.pumpWidget(
          const MaterialApp(
            key: ValueKey('replacement'),
            home: Scaffold(body: Text('新页面')),
          ),
        );
        gate.complete();
        for (var i = 0; i < 20; i++) {
          await tester.runAsync(
            () => Future<void>.delayed(const Duration(milliseconds: 20)),
          );
          await tester.pump(const Duration(milliseconds: 50));
        }
      }, faults);
      expect(find.text('新页面'), findsOneWidget);
      expect(find.text('本地模型配置已可用'), findsNothing);
      expect(tester.takeException(), isNull);
      await harness.close(tester);
    });
  }

  testWidgets('应用等待中运行身份失效，迟到列表不提示成功或重复保存', (tester) async {
    final harness = await _DialogHarness.create(tester);
    final gate = Completer<void>();
    harness.modelsGate = gate.future;
    await harness.open(tester);
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'chat');
    await tester.enterText(
      find.byKey(const ValueKey('model-key')),
      'saved-key',
    );
    await tester.tap(find.text('保存并应用'));
    await tester.pump();
    final env = File('${harness.directory.path}/.env');
    for (var i = 0; i < 30 && !harness.chat.modelsLoading; i++) {
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 20)),
      );
      await tester.pump(const Duration(milliseconds: 50));
    }
    expect(harness.chat.modelsLoading, isTrue);
    final savedEnv = await tester.runAsync(env.readAsString);
    harness.chat.invalidateRuntime();
    gate.complete();
    await _waitForDialog(tester);
    expect(find.text('本地模型配置已可用'), findsNothing);
    expect(find.text('重试加载'), findsOneWidget);
    expect(await tester.runAsync(env.readAsString), savedEnv);
    await _dismissToasts(tester);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.byType(ModelConfigDialog), findsNothing);
    expect(find.text('舍弃未保存的修改？'), findsNothing);
    await harness.close(tester);
  });

  testWidgets('重开配置只显示密钥状态，关闭和 Escape 共用草稿舍弃确认', (tester) async {
    final harness = await _DialogHarness.create(tester);
    await tester.runAsync(
      () => harness.store.saveDeepSeekModels([
        const DeepSeekModelSettings(model: 'chat'),
      ], apiKey: 'never-display-original-key'),
    );
    final config = File(harness.store.configPath);
    final env = File('${harness.directory.path}/.env');
    final originalConfig = await tester.runAsync(config.readAsString);
    final originalEnv = await tester.runAsync(env.readAsString);
    await harness.open(tester);
    expect(find.text('已配置本地密钥'), findsOneWidget);
    expect(find.textContaining('never-display-original-key'), findsNothing);
    expect(
      tester
          .widget<TextFormField>(find.byKey(const ValueKey('model-key')))
          .controller!
          .text,
      isEmpty,
    );
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.byType(ModelConfigDialog), findsNothing);
    await tester.tap(find.text('打开配置'));
    await _waitForDialog(tester);
    await tester.enterText(find.byKey(const ValueKey('model-id')), 'changed');
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('继续编辑'));
    await tester.pumpAndSettle();
    expect(find.text('changed'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('舍弃修改'));
    await tester.pumpAndSettle();
    expect(find.byType(ModelConfigDialog), findsNothing);
    expect(await tester.runAsync(config.readAsString), originalConfig);
    expect(await tester.runAsync(env.readAsString), originalEnv);
    await harness.close(tester);
  });

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
  Future<void>? modelsGate;

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
        await harness.modelsGate;
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

  Future<void> open(
    WidgetTester tester, {
    double textScale = 1,
    bool wait = true,
  }) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(
            context,
          ).copyWith(textScaler: TextScaler.linear(textScale)),
          child: OperationToastHost(child: child!),
        ),
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
    if (wait) {
      await _waitForDialog(tester);
    } else {
      await tester.pump();
    }
  }
}
