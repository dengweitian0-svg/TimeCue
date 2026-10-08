import 'package:flutter/foundation.dart';

DateTime beijingNow() => DateTime.now().toUtc().add(const Duration(hours: 8));
String timeLabel(DateTime date) =>
    '${date.hour.toString().padLeft(2, '0')}:${date.minute.toString().padLeft(2, '0')}';
bool sameDay(DateTime a, DateTime b) =>
    a.year == b.year && a.month == b.month && a.day == b.day;

enum ScheduleKind { single, daily, weekly, monthly }

@immutable
class ScheduleDraft {
  const ScheduleDraft({
    required this.name,
    required this.description,
    required this.kind,
    required this.time,
    this.enabled = true,
  });
  final String name, description;
  final ScheduleKind kind;
  // 示例中的 UTC 字段存放北京时间墙上时刻；正式接入由响应映射统一转换。
  final DateTime time;
  final bool enabled;
}

@immutable
class ScheduleSummary {
  const ScheduleSummary(this.id, this.draft, {this.deleted = false});
  final String id;
  final ScheduleDraft draft;
  final bool deleted;
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

abstract class NotebookRepository extends ChangeNotifier {
  bool get supportsRuleChanges => true;
  bool get serverPagination => false;
  int get pendingTotal => pending.length;
  int get pendingPageIndex => 0;
  int get pendingPageSize => 3;
  bool get loading => false;
  String? get refreshError => null;
  bool get hasSnapshot => true;
  bool markedOn(DateTime date) => entriesOn(date).isNotEmpty;
  Future<void> refresh({DateTime? month, DateTime? date}) async {}
  Future<void> loadPendingPage(int page) async {}
  bool hasMoreOn(DateTime date) => false;
  Future<void> loadMoreOn(DateTime date) async {}
  DateTime get demoNow;
  List<TriggerSummary> get today;
  List<TriggerSummary> get pending;
  List<TriggerSummary> entriesOn(DateTime date);
  ScheduleSummary schedule(String id);
  Future<void> confirm(Set<String> ids);
  Future<void> save(ScheduleDraft draft, {String? id});
  Future<void> delete(String id);
}
