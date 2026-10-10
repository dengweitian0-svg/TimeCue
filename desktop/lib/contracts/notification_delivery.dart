/// 只传递 Go 的决定与身份，不在桌面端计算成员或展示预算。
abstract interface class NotificationDeliveryGateway {
  Future<DeliveryRuntime> deliveryRuntime();
  Future<List<DeliveryClaim>> claimNotifications();
  Future<DeliveryStart> startNotification(String attemptId);
  Future<void> reportNotification(String attemptId, DeliveryOutcome outcome);
}

enum DeliveryOutcome { sent, failed }

class DeliveryRuntime {
  const DeliveryRuntime(this.instanceId);
  final String instanceId;

  factory DeliveryRuntime.fromJson(Object? raw) {
    final value = _object(raw);
    final capabilities = value['capabilities'];
    if (value['status'] != 'ok' ||
        value['apiVersion'] != 'v1' ||
        capabilities is! List ||
        !capabilities.contains('delivery-v1') ||
        !capabilities.contains('recovery-v1')) {
      throw const FormatException('本地服务不支持当前投递协议。');
    }
    return DeliveryRuntime(_identity(value, 'instanceId'));
  }
}

class DeliveryClaim {
  const DeliveryClaim({
    required this.deliveryTaskId,
    required this.taskType,
    required this.attemptId,
    required this.platformId,
    required this.notifyTime,
  });
  final String deliveryTaskId, taskType, attemptId, platformId;
  final DateTime notifyTime;

  factory DeliveryClaim.fromJson(Object? raw) {
    final value = _object(raw);
    final taskType = value['taskType'];
    if (taskType != 'minute' && taskType != 'recovery') {
      throw const FormatException('未知通知任务类型。');
    }
    return DeliveryClaim(
      deliveryTaskId: _identity(value, 'deliveryTaskId'),
      taskType: taskType as String,
      attemptId: _identity(value, 'attemptId'),
      platformId: _identity(value, 'platformId'),
      notifyTime: DateTime.parse(_identity(value, 'notifyTime')),
    );
  }

  bool matches(DeliveryClaim other) =>
      deliveryTaskId == other.deliveryTaskId &&
      taskType == other.taskType &&
      attemptId == other.attemptId &&
      platformId == other.platformId &&
      notifyTime == other.notifyTime;
}

class DeliveryStart {
  const DeliveryStart({
    required this.claim,
    required this.shouldDisplay,
    required this.memberCount,
    required this.attemptNumber,
    this.title,
    this.body,
  });
  final DeliveryClaim claim;
  final bool shouldDisplay;
  final int memberCount, attemptNumber;
  final String? title, body;

  factory DeliveryStart.fromJson(Object? raw) {
    final value = _object(raw);
    final display = value['shouldDisplay'];
    final count = value['memberCount'];
    final number = value['attemptNumber'];
    if (display is! bool ||
        count is! int ||
        count < 0 ||
        number is! int ||
        number < 0 ||
        (display &&
            (count == 0 ||
                number == 0 ||
                value['title'] is! String ||
                value['body'] is! String))) {
      throw const FormatException('通知开始响应无效。');
    }
    return DeliveryStart(
      claim: DeliveryClaim.fromJson(value),
      shouldDisplay: display,
      memberCount: count,
      attemptNumber: number,
      title: display ? value['title'] as String : null,
      body: display ? value['body'] as String : null,
    );
  }
}

String _identity(Map<String, dynamic> value, String key) {
  final identity = value[key];
  if (identity is! String || identity.isEmpty) {
    throw FormatException('通知响应缺少 $key。');
  }
  return identity;
}

Map<String, dynamic> _object(Object? raw) {
  if (raw is! Map<String, dynamic>) {
    throw const FormatException('通知响应必须是对象。');
  }
  return raw;
}
