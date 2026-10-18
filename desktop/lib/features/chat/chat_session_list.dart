import 'package:flutter/material.dart';

import '../../theme/notebook_theme.dart';
import 'chat_view_model.dart';

/// 展示可搜索、分组和管理的服务端历史会话。
class ChatSessionList extends StatefulWidget {
  const ChatSessionList({
    super.key,
    required this.chat,
    required this.selectedSessionId,
    required this.onSelect,
  });

  final ChatViewModel chat;
  final String? selectedSessionId;
  final ValueChanged<String> onSelect;

  @override
  State<ChatSessionList> createState() => _ChatSessionListState();
}

class _ChatSessionListState extends State<ChatSessionList> {
  final TextEditingController _searchController = TextEditingController();
  String _query = '';
  String _historySignature = '';

  @override
  void initState() {
    super.initState();
    _historySignature = _signature();
    widget.chat.historyListenable.addListener(_onChatChanged);
  }

  @override
  void didUpdateWidget(covariant ChatSessionList oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.chat != widget.chat) {
      oldWidget.chat.historyListenable.removeListener(_onChatChanged);
      widget.chat.historyListenable.addListener(_onChatChanged);
      _historySignature = _signature();
    }
  }

  void _onChatChanged() {
    final next = _signature();
    if (next == _historySignature || !mounted) return;
    setState(() => _historySignature = next);
  }

  String _signature() => [
    widget.chat.historyLoading,
    widget.chat.historyLoaded,
    widget.chat.acceptingOperations,
    widget.chat.runtimeError,
    widget.chat.historyError,
    widget.chat.historyCompletedCount,
    widget.chat.historyTotalCount,
    widget.chat.historyHasFailures,
    widget.chat.selectedSessionId,
    for (final conversation in widget.chat.conversations.where(
      (item) => item.isLocal && item.draft.trim().isNotEmpty,
    ))
      '${conversation.sessionId}:${conversation.draft}',
    for (final conversation in widget.chat.historyConversations)
      '${conversation.sessionId}:${conversation.displayName}:${conversation.summary}:${conversation.latestCreatedAt.microsecondsSinceEpoch}:${conversation.historyLoaded}:${conversation.isDeleting}:${conversation.error}:${conversation.activeRun != null}:${_latestRun(conversation)?.phase}',
  ].join('|');

  @override
  void dispose() {
    widget.chat.historyListenable.removeListener(_onChatChanged);
    _searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final chat = widget.chat;
    final all = chat.historyConversations;
    final localDrafts = chat.conversations
        .where((item) => item.isLocal && item.draft.trim().isNotEmpty)
        .toList();
    final query = _query.trim().toLowerCase();
    final visible = query.isEmpty
        ? all
        : all
              .where(
                (conversation) =>
                    conversation.name.toLowerCase().contains(query) ||
                    conversation.summary.toLowerCase().contains(query),
              )
              .toList();
    final searchComplete =
        chat.historyLoaded &&
        !chat.historyLoading &&
        chat.historyError == null &&
        !chat.historyHasFailures;
    final groups = _groupByBeijingDay(visible);
    final header = <Widget>[
      Row(
        children: [
          const Expanded(
            child: _PageTitle(title: '历史会话', subtitle: '按最近对话整理，可搜索名称和摘要。'),
          ),
          IconButton(
            onPressed: chat.acceptingOperations && !chat.historyLoading
                ? () => chat.loadHistory(refresh: true)
                : null,
            tooltip: '刷新历史',
            icon: const Icon(Icons.refresh),
          ),
          IconButton.filledTonal(
            onPressed: _startNewSession,
            tooltip: '新会话',
            icon: const Icon(Icons.add),
          ),
        ],
      ),
      TextField(
        controller: _searchController,
        onChanged: (value) => setState(() => _query = value),
        textInputAction: TextInputAction.search,
        decoration: InputDecoration(
          prefixIcon: const Icon(Icons.search),
          hintText: '搜索名称或摘要',
          isDense: true,
          suffixIcon: _query.isEmpty
              ? null
              : IconButton(
                  tooltip: '清空搜索',
                  onPressed: () {
                    _searchController.clear();
                    setState(() => _query = '');
                  },
                  icon: const Icon(Icons.clear),
                ),
        ),
      ),
      if (chat.historyLoading) ...[
        const SizedBox(height: 10),
        LinearProgressIndicator(
          value: chat.historyTotalCount == 0
              ? null
              : chat.historyCompletedCount / chat.historyTotalCount,
          minHeight: 3,
        ),
        const SizedBox(height: 5),
        Text(
          '正在整理历史 ${chat.historyCompletedCount}/${chat.historyTotalCount}',
          style: const TextStyle(fontSize: 12, color: NotebookTheme.ink),
        ),
      ],
      if (chat.historyError != null)
        _InlineHistoryNotice(
          message: '历史读取失败：${chat.historyError}',
          actionLabel: '重试',
          onPressed: () => chat.loadHistory(refresh: true),
        ),
      if (chat.historyHasFailures)
        _InlineHistoryNotice(
          message: '部分会话读取失败。',
          actionLabel: '重试失败项',
          onPressed: () => chat.loadHistory(refresh: true),
        ),
      if (chat.runtimeError != null)
        _InlineHistoryNotice(
          message: '聊天服务不可用：${chat.runtimeError}',
          actionLabel: '重试',
          onPressed: () => chat.loadHistory(refresh: true),
        ),
      const SizedBox(height: 8),
    ];
    final contents = all.isEmpty && localDrafts.isEmpty && chat.historyLoading
        ? const Center(child: CircularProgressIndicator())
        : all.isEmpty && localDrafts.isEmpty && chat.historyError != null
        ? const Center(child: Text('历史暂时无法读取，请重试。'))
        : all.isEmpty && localDrafts.isEmpty && chat.historyLoaded
        ? Center(
            child: Text(
              query.isNotEmpty && searchComplete
                  ? '没有找到相关会话。'
                  : query.isNotEmpty
                  ? '历史整理完成后再判断搜索结果。'
                  : '还没有历史会话。',
            ),
          )
        : ListView(
            padding: const EdgeInsets.only(bottom: 12),
            children: [
              if (localDrafts.isNotEmpty) ...[
                const Padding(
                  padding: EdgeInsets.fromLTRB(4, 12, 4, 5),
                  child: Text(
                    '未发送草稿',
                    style: TextStyle(
                      fontWeight: FontWeight.w600,
                      color: NotebookTheme.ink,
                    ),
                  ),
                ),
                for (final draft in localDrafts)
                  _HistoryTile(
                    conversation: draft,
                    selected: draft.sessionId == chat.selectedSessionId,
                    onSelect: () => widget.onSelect(draft.sessionId),
                    title: '新会话草稿',
                    subtitle: draft.draft,
                  ),
              ],
              for (final entry in groups.entries) ...[
                Padding(
                  padding: const EdgeInsets.fromLTRB(4, 12, 4, 5),
                  child: Text(
                    entry.key,
                    style: const TextStyle(
                      fontWeight: FontWeight.w600,
                      color: NotebookTheme.ink,
                    ),
                  ),
                ),
                for (final conversation in entry.value)
                  _HistoryTile(
                    conversation: conversation,
                    selected: conversation.sessionId == chat.selectedSessionId,
                    onSelect: () => widget.onSelect(conversation.sessionId),
                    onDelete: () => _confirmDelete(conversation),
                  ),
              ],
              if (all.isNotEmpty && visible.isEmpty && searchComplete)
                const Padding(
                  padding: EdgeInsets.all(18),
                  child: Center(child: Text('没有找到相关会话。')),
                ),
              if (all.isNotEmpty && visible.isEmpty && !searchComplete)
                const Padding(
                  padding: EdgeInsets.all(18),
                  child: Center(child: Text('历史整理完成后再判断搜索结果。')),
                ),
              if (!chat.historyLoaded && !chat.historyLoading)
                Align(
                  alignment: Alignment.center,
                  child: TextButton.icon(
                    onPressed: () => chat.loadHistory(),
                    icon: const Icon(Icons.refresh),
                    label: const Text('继续整理历史'),
                  ),
                ),
            ],
          );
    return LayoutBuilder(
      builder: (context, constraints) {
        final compactHeader =
            constraints.maxHeight < 420 ||
            MediaQuery.textScalerOf(context).scale(14) > 20;
        if (compactHeader) {
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              ConstrainedBox(
                constraints: BoxConstraints(
                  maxHeight: (constraints.maxHeight * .48).clamp(0.0, 220.0),
                ),
                child: SingleChildScrollView(
                  primary: false,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: header,
                  ),
                ),
              ),
              Expanded(child: contents),
            ],
          );
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            ...header,
            Expanded(child: contents),
          ],
        );
      },
    );
  }

  void _startNewSession() {
    widget.chat.createLocalSession();
    widget.onSelect(widget.chat.selectedSessionId!);
    setState(() => _historySignature = _signature());
  }

  Future<void> _confirmDelete(ChatConversation conversation) async {
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
    if (conversation.activeRun != null) {
      if (conversation.activeRun!.runId == null) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('创建结果尚未确认，请先恢复本轮状态再删除。')));
        return;
      }
      await widget.chat.cancel(conversation.sessionId);
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('已请求停止，请等待本轮进入终态后再删除。')));
      return;
    }
    final deleted = await widget.chat.deleteSession(conversation.sessionId);
    if (!mounted || deleted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('删除失败，会话仍保留在历史列表中。')));
  }
}

