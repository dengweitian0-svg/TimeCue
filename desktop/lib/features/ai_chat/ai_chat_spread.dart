import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../contracts/ai_chat.dart';
import '../../theme/notebook_theme.dart';
import 'ai_chat_model.dart';

/// 嵌入日程本的 AI 历史会话与对话双页。
class AiChatSpread extends StatefulWidget {
  const AiChatSpread({super.key, required this.model});

  final AiChatModel model;

  @override
  State<AiChatSpread> createState() => _AiChatSpreadState();
}

class _AiChatSpreadState extends State<AiChatSpread> {
  static const _ink = NotebookTheme.ink;
  static const _muted = NotebookTheme.muted;
  static const _brown = NotebookTheme.cover;
  static const _line = NotebookTheme.line;
  final _searchController = TextEditingController();
  final _inputControllers = <String, TextEditingController>{};
  final _scrollControllers = <String, ScrollController>{};
  final _followTail = <String, bool>{};
  final _thinkingExpanded = <String, bool>{};
  final _initialFocus = FocusNode();
  bool _startedRefresh = false;
  bool _selectionScheduled = false;
  String _query = '';

  @override
  void initState() {
    super.initState();
    widget.model.addListener(_onModelChanged);
    _refreshIfNeeded();
  }

  @override
  void didUpdateWidget(covariant AiChatSpread oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.model != widget.model) {
      oldWidget.model.removeListener(_onModelChanged);
      widget.model.addListener(_onModelChanged);
      _startedRefresh = false;
      _refreshIfNeeded();
    }
  }

  @override
  void dispose() {
    widget.model.removeListener(_onModelChanged);
    _searchController.dispose();
    _initialFocus.dispose();
    for (final controller in _inputControllers.values) {
      controller.dispose();
    }
    for (final controller in _scrollControllers.values) {
      controller.dispose();
    }
    super.dispose();
  }

  void _refreshIfNeeded() {
    final model = widget.model;
    if (_startedRefresh) return;
    _startedRefresh = true;
    if (!model.synchronized && !model.loading) unawaited(_refresh(model));
  }

  void _onModelChanged() {
    if (!mounted) return;
    _refreshIfNeeded();
    final selected = widget.model.selectedSession;
    if (selected != null) {
      final controller = _controllerFor(selected.id);
      if (controller.text != selected.draft) {
        controller.value = TextEditingValue(
          text: selected.draft,
          selection: TextSelection.collapsed(offset: selected.draft.length),
        );
      }
      if (_followTail[selected.id] != false) {
        WidgetsBinding.instance.addPostFrameCallback(
          (_) => _scrollToTail(selected.id),
        );
      }
    }
    setState(() {});
  }

  TextEditingController _controllerFor(String id) =>
      _inputControllers.putIfAbsent(
        id,
        () => TextEditingController(text: widget.model.draftFor(id)),
      );

  ScrollController _scrollControllerFor(String id) =>
      _scrollControllers.putIfAbsent(id, () {
        final controller = ScrollController();
        controller.addListener(() {
          if (!controller.hasClients) return;
          _followTail[id] =
              controller.position.maxScrollExtent - controller.position.pixels <
              48;
        });
        return controller;
      });

  void _scrollToTail(String id) {
    final controller = _scrollControllers[id];
    if (controller == null || !controller.hasClients) return;
    controller.animateTo(
      controller.position.maxScrollExtent,
      duration: const Duration(milliseconds: 180),
      curve: Curves.easeOut,
    );
  }

  void _createSession() {
    if (!widget.model.synchronized || widget.model.loading) return;
    final session = widget.model.createSession();
    _controllerFor(session.id).clear();
    _followTail[session.id] = true;
  }

  Future<void> _retry(AiChatModel model, String sessionId) async {
    if (!model.synchronized || model.loading) return;
    try {
      await model.retry(sessionId);
    } on StateError {
      // 应用关闭对话服务后，旧页面可能仍短暂可见。
    }
  }

  Future<void> _refresh(AiChatModel model) async {
    if (model.loading) return;
    try {
      await model.refresh();
    } on StateError {
      // 应用关闭对话服务后，不继续向已失效的 model 发请求。
    }
  }

  void _ensureEmptySession() {
    final model = widget.model;
    if (model.synchronized &&
        model.selectedSessionId == null &&
        !_selectionScheduled) {
      if (model.sessions.isNotEmpty) {
        _selectionScheduled = true;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          _selectionScheduled = false;
          if (mounted &&
              identical(model, widget.model) &&
              model.synchronized &&
              model.selectedSessionId == null &&
              model.sessions.isNotEmpty) {
            model.selectSession(model.sessions.first.id);
          }
        });
      } else if (model.error == null) {
        _selectionScheduled = true;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          _selectionScheduled = false;
          if (mounted &&
              model.sessions.isEmpty &&
              model.selectedSessionId == null) {
            _createSession();
          }
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final model = widget.model;
    _ensureEmptySession();
    return LayoutBuilder(
      builder: (context, constraints) {
        final compact =
            constraints.maxHeight < 410 ||
            MediaQuery.textScalerOf(context).scale(16) > 24;
        return DecoratedBox(
          decoration: const BoxDecoration(color: NotebookTheme.paper),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Expanded(child: _buildHistoryPage(model, compact)),
              SizedBox(
                width: 18,
                child: DecoratedBox(
                  decoration: const BoxDecoration(
                    gradient: LinearGradient(
                      colors: [
                        Color.fromARGB(255, 232, 224, 205),
                        Color.fromARGB(255, 199, 174, 142),
                        Color.fromARGB(255, 232, 224, 205),
                      ],
                    ),
                  ),
                ),
              ),
              Expanded(child: _buildConversationPage(model, compact)),
            ],
          ),
        );
      },
    );
  }

  Widget _buildHistoryPage(AiChatModel model, bool compact) {
    final sessions = model.sessions.where((session) {
      final query = _query.trim().toLowerCase();
      if (query.isEmpty) return true;
      return session.title.toLowerCase().contains(query) ||
          session.turns.lastOrNull?.text.toLowerCase().contains(query) == true;
    }).toList();
    final groups = _groupSessions(sessions);
    final content = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _historyHeader(model, compact),
        Padding(
          padding: const EdgeInsets.fromLTRB(18, 14, 18, 10),
          child: TextField(
            controller: _searchController,
            onChanged: (value) => setState(() => _query = value),
            decoration: _inputDecoration(
              '搜索历史会话',
              prefix: const Icon(Icons.search, size: 20),
            ),
          ),
        ),
        if (model.loading)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 22),
            child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
          )
        else if (model.error != null && !model.synchronized)
          _loadError(model)
        else if (model.sessions.isEmpty)
          _emptyHistory()
        else if (sessions.isEmpty)
          const Padding(
            padding: EdgeInsets.all(24),
            child: Center(
              child: Text('没有匹配的会话', style: TextStyle(color: _muted)),
            ),
          )
        else if (compact)
          ListView(
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            padding: const EdgeInsets.fromLTRB(18, 0, 18, 20),
            children: [
              for (final group in groups.entries) ...[
                _groupHeading(group.key),
                for (final session in group.value) _historyItem(model, session),
              ],
            ],
          )
        else
          Expanded(
            child: ListView(
              padding: const EdgeInsets.fromLTRB(18, 0, 18, 20),
              children: [
                for (final group in groups.entries) ...[
                  _groupHeading(group.key),
                  for (final session in group.value)
                    _historyItem(model, session),
                ],
              ],
            ),
          ),
      ],
    );
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 20, 8, 14),
      child: compact ? SingleChildScrollView(child: content) : content,
    );
  }

  Widget _historyHeader(AiChatModel model, bool compact) => Padding(
    padding: const EdgeInsets.fromLTRB(2, 0, 10, 0),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '历史会话',
                style: TextStyle(
                  fontFamily: 'SimSun',
                  fontSize: compact ? 22 : 28,
                  color: _ink,
                ),
              ),
              const SizedBox(height: 4),
              const Text(
                '从上次的话题继续',
                style: TextStyle(color: _muted, fontSize: 14),
              ),
            ],
          ),
        ),
        TextButton.icon(
          onPressed: model.synchronized && !model.loading
              ? _createSession
              : null,
          icon: const Icon(Icons.add, size: 19),
          label: const Text('新会话'),
          style: TextButton.styleFrom(
            foregroundColor: Colors.white,
            backgroundColor: _brown,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(7),
            ),
          ),
        ),
      ],
    ),
  );

  Widget _loadError(AiChatModel model) => Padding(
    padding: const EdgeInsets.all(24),
    child: Column(
      children: [
        const Icon(Icons.cloud_off_outlined, color: _muted),
        const SizedBox(height: 8),
        Text(
          model.error ?? '会话加载失败',
          textAlign: TextAlign.center,
          style: const TextStyle(color: _muted),
        ),
        const SizedBox(height: 10),
        OutlinedButton.icon(
          onPressed: model.loading ? null : () => _refresh(model),
          icon: const Icon(Icons.refresh, size: 18),
          label: const Text('重试加载'),
        ),
      ],
    ),
  );

  Widget _emptyHistory() => const Center(
    child: Padding(
      padding: EdgeInsets.all(24),
      child: Text(
        '这里还没有历史会话\n新建会话后即可开始',
        textAlign: TextAlign.center,
        style: TextStyle(color: _muted, height: 1.6),
      ),
    ),
  );

  Map<String, List<AiChatSession>> _groupSessions(
    List<AiChatSession> sessions,
  ) {
    final today = DateTime.now().toUtc().add(const Duration(hours: 8));
    final groups = <String, List<AiChatSession>>{};
    for (final session in sessions) {
      final at = session.lastActivityAt?.toUtc().add(const Duration(hours: 8));
      final title = at == null || _sameDate(at, today)
          ? '今天'
          : _sameDate(at, today.subtract(const Duration(days: 1)))
          ? '昨天'
          : '更早';
      groups.putIfAbsent(title, () => []).add(session);
    }
    return groups;
  }

  bool _sameDate(DateTime a, DateTime b) =>
      a.year == b.year && a.month == b.month && a.day == b.day;

  Widget _groupHeading(String title) => Padding(
    padding: const EdgeInsets.only(top: 10, bottom: 7),
    child: Row(
      children: [
        Text(
          title,
          style: const TextStyle(color: _brown, fontWeight: FontWeight.w600),
        ),
        const SizedBox(width: 10),
        const Expanded(child: Divider(height: 1, color: _line)),
      ],
    ),
  );

  Widget _historyItem(AiChatModel model, AiChatSession session) {
    final selected = model.selectedSessionId == session.id;
    final turn = session.turns.lastOrNull;
    final summary = turn?.text.trim().isNotEmpty == true
        ? turn!.text.trim()
        : turn?.input ?? (session.creating ? '正在开始对话…' : '还没有消息');
    final at = session.lastActivityAt?.toUtc().add(const Duration(hours: 8));
    return InkWell(
      key: ValueKey('history-${session.id}'),
      onTap: () => model.selectSession(session.id),
      borderRadius: BorderRadius.circular(8),
      child: Container(
        margin: const EdgeInsets.only(bottom: 4),
        padding: const EdgeInsets.fromLTRB(10, 10, 8, 10),
        decoration: BoxDecoration(
          color: selected
              ? const Color.fromARGB(255, 242, 235, 218)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(8),
        ),
        child: Row(
          children: [
            Container(
              width: 4,
              height: 42,
              decoration: BoxDecoration(
                color: selected ? _brown : Colors.transparent,
                borderRadius: BorderRadius.circular(4),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    session.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      fontFamily: 'SimSun',
                      fontSize: 17,
                      color: _ink,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    summary,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(color: _muted, fontSize: 13),
                  ),
                ],
              ),
            ),
            const SizedBox(width: 6),
            Text(
              at == null ? '' : _historyTime(at),
              style: const TextStyle(color: _muted, fontSize: 12),
            ),
            const Icon(Icons.chevron_right, color: _muted, size: 20),
          ],
        ),
      ),
    );
  }

  String _historyTime(DateTime beijing) {
    final today = DateTime.now().toUtc().add(const Duration(hours: 8));
    if (_sameDate(beijing, today)) {
      return '${beijing.hour.toString().padLeft(2, '0')}:${beijing.minute.toString().padLeft(2, '0')}';
    }
    return '${beijing.month}月${beijing.day}日';
  }

  Widget _buildConversationPage(AiChatModel model, bool compact) {
    final session = model.selectedSession;
    if (session == null) {
      return Padding(
        padding: const EdgeInsets.all(28),
        child: Center(
          child: Text(
            model.sessions.isEmpty ? '选择或新建一个会话' : '从左侧选择会话继续',
            style: const TextStyle(color: _muted),
          ),
        ),
      );
    }
    final body = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _conversationHeader(session),
        const Divider(height: 18, color: _line),
        if (!compact)
          Expanded(child: _messages(model, session))
        else
          _messages(model, session, compact: true),
        if (session.error != null) _sessionError(context, session),
        _composer(model, session),
      ],
    );
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 20, 22, 14),
      child: compact ? SingleChildScrollView(child: body) : body,
    );
  }

  Widget _conversationHeader(AiChatSession session) => Row(
    children: [
      Expanded(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              session.title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                fontFamily: 'SimSun',
                fontSize: 25,
                color: _ink,
              ),
            ),
            const SizedBox(height: 5),
            Text(
              'AI 助手 · ${session.turns.lastOrNull?.model ?? AiChatModel.defaultModel} · ${_sessionStatus(session)}',
              style: const TextStyle(color: _muted, fontSize: 13),
            ),
          ],
        ),
      ),
      PopupMenuButton<String>(
        tooltip: '更多会话操作',
        icon: const Icon(Icons.more_horiz, color: _brown),
        onSelected: (value) async {
          if (value != 'delete') return;
          final confirmed = await showDialog<bool>(
            context: context,
            builder: (context) => AlertDialog(
              title: const Text('删除会话？'),
              content: Text('“${session.title}”及其对话记录将被删除。'),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(context, false),
                  child: const Text('取消'),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(context, true),
                  child: const Text('删除'),
                ),
              ],
            ),
          );
          if (confirmed == true) await widget.model.deleteSession(session.id);
        },
        itemBuilder: (_) => const [
          PopupMenuItem(value: 'delete', child: Text('删除会话')),
        ],
      ),
    ],
  );

  String _sessionStatus(AiChatSession session) {
    if (session.creating) return '正在开始';
    if (session.createResultUnknown) return '等待确认';
    if (session.createRetryable) return '等待会话停止';
    final turn = session.turns.lastOrNull;
    if (turn == null) return '新会话';
    if (turn.status == AiChatStatus.running) {
      if (turn.cancelling) return '正在取消';
      return switch (turn.connectionState) {
        AiChatConnectionState.connecting => '连接中',
        AiChatConnectionState.reconnecting => '正在重连',
        AiChatConnectionState.disconnected => '连接中断',
        _ => '正在回复',
      };
    }
    return switch (turn.status) {
      AiChatStatus.succeeded => '已完成',
      AiChatStatus.failed => '回复失败',
      AiChatStatus.cancelled => '已停止',
      AiChatStatus.running => '正在回复',
    };
  }

  Widget _messages(
    AiChatModel model,
    AiChatSession session, {
    bool compact = false,
  }) {
    if (session.turns.isEmpty) {
      return const Center(
        child: Text('写下你的想法，开始一段对话。', style: TextStyle(color: _muted)),
      );
    }
    final scroll = _scrollControllerFor(session.id);
    if (compact) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final turn in session.turns) _turnView(model, session, turn),
        ],
      );
    }
    return ListView.builder(
      key: PageStorageKey('messages-${session.id}'),
      controller: scroll,
      padding: const EdgeInsets.fromLTRB(8, 6, 8, 18),
      itemCount: session.turns.length,
      itemBuilder: (context, index) =>
          _turnView(model, session, session.turns[index]),
    );
  }

  Widget _turnView(AiChatModel model, AiChatSession session, AiChatTurn turn) =>
      Padding(
        padding: const EdgeInsets.only(bottom: 18),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Align(
              alignment: Alignment.centerRight,
              child: _bubble(turn.input, user: true),
            ),
            if (turn.thinking.isNotEmpty)
              Align(
                alignment: Alignment.centerLeft,
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 440),
                  child: Card(
                    elevation: 0,
                    color: const Color.fromARGB(255, 241, 237, 226),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: ExpansionTile(
                      key: ValueKey('thinking-${session.id}-${turn.runId}'),
                      initiallyExpanded: _thinkingExpanded[turn.runId] ?? false,
                      onExpansionChanged: (value) =>
                          _thinkingExpanded[turn.runId] = value,
                      tilePadding: const EdgeInsets.symmetric(horizontal: 12),
                      childrenPadding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
                      title: const Text(
                        '思考过程',
                        style: TextStyle(color: _muted, fontSize: 13),
                      ),
                      children: [
                        Align(
                          alignment: Alignment.centerLeft,
                          child: SelectableText(
                            turn.thinking,
                            style: const TextStyle(color: _muted, height: 1.5),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            if (turn.text.isNotEmpty)
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Flexible(child: _bubble(turn.text, user: false)),
                  if (turn.status == AiChatStatus.succeeded) ...[
                    const SizedBox(width: 4),
                    IconButton(
                      tooltip: '复制回复',
                      visualDensity: VisualDensity.compact,
                      onPressed: () =>
                          Clipboard.setData(ClipboardData(text: turn.text)),
                      icon: const Icon(
                        Icons.copy_rounded,
                        size: 16,
                        color: _muted,
                      ),
                    ),
                  ],
                ],
              ),
            if (turn.loading && turn.text.isEmpty && turn.thinking.isEmpty)
              const Align(
                alignment: Alignment.centerLeft,
                child: Padding(
                  padding: EdgeInsets.all(12),
                  child: SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                ),
              ),
            if (turn.error != null || turn.status == AiChatStatus.failed)
              _turnError(context, model, session, turn),
            if (turn.status == AiChatStatus.running && turn.retryable)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: () => _retry(model, session.id),
                  icon: const Icon(Icons.refresh, size: 17),
                  label: const Text('重新连接'),
                ),
              ),
            if (turn.status == AiChatStatus.running)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: turn.cancelling
                      ? null
                      : () => model.cancel(session.id, turn.runId),
                  icon: const Icon(Icons.stop_circle_outlined, size: 17),
                  label: Text(turn.cancelling ? '正在取消…' : '停止回复'),
                ),
              ),
          ],
        ),
      );

  Widget _bubble(String value, {required bool user}) => ConstrainedBox(
    constraints: const BoxConstraints(maxWidth: 440),
    child: Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: user
            ? const Color.fromARGB(255, 242, 235, 218)
            : const Color.fromARGB(255, 241, 237, 226),
        borderRadius: BorderRadius.circular(13),
      ),
      child: SelectableText(
        value,
        style: const TextStyle(color: _ink, height: 1.55),
      ),
    ),
  );

  Widget _turnError(
    BuildContext context,
    AiChatModel model,
    AiChatSession session,
    AiChatTurn turn,
  ) {
    final colors = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(11),
      decoration: BoxDecoration(
        color: colors.errorContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Icon(Icons.info_outline, size: 18, color: colors.error),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              turn.error?.message ?? '这次回复没有完成。',
              style: TextStyle(color: colors.onErrorContainer),
            ),
          ),
          if (turn.retryable)
            TextButton(
              onPressed: () => _retry(model, session.id),
              child: const Text('重试'),
            ),
        ],
      ),
    );
  }

  Widget _sessionError(BuildContext context, AiChatSession session) {
    final colors = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.fromLTRB(4, 4, 4, 2),
      padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 8),
      decoration: BoxDecoration(
        color: colors.errorContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Icon(Icons.info_outline, size: 18, color: colors.error),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              session.error!,
              style: TextStyle(color: colors.onErrorContainer),
            ),
          ),
          if (!session.createResultUnknown &&
              !session.createRetryable &&
              !session.creating &&
              session.turns.isEmpty)
            Text(
              '可修改后重新发送',
              style: TextStyle(color: colors.onErrorContainer, fontSize: 12),
            ),
        ],
      ),
    );
  }

  Widget _composer(AiChatModel model, AiChatSession session) {
    final controller = _controllerFor(session.id);
    final reason = model.sendDisabledReasonFor(session.id);
    final latest = session.turns.lastOrNull;
    final running = latest?.status == AiChatStatus.running;
    final canSend = reason == null && controller.text.trim().isNotEmpty;
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Focus(
            onKeyEvent: (node, event) {
              if (event is! KeyDownEvent ||
                  event.logicalKey != LogicalKeyboardKey.enter) {
                return KeyEventResult.ignored;
              }
              if (HardwareKeyboard.instance.isShiftPressed) {
                return KeyEventResult.ignored;
              }
              if (!controller.value.composing.isCollapsed) {
                return KeyEventResult.ignored;
              }
              _submit(model, session, controller);
              return KeyEventResult.handled;
            },
            child: TextField(
              key: ValueKey('composer-${session.id}'),
              controller: controller,
              focusNode: _initialFocus,
              readOnly:
                  session.createResultUnknown ||
                  session.createRetryable ||
                  session.creating,
              minLines: 2,
              maxLines: 5,
              keyboardType: TextInputType.multiline,
              textInputAction: TextInputAction.newline,
              onChanged: (value) {
                model.setDraft(session.id, value);
                setState(() {});
              },
              decoration: _inputDecoration('写下你的想法…').copyWith(
                suffixIcon: Padding(
                  padding: const EdgeInsets.all(5),
                  child: IconButton.filled(
                    tooltip: running ? '停止回复' : '发送',
                    onPressed: running
                        ? (latest!.cancelling
                              ? null
                              : () => model.cancel(session.id, latest.runId))
                        : canSend
                        ? () => _submit(model, session, controller)
                        : null,
                    icon: Icon(
                      running ? Icons.stop_rounded : Icons.send_rounded,
                      size: 18,
                    ),
                    style: IconButton.styleFrom(
                      backgroundColor: _brown,
                      foregroundColor: Colors.white,
                    ),
                  ),
                ),
              ),
            ),
          ),
          if (session.createResultUnknown)
            Padding(
              padding: const EdgeInsets.only(top: 4, left: 6),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      reason ?? '上次提交结果未确认。',
                      style: const TextStyle(color: _muted, fontSize: 12),
                    ),
                  ),
                  TextButton(
                    onPressed: session.creating
                        ? null
                        : () => _retry(model, session.id),
                    child: Text(session.creating ? '正在重试…' : '重试原内容'),
                  ),
                ],
              ),
            )
          else if (session.createRetryable)
            Padding(
              padding: const EdgeInsets.only(top: 4, left: 6),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      reason ?? '会话正在停止，请稍后重试原内容。',
                      style: const TextStyle(color: _muted, fontSize: 12),
                    ),
                  ),
                  TextButton(
                    onPressed: model.canRetryCreate(session.id)
                        ? () => _retry(model, session.id)
                        : null,
                    child: const Text('重试原内容'),
                  ),
                ],
              ),
            )
          else if (session.creating)
            const Padding(
              padding: EdgeInsets.only(top: 4, left: 6),
              child: Text(
                '正在提交本轮输入，请稍候。',
                style: TextStyle(color: _muted, fontSize: 12),
              ),
            )
          else if (reason != null)
            Padding(
              padding: const EdgeInsets.only(top: 4, left: 6),
              child: Text(
                reason,
                style: const TextStyle(color: _muted, fontSize: 12),
              ),
            )
          else
            const Padding(
              padding: EdgeInsets.only(top: 4, left: 6),
              child: Text(
                'Enter 发送 · Shift+Enter 换行',
                style: TextStyle(color: _muted, fontSize: 12),
              ),
            ),
        ],
      ),
    );
  }

  void _submit(
    AiChatModel model,
    AiChatSession session,
    TextEditingController controller,
  ) {
    if (model.sendDisabledReasonFor(session.id) != null ||
        controller.text.trim().isEmpty) {
      return;
    }
    final input = controller.text;
    model.setDraft(session.id, input);
    unawaited(model.send(session.id, input));
  }

  InputDecoration _inputDecoration(String hint, {Widget? prefix}) =>
      InputDecoration(
        hintText: hint,
        hintStyle: const TextStyle(color: _muted),
        prefixIcon: prefix,
        filled: true,
        fillColor: const Color.fromARGB(255, 252, 245, 239),
        isDense: true,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 12,
          vertical: 12,
        ),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: const BorderSide(color: _line),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: const BorderSide(color: _line),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: const BorderSide(color: _brown, width: 1.4),
        ),
      );
}

extension<T> on List<T> {
  T? get lastOrNull => isEmpty ? null : last;
}
