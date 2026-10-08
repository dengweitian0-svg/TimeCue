import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/schedule_management_dialog.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';

void main() {
  testWidgets('暂停日程没有未来投影，仍可从管理列表进入恢复表单', (tester) async {
    final repo = DemoNotebookRepository();
    addTearDown(repo.dispose);
    await repo.save(
      ScheduleDraft(
        name: '暂停测试',
        description: '',
        kind: ScheduleKind.daily,
        time: repo.demoNow,
        enabled: false,
      ),
      id: 'paused-rule',
    );
    expect(
      repo
          .entriesOn(repo.demoNow)
          .where((entry) => entry.scheduleId == 'paused-rule'),
      isEmpty,
    );
    final model = NotebookViewModel(repo);
    addTearDown(model.dispose);
    String? selected;
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () async {
                selected = await showDialog<String>(
                  context: context,
                  builder: (_) => ScheduleManagementDialog(repository: repo),
                );
                if (selected != null) await model.edit(selected!);
              },
              child: const Text('管理日程'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('管理日程'));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.text('暂停测试'),
      100,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pumpAndSettle();
    final tile = find.ancestor(
      of: find.text('暂停测试'),
      matching: find.byType(ListTile),
    );
    await tester.ensureVisible(tile);
    await tester.pumpAndSettle();
    expect(find.text('暂停测试'), findsOneWidget);
    await tester.tap(tile);
    await tester.pumpAndSettle();
    expect(selected, 'paused-rule');
    expect(model.right, isA<EditingPage>());
    expect(repo.schedule(selected!).draft.enabled, isFalse);
  });
}
