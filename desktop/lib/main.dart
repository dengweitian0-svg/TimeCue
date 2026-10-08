import 'package:flutter/material.dart';
import 'app/timecue_app.dart';
import 'data/demo_notebook_repository.dart';
import 'data/http_notebook_repository.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  const demo = bool.fromEnvironment('TIMECUE_DEMO', defaultValue: false);
  if (!demo) {
    runApp(TimeCueApp(repository: HttpNotebookRepository()));
    return;
  }
  runApp(TimeCueApp(repository: DemoNotebookRepository()));
}
