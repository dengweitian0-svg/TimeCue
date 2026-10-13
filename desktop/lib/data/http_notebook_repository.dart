import 'dart:async';
import 'dart:convert';
import 'dart:io';
import '../contracts/notebook_repository.dart';
import '../contracts/notification_delivery.dart';
import '../contracts/notification_query.dart';
import '../contracts/schedule_rules.dart';

/// Go 是唯一业务事实来源；本类只保存只读快照，不展开规则。
class HttpNotebookRepository extends NotebookRepository
    implements NotificationDeliveryGateway, NotificationQueryGateway {
  HttpNotebookRepository({
    HttpClient? client,
    Uri? base,
    DateTime Function()? now,
    bool managed = false,
  }) : _runtimeAccess = !managed,
       _base = base ?? Uri.parse('http://127.0.0.1:10086/api/v1/scheduler/'),
       _client =
           client ??
           _createClient(
             base ?? Uri.parse('http://127.0.0.1:10086/api/v1/scheduler/'),
           ),
       _clock = now ?? beijingNow,
       _now = (now ?? beijingNow)();

  static HttpClient _createClient(Uri base) {
    final client = HttpClient();
    if (base.host == 'localhost' ||
        InternetAddress.tryParse(base.host)?.isLoopback == true) {
      client.findProxy = (_) => 'DIRECT';
    }
    return client;
  }

  bool _runtimeAccess;
  String? _expectedInstance;
  int? _expectedPid;
  void Function()? onManagedRuntimeLost;
  void enableManagedRuntime(String instanceId, {int? pid}) {
    if (_disposed) return;
    _expectedInstance = instanceId;
    _expectedPid = pid;
    _runtimeAccess = true;
  }

  void invalidateManagedRuntime() {
    final hadAccess = _runtimeAccess;
    _runtimeAccess = false;
    _invalidateReads();
    _loading = false;
    if (hadAccess) _client.close(force: true);
  }

  final HttpClient _client;
  final Uri _base;
  final DateTime Function() _clock;
  int _generation = 0;
  bool _disposed = false, _loading = false, _hasSnapshot = false;
  String? _error;
  DateTime _now;
  DateTime? _month, _date;
  List<TriggerSummary> _today = const [], _pending = const [];
  final Map<String, List<TriggerSummary>> _dates = {};
  final Map<String, String> _dateCursors = {};
  final Map<String, int> _openScheduleCounts = {};
  Set<String> _marks = {};
  int _total = 0;
  RecoveryProgress? _recovery;
  @override
  RecoveryProgress? get recovery => _recovery;
  final Map<String, PendingScope> _pendingMembership = {};
  final _pendingScopes = {
    for (final scope in PendingScope.values) scope: _PendingState(),
  };

  @override
  bool get supportsRuleChanges => true;
  @override
  bool get serverPagination => true;
  @override
  bool get hasSnapshot => _hasSnapshot;
  @override
  bool get loading =>
      _loading || _pendingScopes.values.any((state) => state.loading);
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
  int get pendingPageIndex => pendingPageIndexFor(PendingScope.today);
  @override
  int get pendingPageSize => 50;
  @override
  List<TriggerSummary> pendingFor(PendingScope scope) =>
      _pendingScopes[scope]!.items;
  @override
  int pendingTotalFor(PendingScope scope) => _pendingScopes[scope]!.total;
  @override
  int pendingPageIndexFor(PendingScope scope) => _pendingScopes[scope]!.page;
  @override
  bool pendingLoadingFor(PendingScope scope) =>
      _loading || _pendingScopes[scope]!.loading;
  @override
  String? pendingErrorFor(PendingScope scope) =>
      _error ?? _pendingScopes[scope]!.error;
  @override
  bool get pendingCurrent => _hasSnapshot && dateKey(_now) == dateKey(_clock());
  static String dateKey(DateTime date) =>
      '${date.year.toString().padLeft(4, '0')}-${date.month.toString().padLeft(2, '0')}-${date.day.toString().padLeft(2, '0')}';
  @override
  List<TriggerSummary> entriesOn(DateTime date) =>
      _dates[dateKey(date)] ?? const [];
  @override
  bool markedOn(DateTime date) => _marks.contains(dateKey(date));
  @override
  int? openScheduleCountOn(DateTime date) => _openScheduleCounts[dateKey(date)];
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
    String? method,
    String? operationKey,
    Duration? timeout,
  }) async {
    if (_disposed || (path != '/health' && !_runtimeAccess)) {
      throw StateError('自有本地服务尚未就绪或已经结束。');
    }
    try {
      final watch = Stopwatch()..start();
      Duration remaining() {
        if (timeout == null) return const Duration(seconds: 5);
        final value = timeout - watch.elapsed;
        if (value <= Duration.zero) throw TimeoutException('本地服务请求超时。');
        return value;
      }

      // 提醒资源与 scheduler 同级，不能沿用日程接口的路径前缀。
      final resourcePath = path.startsWith('notifications') ? '../$path' : path;
      final uri = _base.resolve(resourcePath).replace(queryParameters: query);
      final request = await _client
          .openUrl(method ?? (body == null ? 'GET' : 'POST'), uri)
          .timeout(remaining());
      request.followRedirects = false;
      if (operationKey != null) {
        request.headers.set('Idempotency-Key', operationKey);
      }
      if (body != null) {
        request.headers.contentType = ContentType.json;
        request.write(jsonEncode(body));
      }
      final response = await request.close().timeout(remaining());
      final text = await response
          .transform(utf8.decoder)
          .join()
          .timeout(remaining());
      if (_disposed || (path != '/health' && !_runtimeAccess)) {
        throw StateError('本次运行已经结束，忽略迟到响应。');
      }
      if (response.statusCode == 204) return null;
      final endpoint = uri.path;
      dynamic decoded;
      try {
        if (text.isEmpty) throw const FormatException();
        decoded = jsonDecode(text);
      } on FormatException {
        if (response.statusCode < 200 || response.statusCode >= 300) {
          throw NotebookApiException(
            'request_failed',
            '请求 $endpoint 失败（HTTP ${response.statusCode}），服务未返回有效 JSON。',
            status: response.statusCode,
          );
        }
        throw FormatException(
          '请求 $endpoint 返回空内容或无效 JSON（HTTP ${response.statusCode}）。请检查 Go 服务和接口响应。',
        );
      }
      if (decoded is! Map<String, dynamic>) {
        if (response.statusCode < 200 || response.statusCode >= 300) {
          throw NotebookApiException(
            'request_failed',
            '请求 $endpoint 失败（HTTP ${response.statusCode}），服务返回的错误格式无效。',
            status: response.statusCode,
          );
        }
        throw FormatException(
          '请求 $endpoint 返回非对象 JSON（HTTP ${response.statusCode}）。请检查 Go 服务接口响应。',
        );
      }
      final value = decoded;
      if (path == '/health' &&
          _expectedInstance != null &&
          (value['data']?['instanceId'] != _expectedInstance ||
              (_expectedPid != null &&
                  value['data']?['pid'] != _expectedPid))) {
        invalidateManagedRuntime();
        onManagedRuntimeLost?.call();
        throw const FormatException('端口上的服务不是本次自有进程。');
      }
      if (response.statusCode < 200 || response.statusCode >= 300) {
        final data = value['data'];
        final nestedError = data is Map ? data['error'] : null;
        final rawError = value['error'] ?? nestedError;
        final error = rawError is Map ? rawError : null;
        throw NotebookApiException(
          error?['code']?.toString() ?? 'request_failed',
          error?['message']?.toString() ??
              '请求 $endpoint 失败（HTTP ${response.statusCode}）。',
          status: response.statusCode,
        );
      }
      if (!value.containsKey('data') || value['data'] == null) {
        throw FormatException(
          '请求 $endpoint 返回的数据为空（HTTP ${response.statusCode}）。请检查 Go 服务接口响应。',
        );
      }
      return value['data'];
    } on SocketException {
      throw StateError('无法连接本地服务，请先启动 Go 服务。');
    } on TimeoutException {
      throw StateError('本地服务响应超时，请重试。');
    }
  }

  void _invalidateReads() {
    ++_generation;
    for (final state in _pendingScopes.values) {
      ++state.generation;
      state.loading = false;
    }
  }

  void _publish() {
    if (!_disposed) notifyListeners();
  }

  @override
  Future<DeliveryRuntime> deliveryRuntime() async =>
      DeliveryRuntime.fromJson(await _request('/health'));

  @override
  Future<TriggerSummary> notificationDetails(String notificationId) async {
    final result = trigger(
      await _request('notifications/${Uri.encodeComponent(notificationId)}'),
    );
    if (result.notificationId != notificationId) {
      throw const FormatException('提醒详情身份与请求不一致。');
    }
    return result;
  }

  @override
  Future<NotificationMemberPage> notificationMembers(
    String deliveryTaskId, {
    String? attemptId,
    String cursor = '',
  }) async {
    final data = await _request(
      'notifications',
      query: {
        'deliveryTaskId': deliveryTaskId,
        'attemptId': ?attemptId,
        if (cursor.isNotEmpty) 'cursor': cursor,
      },
    );
    final entries = items(data);
    final total = data['total'];
    if (total is! int ||
        total < entries.length ||
        entries.length > 50 ||
        data['nextCursor'] is! String) {
      throw const FormatException('任务成员分页响应无效。');
    }
    return NotificationMemberPage(
      items: entries,
      total: total,
      nextCursor: data['nextCursor'] as String,
      asOf: wallTime(data['asOf'] as String),
      recovery: RecoveryProgress.fromJson(data['recovery']),
    );
  }

  @override
  Future<List<DeliveryClaim>> claimNotifications() async {
    final data = await _request('notifications/claim', body: {'limit': 20});
    if (data is! List) throw const FormatException('通知领取响应必须是数组。');
    return data.map(DeliveryClaim.fromJson).toList(growable: false);
  }

  @override
  Future<DeliveryStart> startNotification(
    String attemptId,
  ) async => DeliveryStart.fromJson(
    await _request(
      'notifications/delivery-attempts/${Uri.encodeComponent(attemptId)}/start',
      body: <String, dynamic>{},
    ),
  );

  @override
  Future<void> reportNotification(
    String attemptId,
    DeliveryOutcome outcome,
  ) async {
    final data = await _request(
      'notifications/delivery-attempts/${Uri.encodeComponent(attemptId)}/results',
      body: {'outcome': outcome.name},
    );
    if (data is! Map<String, dynamic> ||
        data['attemptId'] != attemptId ||
        data['outcome'] != outcome.name) {
      throw const FormatException('通知回填响应与提交身份不一致。');
    }
  }

  @override
  Future<void> reportExecutorExited(
    String instanceId,
    List<String> attemptIds, {
    required Duration timeout,
  }) async {
    final data = await _request(
      'notifications/executor-exit',
      body: {'instanceId': instanceId, 'attemptIds': attemptIds},
      timeout: timeout,
    );
    if (data is! Map<String, dynamic> || data['accepted'] != true) {
      throw const FormatException('执行者退出响应无效。');
    }
  }

  // 每次刷新从新首分页重建当前范围位置，避免确认后复用过期链。
  Future<_PendingState> _readPending(
    PendingScope scope,
    String date,
    int target,
  ) async {
    final result = _PendingState();
    for (var index = 0; index <= target; index++) {
      final cursor = index == 0 ? '' : result.next;
      if (index > 0 && cursor.isEmpty) break;
      final data = await _request(
        'notifications',
        query: {
          'state': 'unacknowledged',
          'scope': scope.name,
          if (cursor.isNotEmpty) 'cursor': cursor,
        },
      );
      if (dateKey(wallTime(data['asOf'] as String)) != date ||
          dateKey(_clock()) != date) {
        throw const _PendingDateChanged();
      }
      final values = items(data);
      if (index > 0 && values.isEmpty) break;
      result.items = values;
      result.total = data['total'] as int;
      result.next = data['nextCursor'] as String;
      result.page = index;
      if (index > 0) result.cursors.add(cursor);
    }
    return result;
  }

  void _rememberPending(PendingScope scope) {
    final state = _pendingScopes[scope]!;
    for (final entry in state.items) {
      _pendingMembership[entry.id] = scope;
    }
  }

  @override
  Future<void> refresh({DateTime? month, DateTime? date}) =>
      _refreshSnapshot(month: month, date: date);

  Future<void> _refreshSnapshot({
    DateTime? month,
    DateTime? date,
    bool resetPending = false,
  }) async {
    _month = month ?? _month;
    _date = date ?? _date;
    _invalidateReads();
    final generation = _generation;
    _loading = true;
    _error = null;
    _publish();
    try {
      // 两类首分页跨午夜时只重试一次；不发布不同日期的组合。
      for (var attempt = 0; attempt < 2; attempt++) {
        try {
          final snapshot = await _request(
            'schedules',
            query: {'view': 'occurrences', 'include': 'unacknowledged'},
          );
          final now = wallTime(snapshot['asOf'] as String);
          final recovery = RecoveryProgress.fromJson(snapshot['recovery']);
          final day = dateKey(now);
          final requestedMonth = _month ?? DateTime.utc(now.year, now.month);
          final marks = await _request(
            'schedules',
            query: {
              'view': 'calendar',
              'month': dateKey(requestedMonth).substring(0, 7),
            },
          );
          final selected = _date;
          final datePage = selected == null || sameDay(selected, now)
              ? null
              : await _request(
                  'schedules',
                  query: {'view': 'occurrences', 'date': dateKey(selected)},
                );
          final sameDate = !resetPending && _hasSnapshot && sameDay(_now, now);
          final scoped = await Future.wait([
            for (final scope in PendingScope.values)
              _readPending(
                scope,
                day,
                sameDate ? _pendingScopes[scope]!.page : 0,
              ),
          ]);
          if (_disposed || generation != _generation) return;
          if (dateKey(_clock()) != day) throw const _PendingDateChanged();
          if (!sameDay(_now, now)) _pendingMembership.clear();
          _now = now;
          _recovery = recovery;
          _today = items(snapshot['occurrences']);
          _pending = items(snapshot['unacknowledged']);
          _total = snapshot['unacknowledged']['total'] as int;
          for (var index = 0; index < PendingScope.values.length; index++) {
            final scope = PendingScope.values[index];
            final state = scoped[index];
            state.generation = _pendingScopes[scope]!.generation;
            _pendingScopes[scope] = state;
            _rememberPending(scope);
          }
          _marks = (marks as List).cast<String>().toSet();
          _dates.clear();
          _dateCursors.clear();
          _openScheduleCounts.clear();
          _dates[day] = _today;
          final todayCount =
              snapshot['occurrences']['openScheduleCount'] as int?;
          if (todayCount != null) _openScheduleCounts[day] = todayCount;
          _dateCursors[day] = snapshot['occurrences']['nextCursor'] as String;
          if (selected != null && datePage != null) {
            _dates[dateKey(selected)] = items(datePage);
            final count = datePage['openScheduleCount'] as int?;
            if (count != null) _openScheduleCounts[dateKey(selected)] = count;
            _dateCursors[dateKey(selected)] = datePage['nextCursor'] as String;
          }
          _hasSnapshot = true;
          break;
        } on _PendingDateChanged {
          if (attempt == 1) rethrow;
        }
      }
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
  Future<void> loadPendingPage(
    int page, {
    PendingScope scope = PendingScope.today,
  }) async {
    final state = _pendingScopes[scope]!;
    if (_loading || state.loading || page < 0) return;
    if (!pendingCurrent) {
      await refresh();
      return;
    }
    if (page == state.page) return;
    if (page > state.page && state.next.isEmpty) return;
    final cursors = List<String>.of(state.cursors);
    if (page == cursors.length) cursors.add(state.next);
    if (page >= cursors.length) return;
    final generation = _generation, request = ++state.generation;
    state.loading = true;
    state.error = null;
    _publish();
    bool current() =>
        !_disposed &&
        generation == _generation &&
        identical(state, _pendingScopes[scope]) &&
        request == state.generation;
    try {
      final data = await _request(
        'notifications',
        query: {
          'state': 'unacknowledged',
          'scope': scope.name,
          if (cursors[page].isNotEmpty) 'cursor': cursors[page],
        },
      );
      if (!current()) return;
      if (!pendingCurrent ||
          dateKey(wallTime(data['asOf'] as String)) != dateKey(_now)) {
        await refresh();
        return;
      }
      if ((data['items'] as List).isEmpty && page > 0) {
        final first = await _readPending(scope, dateKey(_now), 0);
        if (!current()) return;
        first.generation = state.generation;
        _pendingScopes[scope] = first;
        _rememberPending(scope);
        _publish();
        return;
      }
      state.items = items(data);
      state.total = data['total'] as int;
      state.page = page;
      state.next = data['nextCursor'] as String;
      state.cursors = cursors.take(page + 1).toList();
      _rememberPending(scope);
    } on NotebookApiException catch (e) {
      if (!current()) return;
      if (e.status == 400 &&
          e.code == 'invalid_request' &&
          cursors[page].isNotEmpty) {
        // 日期或游标失效不重试旧 cursor，重新读取两类首分页。
        await _refreshSnapshot(resetPending: true);
      } else {
        state.error = '$e';
      }
    } on _PendingDateChanged {
      if (current()) await refresh();
    } catch (e) {
      if (current()) state.error = '$e';
    } finally {
      if (current()) {
        state.loading = false;
        _publish();
      }
    }
  }

  @override
  Future<void> loadMoreOn(DateTime date) async {
    final key = dateKey(date), cursor = _dateCursors[dateKey(date)] ?? '';
    if (_loading || cursor.isEmpty) return;
    if (!pendingCurrent) {
      await refresh();
      return;
    }
    _invalidateReads();
    final generation = _generation;
    _loading = true;
    _publish();
    try {
      final data = await _request(
        'schedules',
        query: {'view': 'occurrences', 'date': key, 'cursor': cursor},
      );
      if (_disposed || generation != _generation) return;
      if (!pendingCurrent) {
        await refresh();
        return;
      }
      final merged = {
        for (final entry in entriesOn(date)) entry.occurrenceKey: entry,
      };
      for (final entry in items(data)) {
        merged[entry.occurrenceKey] = entry;
      }
      _dates[key] = List.unmodifiable(merged.values);
      _dateCursors[key] = data['nextCursor'] as String;
      final count = data['openScheduleCount'] as int?;
      if (count != null) _openScheduleCounts[key] = count;
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
    _invalidateReads();
    dynamic data;
    try {
      data = await _request(
        'notifications/acknowledgements',
        body: {'notificationIds': ids.map(int.parse).toList()},
      );
    } catch (_) {
      _loading = false;
      _publish();
      rethrow;
    }
    if (_disposed) return;
    _invalidateReads();
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
    for (final scope in PendingScope.values) {
      final state = _pendingScopes[scope]!;
      ++state.generation;
      state.loading = false;
      final removed = state.items
          .where((entry) => times.containsKey(entry.id))
          .length;
      state.items = List.unmodifiable(
        state.items.where((entry) => !times.containsKey(entry.id)),
      );
      // 明确 ID 来自同一范围；跨页选择也必须从该范围完整总数中扣除。
      final scopeIds = ids.where((id) => _pendingMembership[id] == scope);
      final count = scopeIds.length > removed ? scopeIds.length : removed;
      state.total = (state.total - count).clamp(0, state.total);
    }
    _total = (_total - times.length).clamp(0, _total);
    // 不能按本次确认总数扣恢复计数：其中可能有不属于本轮的提醒。
    // 等正式刷新取得新统计；刷新失败时也不继续展示旧未确认数量。
    _recovery = _recovery?.invalidateUnacknowledgedCount();
    for (final id in ids) {
      _pendingMembership.remove(id);
    }
    _loading = false;
    _publish();
    // 已成功的确认不能因后续刷新失败被报告为确认失败。
    await refresh();
  }

  final Map<String, ScheduleSummary> _schedules = {};
  final Map<String, (String, String)> _uncertainWrites = {};

  static ScheduleSummary summary(dynamic raw) {
    final item = raw as Map<String, dynamic>;
    final single = item['type'] == 'single';
    final draft = single
        ? ScheduleDraft(
            name: item['name'] as String,
            description: item['description'] as String? ?? '',
            kind: ScheduleKind.single,
            time: wallTime(item['single']['notifyTime'] as String),
          )
        : ScheduleRules.decode(
            name: item['name'] as String,
            description: item['description'] as String? ?? '',
            cron: (item['period']['cron'] as List).cast<String>(),
            enabled: item['period']['open'] as bool,
            anchor: beijingNow(),
          );
    return ScheduleSummary(
      item['id'].toString(),
      draft,
      deleted: item['cancelled'] == true,
      revision: item['revision'] as int,
    );
  }

  @override
  ScheduleSummary schedule(String id) => _schedules[id]!;

  @override
  Future<ScheduleSummary> loadSchedule(String id) async {
    final result = summary(await _request('schedules/$id'));
    if (!_disposed) _schedules[id] = result;
    return result;
  }

  @override
  Future<List<ScheduleSummary>> listSchedules({int offset = 0}) async {
    final data = await _request(
      'schedules',
      query: {'limit': '50', 'offset': '$offset'},
    );
    final result = (data as List).map(summary).toList(growable: false);
    if (!_disposed) {
      for (final item in result) {
        _schedules[item.id] = item;
      }
    }
    return result;
  }

  @override
  Future<void> save(
    ScheduleDraft draft, {
    String? id,
    int? expectedRevision,
    String? operationKey,
  }) async {
    if (draft.kind == ScheduleKind.single &&
        (draft.time.second != 0 ||
            draft.time.millisecond != 0 ||
            draft.time.microsecond != 0)) {
      throw StateError('新版本仅支持分钟级时间，请选择没有秒或亚秒的时间。');
    }
    final body = <String, Object?>{
      'name': draft.name,
      'description': draft.description,
      'type': draft.kind == ScheduleKind.single ? 'single' : 'period',
      if (id != null)
        'expectedRevision': expectedRevision ?? schedule(id).revision,
      if (draft.kind == ScheduleKind.single)
        'single': {
          'notifyTime':
              '${dateKey(draft.time)}T${timeLabel(draft.time)}:00+08:00',
        }
      else
        'period': {'cron': ScheduleRules.encode(draft), 'open': draft.enabled},
    };
    final fingerprint = jsonEncode(body);
    final session = operationKey ?? 'save-${id ?? "new"}';
    final previous = _uncertainWrites[session];
    if (previous != null && previous.$1 != fingerprint) {
      throw StateError('上次保存结果尚未确认，请保留原草稿重试，以免重复创建或覆盖。');
    }
    final key =
        previous?.$2 ??
        '$session-${DateTime.now().microsecondsSinceEpoch}-${_generation++}';
    _uncertainWrites[session] = (fingerprint, key);
    _invalidateReads();
    try {
      final result = summary(
        await _request(
          id == null ? 'schedules' : 'schedules/$id',
          method: id == null ? 'POST' : 'PUT',
          body: body,
          operationKey: key,
        ),
      );
      _uncertainWrites.remove(session);
      if (_disposed) return;
      _invalidateReads();
      _schedules[result.id] = result;
      await refresh();
    } on NotebookApiException catch (e) {
      if (e.status < 500) {
        _uncertainWrites.remove(session);
      }
      rethrow;
    } finally {
      if (!_disposed) {
        _loading = false;
        _publish();
      }
    }
  }

  @override
  Future<void> delete(String id) async {
    _invalidateReads();
    try {
      await _request('schedules/$id', method: 'DELETE');
      if (_disposed) return;
      _invalidateReads();
      _schedules.remove(id);
      await refresh();
    } finally {
      if (!_disposed) {
        _loading = false;
        _publish();
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _invalidateReads();
    _client.close(force: true);
    super.dispose();
  }
}

// 范围各自拥有分页、加载错误与请求代次；不保存业务可变状态。
class _PendingState {
  List<TriggerSummary> items = const [];
  int total = 0, page = 0, generation = 0;
  List<String> cursors = [''];
  String next = '';
  String? error;
  bool loading = false;
}

class _PendingDateChanged implements Exception {
  const _PendingDateChanged();
  @override
  String toString() => '北京时间日期已变化，请重试读取今日与历史提醒。';
}
