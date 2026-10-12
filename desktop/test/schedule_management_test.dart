import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/schedule_management_dialog.dart';
import 'package:timecue_desktop/features/notebook/notebook_overlay.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';

void main() {
  for (final fromRoom in [true, false]) {
    testWidgets('${fromRoom ? '房间' : '今日日程本'}可查看无未来投影的关闭日程并重新开启', (
      tester,
    ) async {
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final repo = DemoNotebookRepository();
      final future = repo.demoNow.add(const Duration(days: 1));
      const slots = [ScheduleTimeSlot(ScheduleKind.daily, 18, 45)];
      await repo.save(
        ScheduleDraft(
          name: '关闭的日程',
          description: '关闭后仍需查看的说明',
          kind: ScheduleKind.daily,
          time: repo.demoNow,
          enabled: false,
          slots: slots,
        ),
        id: 'closed-rule',
      );
      expect(
        repo.entriesOn(future).where((e) => e.scheduleId == 'closed-rule'),
        isEmpty,
      );
      await tester.pumpWidget(TimeCueApp(repository: repo));
      if (!fromRoom) {
        await tester.tap(find.text('日程本  6'));
        await tester.pumpAndSettle();
        expect(find.text('今日安排'), findsOneWidget);
      }
      final management = fromRoom
          ? find.text('管理日程')
          : find.descendant(
              of: find.byType(NotebookOverlay),
              matching: find.text('管理日程'),
            );
      await tester.tap(management);
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.text('关闭的日程'),
        120,
        scrollable: find.descendant(
          of: find.byType(ScheduleManagementDialog),
          matching: find.byType(Scrollable),
        ),
      );
      final tile = find.ancestor(
        of: find.text('关闭的日程'),
        matching: find.byType(ListTile),
      );
      final details = tester.widget<Text>(
        find.descendant(of: tile, matching: find.byType(Text)).last,
      );
      expect(details.data, contains('周期已暂停'));
      expect(details.data, contains('关闭后仍需查看的说明'));
      expect(details.data, contains('18:45'));
      await tester.tap(tile);
      await tester.pumpAndSettle();
      expect(find.byType(ScheduleEditDialog), findsOneWidget);
      expect(
        find.byType(NotebookOverlay),
        fromRoom ? findsNothing : findsOneWidget,
      );
      if (!fromRoom) expect(find.text('今日安排'), findsOneWidget);
      expect(find.byType(ScheduleEditor), findsOneWidget);
      expect(
        tester.widget<TextField>(find.byType(TextField).first).controller!.text,
        '关闭的日程',
      );
      await tester.ensureVisible(find.text('重新开启'));
      await tester.tap(find.text('重新开启'));
      await tester.pumpAndSettle();
      final restored = repo.schedule('closed-rule').draft;
      expect(restored.enabled, isTrue);
      expect(restored.name, '关闭的日程');
      expect(restored.description, '关闭后仍需查看的说明');
      expect(restored.slots, slots);
      expect(
        repo.entriesOn(future).where((e) => e.scheduleId == 'closed-rule'),
        hasLength(1),
      );
      expect(find.byType(ScheduleEditor), findsNothing);
      expect(find.byType(ScheduleManagementDialog), findsOneWidget);
      expect(find.textContaining('周期已启用'), findsWidgets);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      repo.dispose();
    });
  }

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
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () async {
                await showDialog<void>(
                  context: context,
                  builder: (_) => ScheduleManagementDialog(repository: repo),
                );
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
    expect(find.byType(ScheduleEditDialog), findsOneWidget);
    expect(find.byType(NotebookOverlay), findsNothing);
    expect(repo.schedule('paused-rule').draft.enabled, isFalse);
    await tester.enterText(find.byType(TextField).first, '未保存的新名称');
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('继续编辑'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditDialog), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    await tester.tap(find.text('舍弃修改'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditDialog), findsNothing);
    expect(find.byType(ScheduleManagementDialog), findsOneWidget);
    expect(repo.schedule('paused-rule').draft.name, '暂停测试');
    await tester.tap(find.text('暂停测试'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, '修改后的名称');
    await tester.ensureVisible(find.text('保存日程'));
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditDialog), findsNothing);
    expect(find.text('修改后的名称'), findsOneWidget);
    expect(repo.schedule('paused-rule').draft.enabled, isFalse);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('详情读取失败保留管理列表，重试成功后删除刷新列表', (tester) async {
    final repo = _DetailRepository();
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: ScheduleManagementDialog(repository: repo)),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('读一会儿书'));
    await tester.pumpAndSettle();
    expect(find.textContaining('详情读取失败'), findsOneWidget);
    expect(find.byType(ScheduleEditDialog), findsNothing);
    repo.fail = false;
    await tester.tap(find.text('读一会儿书'));
    await tester.pumpAndSettle();
    expect(repo.detailRequests, 2);
    expect(find.byType(ScheduleEditDialog), findsOneWidget);
    await tester.ensureVisible(find.text('删除'));
    await tester.tap(find.text('删除'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '删除'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditDialog), findsNothing);
    expect(find.text('读一会儿书'), findsNothing);
    expect(find.byType(ScheduleManagementDialog), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
}

class _DetailRepository extends DemoNotebookRepository {
  bool fail = true;
  int detailRequests = 0;
  @override
  Future<ScheduleSummary> loadSchedule(String id) async {
    detailRequests++;
    if (fail) throw StateError('详情读取失败');
    return super.loadSchedule(id);
  }
}
