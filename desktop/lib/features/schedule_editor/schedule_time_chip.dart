import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../../contracts/notebook_repository.dart';
import '../../theme/notebook_theme.dart';

class ScheduleTimeChip extends StatefulWidget {
  const ScheduleTimeChip({super.key, required this.slot, this.onRemoved})
    : date = null;
  const ScheduleTimeChip.single({super.key, required this.date, this.onRemoved})
    : slot = null;
  final ScheduleTimeSlot? slot;
  final DateTime? date;
  String get label =>
      slot?.label ??
      '${date!.year}-${date!.month}-${date!.day} ${timeLabel(date!)} · 北京时间';
  final VoidCallback? onRemoved;
  @override
  State<ScheduleTimeChip> createState() => _ScheduleTimeChipState();
}

class _ScheduleTimeChipState extends State<ScheduleTimeChip> {
  bool hovered = false, focused = false;
  @override
  Widget build(BuildContext context) {
    final showDelete = widget.onRemoved != null && (hovered || focused);
    return MouseRegion(
      onEnter: (_) => setState(() => hovered = true),
      onExit: (_) => setState(() => hovered = false),
      child: CallbackShortcuts(
        bindings: {
          if (widget.onRemoved != null)
            const SingleActivator(LogicalKeyboardKey.delete): widget.onRemoved!,
        },
        child: Focus(
          canRequestFocus: widget.onRemoved != null,
          onFocusChange: (value) {
            if (mounted) setState(() => focused = value);
          },
          child: Container(
            constraints: const BoxConstraints(minHeight: 44),
            padding: const EdgeInsets.only(left: 14, right: 4),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(24),
              border: Border.all(
                color: NotebookTheme.cover,
                width: focused ? 1.5 : 1,
              ),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Flexible(
                  child: Text(
                    widget.label,
                    style: const TextStyle(color: NotebookTheme.cover),
                  ),
                ),
                // 删除按钮保留位置，悬停时不会挤动标签或相邻时间项。
                SizedBox(
                  width: 40,
                  height: 40,
                  child: Visibility(
                    visible: showDelete,
                    maintainSize: true,
                    maintainState: true,
                    maintainAnimation: true,
                    child: ExcludeFocus(
                      excluding: !showDelete,
                      child: IconButton(
                        tooltip: '删除${widget.label}',
                        onPressed: widget.onRemoved,
                        padding: EdgeInsets.zero,
                        iconSize: 16,
                        color: NotebookTheme.cover,
                        icon: const Icon(Icons.close),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
