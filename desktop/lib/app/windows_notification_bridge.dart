import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter/foundation.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import '../contracts/notebook_repository.dart';
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

enum _Stage { start, showing, result, done, retired, unknown }

class _Execution {
  _Execution(this.claim);
  final DeliveryClaim claim;
  _Stage stage = _Stage.start;
  DeliveryOutcome? outcome;
}

/// 单执行者的内存记录；结果重传不会重新执行平台展示。
class WindowsNotificationBridge extends ChangeNotifier {
  WindowsNotificationBridge({required this.gateway, required this.platform});
  final NotificationDeliveryGateway gateway;
  final NotificationPlatform platform;
  final Map<String, _Execution> _executions = {};
  final Map<String, int> _platformIds = {};
  Timer? _timer;
  Future<void>? _cycle, _initialization;
  bool _ready = false, _disposed = false, _stopping = false;
  bool _needsHealth = true, _polling = false;
  String? _instanceId, _error;
  int _generation = 0, _nextPlatformId = 1;

  String? get error => _error;
  String? get instanceId => _instanceId;
  int get pendingResults => _executions.values
      .where((execution) => execution.stage == _Stage.result)
      .length;

  Future<void> initialize({bool polling = true}) =>
      _initialization ??= _initialize(polling).whenComplete(() {
        _initialization = null;
      });

  Future<void> _initialize(bool polling) async {
    if (_disposed || _stopping) return;
    try {
      if (!_ready) {
        await platform.initialize();
        if (_disposed) {
          platform.dispose();
          return;
        }
        _ready = true;
      }
      _needsHealth = true;
      _error = null;
      notifyListeners();
      _polling = polling;
      _timer?.cancel();
      if (polling) await runOnce();
    } catch (error) {
      _diagnose('初始化', error);
    }
  }

  /// 测试可控推进；生产入口使用同一循环，重入共享在途操作。
  Future<void> runOnce() {
    if (_disposed || !_ready) return Future<void>.value();
    return _cycle ??= _run().whenComplete(() {
      _cycle = null;
      if (!_disposed && !_stopping && _polling) {
        _timer = Timer(const Duration(seconds: 1), () => unawaited(runOnce()));
      }
    });
  }

  Future<void> _run() async {
    final generation = _generation;
    try {
      if (_needsHealth && !await _checkRuntime()) return;
      final advanced = <String>{};
      // 先处理已有待办，再接新任务；每条失败单独处理。
      for (final execution in _executions.values.toList()) {
        if (!_current(generation)) return;
        if (execution.stage == _Stage.start ||
            execution.stage == _Stage.result) {
          advanced.add(execution.claim.attemptId);
          await _advance(execution, generation);
        }
      }
      if (_stopping || !_current(generation)) return;
      if (_needsHealth && !await _checkRuntime()) return;
      final claims = await gateway.claimNotifications();
      if (_stopping || !_current(generation)) return;
      if (claims.any((claim) => !_executions.containsKey(claim.attemptId))) {
        if (!await _checkRuntime() || !_current(generation)) return;
      }
      for (final claim in claims) {
        if (_stopping || !_current(generation)) return;
        final execution = _executions.putIfAbsent(
          claim.attemptId,
          () => _Execution(claim),
        );
        if (!claim.matches(execution.claim)) {
          execution.stage = _Stage.retired;
          _diagnose('领取', const FormatException('重复尝试身份的任务内容不一致。'));
          continue;
        }
        if (!advanced.contains(claim.attemptId)) {
          await _advance(execution, generation);
        }
      }
    } catch (error) {
      if (_current(generation)) _handleError('领取', error);
    }
  }

  Future<bool> _checkRuntime() async {
    final generation = _generation;
    try {
      final runtime = await gateway.deliveryRuntime();
      if (!_current(generation)) return false;
      final changed = _instanceId != null && _instanceId != runtime.instanceId;
      if (changed) {
        ++_generation;
        _executions.clear();
        _platformIds.clear();
      }
      _instanceId = runtime.instanceId;
      _needsHealth = false;
      if (!_executions.values.any(
        (execution) =>
            execution.stage == _Stage.retired ||
            execution.stage == _Stage.unknown,
      )) {
        _error = null;
      }
      if (!_disposed) notifyListeners();
      return !changed;
    } catch (error) {
      if (!_current(generation)) return false;
      _needsHealth = true;
      _diagnose('连接', error);
      return false;
    }
  }

