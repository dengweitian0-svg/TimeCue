import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'notebook_repository.dart';
import 'notification_delivery.dart';

/// 展示查询只读取 Go 的事实，不承担领取、投递或确认。
abstract interface class NotificationQueryGateway {
  Future<DeliveryRuntime> deliveryRuntime();
  Future<TriggerSummary> notificationDetails(String notificationId);
  Future<NotificationMemberPage> notificationMembers(
    String deliveryTaskId, {
    String? attemptId,
    String cursor = '',
  });
}

@immutable
class NotificationTarget {
  const NotificationTarget({
    required this.instanceId,
    required this.deliveryTaskId,
    required this.taskType,
    this.attemptId,
  });
  final String? instanceId;
  final String deliveryTaskId, taskType;
  final String? attemptId;
  bool get isActivation => attemptId != null;

  static NotificationTarget fromPayload(String payload) {
    final value = jsonDecode(payload);
    if (value is! Map<String, dynamic> || value.length != 4) {
      throw const FormatException('通知定位信息不完整。');
    }
    String identity(String key) {
      final field = value[key];
      if (field is! String ||
          field.isEmpty ||
          field.length > 128 ||
          RegExp(r'[\s\x00-\x1f]').hasMatch(field)) {
        throw const FormatException('通知定位身份无效。');
      }
      return field;
    }

    final kind = identity('taskType');
    if (kind != 'minute' && kind != 'recovery') {
      throw const FormatException('未知通知类型。');
    }
    return NotificationTarget(
      instanceId: identity('instanceId'),
      deliveryTaskId: identity('deliveryTaskId'),
      attemptId: identity('attemptId'),
      taskType: kind,
    );
  }

  NotificationTarget bind(String instance) => NotificationTarget(
    instanceId: instance,
    deliveryTaskId: deliveryTaskId,
    taskType: taskType,
    attemptId: attemptId,
  );

  @override
  bool operator ==(Object other) =>
      other is NotificationTarget &&
      instanceId == other.instanceId &&
      deliveryTaskId == other.deliveryTaskId &&
      attemptId == other.attemptId &&
      taskType == other.taskType;
  @override
  int get hashCode =>
      Object.hash(instanceId, deliveryTaskId, attemptId, taskType);
}

@immutable
class NotificationMemberPage {
  const NotificationMemberPage({
    required this.items,
    required this.total,
    required this.nextCursor,
    required this.asOf,
    this.recovery,
  });
  final List<TriggerSummary> items;
  final int total;
  final String nextCursor;
  final DateTime asOf;
  final RecoveryProgress? recovery;
}
