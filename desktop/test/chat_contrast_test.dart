import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

void main() {
  test('纸面正文与主操作文字达到 WCAG 普通文字对比度', () {
    expect(
      _contrast(NotebookTheme.paper, NotebookTheme.ink),
      greaterThanOrEqualTo(4.5),
    );
    expect(
      _contrast(NotebookTheme.paper, NotebookTheme.action),
      greaterThanOrEqualTo(4.5),
    );
  });

  testWidgets('实际按钮和输入标签主题使用可读的前景色', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: Column(
            children: [
              FilledButton(onPressed: () {}, child: const Text('执行')),
              TextButton(onPressed: () {}, child: const Text('更多')),
              OutlinedButton(onPressed: () {}, child: const Text('取消')),
              const TextField(decoration: InputDecoration(labelText: '名称')),
            ],
          ),
        ),
      ),
    );

    final filledFinder = find.byType(FilledButton);
    final filled = tester.widget<FilledButton>(filledFinder);
    final filledTheme = FilledButtonTheme.of(
      tester.element(filledFinder),
    ).style!;
    final filledStyle = filled.style?.merge(filledTheme) ?? filledTheme;
    expect(
      _contrast(
        filledStyle.backgroundColor!.resolve({})!,
        filledStyle.foregroundColor!.resolve({})!,
      ),
      greaterThanOrEqualTo(4.5),
    );

    final textFinder = find.byType(TextButton);
    final textButton = tester.widget<TextButton>(textFinder);
    final textTheme = TextButtonTheme.of(tester.element(textFinder)).style!;
    final textStyle = textButton.style?.merge(textTheme) ?? textTheme;
    expect(
      _contrast(NotebookTheme.paper, textStyle.foregroundColor!.resolve({})!),
      greaterThanOrEqualTo(4.5),
    );

    final outlineFinder = find.byType(OutlinedButton);
    final outlined = tester.widget<OutlinedButton>(outlineFinder);
    final outlineTheme = OutlinedButtonTheme.of(
      tester.element(outlineFinder),
    ).style!;
    final outlineStyle = outlined.style?.merge(outlineTheme) ?? outlineTheme;
    expect(
      _contrast(
        NotebookTheme.paper,
        outlineStyle.foregroundColor!.resolve({})!,
      ),
      greaterThanOrEqualTo(4.5),
    );

    final labelColor = Theme.of(
      tester.element(find.byType(TextField)),
    ).inputDecorationTheme.labelStyle!.color!;
    expect(
      _contrast(NotebookTheme.paper, labelColor),
      greaterThanOrEqualTo(4.5),
    );
  });

  testWidgets('书封管理、恢复入口和页码小字在纸色标签上显示', (tester) async {
    tester.view.physicalSize = const Size(1200, 900);
    tester.view.devicePixelRatio = 1;
    final repository = _RecoveryRepository();
    final model = NotebookViewModel(repository, onRecovery: () {});
    addTearDown(() {
      model.dispose();
      repository.dispose();
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: Scaffold(
          body: Center(
            child: SizedBox(
              width: 1100,
              height: 800,
              child: NotebookFrame(
                model: model,
                onClose: () {},
                onManage: () {},
                onTab: model.changeTab,
                spread: const SizedBox(),
              ),
            ),
          ),
        ),
      ),
    );

    for (final label in ['管理日程', '历史整理完成，已整理 3 条']) {
      final finder = find.widgetWithText(TextButton, label);
      expect(finder, findsOneWidget);
      final button = tester.widget<TextButton>(finder);
      final style = button.style!;
      expect(
        style.backgroundColor!.resolve({}),
        NotebookTheme.coverLabelBackground,
      );
      expect(
        style.foregroundColor!.resolve({}),
        NotebookTheme.coverLabelForeground,
      );
      expect(
        _contrast(
          style.backgroundColor!.resolve({})!,
          style.foregroundColor!.resolve({})!,
        ),
        greaterThanOrEqualTo(4.5),
      );
    }

    expect(find.byKey(const ValueKey('cover-page-number')), findsOneWidget);
    expect(
      _contrast(
        NotebookTheme.coverLabelBackground,
        NotebookTheme.coverLabelForeground,
      ),
      greaterThanOrEqualTo(4.5),
    );
    expect(tester.takeException(), isNull);
  });
}

double _contrast(Color first, Color second) {
  final a = first.computeLuminance();
  final b = second.computeLuminance();
  final lighter = a > b ? a : b;
  final darker = a > b ? b : a;
  return (lighter + 0.05) / (darker + 0.05);
}

class _RecoveryRepository extends DemoNotebookRepository {
  @override
  RecoveryProgress? get recovery => RecoveryProgress(
    recoveryId: 'recovery-test',
    state: 'completed',
    through: DateTime.utc(2026, 10, 18),
    materializedCount: 3,
    missedTotal: 0,
    displayableCount: 3,
    deliveryTaskId: 'task-test',
  );
}
