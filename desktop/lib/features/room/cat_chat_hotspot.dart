import 'package:flutter/material.dart';

/// 提供房间猫咪上的 AI 对话入口，点击后由调用方打开日程本对话页。
class CatChatHotspot extends StatefulWidget {
  /// 创建猫咪 AI 对话入口。
  const CatChatHotspot({
    super.key,
    required this.onOpen,
    required this.focusNode,
  });

  /// 点击猫咪时调用。
  final VoidCallback onOpen;

  /// 由房间统一管理的键盘焦点节点。
  final FocusNode focusNode;

  @override
  State<CatChatHotspot> createState() => _CatChatHotspotState();
}

class _CatChatHotspotState extends State<CatChatHotspot> {
  final GlobalKey<TooltipState> _tooltipKey = GlobalKey<TooltipState>();
  bool _focusTooltipVisible = false;

  @override
  void initState() {
    super.initState();
    widget.focusNode.addListener(_showTooltipOnFocus);
  }

  @override
  void didUpdateWidget(CatChatHotspot oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.focusNode != widget.focusNode) {
      oldWidget.focusNode.removeListener(_showTooltipOnFocus);
      widget.focusNode.addListener(_showTooltipOnFocus);
    }
  }

  @override
  void dispose() {
    widget.focusNode.removeListener(_showTooltipOnFocus);
    super.dispose();
  }

  void _showTooltipOnFocus() {
    if (widget.focusNode.hasFocus) {
      _tooltipKey.currentState?.ensureTooltipVisible();
      _focusTooltipVisible = true;
    } else if (_focusTooltipVisible) {
      _dismissFocusTooltip();
    }
  }

  void _dismissFocusTooltip() {
    if (!_focusTooltipVisible) return;
    _focusTooltipVisible = false;
    Tooltip.dismissAllToolTips();
  }

  void _open() {
    _focusTooltipVisible = false;
    Tooltip.dismissAllToolTips();
    widget.onOpen();
  }

  @override
  Widget build(BuildContext context) => Tooltip(
    key: _tooltipKey,
    richMessage: const WidgetSpan(
      alignment: PlaceholderAlignment.middle,
      child: _CatChatTooltipContent(),
    ),
    excludeFromSemantics: true,
    // 提示卡片可跨到插画上方，不拦截猫咪热区的点击。
    ignorePointer: true,
    preferBelow: false,
    verticalOffset: 12,
    margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 10),
    constraints: const BoxConstraints(maxWidth: 240),
    decoration: BoxDecoration(
      color: const Color.fromARGB(250, 251, 246, 231),
      borderRadius: BorderRadius.circular(10),
      border: Border.all(color: const Color.fromARGB(220, 222, 212, 188)),
      boxShadow: const [
        BoxShadow(
          color: Color.fromARGB(48, 73, 61, 48),
          blurRadius: 12,
          offset: Offset(0, 4),
        ),
      ],
    ),
    child: Semantics(
      label: '和我聊聊，点击猫咪打开 AI 对话',
      button: true,
      child: TextButton(
        focusNode: widget.focusNode,
        onPressed: _open,
        style:
            TextButton.styleFrom(
              padding: EdgeInsets.zero,
              minimumSize: Size.zero,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              backgroundColor: Colors.transparent,
              overlayColor: Colors.transparent,
              shape: const RoundedRectangleBorder(),
            ).copyWith(
              mouseCursor: const WidgetStatePropertyAll(
                SystemMouseCursors.click,
              ),
              // 插画保持原样，只在键盘焦点到达时描出点击区域。
              side: WidgetStateProperty.resolveWith(
                (states) => states.contains(WidgetState.focused)
                    ? const BorderSide(
                        color: Color.fromARGB(190, 255, 235, 187),
                        width: 1.2,
                      )
                    : BorderSide.none,
              ),
            ),
        child: const SizedBox.expand(),
      ),
    ),
  );
}

class _CatChatTooltipContent extends StatelessWidget {
  const _CatChatTooltipContent();

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        const Icon(
          Icons.chat_bubble,
          size: 19,
          color: Color.fromARGB(255, 145, 116, 79),
        ),
        const SizedBox(width: 10),
        Flexible(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                '和我聊聊',
                softWrap: true,
                style: Theme.of(context).textTheme.titleSmall?.copyWith(
                  color: const Color.fromARGB(255, 89, 71, 51),
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                '点击猫咪，打开 AI 对话',
                softWrap: true,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: const Color.fromARGB(255, 139, 121, 100),
                ),
              ),
            ],
          ),
        ),
      ],
    ),
  );
}
