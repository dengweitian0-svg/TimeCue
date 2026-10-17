import 'package:flutter/material.dart';

import '../../theme/notebook_theme.dart';
import 'notebook_view_model.dart';

/// 展示 AI 对话的双页与单栏欢迎框架，输入内容仅作为运行内草稿保存。
class ChatSpread extends StatefulWidget {
  const ChatSpread({super.key, required this.model});

  final NotebookViewModel model;

  @override
  State<ChatSpread> createState() => _ChatSpreadState();
}

class _ChatSpreadState extends State<ChatSpread> {
  late final TextEditingController _draftController = TextEditingController(
    text: widget.model.chatDraft,
  );

  @override
  void dispose() {
    widget.model.updateChatDraft(_draftController.text);
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
              child: SingleChildScrollView(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        const Expanded(
                          child: _PageTitle(
                            title: '历史会话',
                            subtitle: '已聊过的内容会出现在这里。',
                          ),
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
                        hintStyle: TextStyle(color: NotebookTheme.ink),
                        isDense: true,
                      ),
                    ),
                    const SizedBox(height: 20),
                    const Center(
                      child: Text(
                        '历史会话暂不可用',
                        style: TextStyle(color: NotebookTheme.ink),
                      ),
                    ),
                  ],
                ),
              ),
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
    child: Column(
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
                padding: EdgeInsets.symmetric(horizontal: 12),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (!showHistoryButton)
                      const _PageTitle(
                        title: 'AI 对话',
                        subtitle: '把想法写下来，我们慢慢聊。',
                      ),
                    Text(
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
                  hintStyle: TextStyle(color: NotebookTheme.ink),
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
        const Text(
          '发送暂未开放',
          style: TextStyle(fontSize: 12, color: NotebookTheme.ink),
        ),
      ],
    ),
  );

  Future<void> _startNewDraft() async {
    if (_draftController.text.isNotEmpty) {
      final discard = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('开始新会话？'),
          content: const Text('当前草稿还没有保存，开始新会话会清空它。'),
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
          ? const EdgeInsets.fromLTRB(20, 16, 20, 12)
          : const EdgeInsets.fromLTRB(24, 28, 24, 20),
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
        style: TextStyle(
          fontSize: 10,
          letterSpacing: 2.4,
          color: NotebookTheme.ink,
        ),
      ),
      const SizedBox(height: 8),
      Text(title, style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 6),
      Text(
        subtitle,
        style: const TextStyle(fontSize: 12, color: NotebookTheme.ink),
      ),
      const SizedBox(height: 12),
      const Divider(),
    ],
  );
}
