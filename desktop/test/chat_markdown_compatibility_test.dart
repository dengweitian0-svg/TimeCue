import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

const _wideMarkdown = '''
| 字段名称 | 日期 | 非常长的字段内容用于确认窄屏不会撑开整页 |
| --- | --- | --- |
| 日程名称 | 2026-10-17 | 一段较长的中文内容用于检查宽表格的布局 |
| Session ID | 2026-10-18 | 550e8400-e29b-41d4-a716-446655440000 |

```dart
final longIdentifier = 'this_is_a_long_identifier_that_should_stay_inside_the_code_block';
print(longIdentifier);
```
''';

void main() {
  testWidgets(
    'cumulative complete text updates through partial Markdown syntax',
    (tester) async {
      var accumulated = '# 回复\n\n请稍等，`未完成';
      Future<void> render() async {
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: GptMarkdown(
                accumulated,
                animation: GptMarkdownAnimation.none,
                blockAnimation: GptMarkdownBlockAnimation.none,
                isStreaming: false,
              ),
            ),
          ),
        );
        await tester.pump();
      }

      await render();
      expect(tester.takeException(), isNull);
      expect(find.byType(GptMarkdown), findsOneWidget);

      accumulated += '` 接着是 **完整** 的结论。';
      await render();
      expect(tester.takeException(), isNull);
      expect(
        tester.widget<GptMarkdown>(find.byType(GptMarkdown)).data,
        accumulated,
      );
    },
  );

  testWidgets(
    'unfinished code fence can be completed without renderer errors',
    (tester) async {
      var accumulated = '```dart\nfinal value = 1;';
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: GptMarkdown(
              accumulated,
              animation: GptMarkdownAnimation.none,
              blockAnimation: GptMarkdownBlockAnimation.none,
              isStreaming: false,
            ),
          ),
        ),
      );
      await tester.pump();
      expect(tester.takeException(), isNull);

      accumulated += '\n```';
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: GptMarkdown(
              accumulated,
              animation: GptMarkdownAnimation.none,
              blockAnimation: GptMarkdownBlockAnimation.none,
              isStreaming: false,
            ),
          ),
        ),
      );
      await tester.pump();
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'unfinished link and emphasis can be completed without renderer errors',
    (tester) async {
      var accumulated = '链接 [打开文档](https://example **重点';
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: GptMarkdown(
              accumulated,
              animation: GptMarkdownAnimation.none,
              blockAnimation: GptMarkdownBlockAnimation.none,
              isStreaming: false,
            ),
          ),
        ),
      );
      await tester.pump();
      expect(tester.takeException(), isNull);

      accumulated += '.com) 完成**';
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: GptMarkdown(
              accumulated,
              animation: GptMarkdownAnimation.none,
              blockAnimation: GptMarkdownBlockAnimation.none,
              isStreaming: false,
            ),
          ),
        ),
      );
      await tester.pump();
      expect(tester.takeException(), isNull);
      expect(
        tester.widget<GptMarkdown>(find.byType(GptMarkdown)).data,
        accumulated,
      );
    },
  );

  testWidgets(
    'code and wide table remain usable in small window with large text',
    (tester) async {
      tester.view.physicalSize = const Size(320, 480);
      tester.view.devicePixelRatio = 1;
      addTearDown(() {
        tester.view.resetPhysicalSize();
        tester.view.resetDevicePixelRatio();
      });

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: MediaQuery(
              data: const MediaQueryData(textScaler: TextScaler.linear(2)),
              child: SingleChildScrollView(
                child: SizedBox(
                  width: 300,
                  child: GptMarkdown(
                    _wideMarkdown,
                    animation: GptMarkdownAnimation.none,
                    blockAnimation: GptMarkdownBlockAnimation.none,
                    isStreaming: false,
                  ),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byType(GptMarkdown), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );
}
