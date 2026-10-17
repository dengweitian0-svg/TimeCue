import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

import '../../theme/notebook_theme.dart';

/// 使用 gpt_markdown 渲染累计的助手正文，并提供代码及链接操作。
class ChatMarkdownBody extends StatefulWidget {
  const ChatMarkdownBody({super.key, required this.data});

  /// 截至当前收到的完整助手正文。
  final String data;

  @override
  State<ChatMarkdownBody> createState() => _ChatMarkdownBodyState();
}

class _ChatMarkdownBodyState extends State<ChatMarkdownBody> {
  String? _renderedData;
  TextScaler? _renderedScaler;
  GptMarkdownThemeData? _sourceTheme;
  GptMarkdownThemeData? _renderedTheme;
  GptMarkdown? _markdown;

  @override
  Widget build(BuildContext context) {
    final markdownTheme = GptMarkdownTheme.of(context);
    final scaler = MediaQuery.textScalerOf(context);
    if (_markdown == null ||
        _renderedData != widget.data ||
        _renderedScaler != scaler ||
        !(_sourceTheme?.isSame(markdownTheme) ?? false)) {
      _renderedData = widget.data;
      _renderedScaler = scaler;
      _sourceTheme = markdownTheme;
      _renderedTheme = markdownTheme.copyWith(
        h1: (markdownTheme.h1 ?? const TextStyle()).copyWith(
          fontSize: 22,
          color: NotebookTheme.ink,
        ),
        h2: (markdownTheme.h2 ?? const TextStyle()).copyWith(
          fontSize: 20,
          color: NotebookTheme.ink,
        ),
        h3: (markdownTheme.h3 ?? const TextStyle()).copyWith(
          fontSize: 18,
          color: NotebookTheme.ink,
        ),
        h4: (markdownTheme.h4 ?? const TextStyle()).copyWith(
          fontSize: 16,
          color: NotebookTheme.ink,
        ),
        h5: (markdownTheme.h5 ?? const TextStyle()).copyWith(
          fontSize: 14,
          color: NotebookTheme.ink,
        ),
        h6: (markdownTheme.h6 ?? const TextStyle()).copyWith(
          fontSize: 14,
          color: NotebookTheme.ink,
        ),
        linkColor: NotebookTheme.ink,
        linkHoverColor: NotebookTheme.ink,
      );
      _markdown = GptMarkdown(
        widget.data,
        animation: GptMarkdownAnimation.none,
        blockAnimation: GptMarkdownBlockAnimation.none,
        isStreaming: false,
        style: const TextStyle(
          color: NotebookTheme.ink,
          fontSize: 14,
          height: 1.6,
        ),
        textScaler: scaler,
        styleSheet: const GptMarkdownStyleSheet(
          blockQuote: BlockQuoteStyle(
            barColor: NotebookTheme.cover,
            backgroundColor: Color.fromARGB(255, 242, 235, 218),
            padding: EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          ),
          heading: HeadingStyle(
            textStyle: TextStyle(color: NotebookTheme.ink),
            dividerColor: NotebookTheme.line,
          ),
          codeBlock: CodeBlockStyle(
            backgroundColor: Color.fromARGB(255, 242, 235, 218),
            borderColor: NotebookTheme.line,
            textColor: NotebookTheme.ink,
            showLanguageLabel: true,
            languageStyle: TextStyle(color: NotebookTheme.ink, fontSize: 11),
            showCopyButton: true,
            highlightWhileStreaming: false,
          ),
          table: TableStyle(
            borderColor: NotebookTheme.line,
            headerBackground: Color.fromARGB(255, 242, 235, 218),
            overflow: TableOverflow.scroll,
          ),
          inlineCode: InlineCodeStyle(
            backgroundColor: Color.fromARGB(255, 242, 235, 218),
            color: NotebookTheme.ink,
          ),
          blockSpacing: 10,
        ),
        onCodeCopy: (code) => _copy(context, code, '代码已复制'),
        onLinkTap: (url, _) => _showLinkActions(context, url),
      );
    }
    return GptMarkdownTheme(
      gptThemeData: _renderedTheme!,
      child: SelectionArea(child: _markdown!),
    );
  }

  Future<void> _showLinkActions(BuildContext context, String url) async {
    final action = await showDialog<_LinkAction>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('链接'),
        content: SelectableText(url),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, _LinkAction.copy),
            child: const Text('复制链接'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, _LinkAction.open),
            child: const Text('在浏览器中打开'),
          ),
        ],
      ),
    );
    if (!context.mounted || action == null) return;
    if (action == _LinkAction.copy) {
      await _copy(context, url, '链接已复制');
    } else {
      await _openLink(context, url);
    }
  }

  Future<void> _copy(BuildContext context, String text, String message) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (!context.mounted) return;
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _openLink(BuildContext context, String url) async {
    final uri = Uri.tryParse(url);
    if (uri == null || (uri.scheme != 'https' && uri.scheme != 'http')) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('仅支持打开 HTTP 或 HTTPS 链接')));
      return;
    }
    try {
      if (Platform.isWindows) {
        await Process.start('explorer.exe', [uri.toString()]);
      } else if (Platform.isMacOS) {
        await Process.start('open', [uri.toString()]);
      } else if (Platform.isLinux) {
        await Process.start('xdg-open', [uri.toString()]);
      }
    } on ProcessException {
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('无法打开系统浏览器')));
    }
  }
}

enum _LinkAction { copy, open }
