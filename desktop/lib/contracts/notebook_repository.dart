import 'package:flutter/foundation.dart';

DateTime beijingNow() => DateTime.now().toUtc().add(const Duration(hours: 8));
String timeLabel(DateTime date) =>
    '${date.hour.toString().padLeft(2, '0')}:${date.minute.toString().padLeft(2, '0')}';
bool sameDay(DateTime a, DateTime b) =>
    a.year == b.year && a.month == b.month && a.day == b.day;

enum ScheduleKind { single, daily, weekly, monthly }

@immutable
class ScheduleTimeSlot {
  const ScheduleTimeSlot(this.kind, this.hour, this.minute, {this.day});
  final ScheduleKind kind;
  final int hour, minute;
  final int? day;
  String get clockLabel =>
      '${hour.toString().padLeft(2, '0')}:${minute.toString().padLeft(2, '0')}';
  String get label => switch (kind) {
    ScheduleKind.weekly =>
      '周${['一', '二', '三', '四', '五', '六', '日'][day! - 1]} $clockLabel · 北京时间',
    ScheduleKind.monthly => '每月 $day 日 $clockLabel · 北京时间',
    _ => '$clockLabel · 北京时间',
  };
  bool get valid =>
      hour >= 0 &&
      hour < 24 &&
      minute >= 0 &&
      minute < 60 &&
      switch (kind) {
        ScheduleKind.daily => day == null,
        ScheduleKind.weekly => day != null && day! >= 1 && day! <= 7,
        ScheduleKind.monthly => day != null && day! >= 1 && day! <= 31,
        ScheduleKind.single => false,
      };
  @override
  bool operator ==(Object other) =>
      other is ScheduleTimeSlot &&
      kind == other.kind &&
      hour == other.hour &&
      minute == other.minute &&
      day == other.day;
  @override
  int get hashCode => Object.hash(kind, hour, minute, day);
}

@immutable
class ScheduleDraft {
  const ScheduleDraft({
    required this.name,
    required this.description,
    required this.kind,
    required this.time,
    this.enabled = true,
    this.times,
    this.days,
    this.rawCron,
    this.slots,
  });
  final String name, description;
  final ScheduleKind kind;
  // 示例中的 UTC 字段存放北京时间墙上时刻；正式接入由响应映射统一转换。
  final DateTime time;
  final bool enabled;
  final List<String>? times;
  final List<int>? days;
  // 无法无损映射到预设的旧规则只按原样回传。
  final List<String>? rawCron;
  final List<ScheduleTimeSlot>? slots;
}

@immutable
class ScheduleSummary {
  const ScheduleSummary(
    this.id,
    this.draft, {
    this.deleted = false,
    this.revision = 1,
  });
  final String id;
  final ScheduleDraft draft;
  final bool deleted;
  final int revision;
}

@immutable
class TriggerSummary {
  const TriggerSummary({
    required this.id,
    required this.scheduleId,
    required this.name,
    required this.plannedAt,
    this.delivery = 'sent',
    this.acknowledgedAt,
    this.notificationId,
    this.description = '',
    this.editable = true,
    this.source = 'persisted',
    this.occurrenceKey,
  });
  final String id, scheduleId, name, delivery;
  final DateTime plannedAt;
  final DateTime? acknowledgedAt;
  final String? notificationId, occurrenceKey;
  final String description, source;
  final bool editable;
  TriggerSummary acknowledge(DateTime at) => TriggerSummary(
    id: id,
    scheduleId: scheduleId,
    name: name,
    plannedAt: plannedAt,
    delivery: delivery,
    acknowledgedAt: acknowledgedAt ?? at,
    notificationId: notificationId,
    description: description,
    editable: editable,
    source: source,
    occurrenceKey: occurrenceKey,
  );
}

enum PendingScope { today, history }

