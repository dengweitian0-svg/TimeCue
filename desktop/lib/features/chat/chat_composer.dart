import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../theme/notebook_theme.dart';

/// 提供可编辑草稿、Enter 发送、Shift+Enter 换行和运行停止操作。
class ChatComposer extends StatelessWidget {
  const ChatComposer({
    super.key,
    required this.controller,
    required this.onChanged,
    required this.onSend,
    required this.onStop,
    required this.sending,
    required this.stopping,
    required this.stoppable,
    required this.canSend,
    this.disabledReason,
  });

  final TextEditingController controller;
  final ValueChanged<String> onChanged;
  final VoidCallback onSend;
  final VoidCallback onStop;
  final bool sending;
  final bool stopping;
  final bool stoppable;
  final bool canSend;
  final String? disabledReason;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) => Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Expanded(
              child: ConstrainedBox(
                constraints: BoxConstraints(
                  maxHeight: constraints.maxHeight.isFinite
                      ? (constraints.maxHeight * .34).clamp(52.0, 116.0)
                      : 116.0,
                ),
                child: Focus(
                  onKeyEvent: (node, event) {
                    if (event is! KeyDownEvent ||
                        event.logicalKey != LogicalKeyboardKey.enter) {
                      return KeyEventResult.ignored;
                    }
                    final composing = controller.value.composing;
                    if (composing.isValid && !composing.isCollapsed) {
                      return KeyEventResult.ignored;
                    }
                    if (HardwareKeyboard.instance.isShiftPressed) {
                      final value = controller.value;
                      final selection = value.selection;
                      final start = selection.isValid
                          ? selection.start
                          : value.text.length;
                      final end = selection.isValid
                          ? selection.end
                          : value.text.length;
                      final text = value.text.replaceRange(start, end, '\n');
                      controller.value = TextEditingValue(
                        text: text,
                        selection: TextSelection.collapsed(offset: start + 1),
                      );
                      onChanged(text);
                      return KeyEventResult.handled;
                    }
                    if (sending || !canSend) return KeyEventResult.handled;
                    if (controller.text.trim().isEmpty) {
                      return KeyEventResult.handled;
                    }
                    onSend();
                    return KeyEventResult.handled;
                  },
                  child: TextField(
                    key: const ValueKey('chat-draft-input'),
                    controller: controller,
                    onChanged: onChanged,
                    minLines: 1,
                    maxLines: null,
                    keyboardType: TextInputType.multiline,
                    textInputAction: TextInputAction.newline,
                    decoration: const InputDecoration(
                      hintText: '写下你的想法…',
                      hintStyle: TextStyle(color: NotebookTheme.ink),
                      border: OutlineInputBorder(),
                    ),
                  ),
                ),
              ),
            ),
            const SizedBox(width: 8),
            if (sending && stoppable)
              IconButton.filledTonal(
                key: const ValueKey('chat-stop-button'),
                onPressed: stopping ? null : onStop,
                tooltip: stopping ? '正在停止' : '停止生成',
                icon: stopping
                    ? const SizedBox.square(
                        dimension: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.stop),
              )
            else if (sending)
              const Padding(
                padding: EdgeInsets.all(12),
                child: SizedBox.square(
                  dimension: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              )
            else
              ValueListenableBuilder<TextEditingValue>(
                valueListenable: controller,
                builder: (context, value, _) => IconButton.filled(
                  key: const ValueKey('chat-send-button'),
                  onPressed: value.text.trim().isEmpty || !canSend
                      ? null
                      : onSend,
                  tooltip: canSend ? '发送' : disabledReason ?? '当前无法发送',
                  icon: const Icon(Icons.send),
                ),
              ),
          ],
        ),
        const Align(
          alignment: Alignment.centerLeft,
          child: Padding(
            padding: EdgeInsets.only(top: 4),
            child: Text(
              'Enter 发送 · Shift+Enter 换行',
              style: TextStyle(fontSize: 11, color: NotebookTheme.ink),
            ),
          ),
        ),
      ],
    ),
  );
}