  Future<void> _advance(_Execution execution, int generation) async {
    if (!_current(generation)) return;
    if (_needsHealth && !await _checkRuntime()) return;
    if (!_current(generation)) return;
    final claim = execution.claim;
    try {
      if (execution.stage == _Stage.start) {
        if (_stopping) return;
        final start = await gateway.startNotification(claim.attemptId);
        if (_stopping || !_current(generation)) return;
        if (!start.claim.matches(claim)) {
          throw const FormatException('通知开始响应与领取身份不一致。');
        }
        if (!start.shouldDisplay) {
          execution.stage = _Stage.done;
          return;
        }
        if (_nextPlatformId > 0x7fffffff) {
          throw const FormatException('本次运行的系统通知标识已耗尽。');
        }
        final id = _platformIds.putIfAbsent(
          claim.platformId,
          () => _nextPlatformId++,
        );
        final payload = jsonEncode({
          'instanceId': _instanceId,
          'deliveryTaskId': claim.deliveryTaskId,
          'attemptId': claim.attemptId,
          'taskType': claim.taskType,
        });
        execution.stage = _Stage.showing;
        try {
          execution.outcome = await platform.show(
            id: id,
            start: start,
            payload: payload,
          );
        } catch (error) {
          if (_current(generation)) {
            execution.stage = _Stage.unknown;
            _diagnose('展示结果未知', error, claim.attemptId);
          }
          return;
        }
        if (!_current(generation)) return;
        if (execution.outcome == null) {
          execution.stage = _Stage.unknown;
          _diagnose('展示结果未知', StateError('平台未返回明确结果。'), claim.attemptId);
          return;
        }
        execution.stage = _Stage.result;
      }
      if (execution.stage == _Stage.result) {
        await gateway.reportNotification(claim.attemptId, execution.outcome!);
        if (!_current(generation)) return;
        execution.stage = _Stage.done;
        if (!_disposed) notifyListeners();
      }
    } catch (error) {
      if (!_current(generation)) return;
      if (error is NotebookApiException && error.status == 404) {
        _needsHealth = true;
        await _checkRuntime();
        if (!_current(generation)) return;
        execution.stage = _Stage.retired;
      } else if (error is FormatException ||
          error is NotebookApiException &&
              error.status >= 400 &&
              error.status < 500) {
        execution.stage = _Stage.retired;
      }
      _handleError('投递', error, claim.attemptId);
    }
  }

  bool _current(int generation) => !_disposed && generation == _generation;

  void _handleError(String operation, Object error, [String? attempt]) {
    if (error is NotebookApiException && error.code == 'delivery_stopping') {
      stopClaiming();
    } else if (error is! FormatException &&
        (error is! NotebookApiException || error.status >= 500)) {
      _needsHealth = true;
    }
    _diagnose(operation, error, attempt);
  }

  void _diagnose(String operation, Object error, [String? attempt]) {
    if (_disposed) return;
    // 不记录日程文案和 payload；日志只含执行身份及失败阶段。
    debugPrint(
      'TimeCue 通知 $operation instance=$_instanceId attempt=$attempt error=$error',
    );
    _error = '系统提醒$operation失败：$error';
    notifyListeners();
  }

  void stopClaiming() {
    _stopping = true;
    _timer?.cancel();
  }

  /// 使用宿主剩余预算，超时不改变已知结果或伪造平台失败。
  Future<void> stopAndDrain(Duration budget) async {
    stopClaiming();
    final elapsed = Stopwatch()..start();
    try {
      final active = _cycle;
      if (active != null) await active.timeout(budget);
      while (!_disposed && pendingResults > 0 && elapsed.elapsed < budget) {
        final remaining = budget - elapsed.elapsed;
        await runOnce().timeout(remaining);
        if (pendingResults > 0) {
          final wait = budget - elapsed.elapsed;
          if (wait <= Duration.zero) break;
          await Future<void>.delayed(
            wait < const Duration(seconds: 1)
                ? wait
                : const Duration(seconds: 1),
          );
        }
      }
    } on TimeoutException {
      // 退出可以放弃本次内存回填，不能再次展示或另建持久回执。
    }
  }

  @override
  void dispose() {
    _disposed = true;
    ++_generation;
    _timer?.cancel();
    platform.dispose();
    super.dispose();
  }
}
