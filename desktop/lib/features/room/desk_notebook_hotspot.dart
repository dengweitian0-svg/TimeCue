import 'package:flutter/material.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

/// 背景图已经包含完整本子，只提供透明入口，避免再次绘制造成风格割裂。
class DeskNotebookHotspot extends StatelessWidget {
  const DeskNotebookHotspot({
    super.key,
    required this.onOpen,
    required this.focusNode,
    required this.outline,
  });
  final VoidCallback onOpen;
  final FocusNode focusNode;
  final Path outline;

  @override
  Widget build(BuildContext context) => Tooltip(
    message: '打开日程本',
    // 按钮已有语义名称，悬停提示不再提供重复的无障碍信息。
    excludeFromSemantics: true,
    padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
    textStyle: const TextStyle(
      color: NotebookTheme.roomActionForeground,
      fontSize: 14,
      fontWeight: FontWeight.w600,
    ),
    decoration: BoxDecoration(
      color: NotebookTheme.roomActionBackground,
      borderRadius: BorderRadius.circular(12),
      boxShadow: const [
        BoxShadow(
          color: Color.fromARGB(72, 61, 45, 30),
          blurRadius: 8,
          offset: Offset(0, 2),
        ),
      ],
    ),
    child: ClipPath(
      clipper: _NotebookHitClipper(outline),
      child: Semantics(
        label: '打开日程本',
        child: TextButton(
          focusNode: focusNode,
          onPressed: onOpen,
          style:
              TextButton.styleFrom(
                padding: EdgeInsets.zero,
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                backgroundColor: Colors.transparent,
                overlayColor: Colors.transparent,
                shape: const RoundedRectangleBorder(),
              ).copyWith(
                // 仅键盘聚焦时显示边界，日常展示与悬停保持原图完整。
                side: WidgetStateProperty.resolveWith(
                  (states) => states.contains(WidgetState.focused)
                      ? const BorderSide(
                          color: Color.fromARGB(170, 255, 235, 187),
                          width: .8,
                        )
                      : BorderSide.none,
                ),
              ),
          child: const SizedBox.expand(),
        ),
      ),
    ),
  );
}

class _NotebookHitClipper extends CustomClipper<Path> {
  const _NotebookHitClipper(this.outline);
  final Path outline;
  @override
  Path getClip(Size size) => outline;
  @override
  bool shouldReclip(_NotebookHitClipper oldClipper) =>
      oldClipper.outline != outline;
}
