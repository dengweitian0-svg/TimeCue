import 'package:flutter/material.dart';
import 'app/timecue_app.dart';
import 'data/demo_notebook_repository.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  const demo = bool.fromEnvironment('TIMECUE_DEMO', defaultValue: false);
  if (!demo) {
    runApp(
      const MaterialApp(
        home: Scaffold(
          body: Center(
            child: SelectableText(
              '视觉骨架请显式启用示例仓储：\nflutter run -d windows --dart-define=TIMECUE_DEMO=true\n\nGo 接入尚未实现。',
              textAlign: TextAlign.center,
            ),
          ),
        ),
      ),
    );
    return;
  }
  runApp(TimeCueApp(repository: DemoNotebookRepository()));
}
