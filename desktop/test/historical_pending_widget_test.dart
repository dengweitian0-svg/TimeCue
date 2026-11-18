import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_overlay.dart';
import 'package:timecue_desktop/app/timecue_app.dart';

void main() {
  testWidgets('后台恢复与次日重开立即刷新，清理旧范围选择并保持只读', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    var now = DateTime.utc(2026, 10, 7, 23, 59);
    final repo = _RefreshRepository(() => now);
    await tester.pumpWidget(TimeCueApp(repository: repo));
    await tester.tap(find.text('日程本  9'));
    await tester.pumpAndSettle();
    final vm = tester
        .widget<NotebookOverlay>(find.byType(NotebookOverlay))
        .model;
    vm.changeTab(NotebookTab.history);
    vm.selectPage();
    vm.turnPage(1);
    expect(vm.page, 1);
    final before = repo.reads;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    now = now.add(const Duration(minutes: 1));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    expect(repo.reads, greaterThan(before));
    expect(vm.page, 0);
    expect(vm.selected, isEmpty);
    expect(repo.pendingTotalFor(PendingScope.today), 0);
    expect(repo.pendingTotalFor(PendingScope.history), 9);
    await tester.tap(find.byTooltip('合上日程本'));
    await tester.pumpAndSettle();
    now = now.add(const Duration(days: 1));
    await tester.tap(find.text('日程本  9'));
    await tester.pumpAndSettle();
    expect(vm.tab, NotebookTab.today);
    expect(vm.page, 0);
    expect(repo.confirmations, 0);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('四标签顺序、今日范围、历史完整日期及真实投递状态', (tester) async {
    final repo = _DisplayRepository();
    final vm = NotebookViewModel(repo);
    addTearDown(() {
      vm.dispose();
      repo.dispose();
    });
    await repo.refresh();
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ListenableBuilder(
            listenable: vm,
            builder: (_, _) => NotebookFrame(
              model: vm,
              onClose: () {},
              onManage: () {},
              onTab: vm.changeTab,
              spread: vm.tab == NotebookTab.history
                  ? HistorySpread(model: vm)
                  : TodaySpread(model: vm),
            ),
          ),
        ),
      ),
    );
    expect(
      tester.getTopLeft(find.text('今天')).dx,
      lessThan(tester.getTopLeft(find.text('历史待确认')).dx),
    );
    expect(
      tester.getTopLeft(find.text('历史待确认')).dx,
      lessThan(tester.getTopLeft(find.text('日历')).dx),
    );
    expect(
      tester.getTopLeft(find.text('日历')).dx,
      lessThan(tester.getTopLeft(find.text('AI 对话')).dx),
    );
    expect(find.text('提醒 1'), findsNothing);
    expect(find.text('提醒 1001'), findsNWidgets(2));
    await tester.tap(find.text('历史待确认'));
    await tester.pump();
    expect(find.text('2026 年 10 月 7 日'), findsOneWidget);
    expect(find.text('提醒 1001'), findsNothing);
    expect(
      find.textContaining('2026 年 10 月 7 日 · 09:00 · 已提醒'),
      findsOneWidget,
    );
    expect(find.textContaining('投递失败'), findsOneWidget);
    expect(find.textContaining('待投递'), findsNWidgets(2));
    await tester.tap(find.text('提醒 1'));
    await tester.pumpAndSettle();
    expect(find.textContaining('2026 年 10 月 7 日 09:00'), findsOneWidget);
    expect(repo.confirmed, isEmpty);
    await tester.pumpWidget(const SizedBox());
  });
}

class _RefreshRepository extends DemoNotebookRepository {
  _RefreshRepository(DateTime Function() clock) : super(clock: clock);
  int reads = 0, confirmations = 0;
  @override
  Future<void> refresh({DateTime? month, DateTime? date}) async {
    ++reads;
    await super.refresh(month: month, date: date);
  }

  @override
  Future<void> confirm(Set<String> ids) async {
    ++confirmations;
    await super.confirm(ids);
  }
}

class _DisplayRepository extends DemoNotebookRepository {
  _DisplayRepository() : super(now: DateTime.utc(2026, 10, 8, 23, 59));
  final confirmed = <String>{};
  @override
  int get pendingPageSize => 50;
  @override
  List<TriggerSummary> get pending => [
    for (var i = 0; i < 4; i++)
      TriggerSummary(
        id: '${i + 1}',
        scheduleId: 's1',
        name: '提醒 ${i + 1}',
        description: '完整内容',
        plannedAt: DateTime.utc(2026, 10, 7, 9),
        delivery: ['sent', 'failed', 'pending', 'loaded'][i],
      ),
    for (var i = 0; i < 4; i++)
      TriggerSummary(
        id: '${i + 1001}',
        scheduleId: 's1',
        name: '提醒 ${i + 1001}',
        description: '完整内容',
        plannedAt: DateTime.utc(2026, 10, 8, 9),
      ),
  ];
  @override
  List<TriggerSummary> get today =>
      pending.where((entry) => sameDay(entry.plannedAt, demoNow)).toList();
  @override
  Future<void> confirm(Set<String> ids) async => confirmed.addAll(ids);
}
