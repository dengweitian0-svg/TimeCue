import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../../theme/notebook_theme.dart';
import '../schedule_editor/schedule_editor.dart';
import 'notebook_view_model.dart';
import 'notebook_spreads.dart';
import 'schedule_management_dialog.dart';

enum NotebookPhase { closed, opening, open, closing }

class NotebookOverlay extends StatefulWidget {
  const NotebookOverlay({
    super.key,
    required this.model,
    required this.origin,
    required this.onClosed,
    this.historyRequest = 0,
  });
  final NotebookViewModel model;
  final Offset origin;
  final VoidCallback onClosed;
  final int historyRequest;
  @override
  State<NotebookOverlay> createState() => _NotebookOverlayState();
}

class _NotebookOverlayState extends State<NotebookOverlay>
    with SingleTickerProviderStateMixin {
  late final AnimationController controller = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1100),
    reverseDuration: const Duration(milliseconds: 850),
  );
  NotebookPhase phase = NotebookPhase.opening;
  ScheduleEditorViewModel? editor;
  bool started = false;
  @override
  void didUpdateWidget(covariant NotebookOverlay oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.historyRequest != widget.historyRequest) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) {
          navigate(() => widget.model.changeTab(NotebookTab.history));
        }
      });
    }
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (started) return;
    started = true;
    if (MediaQuery.disableAnimationsOf(context)) {
      controller.value = 1;
      phase = NotebookPhase.open;
    } else {
      controller.forward().then((_) {
        if (mounted && phase == NotebookPhase.opening) {
          setState(() => phase = NotebookPhase.open);
        }
      });
    }
  }

  Future<void> navigate(VoidCallback action) async {
    if (phase != NotebookPhase.open || editor?.busy == true) return;
    if (editor != null && !await allowDiscard(context, editor!)) return;
    if (!mounted) return;
    editor?.dispose();
    editor = null;
    action();
  }

  Future<void> close() async {
    if (phase != NotebookPhase.open || editor?.busy == true) return;
    if (editor != null && !await allowDiscard(context, editor!)) return;
    if (!mounted) return;
    setState(() => phase = NotebookPhase.closing);
    if (!MediaQuery.disableAnimationsOf(context)) await controller.reverse();
    if (mounted) {
      phase = NotebookPhase.closed;
      widget.onClosed();
    }
  }

  Future<void> manage() async {
    if (phase != NotebookPhase.open || editor?.busy == true) return;
    await showDialog<void>(
      context: context,
      builder: (_) =>
          ScheduleManagementDialog(repository: widget.model.repository),
    );
  }

  Widget? buildEditor() {
    final right = widget.model.right;
    if (right is! EditingPage) return null;
    if (editor?.original?.id != right.scheduleId) {
      editor?.dispose();
      editor = ScheduleEditorViewModel(
        widget.model.repository,
        widget.model.repository.schedule(right.scheduleId),
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextButton(
          onPressed: () => navigate(widget.model.back),
          child: const Text('‹ 返回当天安排'),
        ),
        PageHeading(
          editor!.readOnly ? '历史详情' : '写下安排',
          editor!.readOnly ? '日程已删除，历史仍保留。' : '修改未来安排，保留已经发生的提醒。',
        ),
        ScheduleEditor(
          model: editor!,
          onSaved: () {
            editor?.dispose();
            editor = null;
            widget.model.back();
          },
          onCancel: () => navigate(widget.model.back),
        ),
      ],
    );
  }

  @override
  void dispose() {
    controller.dispose();
    editor?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => CallbackShortcuts(
    bindings: {const SingleActivator(LogicalKeyboardKey.escape): close},
    child: FocusScope(
      autofocus: true,
      child: BlockSemantics(
        child: LayoutBuilder(
          builder: (context, constraints) {
            final width = (constraints.maxWidth - 64).clamp(260.0, 1120.0);
            final height = (constraints.maxHeight - 108).clamp(160.0, 720.0);
            final end = Rect.fromCenter(
              center: Offset(
                constraints.maxWidth / 2,
                constraints.maxHeight / 2 + 8,
              ),
              width: width,
              height: height,
            );
            final begin = Rect.fromCenter(
              center: widget.origin,
              width: 180,
              height: 100,
            );
            return AnimatedBuilder(
              animation: controller,
              builder: (context, _) {
                final progress = Curves.easeInOutCubic.transform(
                  controller.value,
                );
                final rect = Rect.lerp(begin, end, progress)!;
                return Stack(
                  children: [
                    Positioned.fill(
                      child: GestureDetector(
                        onTap: close,
                        child: BackdropFilter(
                          filter: ImageFilter.blur(
                            sigmaX: 4 * progress,
                            sigmaY: 4 * progress,
                          ),
                          child: ColoredBox(
                            color: Colors.black.withValues(
                              alpha: .28 * progress,
                            ),
                          ),
                        ),
                      ),
                    ),
                    Positioned.fromRect(
                      rect: rect,
                      child: Transform.rotate(
                        angle: -.28 * (1 - progress),
                        child: OverflowBox(
                          minWidth: width,
                          maxWidth: width,
                          minHeight: height,
                          maxHeight: height,
                          child: Transform.scale(
                            scale: rect.width / width,
                            child: SizedBox(
                              width: width,
                              height: height,
                              child: IgnorePointer(
                                ignoring: phase != NotebookPhase.open,
                                child: ListenableBuilder(
                                  listenable: widget.model,
                                  builder: (context, _) {
                                    return NotebookFrame(
                                      model: widget.model,
                                      onClose: close,
                                      onManage: manage,
                                      onTab: (tab) => navigate(
                                        () => widget.model.changeTab(tab),
                                      ),
                                      spread: Opacity(
                                        opacity:
                                            ((controller.value - .75) / .25)
                                                .clamp(0.0, 1.0),
                                        child: ExcludeFocus(
                                          excluding:
                                              phase != NotebookPhase.open,
                                          child: switch (widget.model.tab) {
                                            NotebookTab.today => TodaySpread(
                                              model: widget.model,
                                            ),
                                            NotebookTab.history =>
                                              HistorySpread(
                                                model: widget.model,
                                              ),
                                            NotebookTab.calendar =>
                                              CalendarSpread(
                                                model: widget.model,
                                                onNavigate: navigate,
                                                onManage: manage,
                                                editor: buildEditor(),
                                              ),
                                          },
                                        ),
                                      ),
                                    );
                                  },
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                  ],
                );
              },
            );
          },
        ),
      ),
    ),
  );
}
