import 'dart:async';

import 'package:flutter/material.dart';

import '../chat/chat_composer.dart';
import '../chat/chat_message_list.dart';
import '../chat/chat_session_list.dart';
import '../chat/chat_view_model.dart';
import '../../theme/notebook_theme.dart';
import 'notebook_view_model.dart';

/// 展示 AI 对话的双页与单栏页面，聊天模型由宿主持有。
class ChatSpread extends StatefulWidget {
  const ChatSpread({super.key, required this.model, this.chat});

  final NotebookViewModel model;
  final ChatViewModel? chat;

  @override
  State<ChatSpread> createState() => _ChatSpreadState();
}

class _ChatSpreadState extends State<ChatSpread> {
  late final TextEditingController _draftController;
  String? _chatSessionId;

  @override
  void initState() {
    super.initState();
    final chat = widget.chat;
    if (chat == null) {
      _draftController = TextEditingController(text: widget.model.chatDraft);
    } else {
      final conversation =
          chat.selectedConversation ?? chat.createLocalSession();
      _chatSessionId = conversation.sessionId;
      _draftController = TextEditingController(text: conversation.draft);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) unawaited(chat.loadModels());
        if (mounted && !chat.historyLoaded) unawaited(chat.loadHistory());
      });
    }
  }

  @override
  void dispose() {
    if (widget.chat == null) {
      widget.model.updateChatDraft(_draftController.text);
    } else if (_chatSessionId case final sessionId?) {
      final conversation = widget.chat!.conversations
          .where((item) => item.sessionId == sessionId)
          .firstOrNull;
      if (conversation != null && conversation.draft != _draftController.text) {
        widget.chat!.updateDraft(sessionId, _draftController.text);
      }
    }
    _draftController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final narrow =
          constraints.maxWidth < 840 ||
          MediaQuery.textScalerOf(context).scale(14) > 20;
      if (narrow) return _singleColumn(context);
      return Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(child: _historyPage(context, compact: false)),
          Expanded(child: _conversationPage(context, showHistoryButton: false)),
        ],
      );
    },
  );

  Widget _singleColumn(BuildContext context) => widget.model.chatShowingHistory
      ? _historyPage(context, compact: true)
      : _conversationPage(context, showHistoryButton: true);

  Widget _historyPage(BuildContext context, {required bool compact}) =>
      _ChatPaper(
        right: false,
        compact: compact,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (compact)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: widget.model.returnToChat,
                  icon: const Icon(Icons.arrow_back),
                  label: const Text('返回对话'),
                ),
              ),
            Expanded(
              child: widget.chat == null
                  ? _legacyHistory()
                  : ChatSessionList(
                      chat: widget.chat!,
                      selectedSessionId: widget.chat!.selectedSessionId,
                      onSelect: (id) {
                        widget.chat!.selectSession(id);
                        widget.model.returnToChat();
                      },
                    ),
            ),
          ],
        ),
      );

  Widget _legacyHistory() => SingleChildScrollView(
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            const Expanded(
              child: _PageTitle(title: '历史会话', subtitle: '已聊过的内容会出现在这里。'),
            ),
            IconButton.filledTonal(
              onPressed: _startNewDraft,
              tooltip: '新会话',
              icon: const Icon(Icons.add),
            ),
          ],
        ),
        const SizedBox(height: 14),
        const TextField(
          enabled: false,
          decoration: InputDecoration(
            prefixIcon: Icon(Icons.search),
            hintText: '搜索暂未开放',
            isDense: true,
          ),
        ),
        const SizedBox(height: 20),
        const Center(
          child: Text('历史会话暂不可用', style: TextStyle(color: NotebookTheme.ink)),
        ),
      ],
    ),
  );

  Widget _conversationPage(
    BuildContext context, {
    required bool showHistoryButton,
  }) => _ChatPaper(
    right: true,
    compact: showHistoryButton,
    child: widget.chat == null
        ? _legacyConversation(context, showHistoryButton: showHistoryButton)
        : _activeConversation(context, showHistoryButton: showHistoryButton),
  );

  Widget _legacyConversation(
    BuildContext context, {
    required bool showHistoryButton,
  }) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      if (showHistoryButton)
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            onPressed: widget.model.showChatHistory,
            icon: const Icon(Icons.history),
            label: const Text('历史会话'),
          ),
        ),
      if (showHistoryButton)
        Text('AI 对话', style: Theme.of(context).textTheme.titleLarge),
      Expanded(
        child: SingleChildScrollView(
          child: Center(
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (!showHistoryButton)
                    const _PageTitle(title: 'AI 对话', subtitle: '把想法写下来，我们慢慢聊。'),
                  const Text(
                    '你好，今天想从什么开始？',
                    textAlign: TextAlign.center,
                    style: TextStyle(
                      fontFamily: 'SimSun',
                      fontSize: 20,
                      color: NotebookTheme.ink,
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
      const Divider(),
      Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: TextField(
              key: const ValueKey('chat-draft-input'),
              controller: _draftController,
              onChanged: widget.model.updateChatDraft,
              minLines: 1,
              maxLines: 2,
              textInputAction: TextInputAction.newline,
              decoration: const InputDecoration(
                hintText: '写下你的想法…',
                border: OutlineInputBorder(),
              ),
            ),
          ),
          Tooltip(
            message: '发送功能暂未开放',
            child: IconButton.filled(
              onPressed: null,
              tooltip: '发送功能暂未开放',
              icon: const Icon(Icons.send),
            ),
          ),
        ],
      ),
      const SizedBox(height: 4),
      const Text('发送暂未开放', style: TextStyle(fontSize: 12)),
    ],
  );

  Widget _activeConversation(
    BuildContext context, {
    required bool showHistoryButton,
  }) => ListenableBuilder(
    listenable: widget.chat!,
    builder: (context, _) {
      final chat = widget.chat!;
      final conversation = chat.selectedConversation;
      if (conversation == null) {
        return Column(
          children: [
            if (showHistoryButton)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: widget.model.showChatHistory,
                  icon: const Icon(Icons.history),
                  label: const Text('历史会话'),
                ),
              ),
            const Expanded(child: Center(child: Text('当前没有可显示的会话。'))),
            FilledButton.icon(
              onPressed: () => chat.createLocalSession(),
              icon: const Icon(Icons.add),
              label: const Text('新会话'),
            ),
          ],
        );
      }
      if (conversation.sessionId != _chatSessionId) {
        _saveCurrentDraft();
        _chatSessionId = conversation.sessionId;
        _draftController.value = TextEditingValue(
          text: conversation.draft,
          selection: TextSelection.collapsed(offset: conversation.draft.length),
        );
      }
      final sessionId = conversation.sessionId;
      final activeRun = conversation.activeRun;
      final canSend = chat.canSend(sessionId) && chat.selectedModel != null;
      final disabledReason = chat.activeRunCount >= 3
          ? '已有 3 轮回复正在运行，请等待完成后发送'
          : !chat.acceptingOperations
          ? '聊天服务不可用，无法发送新消息'
          : chat.historyError != null || chat.historyHasFailures
          ? '历史读取失败，请先重试'
          : chat.historyLoading
          ? '正在整理历史，请稍后发送'
          : !conversation.isLocal && !conversation.historyLoaded
          ? '正在读取会话，请稍后发送'
          : chat.modelsError != null
          ? '模型列表读取失败，请刷新重试'
          : chat.selectedModel == null
          ? '暂无可用模型，请配置密钥后刷新'
          : '当前不能开始新一轮对话';
      final displayName = conversation.displayName;
      final header = <Widget>[
        if (showHistoryButton)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: widget.model.showChatHistory,
              icon: const Icon(Icons.history),
              label: const Text('历史会话'),
            ),
          ),
        Row(
          children: [
            Expanded(
              child: Tooltip(
                message: displayName,
                child: Semantics(
                  header: true,
                  child: Text(
                    displayName,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
              ),
            ),
            if (!conversation.isLocal)
              PopupMenuButton<String>(
                tooltip: '会话操作',
                onSelected: (value) {
                  if (value == 'refresh') {
                    unawaited(chat.refreshSession(sessionId));
                  } else if (value == 'delete') {
                    unawaited(_confirmDeleteCurrent(conversation));
                  }
                },
                itemBuilder: (context) => const [
                  PopupMenuItem(value: 'refresh', child: Text('刷新会话')),
                  PopupMenuItem(value: 'delete', child: Text('删除会话')),
                ],
              ),
          ],
        ),
        Row(
          children: [
            Expanded(
              child: DropdownButton<String>(
                key: const ValueKey('chat-model-selector'),
                value: chat.selectedModel?.identity,
                isExpanded: true,
                isDense: true,
                hint: Text(chat.modelsLoading ? '正在加载模型…' : '选择模型'),
                items: chat.models
                    .map(
                      (option) => DropdownMenuItem(
                        value: option.identity,
                        enabled: option.available,
                        child: Text(
                          '${option.provider} / ${option.model}${option.available ? '' : '（不可用）'}',
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    )
                    .toList(),
                onChanged: chat.modelsLoading
                    ? null
                    : (value) {
                        if (value != null) chat.selectModel(value);
                      },
              ),
            ),
            IconButton(
              tooltip: '刷新模型',
              onPressed: chat.modelsLoading ? null : chat.loadModels,
              icon: const Icon(Icons.refresh, size: 18),
            ),
          ],
        ),
        Text(
          _conversationStatus(conversation),
          style: const TextStyle(fontSize: 12, color: NotebookTheme.ink),
        ),
        if (conversation.error != null)
          _InlineError(
            message: '会话信息读取失败：${conversation.error}',
            onRetry: () => chat.refreshSession(sessionId),
          ),
        if (chat.runtimeError != null)
          _InlineError(
            message: '聊天服务不可用：${chat.runtimeError}',
            onRetry: () => chat.refreshSession(sessionId),
          ),
      ];
      final messages =
          !conversation.isLocal &&
              conversation.runs.isEmpty &&
              conversation.error != null
          ? _InlineError(
              message: '读取会话失败：${conversation.error}',
              onRetry: () => chat.refreshSession(sessionId),
            )
          : !conversation.isLocal &&
                conversation.runs.isEmpty &&
                !conversation.historyLoaded
          ? const Center(child: CircularProgressIndicator())
          : ChatMessageList(
              key: ValueKey('chat-message-list-$sessionId'),
              sessionId: sessionId,
              runs: _oldestFirst(conversation.runs),
              initialOffset: conversation.readingOffset,
              initiallyFollowLatest: conversation.followLatest,
              onReadingPosition: (offset, followLatest) {
                if (chat.conversations.any(
                  (item) => item.sessionId == sessionId,
                )) {
                  chat.updateReadingPosition(
                    sessionId,
                    offset: offset,
                    followLatest: followLatest,
                  );
                }
              },
              onRetryCreate: (id) => chat.retryCreate(id),
              onRetryConnection: (id) => chat.retryConnection(id),
              onLoadMoreEvents: chat.supportsEventHistory
                  ? (id) => chat.loadMoreEvents(id)
                  : null,
            );
      final composer = ChatComposer(
        controller: _draftController,
        onChanged: (text) => chat.updateDraft(sessionId, text),
        onSend: () {
          final text = _draftController.text;
          final model = chat.selectedModel;
          if (text.trim().isEmpty || !canSend || model == null) return;
          chat.updateDraft(sessionId, text);
          _draftController.clear();
          unawaited(
            chat.send(sessionId, provider: model.provider, model: model.model),
          );
        },
        onStop: () => chat.cancel(sessionId),
        sending: activeRun != null,
        stopping: activeRun?.phase == ChatRunPhase.stopping,
        stoppable: activeRun?.runId != null,
        canSend: canSend,
        disabledReason: canSend ? null : disabledReason,
      );
      final disabledHint = !canSend && activeRun == null
          ? Padding(
              padding: const EdgeInsets.only(top: 3),
              child: Text(
                disabledReason,
                style: const TextStyle(fontSize: 11, color: NotebookTheme.ink),
              ),
            )
          : null;

      return LayoutBuilder(
        builder: (context, constraints) {
          final compactHeader =
              constraints.maxHeight < 420 ||
              MediaQuery.textScalerOf(context).scale(14) > 20;
          if (compactHeader) {
            return Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                ConstrainedBox(
                  constraints: BoxConstraints(
                    maxHeight: (constraints.maxHeight * .4).clamp(0.0, 240.0),
                  ),
                  child: SingleChildScrollView(
                    primary: false,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [...header, ?disabledHint, const Divider()],
                    ),
                  ),
                ),
                Expanded(child: messages),
                const Divider(),
                composer,
              ],
            );
          }
          return Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ...header,
              const Divider(),
              Expanded(child: messages),
              const Divider(),
              composer,
              ?disabledHint,
            ],
          );
        },
      );
    },
  );

  String _conversationStatus(ChatConversation conversation) {
    if (conversation.isDeleting) return '正在删除';
    final run = conversation.activeRun;
    if (run != null) {
      return switch (run.phase) {
        ChatRunPhase.creating => '正在发送',
        ChatRunPhase.connecting => '正在连接',
        ChatRunPhase.reconnecting => '正在重连',
        ChatRunPhase.stopping => '正在停止',
        ChatRunPhase.thinking => 'Thinking · 正在思考',
        ChatRunPhase.toolCalling => 'Tool calling',
        ChatRunPhase.replying => 'Replying · 正在回复',
        _ => '正在生成',
      };
    }
    final latest = _latestRun(conversation.runs);
    if (latest == null) return '开始一段新的对话';
    return switch (latest.phase) {
      ChatRunPhase.failed => '回复失败',
      ChatRunPhase.cancelled => '已停止',
      ChatRunPhase.succeeded => '最近回复已完成',
      _ => '最近回复尚未完成',
    };
  }

  ChatRunState? _latestRun(List<ChatRunState> runs) {
    ChatRunState? latest;
    for (final run in runs) {
      if (latest == null || run.createdAt.isAfter(latest.createdAt)) {
        latest = run;
      }
    }
    return latest;
  }

  Future<void> _confirmDeleteCurrent(ChatConversation conversation) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('删除会话？'),
        content: Text('“${conversation.displayName}”及其消息将从列表中删除。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final chat = widget.chat!;
    if (conversation.activeRun != null) {
      if (conversation.activeRun!.runId == null) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('创建结果尚未确认，请先恢复本轮状态再删除。')));
        return;
      }
      await chat.cancel(conversation.sessionId);
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('已请求停止，请等待本轮进入终态后再删除。')));
      return;
    }
    final deleted = await chat.deleteSession(conversation.sessionId);
    if (!mounted || deleted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('删除失败，会话仍保留在历史列表中。')));
  }

  void _saveCurrentDraft() {
    final chat = widget.chat;
    final id = _chatSessionId;
    if (chat == null || id == null) return;
    if (chat.conversations.any(
      (conversation) => conversation.sessionId == id,
    )) {
      chat.updateDraft(id, _draftController.text);
    }
  }

  Future<void> _startNewDraft() async {
    if (_draftController.text.isNotEmpty) {
      final discard = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('开始新会话？'),
          content: const Text('演示页面只保留一个临时草稿，开始新会话会清空它。'),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('继续编辑'),
            ),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(true),
              child: const Text('清空草稿'),
            ),
          ],
        ),
      );
      if (discard != true || !mounted) return;
    }
    widget.model.startChatDraft();
    _draftController.clear();
  }
}