class _HistoryTile extends StatelessWidget {
  const _HistoryTile({
    required this.conversation,
    required this.selected,
    required this.onSelect,
    this.onDelete,
    this.title,
    this.subtitle,
  });

  final ChatConversation conversation;
  final bool selected;
  final VoidCallback onSelect;
  final VoidCallback? onDelete;
  final String? title;
  final String? subtitle;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    selected: selected,
    label: title ?? conversation.displayName,
    child: Material(
      color: selected ? const Color.fromARGB(255, 242, 235, 218) : null,
      borderRadius: BorderRadius.circular(8),
      child: InkWell(
        onTap: onSelect,
        borderRadius: BorderRadius.circular(8),
        child: Container(
          constraints: const BoxConstraints(minHeight: 58),
          decoration: BoxDecoration(
            border: Border(
              left: BorderSide(
                color: selected ? NotebookTheme.cover : Colors.transparent,
                width: 3,
              ),
            ),
          ),
          padding: const EdgeInsets.fromLTRB(10, 6, 2, 6),
          child: Row(
            children: [
              Expanded(
                child: Tooltip(
                  message: title ?? conversation.displayName,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        title ?? conversation.displayName,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                      if ((subtitle ?? conversation.summary).isNotEmpty)
                        Text(
                          subtitle ?? conversation.summary,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            fontSize: 12,
                            color: NotebookTheme.ink,
                          ),
                        ),
                      if (title == null)
                        Text(
                          '${_historyStatus(conversation)} · ${_formatBeijingDateTime(conversation.latestCreatedAt)}',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            fontSize: 11,
                            color: NotebookTheme.ink,
                          ),
                        ),
                      if (title == null && !conversation.historyLoaded)
                        const Text('会话内容尚未整理', style: TextStyle(fontSize: 12)),
                      if (title == null && conversation.error != null)
                        Text(
                          '读取或删除失败：${conversation.error}',
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(fontSize: 12),
                        ),
                    ],
                  ),
                ),
              ),
              if (onDelete != null)
                IconButton(
                  tooltip: conversation.isDeleting
                      ? '正在删除'
                      : conversation.historyLoaded
                      ? '删除会话'
                      : '会话详情读取中',
                  onPressed:
                      conversation.isDeleting || !conversation.historyLoaded
                      ? null
                      : onDelete,
                  icon: conversation.isDeleting
                      ? const SizedBox.square(
                          dimension: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.delete_outline),
                ),
            ],
          ),
        ),
      ),
    ),
  );
}

