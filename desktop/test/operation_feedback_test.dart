import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/config_file_store.dart';
import 'package:timecue_desktop/app/operation_toast.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';
import 'package:timecue_desktop/features/settings/settings_dialog.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

Widget _toastApp(Widget home) => MaterialApp(
  theme: NotebookTheme.data,
  builder: (context, child) => OperationToastHost(child: child!),
  home: home,
);

Widget _pendingView(NotebookViewModel model) => ListenableBuilder(
  listenable: model,
  builder: (_, _) => TodaySpread(model: model),
);

Future<void> _dismissToast(WidgetTester tester, String message) async {
  final card = find.ancestor(
    of: find.text(message),
    matching: find.byType(Material),
  );
  await tester.tap(
    find.descendant(of: card.first, matching: find.byIcon(Icons.close)),
  );
  await tester.pump();
}

void main() {
  testWidgets('实际确认按钮成功后清除选择并显示成功提醒', (tester) async {
    final repository = DemoNotebookRepository();
    final model = NotebookViewModel(
      repository,
      onOperationFeedback: (status, message) {
        switch (status) {
          case NotebookOperationStatus.success:
            OperationToast.success(_feedbackContext!, message);
          case NotebookOperationStatus.warning:
            OperationToast.warning(_feedbackContext!, message);
          case NotebookOperationStatus.error:
            OperationToast.error(_feedbackContext!, message);
        }
      },
    );
    addTearDown(() {
      model.dispose();
      repository.dispose();
    });
    await tester.pumpWidget(
      _toastApp(
        Scaffold(
          body: Builder(
            builder: (context) {
              _feedbackContext = context;
              return _pendingView(model);
            },
          ),
        ),
      ),
    );

    await tester.tap(find.byType(Checkbox).first);
    await tester.pump();
    await tester.tap(find.text('确认选中'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(model.selected, isEmpty);
    expect(repository.pending.any((entry) => entry.id == 'n1'), isFalse);
    expect(find.text('已确认 1 条提醒'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('确认失败时保留选择并显示失败提醒', (tester) async {
    final repository = _FeedbackRepository()..failConfirm = true;
    final events = <(NotebookOperationStatus, String)>[];
    final model = NotebookViewModel(
      repository,
      onOperationFeedback: (status, message) {
        events.add((status, message));
        OperationToast.error(_feedbackContext!, message);
      },
    );
    addTearDown(() {
      model.dispose();
      repository.dispose();
    });
    await tester.pumpWidget(
      _toastApp(
        Scaffold(
          body: Builder(
            builder: (context) {
              _feedbackContext = context;
              return _pendingView(model);
            },
          ),
        ),
      ),
    );

    await tester.tap(find.byType(Checkbox).first);
    await tester.pump();
    await tester.tap(find.text('确认选中'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(model.selected, {'n1'});
    expect(repository.pending.any((entry) => entry.id == 'n1'), isTrue);
    expect(model.error, contains('模拟确认失败'));
    expect(events, hasLength(1));
    expect(events.single.$1, NotebookOperationStatus.error);
    expect(find.textContaining('模拟确认失败'), findsWidgets);
    expect(tester.takeException(), isNull);
  });

  testWidgets('确认已提交但重新读取失败时显示提醒而非失败', (tester) async {
    final repository = _FeedbackRepository()..refreshErrorAfterConfirm = true;
    final events = <(NotebookOperationStatus, String)>[];
    final model = NotebookViewModel(
      repository,
      onOperationFeedback: (status, message) {
        events.add((status, message));
        if (status == NotebookOperationStatus.warning) {
          OperationToast.warning(_feedbackContext!, message);
        } else if (status == NotebookOperationStatus.error) {
          OperationToast.error(_feedbackContext!, message);
        }
      },
    );
    addTearDown(() {
      model.dispose();
      repository.dispose();
    });
    await tester.pumpWidget(
      _toastApp(
        Scaffold(
          body: Builder(
            builder: (context) {
              _feedbackContext = context;
              return _pendingView(model);
            },
          ),
        ),
      ),
    );

    await tester.tap(find.byType(Checkbox).first);
    await tester.pump();
    await tester.tap(find.text('确认选中'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(repository.pending.any((entry) => entry.id == 'n1'), isFalse);
    expect(model.selected, isEmpty);
    expect(events, hasLength(1));
    expect(events.single.$1, NotebookOperationStatus.warning);
    expect(find.text(events.single.$2), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('日程创建、修改、暂停、重新开启和删除的实际按钮显示结果', (tester) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = DemoNotebookRepository();
    addTearDown(repository.dispose);
    await tester.pumpWidget(
      _toastApp(
        Scaffold(
          body: Builder(
            builder: (context) => Column(
              children: [
                TextButton(
                  onPressed: () => showDialog<void>(
                    context: context,
                    builder: (_) =>
                        ScheduleCreateDialog(repository: repository),
                  ),
                  child: const Text('新建日程'),
                ),
                TextButton(
                  onPressed: () => showDialog<void>(
                    context: context,
                    builder: (_) => ScheduleEditDialog(
                      repository: repository,
                      original: repository.schedule('s1'),
                    ),
                  ),
                  child: const Text('编辑日程'),
                ),
              ],
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('新建日程'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, 'Toast 新日程');
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(repository.schedule('created0').draft.name, 'Toast 新日程');
    expect(find.text('日程已创建'), findsOneWidget);
    await _dismissToast(tester, '日程已创建');

    await tester.tap(find.text('编辑日程'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, 'Toast 修改日程');
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(repository.schedule('s1').draft.name, 'Toast 修改日程');
    expect(find.text('日程已保存'), findsOneWidget);
    await _dismissToast(tester, '日程已保存');

    await tester.tap(find.text('编辑日程'));
    await tester.pumpAndSettle();
    await tester.tap(find.byType(SwitchListTile));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(repository.schedule('s1').draft.enabled, isFalse);
    expect(find.text('日程已保存'), findsOneWidget);
    await _dismissToast(tester, '日程已保存');

    await tester.tap(find.text('编辑日程'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('重新开启'));
    await tester.pumpAndSettle();
    expect(repository.schedule('s1').draft.enabled, isTrue);
    expect(find.text('日程已重新开启'), findsOneWidget);
    await _dismissToast(tester, '日程已重新开启');

    await tester.tap(find.text('编辑日程'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('删除'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('删除').last);
    await tester.pumpAndSettle();
    expect(repository.schedule('s1').deleted, isTrue);
    expect(find.text('日程已删除'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('表单校验和仓储保存失败显示失败提醒并保留日程草稿', (tester) async {
    final repository = _FeedbackRepository()..failSave = true;
    addTearDown(repository.dispose);
    await tester.pumpWidget(
      _toastApp(
        Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => showDialog<void>(
                context: context,
                builder: (_) => ScheduleCreateDialog(repository: repository),
              ),
              child: const Text('打开新建表单'),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('打开新建表单'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(find.text('日程未能保存，请查看表单中的错误。'), findsOneWidget);
    expect(find.text('请填写日程名称'), findsOneWidget);
    await _dismissToast(tester, '日程未能保存，请查看表单中的错误。');

    await tester.enterText(find.byType(TextField).first, '保留的日程草稿');
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('确定'));
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('保存日程'));
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(find.text('日程未能保存，请查看表单中的错误。'), findsOneWidget);
    expect(find.textContaining('模拟日程保存失败'), findsOneWidget);
    expect(find.text('保留的日程草稿'), findsOneWidget);
    expect(repository.saves, 1);
  });

  testWidgets('设置保存、恢复默认及写入失败均显示对应结果', (tester) async {
    final directory = (await tester.runAsync(
      () => Directory.systemTemp.createTemp('timecue-toast-'),
    ))!;
    addTearDown(() => tester.runAsync(() => directory.delete(recursive: true)));
    final store = ConfigFileStore(directory.path);
    await tester.runAsync(
      () => File(store.configPath).writeAsString('log:\n  level: info\n'),
    );
    await tester.pumpWidget(
      _toastApp(
        Scaffold(
          body: Builder(
            builder: (context) => TextButton(
              onPressed: () => showDialog<void>(
                context: context,
                builder: (_) => SettingsDialog(store: store),
              ),
              child: const Text('打开设置'),
            ),
          ),
        ),
      ),
    );
    Future<void> flushSettings() async {
      await tester.pump();
      final progress = find.descendant(
        of: find.byType(SettingsDialog),
        matching: find.byType(LinearProgressIndicator),
      );
      for (
        var attempt = 0;
        attempt < 100 && progress.evaluate().isNotEmpty;
        attempt++
      ) {
        await tester.runAsync(
          () => Future<void>.delayed(const Duration(milliseconds: 10)),
        );
        await tester.pump(const Duration(milliseconds: 20));
      }
      expect(progress, findsNothing);
      await tester.pumpAndSettle();
    }

    await tester.tap(find.text('打开设置'));
    await flushSettings();
    await tester.tap(find.byType(DropdownButtonFormField<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('debug').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('保存设置'));
    await flushSettings();
    expect(find.text('设置已保存'), findsOneWidget);
    await _dismissToast(tester, '设置已保存');

    await tester.tap(find.text('恢复默认设置'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('恢复默认'));
    await flushSettings();
    expect(find.text('已恢复默认设置'), findsOneWidget);
    await _dismissToast(tester, '已恢复默认设置');

    await tester.tap(find.byType(DropdownButtonFormField<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('error').last);
    await tester.pumpAndSettle();
    await tester.runAsync(() => File(store.configPath).writeAsString('[]'));
    await tester.tap(find.text('保存设置'));
    await flushSettings();
    expect(find.text('设置操作未完成，请查看弹窗中的错误。'), findsOneWidget);
    expect(find.textContaining('保存设置失败'), findsOneWidget);
    expect(
      tester
          .widget<DropdownButtonFormField<String>>(
            find.byType(DropdownButtonFormField<String>),
          )
          .initialValue,
      'error',
    );
    expect(tester.takeException(), isNull);
  });

  test('确认请求等待时 ViewModel 释放，迟到结果不再回调', () async {
    final repository = _FeedbackRepository()..confirmGate = Completer<void>();
    final events = <(NotebookOperationStatus, String)>[];
    final model = NotebookViewModel(
      repository,
      onOperationFeedback: (status, message) => events.add((status, message)),
    );
    model.toggle('n1');
    final confirmation = model.confirm(model.selected);
    await Future<void>.delayed(Duration.zero);

    model.dispose();
    repository.confirmGate!.complete();
    await confirmation;

    expect(events, isEmpty);
    repository.dispose();
  });
}

BuildContext? _feedbackContext;

class _FeedbackRepository extends DemoNotebookRepository {
  bool failConfirm = false;
  bool failSave = false;
  bool refreshErrorAfterConfirm = false;
  Completer<void>? confirmGate;
  var saves = 0;
  String? _refreshError;

  @override
  String? get refreshError => _refreshError;

  @override
  Future<void> confirm(Set<String> ids) async {
    await confirmGate?.future;
    if (failConfirm) throw StateError('模拟确认失败');
    await super.confirm(ids);
    if (refreshErrorAfterConfirm) _refreshError = '模拟重新读取失败';
  }

  @override
  Future<void> save(
    ScheduleDraft draft, {
    String? id,
    int? expectedRevision,
    String? operationKey,
  }) async {
    if (failSave) {
      saves++;
      throw StateError('模拟日程保存失败');
    }
    await super.save(
      draft,
      id: id,
      expectedRevision: expectedRevision,
      operationKey: operationKey,
    );
  }
}
