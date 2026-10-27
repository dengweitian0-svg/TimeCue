import 'dart:convert';

import 'package:flutter/material.dart';

import '../../theme/notebook_theme.dart';
import 'chat_view_model.dart';

/// 按开始事件的实例身份展示工具请求及其原始结果。
class ChatToolCallList extends StatelessWidget {
  const ChatToolCallList({super.key, required this.calls});

  final List<ChatToolCall> calls;

  @override
  Widget build(BuildContext context) {
    if (calls.isEmpty) return const SizedBox.shrink();
    return ExpansionTile(
      key: const ValueKey('chat-tool-call-list'),
      initiallyExpanded: true,
      tilePadding: EdgeInsets.zero,
      title: Text(
        'Tool calling · 已发起 ${calls.length} 次调用',
        style: const TextStyle(color: NotebookTheme.ink, fontSize: 12),
      ),
      children: [
        for (var i = 0; i < calls.length; i++)
          ExpansionTile(
            key: ValueKey('chat-tool-${calls[i].instanceId}'),
            tilePadding: EdgeInsets.zero,
            leading: Text(
              '#${i + 1}',
              style: const TextStyle(color: NotebookTheme.ink),
            ),
            title: Text(
              calls[i].name,
              style: const TextStyle(color: NotebookTheme.ink, fontSize: 13),
            ),
            subtitle: Text(
              !calls[i].hasResult
                  ? '已发起'
                  : calls[i].error?.code == 'tool_skipped'
                  ? '已跳过'
                  : calls[i].error != null
                  ? '处理失败'
                  : '已返回',
              style: const TextStyle(fontSize: 11),
            ),
            childrenPadding: const EdgeInsets.only(left: 8, bottom: 12),
            children: [
              Align(
                alignment: Alignment.centerLeft,
                child: SelectableText('工具 ID：${calls[i].id}'),
              ),
              _JsonDetail(label: '参数', value: calls[i].argument),
              if (calls[i].hasResult)
                _JsonDetail(label: '原始结果', value: calls[i].output),
              if (calls[i].error case final error?)
                _JsonDetail(
                  label: '错误',
                  value: {'code': error.code, 'message': error.message},
                ),
            ],
          ),
      ],
    );
  }
}

class _JsonDetail extends StatelessWidget {
  const _JsonDetail({required this.label, required this.value});
  final String label;
  final Object? value;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Align(
      alignment: Alignment.centerLeft,
      child: SelectableText(
        '$label：\n${const JsonEncoder.withIndent('  ').convert(value)}',
        style: const TextStyle(fontSize: 12),
      ),
    ),
  );
}
