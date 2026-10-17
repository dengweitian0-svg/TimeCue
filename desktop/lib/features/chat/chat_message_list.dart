import 'package:flutter/material.dart';

import '../../theme/notebook_theme.dart';
import 'chat_markdown_body.dart';
import 'chat_tool_call_list.dart';
import 'chat_view_model.dart';

/// 显示单会话的用户消息、助手正文和独立过程信息。
class ChatMessageList extends StatefulWidget {
  const ChatMessageList({
    super.key,
    required this.sessionId,
    required this.runs,
    required this.initialOffset,
    required this.initiallyFollowLatest,
    required this.onReadingPosition,
    required this.onRetryCreate,
    required this.onRetryConnection,
  });

  final String sessionId;
  final List<ChatRunState> runs;
  final double initialOffset;
  final bool initiallyFollowLatest;
  final void Function(double offset, bool followLatest) onReadingPosition;
  final ValueChanged<String> onRetryCreate;
  final ValueChanged<String> onRetryConnection;

  @override
  State<ChatMessageList> createState() => _ChatMessageListState();
}

class _ChatMessageListState extends State<ChatMessageList> {
  final ScrollController _scrollController = ScrollController();
  late bool _followLatest;
  bool _restorePending = true;
  double _lastSavedOffset = -1;

  @override
  void initState() {
    super.initState();
    _followLatest = widget.initiallyFollowLatest;
    WidgetsBinding.instance.addPostFrameCallback((_) => _restorePosition());
  }

  @override
  void didUpdateWidget(covariant ChatMessageList oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_followLatest) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _scrollController.hasClients) {
          _scrollController.jumpTo(_scrollController.position.maxScrollExtent);
        }
      });
    } else if (_restorePending) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _restorePosition());
    }
  }

  void _restorePosition() {
    if (!mounted || !_scrollController.hasClients) return;
    final position = _scrollController.position;
    final target = _followLatest
        ? position.maxScrollExtent
        : widget.initialOffset.clamp(
            position.minScrollExtent,
            position.maxScrollExtent,
          );
    _scrollController.jumpTo(target);
    _restorePending = false;
    _savePosition(force: true);
  }

  void _savePosition({bool force = false}) {
    if (!_scrollController.hasClients) return;
    final offset = _scrollController.position.pixels;
    if (!force &&
        (offset - _lastSavedOffset).abs() < 4 &&
        _followLatest == widget.initiallyFollowLatest) {
      return;
    }
    _lastSavedOffset = offset;
    widget.onReadingPosition(offset, _followLatest);
  }

  @override
  void dispose() {
    _savePosition(force: true);
    _scrollController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Stack(
    children: [
      Positioned.fill(
        child: NotificationListener<ScrollNotification>(
          onNotification: (notification) {
            if (notification.depth == 0 &&
                notification.metrics.axis == Axis.vertical &&
                (notification is UserScrollNotification ||
                    notification is ScrollUpdateNotification)) {
              final distance = _scrollController.hasClients
                  ? _scrollController.position.maxScrollExtent -
                        _scrollController.position.pixels
                  : 0.0;
              final following = distance < 48;
              if (following != _followLatest) {
                setState(() => _followLatest = following);
              }
              _savePosition();
            }
            return false;
          },
          child: ListView.builder(
            controller: _scrollController,
            padding: const EdgeInsets.fromLTRB(2, 10, 10, 14),
            itemCount: widget.runs.isEmpty ? 1 : widget.runs.length,
            itemBuilder: (context, index) => widget.runs.isEmpty
                ? const _WelcomeMessage()
                : _ChatTurn(
                    key: ValueKey(
                      'chat-message-${widget.runs[index].messageId}',
                    ),
                    sessionId: widget.sessionId,
                    run: widget.runs[index],
                    onRetryCreate: widget.onRetryCreate,
                    onRetryConnection: widget.onRetryConnection,
                  ),
          ),
        ),
      ),
      if (!_followLatest)
        Positioned(
          right: 8,
          bottom: 8,
          child: FilledButton.tonalIcon(
            onPressed: () {
              setState(() => _followLatest = true);
              _scrollController.animateTo(
                _scrollController.position.maxScrollExtent,
                duration: const Duration(milliseconds: 180),
                curve: Curves.easeOut,
              );
              WidgetsBinding.instance.addPostFrameCallback((_) {
                if (mounted) _savePosition(force: true);
              });
            },
            icon: const Icon(Icons.south, size: 16),
            label: const Text('回到最新'),
          ),
        ),
    ],
  );
}