String _historyStatus(ChatConversation conversation) {
  final active = conversation.activeRun;
  if (active != null) return '正在回复';
  if (conversation.runs.isEmpty) return '尚无消息';
  return switch (_latestRun(conversation)?.phase) {
    ChatRunPhase.failed => '回复失败',
    ChatRunPhase.cancelled => '已停止',
    _ => '回复完成',
  };
}

ChatRunState? _latestRun(ChatConversation conversation) {
  ChatRunState? latest;
  for (final run in conversation.runs) {
    if (latest == null || run.createdAt.isAfter(latest.createdAt)) latest = run;
  }
  return latest;
}

String _formatBeijingDateTime(DateTime value) {
  final time = value.toUtc().add(const Duration(hours: 8));
  String twoDigits(int number) => number.toString().padLeft(2, '0');
  return '${time.year}-${twoDigits(time.month)}-${twoDigits(time.day)} ${twoDigits(time.hour)}:${twoDigits(time.minute)}';
}

class _InlineHistoryNotice extends StatelessWidget {
  const _InlineHistoryNotice({
    required this.message,
    required this.actionLabel,
    required this.onPressed,
  });

  final String message;
  final String actionLabel;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 6),
    child: Row(
      children: [
        Expanded(child: Text(message, style: const TextStyle(fontSize: 12))),
        TextButton(onPressed: onPressed, child: Text(actionLabel)),
      ],
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
      const SizedBox(height: 4),
      Text(title, style: Theme.of(context).textTheme.titleLarge),
      Text(subtitle, style: const TextStyle(fontSize: 12)),
      const SizedBox(height: 8),
      const Divider(),
    ],
  );
}

Map<String, List<ChatConversation>> _groupByBeijingDay(
  List<ChatConversation> conversations,
) {
  final now = DateTime.now().toUtc().add(const Duration(hours: 8));
  final today = DateTime(now.year, now.month, now.day);
  final yesterday = today.subtract(const Duration(days: 1));
  final groups = <String, List<ChatConversation>>{};
  for (final conversation in conversations) {
    final created = conversation.latestCreatedAt.toUtc().add(
      const Duration(hours: 8),
    );
    final day = DateTime(created.year, created.month, created.day);
    final title = day == today
        ? '今天'
        : day == yesterday
        ? '昨天'
        : '更早';
    groups.putIfAbsent(title, () => []).add(conversation);
  }
  return groups;
}
