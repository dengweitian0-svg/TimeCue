import 'dart:async';
import 'dart:convert';
import 'dart:io';
import '../contracts/notebook_repository.dart';

/// Go 是唯一业务事实来源；本类只保存只读快照，不展开规则。
class HttpNotebookRepository extends NotebookRepository {
  HttpNotebookRepository({HttpClient? client, Uri? base})
    : _client = client ?? HttpClient(),
      _base = base ?? Uri.parse('http://127.0.0.1:10086/api/v1/scheduler/');
  final HttpClient _client;
  final Uri _base;
  int _generation = 0;
  bool _disposed = false, _loading = false, _hasSnapshot = false;
  String? _error;
  DateTime _now = beijingNow();
  DateTime? _month, _date;
  List<TriggerSummary> _today = const [], _pending = const [];
  final Map<String, List<TriggerSummary>> _dates = {};
  final Map<String, String> _dateCursors = {};
  Set<String> _marks = {};
  int _total = 0, _page = 0;
  List<String> _pendingCursors = [''];
  String _nextPending = '';

  @override
  bool get supportsRuleChanges => false;
  @override
  bool get serverPagination => true;
  @override
  bool get hasSnapshot => _hasSnapshot;
  @override
  bool get loading => _loading;
  @override
  String? get refreshError => _error;
  @override
  DateTime get demoNow => _now;
  @override
  List<TriggerSummary> get today => _today;
  @override
  List<TriggerSummary> get pending => _pending;
  @override
  int get pendingTotal => _total;
  @override
  int get pendingPageIndex => _page;
  @override
  int get pendingPageSize => 50;
  static String dateKey(DateTime date) =>
      '${date.year.toString().padLeft(4, '0')}-${date.month.toString().padLeft(2, '0')}-${date.day.toString().padLeft(2, '0')}';
  @override
  List<TriggerSummary> entriesOn(DateTime date) =>
      _dates[dateKey(date)] ?? const [];
  @override
  bool markedOn(DateTime date) => _marks.contains(dateKey(date));
  @override
  bool hasMoreOn(DateTime date) =>
      (_dateCursors[dateKey(date)] ?? '').isNotEmpty;

  // 将带偏移时间转换为北京时间墙上字段，避免 Windows 时区改变业务日期。
  static DateTime wallTime(String value) =>
      DateTime.parse(value).toUtc().add(const Duration(hours: 8));
  static TriggerSummary trigger(dynamic raw) {
    final item = raw as Map<String, dynamic>;
    return TriggerSummary(
      id: item['notificationId']?.toString() ?? item['occurrenceKey'] as String,
      notificationId: item['notificationId']?.toString(),
      occurrenceKey: item['occurrenceKey'] as String,
      scheduleId: item['scheduleId'].toString(),
      name: item['name'] as String,
      description: item['description'] as String? ?? '',
      plannedAt: wallTime(item['notifyTime'] as String),
      delivery: item['deliveryStatus'] as String? ?? '',
      acknowledgedAt: item['acknowledgedAt'] == null
          ? null
          : wallTime(item['acknowledgedAt'] as String),
      editable: item['editable'] as bool,
      source: item['source'] as String,
    );
  }

  static List<TriggerSummary> items(dynamic page) =>
      List.unmodifiable((page['items'] as List).map(trigger));

  Future<dynamic> _request(
    String path, {
    Map<String, String>? query,
    Object? body,
  }) async {
    try {
      // 提醒资源与 scheduler 同级，不能沿用日程接口的路径前缀。
      final resourcePath = path.startsWith('notifications') ? '../$path' : path;
      final uri = _base.resolve(resourcePath).replace(queryParameters: query);
      final request = await _client
          .openUrl(body == null ? 'GET' : 'POST', uri)
          .timeout(const Duration(seconds: 5));
      request.followRedirects = false;
      if (body != null) {
        request.headers.contentType = ContentType.json;
        request.write(jsonEncode(body));
      }
      final response = await request.close().timeout(
        const Duration(seconds: 5),
      );
      final text = await response
          .transform(utf8.decoder)
          .join()
          .timeout(const Duration(seconds: 5));
      final value = jsonDecode(text) as Map<String, dynamic>;
      if (response.statusCode != 200) {
        throw StateError(
          value['error']?['message']?.toString() ??
              '请求失败（${response.statusCode}）',
        );
      }
      return value['data'];
    } on SocketException {
      throw StateError('无法连接本地服务，请先启动 Go 服务。');
    } on TimeoutException {
      throw StateError('本地服务响应超时，请重试。');
    }
  }

  void _publish() {
    if (!_disposed) notifyListeners();
  }