class _WelcomeMessage extends StatelessWidget {
  const _WelcomeMessage();

  @override
  Widget build(BuildContext context) => Center(
    child: Padding(
      padding: const EdgeInsets.all(20),
      child: Text(
        '你好，今天想从什么开始？',
        textAlign: TextAlign.center,
        style: TextStyle(
          fontFamily: 'SimSun',
          fontSize: 20,
          color: NotebookTheme.ink,
        ),
      ),
    ),
  );
}

class _ChatTurn extends StatelessWidget {
  const _ChatTurn({
    super.key,
    required this.sessionId,
    required this.run,
    required this.onRetryCreate,
    required this.onRetryConnection,
  });

  final String sessionId;
  final ChatRunState run;
  final ValueChanged<String> onRetryCreate;
  final ValueChanged<String> onRetryConnection;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(bottom: 18),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Align(
          alignment: Alignment.centerRight,
          child: Container(
            constraints: const BoxConstraints(maxWidth: 620),
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
            decoration: BoxDecoration(
              color: const Color.fromARGB(255, 242, 235, 218),
              borderRadius: BorderRadius.circular(12),
            ),
            child: SelectableText(run.input),
          ),
        ),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 620),
            child: Material(
              color: const Color.fromARGB(255, 255, 250, 240),
              borderRadius: BorderRadius.circular(12),
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        const Icon(
                          Icons.auto_awesome,
                          size: 16,
                          color: NotebookTheme.ink,
                        ),
                        const SizedBox(width: 6),
                        Expanded(
                          child: Text(
                            _phaseLabel(run),
                            style: const TextStyle(
                              fontSize: 12,
                              color: NotebookTheme.ink,
                            ),
                          ),
                        ),
                      ],
                    ),
                    if (run.toolCalls.isNotEmpty)
                      ChatToolCallList(
                        calls: run.toolCalls,
                        idOf: (call) => call.id,
                        nameOf: (call) => call.name,
                      ),
                    if (run.rawText.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.only(top: 4),
                        child: ChatMarkdownBody(
                          key: ValueKey('chat-assistant-${run.messageId}'),
                          data: run.rawText,
                        ),
                      )
                    else if (run.phase == ChatRunPhase.creating ||
                        run.phase == ChatRunPhase.connecting ||
                        run.phase == ChatRunPhase.thinking)
                      const Padding(
                        padding: EdgeInsets.only(top: 5),
                        child: LinearProgressIndicator(minHeight: 2),
                      ),
                    if (run.error != null)
                      _InlineChatError(
                        message: run.error!,
                        onRetry:
                            run.phase == ChatRunPhase.creating &&
                                run.runId == null
                            ? () => onRetryCreate(sessionId)
                            : run.connectionState ==
                                      ChatConnectionState.disconnected &&
                                  run.runId != null &&
                                  !run.isTerminal
                            ? () => onRetryConnection(sessionId)
                            : null,
                      ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ],
    ),
  );
}

String _phaseLabel(ChatRunState run) => switch (run.phase) {
  ChatRunPhase.creating => '正在发送',
  ChatRunPhase.connecting => '正在连接',
  ChatRunPhase.running => '正在生成',
  ChatRunPhase.thinking => 'Thinking · 正在思考',
  ChatRunPhase.toolCalling => 'Tool calling · 已发起 ${run.toolCalls.length} 次调用',
  ChatRunPhase.replying => 'Replying · 正在回复',
  ChatRunPhase.reconnecting => '正在重连',
  ChatRunPhase.stopping => '正在停止',
  ChatRunPhase.succeeded => '回复完成',
  ChatRunPhase.failed => '回复失败',
  ChatRunPhase.cancelled => '已停止',
};

class _InlineChatError extends StatelessWidget {
  const _InlineChatError({required this.message, required this.onRetry});

  final String message;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) => Container(
    margin: const EdgeInsets.only(top: 8),
    padding: const EdgeInsets.all(10),
    decoration: BoxDecoration(
      color: const Color.fromARGB(255, 255, 239, 225),
      border: Border.all(color: NotebookTheme.cover),
      borderRadius: BorderRadius.circular(8),
    ),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(child: SelectableText('连接或请求失败：$message')),
        if (onRetry != null)
          TextButton(onPressed: onRetry, child: const Text('重试')),
      ],
    ),
  );
}
