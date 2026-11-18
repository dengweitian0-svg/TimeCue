import 'dart:convert';

import 'package:flutter/material.dart';

import '../../theme/notebook_theme.dart';
import 'chat_display_content.dart';
import 'chat_view_model.dart';

/// 按开始事件的实例身份展示工具请求及其业务结果。
class ChatToolCallList extends StatelessWidget {
  const ChatToolCallList({super.key, required this.calls});

  final List<ChatToolCall> calls;

  @override
  Widget build(BuildContext context) {
    if (calls.isEmpty) return const SizedBox.shrink();
    final storage = key == null ? null : PageStorage.maybeOf(context);
    return ExpansionTile(
      key: const ValueKey('chat-tool-call-list'),
      initiallyExpanded: storage?.readState(context, identifier: key) == true,
      onExpansionChanged: (expanded) =>
          storage?.writeState(context, expanded, identifier: key),
      tilePadding: EdgeInsets.zero,
      title: Text(
        'Tool calling · 已发起 ${calls.length} 次调用',
        style: const TextStyle(color: NotebookTheme.ink, fontSize: 12),
      ),
      children: [
        for (var i = 0; i < calls.length; i++) _callTile(calls[i], i + 1),
      ],
    );
  }

  Widget _callTile(ChatToolCall call, int number) {
    final argument = _displayValue(call.argument);
    final result = _displayValue(call.output);
    return ExpansionTile(
      key: ValueKey('chat-tool-${call.instanceId}'),
      tilePadding: EdgeInsets.zero,
      leading: Text(
        '#$number',
        style: const TextStyle(color: NotebookTheme.ink),
      ),
      title: Text(
        call.name,
        style: const TextStyle(color: NotebookTheme.ink, fontSize: 13),
      ),
      subtitle: Text(
        [
          !call.hasResult
              ? '已发起'
              : call.error?.code == 'tool_skipped'
              ? '已跳过'
              : call.error != null
              ? '处理失败'
              : '已返回',
          if (call.hasResult) _resultPreview(result),
        ].where((value) => value.isNotEmpty).join(' · '),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: const TextStyle(fontSize: 11),
      ),
      childrenPadding: const EdgeInsets.only(left: 8, bottom: 12),
      children: [
        if (argument is! Map || argument.isNotEmpty)
          _JsonDetail(label: '参数', value: argument),
        if (call.hasResult) _JsonDetail(label: '结果', value: result),
        if (call.error case final error?)
          SelectableText('错误：${chatDisplayText(error.message)}'),
      ],
    );
  }
}

const _internalKeys = {
  'id',
  'scheduleid',
  'notificationid',
  'runid',
  'sessionid',
  'toolcallid',
  'revision',
  'expectedrevision',
  'configrevision',
  'version',
  'cursor',
  'nextcursor',
};

// 生成展示副本，工具配对和历史仍使用原始协议值。
Object? _displayValue(Object? value) {
  if (value is Map<String, Object?>) {
    return {
      for (final entry in value.entries)
        if (entry.key == 'open' && entry.value is bool)
          '状态': entry.value == true ? '已启用' : '已停用'
        else if (!_internalKeys.contains(
          entry.key.replaceAll('_', '').toLowerCase(),
        ))
          entry.key: _displayValue(entry.value),
    };
  }
  if (value is List) return value.map(_displayValue).toList();
  return value;
}

// 界面直接截取真实结果的文本预览，不额外调用模型生成摘要。
String _resultPreview(Object? value) {
  final text = value is String
      ? value
      : value == null
      ? 'null'
      : const JsonEncoder().convert(value);
  final plain = text.replaceAll(RegExp(r'\s+'), ' ').trim();
  if (plain.isEmpty) return '';
  final characters = plain.runes.toList(growable: false);
  return characters.length <= 48
      ? plain
      : '${String.fromCharCodes(characters.take(47))}…';
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
