import 'package:flutter/material.dart';

import '../../theme/notebook_theme.dart';

/// 展示一个 run 已收到的工具调用名称和稳定调用编号。
class ChatToolCallList<T> extends StatelessWidget {
  const ChatToolCallList({
    super.key,
    required this.calls,
    required this.idOf,
    required this.nameOf,
  });

  /// 按首次收到顺序排列的工具调用。
  final List<T> calls;

  /// 返回工具调用身份，不使用名称作为去重键。
  final String Function(T call) idOf;

  /// 返回协议中的 function.name。
  final String Function(T call) nameOf;

  @override
  Widget build(BuildContext context) {
    if (calls.isEmpty) return const SizedBox.shrink();
    return ExpansionTile(
      key: const ValueKey('chat-tool-call-list'),
      initiallyExpanded: true,
      tilePadding: EdgeInsets.zero,
      childrenPadding: const EdgeInsets.only(bottom: 8),
      title: Text(
        'Tool calling · 已发起 ${calls.length} 次调用',
        style: const TextStyle(color: NotebookTheme.ink, fontSize: 12),
      ),
      children: [
        for (var i = 0; i < calls.length; i++)
          _ToolCallRow(
            key: ValueKey('chat-tool-${idOf(calls[i])}'),
            index: i + 1,
            name: nameOf(calls[i]),
          ),
      ],
    );
  }
}

class _ToolCallRow extends StatelessWidget {
  const _ToolCallRow({super.key, required this.index, required this.name});

  final int index;
  final String name;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 4),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 38,
          child: Text(
            '#$index',
            style: const TextStyle(color: NotebookTheme.ink),
          ),
        ),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SelectableText(
                name,
                style: const TextStyle(color: NotebookTheme.ink),
              ),
              const Text(
                '已发起',
                style: TextStyle(fontSize: 11, color: NotebookTheme.ink),
              ),
            ],
          ),
        ),
      ],
    ),
  );
}
