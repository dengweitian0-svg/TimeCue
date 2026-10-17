import 'package:flutter/material.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

import 'package:timecue_desktop/theme/notebook_theme.dart';

/// 启动 Markdown 渲染依赖检查应用。
void main() => runApp(const ChatMarkdownSmokeApp());

/// 使用发行构建验证 gpt_markdown 与 Windows AOT 链接。
class ChatMarkdownSmokeApp extends StatelessWidget {
  const ChatMarkdownSmokeApp({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'TimeCue Markdown 检查',
    theme: NotebookTheme.data,
    home: Scaffold(
      appBar: AppBar(title: const Text('Markdown 渲染检查')),
      body: const SingleChildScrollView(
        padding: EdgeInsets.all(24),
        child: GptMarkdown(
          _mixedMarkdown,
          animation: GptMarkdownAnimation.none,
          blockAnimation: GptMarkdownBlockAnimation.none,
          isStreaming: false,
        ),
      ),
    ),
  );
}

const _mixedMarkdown = '''
# 今日日程

你好，**中文回复**可随着累计文本逐步补全。这里包含尚未闭合的语法片段已经在最终文本中补完：`未完成`。

| 名称 | 日期 | 状态 |
| --- | --- | --- |
| 阅读 | 2026-10-17 | 已安排 |
| 散步 | 2026-10-18 | 待确认 |

```dart
final message = '代码块也需要被打包';
print(message);
```
''';
