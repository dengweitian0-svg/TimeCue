import 'dart:io';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import '../contracts/notification_delivery.dart';

abstract interface class NotificationPlatform {
  Future<void> initialize();
  Future<DeliveryOutcome?> show({
    required int id,
    required DeliveryStart start,
    required String payload,
  });
  void dispose();
}

/// Windows 只执行即时请求，名称、数量和重试资格均来自 Go。
class WindowsNotificationPlatform implements NotificationPlatform {
  WindowsNotificationPlatform({this.onActivated});
  final void Function(String payload)? onActivated;
  FlutterLocalNotificationsWindows? _windows;
  @override
  Future<void> initialize() async {
    if (!Platform.isWindows) throw UnsupportedError('系统通知仅支持 Windows。');
    final windows = FlutterLocalNotificationsPlugin()
        .resolvePlatformSpecificImplementation<
          FlutterLocalNotificationsWindows
        >();
    if (windows == null) throw StateError('Windows 通知插件未注册。');
    final ready = await windows.initialize(
      settings: WindowsInitializationSettings(
        appName: 'TimeCue',
        appUserModelId: 'TimeCue.Desktop',
        guid: '58c9ed24-88f1-4a4a-a6b8-9a677d2d46d9',
      ),
      onDidReceiveNotificationResponse: (response) {
        final payload = response.payload;
        if (payload != null) onActivated?.call(payload);
      },
    );
    if (!ready) throw StateError('Windows 通知初始化失败。');
    _windows = windows;
  }

  @override
  Future<DeliveryOutcome?> show({
    required int id,
    required DeliveryStart start,
    required String payload,
  }) async {
    final windows = _windows;
    if (windows == null) throw StateError('Windows 通知尚未初始化。');
    try {
      await windows.show(
        id: id,
        title: start.title,
        body: start.body,
        payload: payload,
      );
      return DeliveryOutcome.sent;
    } on Exception {
      // 固定版本的原生 show 拒绝请求时抛出异常，允许 Go 决定后续重试。
      return DeliveryOutcome.failed;
    }
  }

  @override
  void dispose() {
    _windows?.dispose();
    _windows = null;
  }
}
