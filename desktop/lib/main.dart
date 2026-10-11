import 'dart:async';
import 'package:flutter/material.dart';
import 'app/timecue_app.dart';
import 'app/windows_notification_bridge.dart';
import 'data/demo_notebook_repository.dart';
import 'data/http_notebook_repository.dart';
import 'features/notebook/notification_view_model.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  const demo = bool.fromEnvironment('TIMECUE_DEMO', defaultValue: false);
  if (!demo) {
    final repository = HttpNotebookRepository();
    final views = NotificationViewModel(repository);
    runApp(
      TimeCueApp(
        repository: repository,
        notificationViews: views,
        notifications: WindowsNotificationBridge(
          gateway: repository,
          platform: WindowsNotificationPlatform(
            onActivated: (payload) => unawaited(views.activate(payload)),
          ),
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