  @override
  Future<void> refresh({DateTime? month, DateTime? date}) async {
    _month = month ?? _month;
    _date = date ?? _date;
    final generation = ++_generation;
    _loading = true;
    _error = null;
    _publish();
    try {
      final snapshot = await _request(
        'schedules',
        query: {'view': 'occurrences', 'include': 'unacknowledged'},
      );
      final now = wallTime(snapshot['asOf'] as String);
      final requestedMonth = _month ?? DateTime.utc(now.year, now.month);
      final monthKey = dateKey(requestedMonth).substring(0, 7);
      final marks = await _request(
        'schedules',
        query: {'view': 'calendar', 'month': monthKey},
      );
      final selected = _date;
      final datePage = selected == null || sameDay(selected, now)
          ? null
          : await _request(
              'schedules',
              query: {'view': 'occurrences', 'date': dateKey(selected)},
            );
      if (_disposed || generation != _generation) return;
      _now = now;
      _today = items(snapshot['occurrences']);
      _pending = items(snapshot['unacknowledged']);
      _total = snapshot['unacknowledged']['total'] as int;
      _page = 0;
      _pendingCursors = [''];
      _nextPending = snapshot['unacknowledged']['nextCursor'] as String;
      _marks = (marks as List).cast<String>().toSet();
      _dates.clear();
      _dateCursors.clear();
      _dates[dateKey(now)] = _today;
      _dateCursors[dateKey(now)] =
          snapshot['occurrences']['nextCursor'] as String;
      if (selected != null && datePage != null) {
        _dates[dateKey(selected)] = items(datePage);
        _dateCursors[dateKey(selected)] = datePage['nextCursor'] as String;
      }
      _hasSnapshot = true;
    } catch (e) {
      if (generation == _generation && !_disposed) _error = '$e';
    } finally {
      if (generation == _generation && !_disposed) {
        _loading = false;
        _publish();
      }
    }
  }

  @override
  Future<void> loadPendingPage(int page) async {
    if (_loading || page < 0 || page == _page) return;
    if (page > _page && _nextPending.isEmpty) return;
    if (page == _pendingCursors.length) _pendingCursors.add(_nextPending);
    if (page >= _pendingCursors.length) return;
    final generation = ++_generation;
    _loading = true;
    _error = null;
    _publish();
    try {
      final data = await _request(
        'notifications',
        query: {'state': 'unacknowledged', 'cursor': _pendingCursors[page]},
      );
      if (_disposed || generation != _generation) return;
      if ((data['items'] as List).isEmpty && page > 0) {
        await refresh();
        return;
      }
      _pending = items(data);
      _total = data['total'] as int;
      _page = page;
      _nextPending = data['nextCursor'] as String;
      _pendingCursors = _pendingCursors.take(page + 1).toList();
    } catch (e) {
      if (!_disposed && generation == _generation) _error = '$e';
    } finally {
      if (!_disposed && generation == _generation) {
        _loading = false;
        _publish();
      }
    }
  }

  @override
  Future<void> loadMoreOn(DateTime date) async {
    final key = dateKey(date), cursor = _dateCursors[dateKey(date)] ?? '';
    if (_loading || cursor.isEmpty) return;
    final generation = ++_generation;
    _loading = true;
    _publish();
    try {
      final data = await _request(
        'schedules',
        query: {'view': 'occurrences', 'date': key, 'cursor': cursor},
      );
      if (_disposed || generation != _generation) return;
      final merged = {
        for (final entry in entriesOn(date)) entry.occurrenceKey: entry,
      };
      for (final entry in items(data)) {
        merged[entry.occurrenceKey] = entry;
      }
      _dates[key] = List.unmodifiable(merged.values);
      _dateCursors[key] = data['nextCursor'] as String;
      if (sameDay(date, _now)) _today = _dates[key]!;
    } catch (e) {
      if (!_disposed && generation == _generation) _error = '$e';
    } finally {
      if (!_disposed && generation == _generation) {
        _loading = false;
        _publish();
      }
    }
  }

  @override
  Future<void> confirm(Set<String> ids) async {
    // 确认会使所有在途读取失效；服务失败前保持原快照。
    ++_generation;
    final data = await _request(
      'notifications/acknowledgements',
      body: {'notificationIds': ids.map(int.parse).toList()},
    );
    if (_disposed) return;
    ++_generation;
    final times = {
      for (final raw in data as List)
        raw['notificationId'].toString(): wallTime(
          raw['acknowledgedAt'] as String,
        ),
    };
    if (times.length != ids.length || !times.keys.toSet().containsAll(ids)) {
      throw StateError('确认响应不完整，请重新查询后重试。');
    }
    List<TriggerSummary> apply(List<TriggerSummary> values) =>
        List.unmodifiable(
          values.map(
            (entry) => times.containsKey(entry.id)
                ? entry.acknowledge(times[entry.id]!)
                : entry,
          ),
        );
    _today = apply(_today);
    for (final key in _dates.keys.toList()) {
      _dates[key] = apply(_dates[key]!);
    }
    _pending = List.unmodifiable(
      _pending.where((entry) => !times.containsKey(entry.id)),
    );
    _total = (_total - times.length).clamp(0, _total);
    _loading = false;
    _publish();
    // 已成功的确认不能因后续刷新失败被报告为确认失败。
    await refresh();
  }

  @override
  ScheduleSummary schedule(String id) =>
      throw UnsupportedError('规则编辑将在阶段 C 接入');
  @override
  Future<void> save(ScheduleDraft draft, {String? id}) async =>
      throw UnsupportedError('规则编辑将在阶段 C 接入');
  @override
  Future<void> delete(String id) async =>
      throw UnsupportedError('规则删除将在阶段 C 接入');
  @override
  void dispose() {
    _disposed = true;
    ++_generation;
    _client.close(force: true);
    super.dispose();
  }
}
