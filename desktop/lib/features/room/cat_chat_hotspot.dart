import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

/// 在背景猫咪上提供透明、可聚焦的 AI 对话入口和窗口内提示气泡。
class CatChatHotspot extends StatefulWidget {
  const CatChatHotspot({
    super.key,
    required this.visibleBounds,
    required this.viewport,
    required this.focusNode,
    required this.onOpen,
  });

  final Rect visibleBounds;
  final Size viewport;
  final FocusNode focusNode;
  final VoidCallback onOpen;

  @override
  State<CatChatHotspot> createState() => _CatChatHotspotState();
}

class _CatChatHotspotState extends State<CatChatHotspot> {
  bool _hovered = false;
  bool _focused = false;

  bool get _showHint => _hovered || _focused;

  @override
  void initState() {
    super.initState();
    _focused = widget.focusNode.hasFocus;
    widget.focusNode.addListener(_onFocusChanged);
  }

  @override
  void didUpdateWidget(CatChatHotspot oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.focusNode != widget.focusNode) {
      oldWidget.focusNode.removeListener(_onFocusChanged);
      widget.focusNode.addListener(_onFocusChanged);
      _focused = widget.focusNode.hasFocus;
    }
  }

  void _onFocusChanged() {
    if (mounted && _focused != widget.focusNode.hasFocus) {
      setState(() => _focused = widget.focusNode.hasFocus);
    }
  }

  @override
  void dispose() {
    widget.focusNode.removeListener(_onFocusChanged);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Stack(
    clipBehavior: Clip.hardEdge,
    children: [
      Positioned.fromRect(
        rect: widget.visibleBounds,
        child: MouseRegion(
          onEnter: (_) => setState(() => _hovered = true),
          onExit: (_) => setState(() => _hovered = false),
          child: Semantics(
            button: true,
            label: '和我聊聊，点击猫咪打开 AI 对话',
            child: TextButton(
              key: const ValueKey('cat-chat-hotspot-button'),
              focusNode: widget.focusNode,
              onPressed: widget.onOpen,
              style:
                  TextButton.styleFrom(
                    padding: EdgeInsets.zero,
                    minimumSize: Size.zero,
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    backgroundColor: Colors.transparent,
                    foregroundColor: Colors.transparent,
                    overlayColor: Colors.transparent,
                    shape: const RoundedRectangleBorder(),
                  ).copyWith(
                    side: WidgetStateProperty.resolveWith(
                      (states) => states.contains(WidgetState.focused)
                          ? const BorderSide(
                              color: Color.fromARGB(235, 255, 249, 237),
                              width: 2,
                            )
                          : BorderSide.none,
                    ),
                  ),
              child: const SizedBox.expand(),
            ),
          ),
        ),
      ),
      if (_showHint)
        Positioned.fromRect(
          rect: _hintBounds,
          child: IgnorePointer(
            child: Material(
              color: NotebookTheme.roomActionBackground,
              surfaceTintColor: Colors.transparent,
              elevation: 4,
              shadowColor: const Color.fromARGB(72, 61, 45, 30),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
                side: BorderSide.none,
              ),
              child: SingleChildScrollView(
                padding: const EdgeInsets.symmetric(
                  horizontal: 14,
                  vertical: 9,
                ),
                child: const Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '和我聊聊',
                      style: TextStyle(
                        color: NotebookTheme.roomActionForeground,
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    SizedBox(height: 2),
                    Text(
                      '点击猫咪，打开 AI 对话',
                      style: TextStyle(
                        color: NotebookTheme.roomActionForeground,
                        fontSize: 12,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
    ],
  );

  Rect get _hintBounds {
    final viewport = widget.viewport;
    final target = widget.visibleBounds;
    final width = math.min(220.0, viewport.width);
    final scaler = MediaQuery.textScalerOf(context);
    final textWidth = math.max(0.0, width - 30);
    final title = TextPainter(
      text: TextSpan(
        text: '和我聊聊',
        style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w600),
      ),
      textDirection: TextDirection.ltr,
      textScaler: scaler,
    )..layout(maxWidth: textWidth);
    final subtitle = TextPainter(
      text: TextSpan(
        text: '点击猫咪，打开 AI 对话',
        style: const TextStyle(fontSize: 12),
      ),
      textDirection: TextDirection.ltr,
      textScaler: scaler,
    )..layout(maxWidth: textWidth);
    final naturalHeight = title.height + subtitle.height + 31;
    final height = math.min(naturalHeight, viewport.height);
    const gap = 12.0;
    double left;
    double top;

    if (target.left >= width + gap) {
      left = target.left - width - gap;
      top = target.center.dy - height / 2;
    } else if (viewport.width - target.right >= width + gap) {
      left = target.right + gap;
      top = target.center.dy - height / 2;
    } else if (target.top >= height + gap) {
      left = target.center.dx - width / 2;
      top = target.top - height - gap;
    } else {
      left = target.center.dx - width / 2;
      top = target.bottom + gap;
    }

    left = left.clamp(0.0, math.max(0.0, viewport.width - width));
    top = top.clamp(0.0, math.max(0.0, viewport.height - height));
    return Rect.fromLTWH(left, top, width, height);
  }
}
