import 'dart:convert';

import 'package:flutter/material.dart';

import '../../contracts/chat_contracts.dart';
import '../../theme/notebook_theme.dart';
import 'chat_view_model.dart';

/// 按原始事件顺序展示运行记录，思考和完整 JSON 详情默认折叠。
class ChatEventTimeline extends StatelessWidget {
  const ChatEventTimeline({super.key, required this.run, this.onLoadMore});

  final ChatRunState run;
  final ValueChanged<String>? onLoadMore;

  @override
  Widget build(BuildContext context) => ExpansionTile(
    key: ValueKey('chat-event-timeline-${run.runId}'),
    tilePadding: EdgeInsets.zero,
    title: const Text(
      '运行记录',
      style: TextStyle(fontSize: 12, color: NotebookTheme.ink),
    ),
    subtitle: Text(
      '已加载 ${run.timeline.length} 条',
      style: const TextStyle(fontSize: 11),
    ),
    onExpansionChanged: (expanded) {
      if (expanded &&
          run.timeline.isEmpty &&
          !run.eventsLoading &&
          run.hasMoreEvents &&
          run.runId != null) {
        onLoadMore?.call(run.runId!);
      }
    },
    children: [
      for (final event in run.timeline)
        ExpansionTile(
          key: ValueKey('chat-event-${run.runId}-${event.eventId}'),
          tilePadding: const EdgeInsets.only(left: 8),
          title: Text(
            '#${event.eventId} · ${_label(event.type)}',
            style: const TextStyle(fontSize: 12),
          ),
          childrenPadding: const EdgeInsets.fromLTRB(8, 0, 8, 12),
          children: [
            Align(
              alignment: Alignment.centerLeft,
              child: SelectableText(_content(event)),
            ),
          ],
        ),
      if (run.eventsError != null)
        Padding(
          padding: const EdgeInsets.all(8),
          child: Text('运行记录读取失败：${run.eventsError}'),
        ),
      if (run.eventsLoading)
        const Padding(
          padding: EdgeInsets.all(8),
          child: LinearProgressIndicator(minHeight: 2),
        )
      else if (onLoadMore != null &&
          run.runId != null &&
          (run.hasMoreEvents || run.eventsError != null || run.needsSnapshot))
        TextButton(
          onPressed: () => onLoadMore!(run.runId!),
          child: Text(run.eventsError == null ? '加载更多记录' : '重试读取记录'),
        ),
    ],
  );
}

String _label(ChatEventType type) => switch (type) {
  ChatEventType.start => '开始',
  ChatEventType.thinking => '思考',
  ChatEventType.text => '正文增量',
  ChatEventType.toolStart => '工具请求',
  ChatEventType.toolResult => '工具结果',
  ChatEventType.error => '错误',
  ChatEventType.compact => '摘要请求',
  ChatEventType.end => '结束',
};

String _content(ChatSseEvent event) {
  if (event.data case final ChatContentEventData content) {
    return content.content;
  }
  if (event.rawData != null) {
    return const JsonEncoder.withIndent('  ').convert(event.rawData);
  }
  final data = event.data;
  if (data is List<ChatToolEventData>) {
    return const JsonEncoder.withIndent(
      '  ',
    ).convert({'function': data.map(_toolData).toList()});
  }
  if (data is ChatToolEventData) {
    return const JsonEncoder.withIndent('  ').convert({
      'function': [_toolData(data)],
    });
  }
  if (data is ChatToolResultData) {
    return const JsonEncoder.withIndent('  ').convert({
      'tool_call_id': data.toolCallId,
      'output': data.output,
      'error': data.error == null
          ? null
          : {'code': data.error!.code, 'message': data.error!.message},
    });
  }
  return switch (event.data) {
    ChatProtocolError error => '${error.code}：${error.message}',
    ChatEndEventData end => end.status.name,
    _ => const JsonEncoder.withIndent('  ').convert(event.data),
  };
}

Map<String, Object?> _toolData(ChatToolEventData tool) => {
  'id': tool.id,
  'name': tool.function.name,
  'argument': tool.function.args.value,
};
