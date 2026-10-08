import 'package:flutter/material.dart';
import '../../contracts/notebook_repository.dart';
import '../notebook/notebook_overlay.dart';
import '../notebook/notebook_view_model.dart';
import '../schedule_editor/schedule_editor.dart';
import 'desk_notebook_hotspot.dart';
import 'room_backdrop.dart';
import 'room_clock.dart';
import 'room_geometry.dart';

class RoomScreen extends StatefulWidget {
  const RoomScreen({super.key, required this.repository});
  final NotebookRepository repository;
  @override
  State<RoomScreen> createState() => _RoomScreenState();
}

class _RoomScreenState extends State<RoomScreen> {
  late final notebook = NotebookViewModel(widget.repository);
  late final clock = RoomClockController();
  final deskFocus = FocusNode(debugLabel: '桌面本子入口');
  final actionFocus = FocusNode(debugLabel: '固定本子入口');
  bool bookVisible = false;
  bool fromDesk = false;
  void open(bool desk) {
    if (bookVisible) return;
    notebook.open();
    setState(() {
      fromDesk = desk;
      bookVisible = true;
    });
  }

  Future<void> create() async {
    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (_) => ScheduleCreateDialog(repository: widget.repository),
    );
  }

  @override
  void dispose() {
    notebook.dispose();
    clock.dispose();
    deskFocus.dispose();
    actionFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final geometry = RoomGeometry(constraints.biggest);
      final fixedOrigin = Offset(constraints.maxWidth - 100, 48);
      return Stack(
        fit: StackFit.expand,
        children: [
          // 秒级更新只驱动时钟；背景只有跨时段时才切换图片。
          ValueListenableBuilder(
            valueListenable: clock.scene,
            builder: (context, period, _) =>
                RepaintBoundary(child: RoomBackdrop(period: period)),
          ),
          Positioned(
            top: constraints.maxWidth < 650 ? 80 : 26,
            left: 0,
            right: 0,
            child: Center(child: RoomClock(clock: clock)),
          ),
          if (geometry.notebookVisible)
            Positioned.fromRect(
              rect: geometry.visibleNotebookBounds,
              child: DeskNotebookHotspot(
                onOpen: () => open(true),
                focusNode: deskFocus,
                outline: geometry.visibleNotebookPath.shift(
                  -geometry.visibleNotebookBounds.topLeft,
                ),
              ),
            ),
          Positioned(
            top: 24,
            right: 24,
            child: ListenableBuilder(
              listenable: notebook,
              builder: (context, _) => RoomActions(
                onOpen: () => open(false),
                pendingCount: widget.repository.pending.length,
                focusNode: actionFocus,
              ),
            ),
          ),
          Positioned(
            bottom: 24,
            right: 28,
            child: FilledButton.icon(
              onPressed: create,
              style: _roomActionStyle,
              icon: const Icon(Icons.add, size: 17),
              label: const Text('添加日程'),
            ),
          ),
          if (bookVisible)
            Positioned.fill(
              child: NotebookOverlay(
                model: notebook,
                origin: fromDesk && geometry.notebookVisible
                    ? geometry.visibleNotebookBounds.center
                    : fixedOrigin,
                onClosed: () {
                  setState(() => bookVisible = false);
                  (fromDesk && geometry.notebookVisible
                          ? deskFocus
                          : actionFocus)
                      .requestFocus();
                },
              ),
            ),
        ],
      );
    },
  );
}

// 房间的固定入口共用无边框半透明底色，文字和图标保持相同透明度。
// 使用 getter，避免热重载后顶层 final 仍保留修改前的描边和透明度。
ButtonStyle get _roomActionStyle => FilledButton.styleFrom(
  backgroundColor: const Color.fromARGB(128, 73, 61, 48),
  foregroundColor: const Color.fromARGB(159, 255, 249, 237),
  side: BorderSide.none,
  shape: const StadiumBorder(side: BorderSide.none),
  textStyle: const TextStyle(fontSize: 12),
);

class RoomActions extends StatelessWidget {
  const RoomActions({
    super.key,
    required this.onOpen,
    required this.pendingCount,
    required this.focusNode,
  });
  final VoidCallback onOpen;
  final int pendingCount;
  final FocusNode focusNode;
  @override
  Widget build(BuildContext context) => FilledButton.icon(
    focusNode: focusNode,
    onPressed: onOpen,
    style: _roomActionStyle,
    icon: const Icon(Icons.menu_book_outlined, size: 17),
    label: Text('日程本${pendingCount > 0 ? '  $pendingCount' : ''}'),
  );
}
