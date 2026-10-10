import 'package:flutter/material.dart';
import 'app/timecue_app.dart';
import 'app/windows_notification_bridge.dart';
import 'data/demo_notebook_repository.dart';
import 'data/http_notebook_repository.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  const demo = bool.fromEnvironment('TIMECUE_DEMO', defaultValue: false);
  if (!demo) {
    final repository = HttpNotebookRepository();
    runApp(
      TimeCueApp(
        repository: repository,
        notifications: WindowsNotificationBridge(
          gateway: repository,
          platform: WindowsNotificationPlatform(),
        ),
        ownsRepository: true,
      ),
    );
    return;
  }
  runApp(
    TimeCueApp(repository: DemoNotebookRepository(), ownsRepository: true),
  );
}
