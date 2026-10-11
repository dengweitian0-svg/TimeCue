import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_overlay.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/notebook/notification_reader_dialog.dart';
import 'package:timecue_desktop/features/notebook/notification_details.dart';
import 'package:timecue_desktop/features/notebook/notification_view_model.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';
import 'notification_view_model_test.dart' as fixture;

void sizeWindow(WidgetTester tester, {Size size = const Size(1280, 800)}) {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  testWidgets('房间与本子复用同一恢复摘要，入口读取历史成员且保持原待确认总数', (tester) async {
    sizeWindow(tester);
    final repo = _RecoveryRepository();
    final gateway = fixture.QueryFixture();
    gateway.onMembers = (_, _, _) =>
        fixture.memberPage([], recovery: repo.recovery);
    final model = NotificationViewModel(gateway);
    await tester.pumpWidget(
      TimeCueApp(repository: repo, notificationViews: model),
    );
    expect(find.text('日程本  6'), findsOneWidget);
    expect(find.textContaining('遗漏提醒'), findsNothing);
    repo.completeRound(0);
    await tester.pump();
    expect(find.textContaining('遗漏提醒'), findsNothing);
    repo.completeRound(3);
    await tester.pump();
    await tester.tap(find.text('有 3 条遗漏提醒'));
    await tester.pumpAndSettle();
    expect(gateway.reads.last.$2, isNull);
    expect(find.text('本轮恢复记录'), findsOneWidget);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.textContaining('遗漏提醒'), findsNothing);
    repo.republish();
    await tester.pump();
    expect(find.textContaining('遗漏提醒'), findsNothing);
    await tester.tap(find.text('日程本  6'));
    await tester.pumpAndSettle();
    await tester.tap(find.textContaining('历史整理完成'));
    await tester.pumpAndSettle();
    expect(gateway.reads.length, 2);
    expect(gateway.reads.every((read) => read.$2 == null), isTrue);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    expect(find.textContaining('遗漏提醒'), findsNothing);
    repo.nextRound();
    await tester.pump();
    expect(find.text('有 2 条遗漏提醒'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('已确认的失败详情保留真实投递状态，长内容可滚动', (tester) async {
    sizeWindow(tester, size: const Size(480, 400));
    final entry = TriggerSummary(
      id: '1',
      scheduleId: '1',
      name: '历史提醒',
      notificationId: '1',
      plannedAt: DateTime.utc(2026, 10, 8, 9),
      delivery: 'failed',
      acknowledgedAt: DateTime.utc(2026, 10, 8, 10),
      description: '完整说明\n' * 80,
    );
    await tester.pumpWidget(
      MaterialApp(home: NotificationDetailsDialog(entry: entry)),
    );
    expect(find.textContaining('投递失败'), findsOneWidget);
    expect(find.textContaining('已完成'), findsOneWidget);
    expect(find.byType(SingleChildScrollView), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets('缓冲点击在宿主就绪后打开，重复及新点击复用同一弹窗', (tester) async {
    sizeWindow(tester);
    final gateway = fixture.QueryFixture();
    final model = NotificationViewModel(gateway);
    final repo = DemoNotebookRepository();
    await model.activate(fixture.activation());
    await tester.pumpWidget(
      TimeCueApp(repository: repo, notificationViews: model),
    );
    await tester.pumpAndSettle();
    expect(find.byType(NotificationReaderDialog), findsOneWidget);
    expect(find.text('成员 1'), findsOneWidget);
    await model.activate(fixture.activation());
    gateway.onMembers = (_, _, _) =>
        fixture.memberPage([fixture.member('2'), fixture.member('3')]);
    await model.activate(
      fixture.activation(task: 'task-2', attempt: 'attempt-2'),
    );
    await tester.pumpAndSettle();
    expect(find.byType(NotificationReaderDialog), findsOneWidget);
    expect(find.text('本次通知成员'), findsOneWidget);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(model.active, isFalse);
    await model.activate(fixture.activation());
    await tester.pumpAndSettle();
    expect(find.byType(NotificationReaderDialog), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('点击通知不丢弃创建草稿，不重置本子选择及标签', (tester) async {
    sizeWindow(tester);
    final repo = DemoNotebookRepository();
    final model = NotificationViewModel(fixture.QueryFixture());
    await tester.pumpWidget(
      TimeCueApp(repository: repo, notificationViews: model),
    );
    await tester.tap(find.text('日程本  6'));
    await tester.pumpAndSettle();
    final notebook = tester
        .widget<NotebookOverlay>(find.byType(NotebookOverlay))
        .model;
    notebook.selectPage();
    expect(notebook.selected, isNotEmpty);
    notebook.changeTab(NotebookTab.calendar);
    final selected = Set<String>.of(notebook.selected);
    // 通过房间的创建入口打开独立草稿，再收到系统点击。
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('添加日程'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, '未保存的草稿');
    await model.activate(fixture.activation());
    await tester.pumpAndSettle();
    expect(find.byType(NotificationReaderDialog), findsOneWidget);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleCreateDialog), findsOneWidget);
    expect(
      tester.widget<TextField>(find.byType(TextField).first).controller!.text,
      '未保存的草稿',
    );
    expect(notebook.tab, NotebookTab.calendar);
    expect(notebook.selected, selected);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('恢复空页显示未知总数，失败与完成分别显示且不伪装投递成功', (tester) async {
    sizeWindow(tester);
    final gateway = fixture.QueryFixture();
    var progress = fixture.recovery();
    gateway.onMembers = (_, _, _) => fixture.memberPage([], recovery: progress);
    final model = NotificationViewModel(gateway);
    await model.openRecovery(progress);
    await tester.pumpWidget(
      MaterialApp(
        home: NotificationReaderDialog(model: model, onHistory: () {}),
      ),
    );
    expect(find.textContaining('遗漏总数待整理完成'), findsOneWidget);
    expect(find.textContaining('历史整理可能仍在进行'), findsOneWidget);
    expect(find.textContaining('%'), findsNothing);
    progress = fixture.recovery(state: 'failed');
    await model.refresh();
    await tester.pump();
    expect(find.textContaining('历史整理暂时失败'), findsOneWidget);
    progress = fixture.recovery(state: 'completed', total: 0);
    await model.refresh();
    await tester.pump();
    expect(find.textContaining('历史整理完成'), findsOneWidget);
    expect(find.textContaining('完整遗漏 0 条'), findsOneWidget);
    expect(find.textContaining('已提醒'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    model.dispose();
  });
  testWidgets('后台停止成员轮询，恢复前台立即重读，详情保持只读', (tester) async {
    sizeWindow(tester);
    final gateway = fixture.QueryFixture();
    gateway.onMembers = (_, _, _) =>
        fixture.memberPage([fixture.member('1'), fixture.member('2')]);
    final model = NotificationViewModel(gateway);
    await model.activate(fixture.activation(type: 'recovery'));
    await tester.pumpWidget(
      MaterialApp(
        home: NotificationReaderDialog(model: model, onHistory: () {}),
      ),
    );
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 10));
    expect(gateway.reads.length, 1);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    expect(gateway.reads.length, 2);
    await tester.tap(find.text('成员 1'));
    await tester.pump();
    expect(find.textContaining('确认状态 · 等待你的确认'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    model.dispose();
  });
  testWidgets('过期通知只经用户入口打开历史待确认', (tester) async {
    sizeWindow(tester);
    final repo = DemoNotebookRepository();
    final gateway = fixture.QueryFixture()..instance = 'go-2';
    final model = NotificationViewModel(gateway);
    await tester.pumpWidget(
      TimeCueApp(repository: repo, notificationViews: model),
    );
    await model.activate(fixture.activation());
    await tester.pumpAndSettle();
    expect(find.textContaining('已结束的运行'), findsOneWidget);
    expect(find.byType(NotebookOverlay), findsNothing);
    await tester.tap(find.text('打开历史待确认'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<NotebookOverlay>(find.byType(NotebookOverlay)).model.tab,
      NotebookTab.history,
    );
    expect(gateway.reads, isEmpty);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
}

class _RecoveryRepository extends DemoNotebookRepository {
  RecoveryProgress _progress = fixture.recovery();
  @override
  RecoveryProgress get recovery => _progress;
  void republish() => notifyListeners();
  void completeRound(int total) {
    _progress = fixture.recovery(state: 'completed', total: total);
    notifyListeners();
  }

  void nextRound() {
    _progress = fixture.recovery(state: 'completed', total: 2, task: 'task-2');
    notifyListeners();
  }
}
