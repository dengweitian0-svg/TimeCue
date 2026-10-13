import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/timecue_app.dart';
import 'package:timecue_desktop/app/windows_notification_bridge.dart';
import 'package:timecue_desktop/contracts/notification_delivery.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_overlay.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/schedule_editor/schedule_editor.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';
import 'package:timecue_desktop/features/room/desk_notebook_hotspot.dart';
import 'package:timecue_desktop/features/room/room_geometry.dart';

void main() {
  testWidgets('只有星期四有安排时，同一行日期数字仍水平对齐', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = _ThursdayMarkedRepository();
    final model = NotebookViewModel(repo);
    for (final month in [10, 11]) {
      model.month = DateTime.utc(2026, month);
      final selected = month == 10 ? 8 : 5;
      await model.selectDate(DateTime.utc(2026, month, selected));
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Align(
              alignment: Alignment.topLeft,
              child: SizedBox(
                width: 600,
                child: MonthGrid(model: model, onSelect: (_) {}),
              ),
            ),
          ),
        ),
      );
      final thursdays = month == 10 ? [8, 15, 22, 29] : [5, 12, 19, 26];
      for (final day in thursdays) {
        final markedY = tester.getCenter(find.text('$day')).dy;
        final unmarkedY = tester.getCenter(find.text('${day + 1}')).dy;
        expect(
          markedY,
          closeTo(unmarkedY, .01),
          reason: '$month 月 $day 日与下一日的数字应对齐',
        );
      }
    }
    await tester.pumpWidget(const SizedBox());
    model.dispose();
    repo.dispose();
  });
  testWidgets('日历底部显示选中日期数量和无日程状态，不再显示固定图例', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    final model = NotebookViewModel(repo);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ListenableBuilder(
            listenable: model,
            builder: (_, _) => CalendarSpread(
              model: model,
              onNavigate: (action) async => action(),
              onManage: () {},
              editor: null,
            ),
          ),
        ),
      ),
    );
    expect(find.text('当日共 5 个日程'), findsOneWidget);
    expect(find.textContaining('日历标记'), findsNothing);
    await repo.confirm({'n1'});
    await tester.pump();
    expect(find.text('当日共 5 个日程'), findsOneWidget);
    await model.selectDate(DateTime.utc(2026, 10, 1));
    await tester.pump();
    expect(find.text('无日程'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    model.dispose();
    repo.dispose();
  });
  testWidgets('缩窄窗口后本子露出的部分仍可点击，完全裁掉才隐藏', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(1280, 800);
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    await tester.pumpWidget(TimeCueApp(repository: repo));
    for (final width in [1280.0, 1100.0, 960.0]) {
      tester.view.physicalSize = Size(width, 800);
      await tester.pumpAndSettle();
      final geometry = RoomGeometry(Size(width, 800));
      final point = geometry.project(const Offset(275 / 1672, 542 / 941));
      expect(find.byType(DeskNotebookHotspot), findsOneWidget);
      await tester.tapAt(point);
      await tester.pumpAndSettle();
      expect(find.text('今日安排'), findsOneWidget);
      await tester.tap(find.byTooltip('合上日程本'));
      await tester.pumpAndSettle();
    }
    tester.view.physicalSize = const Size(500, 800);
    await tester.pumpAndSettle();
    expect(find.byType(DeskNotebookHotspot), findsNothing);
    expect(find.text('日程本  6'), findsOneWidget);
    tester.view.physicalSize = const Size(1440, 810);
    await tester.pumpAndSettle();
    expect(find.byType(DeskNotebookHotspot), findsOneWidget);
    await tester.tap(find.byType(DeskNotebookHotspot));
    await tester.pumpAndSettle();
    expect(find.text('今日安排'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('添加草稿通过 Escape 舍弃守卫，保存后刷新示例清单', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    await tester.pumpWidget(TimeCueApp(repository: repo));
    await tester.tap(find.text('添加日程'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, '新安排');
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.text('舍弃未保存的修改？'), findsOneWidget);
    await tester.tap(find.text('继续编辑'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byKey(const ValueKey('add-single-time')));
    await tester.tap(find.byKey(const ValueKey('add-single-time')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('保存日程'));
    await tester.tap(find.text('保存日程'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleCreateDialog), findsNothing);
    expect(repo.today.any((e) => e.name == '新安排'), isTrue);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('连接未就绪时添加入口清晰可见并说明原因，恢复后打开创建弹窗', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = _WriteGateRepository()..setState(loading: true);
    await tester.pumpWidget(TimeCueApp(repository: repo));

    final addButton = find.widgetWithText(FilledButton, '添加日程');
    expect(addButton, findsOneWidget);
    expect(tester.widget<FilledButton>(addButton).onPressed, isNull);
    expect(find.byTooltip('正在连接本地服务，暂时不能添加日程'), findsOneWidget);
    final disabledStyle = tester.widget<FilledButton>(addButton).style!;
    expect(
      disabledStyle.backgroundColor?.resolve({WidgetState.disabled}),
      const Color.fromARGB(225, 238, 222, 190),
    );

    repo.setState(loading: false, error: '连接失败');
    await tester.pump();
    expect(tester.widget<FilledButton>(addButton).onPressed, isNull);
    expect(find.byTooltip('本地服务连接失败，请重试后添加日程'), findsOneWidget);

    repo.setState(loading: false, hasSnapshot: true, error: null);
    await tester.pump();
    final enabledButton = tester.widget<FilledButton>(addButton);
    expect(enabledButton.onPressed, isNotNull);
    expect(
      enabledButton.style!.backgroundColor?.resolve({}),
      const Color.fromARGB(128, 73, 61, 48),
    );
    await tester.tap(addButton);
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleCreateDialog), findsOneWidget);

    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('系统通知错误栏出现时仍可点击房间添加入口', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    final bridge = WindowsNotificationBridge(
      gateway: _ErrorNotificationGateway(),
      platform: _FailingNotificationPlatform(),
    );
    await tester.pumpWidget(
      TimeCueApp(repository: repo, notifications: bridge),
    );
    await tester.pump();
    expect(find.text('重试连接'), findsOneWidget);

    final addButton = find.widgetWithText(FilledButton, '添加日程');
    final addRect = tester.getRect(addButton);
    final errorRect = tester.getRect(find.text('重试连接'));
    expect(addRect.overlaps(errorRect), isFalse);
    await tester.tap(addButton);
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleCreateDialog), findsOneWidget);

    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('房间到双页、空白右页、右页编辑和 Escape 合上', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    await tester.pumpWidget(TimeCueApp(repository: repo));
    await tester.tap(find.text('日程本  6'));
    await tester.pumpAndSettle();
    expect(find.text('今日安排'), findsOneWidget);
    await tester.tap(find.text('日历'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('calendar-blank')), findsOneWidget);
    await tester.tap(find.text('7').first);
    await tester.pumpAndSettle();
    await tester.tap(find.text('喝水，伸个懒腰'));
    await tester.pumpAndSettle();
    expect(find.byType(ScheduleEditor), findsOneWidget);
    expect(find.text('2026 年 10 月'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.byType(NotebookOverlay), findsNothing);
    await tester.pumpWidget(const SizedBox());
    repo.dispose();
  });
  testWidgets('小窗口和高文本缩放支持横向双页，减少动效立即到位', (tester) async {
    tester.view.physicalSize = const Size(640, 480);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repo = DemoNotebookRepository();
    final vm = NotebookViewModel(repo);
    await tester.pumpWidget(
      MaterialApp(
        theme: NotebookTheme.data,
        home: MediaQuery(
          data: const MediaQueryData(
            size: Size(640, 480),
            disableAnimations: true,
            textScaler: TextScaler.linear(1.6),
          ),
          child: Scaffold(
            body: NotebookOverlay(
              model: vm,
              origin: const Offset(500, 40),
              onClosed: () {},
            ),
          ),
        ),
      ),
    );
    await tester.pump();
    expect(find.text('今日安排'), findsOneWidget);
    expect(tester.takeException(), isNull);
    final horizontal = find.byWidgetPredicate(
      (w) => w is SingleChildScrollView && w.scrollDirection == Axis.horizontal,
    );
    await tester.drag(horizontal, const Offset(-600, 0));
    await tester.pumpAndSettle();
    expect(find.text('待确认'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    vm.dispose();
    repo.dispose();
  });
}

class _ThursdayMarkedRepository extends DemoNotebookRepository {
  @override
  bool markedOn(DateTime date) => date.weekday == DateTime.thursday;
}

class _WriteGateRepository extends DemoNotebookRepository {
  bool _loading = false;
  bool _hasSnapshot = false;
  String? _error;

  @override
  bool get serverPagination => true;
  @override
  bool get loading => _loading;
  @override
  bool get hasSnapshot => _hasSnapshot;
  @override
  String? get refreshError => _error;

  void setState({required bool loading, bool? hasSnapshot, String? error}) {
    _loading = loading;
    if (hasSnapshot != null) _hasSnapshot = hasSnapshot;
    _error = error;
    notifyListeners();
  }

  @override
  Future<void> refresh({DateTime? month, DateTime? date}) async {}
}

class _ErrorNotificationGateway implements NotificationDeliveryGateway {
  @override
  Future<DeliveryRuntime> deliveryRuntime() async =>
      const DeliveryRuntime('test-instance');
  @override
  Future<List<DeliveryClaim>> claimNotifications() async => const [];
  @override
  Future<DeliveryStart> startNotification(String attemptId) async =>
      throw UnimplementedError();
  @override
  Future<void> reportNotification(
    String attemptId,
    DeliveryOutcome outcome,
  ) async {}
  @override
  Future<void> reportExecutorExited(
    String instanceId,
    List<String> attemptIds, {
    required Duration timeout,
  }) async {}
}

class _FailingNotificationPlatform implements NotificationPlatform {
  @override
  Future<void> initialize() async => throw StateError('初始化失败');
  @override
  Future<DeliveryOutcome?> show({
    required int id,
    required DeliveryStart start,
    required String payload,
  }) async => null;
  @override
  void dispose() {}
}