class _ChatPaper extends StatelessWidget {
  const _ChatPaper({
    required this.child,
    required this.right,
    this.compact = false,
  });

  final Widget child;
  final bool right;
  final bool compact;

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      gradient: LinearGradient(
        colors: right
            ? const [
                Color.fromARGB(255, 229, 220, 198),
                NotebookTheme.paper,
                Color.fromARGB(255, 255, 250, 240),
              ]
            : const [
                Color.fromARGB(255, 255, 250, 240),
                NotebookTheme.paper,
                Color.fromARGB(255, 229, 220, 198),
              ],
        stops: right ? const [0, .055, 1] : const [0, .945, 1],
      ),
    ),
    child: Padding(
      padding: compact
          ? const EdgeInsets.fromLTRB(20, 12, 20, 10)
          : const EdgeInsets.fromLTRB(24, 20, 24, 16),
      child: child,
    ),
  );
}

class _PageTitle extends StatelessWidget {
  const _PageTitle({required this.title, required this.subtitle});

  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      const Text(
        'A LITTLE EVERYDAY',
        style: TextStyle(fontSize: 10, letterSpacing: 2.4),
      ),
      const SizedBox(height: 8),
      Text(title, style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 6),
      Text(subtitle, style: const TextStyle(fontSize: 12)),
      const SizedBox(height: 12),
      const Divider(),
    ],
  );
}

class _InlineError extends StatelessWidget {
  const _InlineError({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) => Row(
    children: [
      Expanded(child: SelectableText(message)),
      TextButton(onPressed: onRetry, child: const Text('重试')),
    ],
  );
}

List<ChatRunState> _oldestFirst(List<ChatRunState> runs) {
  final indexed = runs.indexed.toList()
    ..sort((left, right) {
      final byTime = left.$2.createdAt.compareTo(right.$2.createdAt);
      return byTime == 0 ? left.$1.compareTo(right.$1) : byTime;
    });
  return indexed.map((entry) => entry.$2).toList(growable: false);
}
