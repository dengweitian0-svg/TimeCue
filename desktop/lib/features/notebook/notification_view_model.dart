import 'package:flutter/foundation.dart';
import '../../contracts/notebook_repository.dart';
import '../../contracts/notification_query.dart';

/// 只读定位与分页状态；通知的投递循环和确认选择不经过这里。
class NotificationViewModel extends ChangeNotifier {
  NotificationViewModel(this.gateway);
  final NotificationQueryGateway gateway;
  NotificationTarget? target;
  NotificationMemberPage? members;
  TriggerSummary? detail;
  RecoveryProgress? progress;
  String? error, detailError, notice;
  bool active = false, loading = false, detailLoading = false, expired = false;
  int pageIndex = 0;
  int _request = 0, _detailRequest = 0;
  bool _disposed = false;
  List<String> _cursors = [''];
  String _retryCursor = '';
  int _retryPage = 0;
  bool get hasNext => error == null && members?.nextCursor.isNotEmpty == true;
  bool get canPrevious =>
      error == null && pageIndex > 0 && pageIndex <= _cursors.length;
  bool get singleMinute =>
      target?.isActivation == true &&
      target?.taskType == 'minute' &&
      members?.total == 1;
  bool get showsDetail =>
      detail != null || detailLoading || detailError != null;

  Future<void> activate(String payload) async {
    if (_disposed) return;
    try {
      final next = NotificationTarget.fromPayload(payload);
      if (active && target == next) return;
      _open(next);
      await refresh();
    } catch (failure) {
      if (_disposed) return;
      _open(null);
      error = '无法定位该通知：$failure';
      notifyListeners();
    }
  }

  Future<void> openRecovery(RecoveryProgress value) async {
    if (_disposed) return;
    _open(
      NotificationTarget(
        instanceId: null,
        deliveryTaskId: value.deliveryTaskId,
        taskType: 'recovery',
      ),
    );
    progress = value;
    await refresh();
  }

  void _open(NotificationTarget? next) {
    ++_request;
    ++_detailRequest;
    target = next;
    active = true;
    members = null;
    detail = null;
    progress = null;
    error = detailError = notice = null;
    loading = detailLoading = expired = false;
    _cursors = [''];
    pageIndex = 0;
    notifyListeners();
  }

  /// D5 身份变化和 D8 的进程退出均可使当前运行目标立即失效。
  void observeRuntime(String? instanceId) {
    if (_disposed || !active || target?.instanceId == null) return;
    if (instanceId != target!.instanceId) _expire();
  }

  void _expire() {
    if (expired) return;
    ++_request;
    ++_detailRequest;
    expired = true;
    loading = detailLoading = false;
    error = '该通知来自已结束的运行，原成员定位已失效。';
    detailError = detail == null ? null : '运行已结束，保留上次读取的提醒详情。';
    notifyListeners();
  }

  Future<void> refresh() => _readPage('', 0);
  Future<void> refreshCurrent() => _readPage(_cursors[pageIndex], pageIndex);
  Future<void> retry() => _readPage(_retryCursor, _retryPage);
  Future<void> nextPage() async {
    if (!hasNext) return;
    await _readPage(members!.nextCursor, pageIndex + 1);
  }

  Future<void> previousPage() async {
    if (!canPrevious) return;
    await _readPage(_cursors[pageIndex - 1], pageIndex - 1);
  }

  Future<void> _readPage(String cursor, int page) async {
    if (_disposed || !active || target == null || loading || expired) return;
    final request = ++_request;
    bool current() => !_disposed && active && request == _request;
    loading = true;
    error = null;
    _retryCursor = cursor;
    _retryPage = page;
    notifyListeners();
    try {
      final runtime = await gateway.deliveryRuntime();
      if (!current()) return;
      if (target!.instanceId != null &&
          target!.instanceId != runtime.instanceId) {
        _expire();
        return;
      }
      target = target!.bind(runtime.instanceId);
      NotificationMemberPage result;
      try {
        result = await gateway.notificationMembers(
          target!.deliveryTaskId,
          attemptId: target!.attemptId,
          cursor: cursor,
        );
      } on NotebookApiException catch (failure) {
        if (!current()) return;
        if (failure.status != 409 || cursor.isEmpty) rethrow;
        // 只重新读取一次首分页；成员继续变化时由下次刷新处理。
        notice = '恢复记录已更新，已重新读取首分页。';
        _cursors = [''];
        cursor = '';
        page = 0;
        _retryCursor = '';
        _retryPage = 0;
        result = await gateway.notificationMembers(
          target!.deliveryTaskId,
          attemptId: target!.attemptId,
        );
      }
      if (!current()) return;
      if (result.recovery != null &&
          result.recovery!.deliveryTaskId != target!.deliveryTaskId) {
        throw const FormatException('恢复摘要与查询任务不一致。');
      }
      members = result;
      if (!target!.isActivation) progress = result.recovery ?? progress;
      pageIndex = page;
      if (page == 0) _cursors = [''];
      if (_cursors.length <= page) _cursors.add(cursor);
      if (singleMinute && detail == null && result.items.isNotEmpty) {
        await readDetail(result.items.single);
      }
    } on NotebookApiException catch (failure) {
      if (!current()) return;
      if (failure.status == 404) {
        _expire();
      } else {
        error = failure.status == 409
            ? '该次展示成员当前不可读取，请重试原通知。'
            : failure.status == 400
            ? '定位参数或分页已失效，请刷新首分页。'
            : '读取失败：$failure';
        if (failure.status == 400) {
          _cursors = [''];
          _retryCursor = '';
          _retryPage = 0;
        }
      }
    } catch (failure) {
      if (current()) error = '读取失败：$failure';
    } finally {
      if (current()) {
        loading = false;
        notifyListeners();
      }
    }
  }

  Future<void> readDetail(TriggerSummary entry) async {
    if (_disposed || !active) return;
    final request = ++_detailRequest;
    detail = entry;
    detailLoading = true;
    detailError = null;
    notifyListeners();
    try {
      final id = entry.notificationId;
      if (id == null) throw StateError('该记录没有持久提醒身份。');
      final result = await gateway.notificationDetails(id);
      if (_disposed || !active || request != _detailRequest) return;
      detail = result;
    } catch (failure) {
      if (!_disposed && active && request == _detailRequest) {
        detailError = '详情未更新：$failure';
      }
    } finally {
      if (!_disposed && active && request == _detailRequest) {
        detailLoading = false;
        notifyListeners();
      }
    }
  }

  void backToMembers() {
    ++_detailRequest;
    detail = null;
    detailError = null;
    detailLoading = false;
    notifyListeners();
  }

  void close() {
    if (_disposed) return;
    ++_request;
    ++_detailRequest;
    active = loading = detailLoading = false;
    target = null;
    members = null;
    detail = null;
    progress = null;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    ++_request;
    ++_detailRequest;
    super.dispose();
  }
}