@immutable
class RecoveryProgress {
  const RecoveryProgress({
    required this.recoveryId,
    required this.state,
    required this.through,
    required this.materializedCount,
    required this.missedTotal,
    required this.displayableCount,
    required this.deliveryTaskId,
  });
  final String recoveryId, state, deliveryTaskId;
  final DateTime through;
  final int materializedCount, displayableCount;
  final int? missedTotal;

  static RecoveryProgress? fromJson(dynamic value) {
    if (value == null) return null;
    if (value is! Map<String, dynamic> ||
        !['running', 'completed', 'failed'].contains(value['state'])) {
      throw const FormatException('历史整理状态无效。');
    }
    String identity(String key) {
      final field = value[key];
      if (field is! String || field.isEmpty) {
        throw const FormatException('历史整理身份缺失。');
      }
      return field;
    }

    int count(String key) {
      final field = value[key];
      if (field is! int || field < 0) {
        throw const FormatException('历史整理数量无效。');
      }
      return field;
    }

    return RecoveryProgress(
      recoveryId: identity('recoveryId'),
      state: value['state'] as String,
      through: DateTime.parse(
        value['through'] as String,
      ).toUtc().add(const Duration(hours: 8)),
      materializedCount: count('materializedCount'),
      missedTotal: value['missedTotal'] == null ? null : count('missedTotal'),
      displayableCount: count('displayableCount'),
      deliveryTaskId: identity('deliveryTaskId'),
    );
  }
}

abstract class NotebookRepository extends ChangeNotifier {
  RecoveryProgress? get recovery => null;
  bool get supportsRuleChanges => true;
  bool get canWrite =>
      supportsRuleChanges &&
      (!serverPagination || (hasSnapshot && refreshError == null));
  bool get serverPagination => false;
  int get pendingTotal => pending.length;
  int get pendingPageIndex => 0;
  int get pendingPageSize => 3;
  // 房间总数保持完整集合；页面按范围分别读取，演示仓储仅分类已有事实。
  List<TriggerSummary> pendingFor(PendingScope scope) => List.unmodifiable(
    pending.where(
      (entry) =>
          sameDay(entry.plannedAt, demoNow) == (scope == PendingScope.today),
    ),
  );
  int pendingTotalFor(PendingScope scope) => pendingFor(scope).length;
  int pendingPageIndexFor(PendingScope scope) => 0;
  bool pendingLoadingFor(PendingScope scope) => loading;
  String? pendingErrorFor(PendingScope scope) => refreshError;
  bool get pendingCurrent => true;
  bool get loading => false;
  String? get refreshError => null;
  bool get hasSnapshot => true;
  bool markedOn(DateTime date) => entriesOn(date).isNotEmpty;
  // 按启用日程身份统计全部当日安排，已确认的触发同样计入。
  int? openScheduleCountOn(DateTime date) => entriesOn(date)
      .where((entry) {
        final value = schedule(entry.scheduleId);
        return !value.deleted && value.draft.enabled;
      })
      .map((entry) => entry.scheduleId)
      .toSet()
      .length;
  Future<void> refresh({DateTime? month, DateTime? date}) async {}
  Future<void> loadPendingPage(
    int page, {
    PendingScope scope = PendingScope.today,
  }) async {}
  bool hasMoreOn(DateTime date) => false;
  Future<void> loadMoreOn(DateTime date) async {}
  DateTime get demoNow;
  List<TriggerSummary> get today;
  List<TriggerSummary> get pending;
  List<TriggerSummary> entriesOn(DateTime date);
  ScheduleSummary schedule(String id);
  Future<ScheduleSummary> loadSchedule(String id) async => schedule(id);
  Future<List<ScheduleSummary>> listSchedules({int offset = 0}) async =>
      const [];
  Future<void> confirm(Set<String> ids);
  Future<void> save(
    ScheduleDraft draft, {
    String? id,
    int? expectedRevision,
    String? operationKey,
  });
  Future<void> delete(String id);
}

class NotebookApiException implements Exception {
  const NotebookApiException(this.code, this.message, {this.status = 400});
  final String code, message;
  final int status;
  @override
  String toString() => message;
}
