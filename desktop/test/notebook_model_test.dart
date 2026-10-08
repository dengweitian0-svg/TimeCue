import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/room/room_geometry.dart';
import 'package:timecue_desktop/features/room/room_backdrop.dart';
import 'package:flutter/material.dart';

void main() {
  test('确认请求失败保留原快照与选择，允许重试', () async {
    final repo = _FailingRepository();
    final vm = NotebookViewModel(repo);
    addTearDown(() {
      vm.dispose();
      repo.dispose();
    });
    vm.toggle('n1');
    await vm.confirm(vm.selected);
    expect(vm.selected, {'n1'});
    expect(vm.error, isNotNull);
    expect(repo.pending.any((e) => e.id == 'n1'), isTrue);
    expect(vm.busy, isFalse);
  });
  test('失败投递也可独立确认，完成不改写投递结果和其他触发', () async {
    final repo = DemoNotebookRepository();
    addTearDown(repo.dispose);
    await repo.confirm({'n1'});
    expect(repo.today.singleWhere((e) => e.id == 'n1').delivery, 'failed');
    expect(
      repo.today.singleWhere((e) => e.id == 'n1').acknowledgedAt,
      isNotNull,
    );
    expect(repo.pending.any((e) => e.id == 'history1'), isTrue);
    await expectLater(repo.confirm({'n2'}), throwsStateError);
  });
  test('全选限本页，跨页保留选择且批量只确认明确 ID', () async {
    final repo = DemoNotebookRepository();
    final vm = NotebookViewModel(repo);
    addTearDown(() {
      vm.dispose();
      repo.dispose();
    });
    vm.selectPage();
    final first = vm.selected;
    expect(first.length, 3);
    vm.turnPage(1);
    vm.toggle(vm.pendingPage.first.id);
    expect(vm.selected.containsAll(first), isTrue);
    expect(vm.selected.length, 4);
    await vm.confirm(vm.selected);
    expect(repo.pending.length, 2);
    expect(vm.selected, isEmpty);
  });
  test('日历初始空白，会话保留日期，正常打开返回今天', () {
    final repo = DemoNotebookRepository();
    final vm = NotebookViewModel(repo);
    addTearDown(() {
      vm.dispose();
      repo.dispose();
    });
    vm.changeTab(NotebookTab.calendar);
    expect(vm.right, isA<BlankPage>());
    vm.selectDate(DateTime.utc(2026, 10, 7));
    vm.changeTab(NotebookTab.today);
    vm.changeTab(NotebookTab.calendar);
    expect(vm.selectedDate, DateTime.utc(2026, 10, 7));
    vm.open();
    expect(vm.tab, NotebookTab.today);
  });
  test('未来规则修改和删除保留历史原时间及确认资格', () async {
    final repo = DemoNotebookRepository();
    addTearDown(repo.dispose);
    final old = repo.pending.singleWhere((e) => e.id == 'n1');
    await repo.save(
      ScheduleDraft(
        name: '休息',
        description: '',
        kind: ScheduleKind.daily,
        time: DateTime.utc(2026, 10, 7, 17),
      ),
      id: 's1',
    );
    expect(
      repo.pending.singleWhere((e) => e.id == 'n1').plannedAt,
      old.plannedAt,
    );
    expect(
      repo
          .entriesOn(repo.demoNow)
          .any((e) => e.scheduleId == 's1' && e.plannedAt.hour == 17),
      isTrue,
    );
    await repo.delete('s1');
    expect(repo.schedule('s1').deleted, isTrue);
    await repo.confirm({'n1'});
    expect(repo.schedule('s1').deleted, isTrue);
  });
  test('cover 裁切锚点与时段边界', () {
    final geometry = RoomGeometry(const Size(1280, 800));
    expect(geometry.project(const Offset(.5, .5)), const Offset(640, 400));
    expect(RoomGeometry(const Size(500, 800)).notebookVisible, isFalse);
    final partial = RoomGeometry(const Size(960, 800));
    expect(partial.notebook.dx, lessThan(0));
    expect(partial.notebookVisible, isTrue);
    expect(partial.visibleNotebookBounds.left, greaterThanOrEqualTo(0));
    expect(
      partial.visibleNotebookPath.contains(
        partial.project(const Offset(275 / 1672, 542 / 941)),
      ),
      isTrue,
    );
    expect(roomPeriod(DateTime.utc(2026, 10, 7, 6)), 'morning');
    expect(roomPeriod(DateTime.utc(2026, 10, 7, 12)), 'afternoon');
    expect(roomPeriod(DateTime.utc(2026, 10, 7, 18)), 'night');
  });
}

class _FailingRepository extends DemoNotebookRepository {
  @override
  Future<void> confirm(Set<String> ids) async => throw StateError('示例网络故障');
}
