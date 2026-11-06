import 'dart:convert';

import 'package:flutter/material.dart';

import '../../contracts/chat_contracts.dart';
import '../../theme/notebook_theme.dart';
import 'chat_tool_call_list.dart';
import 'chat_view_model.dart';

/// 按事件顺序合并连续同类记录，保留可展开的原文和工具详情。
class ChatEventTimeline extends StatelessWidget {
  const ChatEventTimeline({super.key, required this.run, this.onLoadMore});

  final ChatRunState run;
  final ValueChanged<String>? onLoadMore;

  @override
  Widget build(BuildContext context) {
    final callsByStart = <int, List<ChatToolCall>>{};
    for (final call in run.toolCalls) {
      (callsByStart[call.startSeq] ??= []).add(call);
    }
    final sections = <Widget>[];
    for (final section in _groupEvents(run.timeline)) {
      if (section.kind == _SectionKind.tool) {
        final calls = [
          for (final event in section.events) ...?callsByStart[event.eventId],
        ];
        if (calls.isEmpty) continue;
        sections.add(
          ChatToolCallList(
            key: ValueKey('chat-event-tools-${run.runId}-${section.startSeq}'),
            calls: calls,
            initiallyExpanded: false,
          ),
        );
      } else {
        sections.add(
          ExpansionTile(
            key: ValueKey('chat-event-${run.runId}-${section.startSeq}'),
            tilePadding: const EdgeInsets.only(left: 8),
            title: Text(
              '#${section.startSeq} · ${section.kind.label}',
              style: const TextStyle(fontSize: 12),
            ),
            childrenPadding: const EdgeInsets.fromLTRB(8, 0, 8, 12),
            children: [
              Align(
                alignment: Alignment.centerLeft,
                child: SelectableText(section.content),
              ),
            ],
          ),
        );
      }
    }
    return ExpansionTile(
      key: ValueKey('chat-event-timeline-${run.runId}'),
      tilePadding: EdgeInsets.zero,
      title: const Text(
        '运行记录',
        style: TextStyle(fontSize: 12, color: NotebookTheme.ink),
      ),
      subtitle: Text(
        '已加载 ${run.lastEventId} 条事件',
        style: const TextStyle(fontSize: 11),
      ),
      onExpansionChanged: (expanded) {
        if (expanded &&
            run.lastEventId == 0 &&
            !run.eventsLoading &&
            run.hasMoreEvents &&
            run.runId != null) {
          onLoadMore?.call(run.runId!);
        }
      },
      children: [
        ...sections,
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
}

enum _SectionKind {
  thinking('思考'),
  text('正文'),
  tool('工具调用'),
  error('错误'),
  compact('摘要请求');

  const _SectionKind(this.label);
  final String label;
}

class _EventSection {
  _EventSection(this.kind, ChatSseEvent first) : events = [first];

  final _SectionKind kind;
  final List<ChatSseEvent> events;
  int get startSeq => events.first.eventId;

  String get content {
    if (kind == _SectionKind.thinking || kind == _SectionKind.text) {
      return events
          .map((event) => (event.data as ChatContentEventData).content)
          .join();
    }
    return events
        .map((event) {
          final data = event.data;
          if (event.rawData == null && data is ChatProtocolError) {
            return '${data.code}：${data.message}';
          }
          return const JsonEncoder.withIndent(
            '  ',
          ).convert(event.rawData ?? data);
        })
        .join('\n\n');
  }
}

List<_EventSection> _groupEvents(List<ChatSseEvent> events) {
  final sections = <_EventSection>[];
  for (final event in events) {
    final kind = switch (event.type) {
      ChatEventType.start || ChatEventType.end => null,
      ChatEventType.thinking => _SectionKind.thinking,
      ChatEventType.text => _SectionKind.text,
      ChatEventType.toolStart || ChatEventType.toolResult => _SectionKind.tool,
      ChatEventType.error => _SectionKind.error,
      ChatEventType.compact => _SectionKind.compact,
    };
    if (kind == null) continue;
    if (sections.isNotEmpty && sections.last.kind == kind) {
      sections.last.events.add(event);
    } else {
      sections.add(_EventSection(kind, event));
    }
  }
  return sections;
}
