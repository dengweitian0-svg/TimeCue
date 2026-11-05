import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../../theme/notebook_theme.dart';
import 'chat_markdown_body.dart';
import 'chat_tool_call_list.dart';
import 'chat_view_model.dart';

/// 按事件发生顺序展示一段运行中的思考、正文和工具内容。
class ChatContentSegments extends StatelessWidget {
  const ChatContentSegments({
    super.key,
    required this.run,
    this.onToggleReasoning,
  });

  /// 当前运行的只读展示状态。
  final ChatRunState run;

  /// 用户手动展开或折叠某段思考；为 null 时保持只读。
  final void Function(ChatContentSegment segment, bool expanded)?
  onToggleReasoning;

  @override
  Widget build(BuildContext context) {
    final segments = run.segments;
    final streamed = segments
        .where((segment) => segment.kind == ChatSegmentKind.text)
        .map((segment) => segment.content)
        .join();
    // 只用终态概况补齐事件流中缺少的最终正文，不覆盖也不重复过程内容。
    final needsFinal =
        run.rawText.isNotEmpty && !streamed.endsWith(run.rawText);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final segment in segments) _segment(context, segment),
        if (needsFinal)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: ChatMarkdownBody(
              key: ValueKey('chat-assistant-${run.messageId}'),
              data: run.rawText,
            ),
          ),
        if (segments.isEmpty &&
            !needsFinal &&
            (run.phase == ChatRunPhase.creating ||
                run.phase == ChatRunPhase.connecting ||
                run.phase == ChatRunPhase.thinking))
          const Padding(
            padding: EdgeInsets.only(top: 5),
            child: LinearProgressIndicator(minHeight: 2),
          ),
      ],
    );
  }

  Widget _segment(BuildContext context, ChatContentSegment segment) =>
      switch (segment.kind) {
        ChatSegmentKind.reasoning => _ReasoningSection(
          segment: segment,
          isStreaming: !run.isTerminal,
          onToggle: onToggleReasoning == null
              ? null
              : (expanded) => onToggleReasoning!(segment, expanded),
        ),
        ChatSegmentKind.text => Padding(
          padding: const EdgeInsets.only(top: 4),
          child: ChatAnimatedMarkdown(
            key: ValueKey('chat-text-${segment.id}'),
            data: segment.content,
            instantLength: segment.instantLength,
            isStreaming: !run.isTerminal,
          ),
        ),
        ChatSegmentKind.tool => Padding(
          padding: const EdgeInsets.only(top: 4),
          child: ChatToolCallList(
            calls: [
              for (final call in run.toolCalls)
                if (segment.toolIds.contains(call.instanceId)) call,
            ],
          ),
        ),
      };
}

/// 思考段落：生成期间展开并逐步追加，本段完成后自动折叠。
class _ReasoningSection extends StatelessWidget {
  const _ReasoningSection({
    required this.segment,
    required this.isStreaming,
    this.onToggle,
  });

  final ChatContentSegment segment;
  final bool isStreaming;
  final ValueChanged<bool>? onToggle;

  @override
  Widget build(BuildContext context) {
    final expanded = segment.expanded;
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          InkWell(
            key: ValueKey('chat-reasoning-toggle-${segment.id}'),
            borderRadius: BorderRadius.circular(6),
            onTap: onToggle == null ? null : () => onToggle!(!expanded),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Row(
                children: [
                  Icon(
                    expanded ? Icons.expand_more : Icons.chevron_right,
                    size: 16,
                    color: NotebookTheme.ink,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    expanded
                        ? '正在思考…'
                        : segment.completed
                        ? '思考过程（已完成）'
                        : '思考过程',
                    style: const TextStyle(
                      fontSize: 12,
                      color: NotebookTheme.ink,
                    ),
                  ),
                ],
              ),
            ),
          ),
          if (expanded)
            Container(
              key: ValueKey('chat-reasoning-${segment.id}'),
              margin: const EdgeInsets.only(top: 2, left: 6),
              padding: const EdgeInsets.only(left: 8),
              decoration: const BoxDecoration(
                border: Border(
                  left: BorderSide(color: NotebookTheme.line, width: 2),
                ),
              ),
              child: ChatAnimatedMarkdown(
                data: segment.content,
                instantLength: segment.instantLength,
                isStreaming: isStreaming,
              ),
            ),
        ],
      ),
    );
  }
}

/// 逐步显示已接收正文的原文前缀；既有内容首次显示时直接完整展示。
class ChatAnimatedMarkdown extends StatefulWidget {
  const ChatAnimatedMarkdown({
    super.key,
    required this.data,
    this.instantLength = 0,
    this.isStreaming = false,
  });

  /// 已确认接收的完整原文，动画只改变显示位置。
  final String data;

  /// 历史或补读已存在的长度，直接从该位置显示。
  final int instantLength;

  /// 是否仍会接收增量；终态时立即补齐全部已收文字。
  final bool isStreaming;

  @override
  State<ChatAnimatedMarkdown> createState() => _ChatAnimatedMarkdownState();
}

class _ChatAnimatedMarkdownState extends State<ChatAnimatedMarkdown> {
  static const _tick = Duration(milliseconds: 20);

  // 首次构建即显示全部既有内容，历史、补读及切回会话不会重播。
  late int _revealed = widget.data.length;
  Timer? _timer;

  @override
  void didUpdateWidget(covariant ChatAnimatedMarkdown oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.instantLength > _revealed) {
      _revealed = widget.instantLength;
    }
    if (!widget.isStreaming || widget.data.length <= _revealed) {
      _timer?.cancel();
      _timer = null;
      _revealed = widget.data.length;
      return;
    }
    _timer ??= Timer.periodic(_tick, (_) => _advance());
  }

  void _advance() {
    if (!mounted) return;
    final target = widget.data.length;
    if (_revealed >= target) {
      _timer?.cancel();
      _timer = null;
      return;
    }
    // 按剩余长度收敛，长批次快速补到当前末尾，短回复自然逐字显示。
    var next = _revealed + math.max<int>(1, ((target - _revealed) / 8).ceil());
    if (next > target) next = target;
    var safe = _safePrefixLength(widget.data, next);
    // 代理对边界不能拆开，也不能因此停在原地；跳过整对继续推进。
    if (safe <= _revealed) safe = _safePrefixLength(widget.data, next + 1);
    if (safe <= _revealed) safe = target;
    setState(() => _revealed = safe);
  }

  // 不把代理对拆成半个字符，保证 UTF-8 内容不被破坏。
  int _safePrefixLength(String value, int length) {
    if (length <= 0) return 0;
    if (length >= value.length) return value.length;
    final unit = value.codeUnitAt(length - 1);
    return unit >= 0xD800 && unit <= 0xDBFF ? length - 1 : length;
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ChatMarkdownBody(
    data: _revealed >= widget.data.length
        ? widget.data
        : widget.data.substring(0, _revealed),
    isStreaming: widget.isStreaming || _revealed < widget.data.length,
  );
}
