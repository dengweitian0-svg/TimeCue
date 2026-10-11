import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import '../contracts/notification_delivery.dart';
import '../data/config_file_store.dart';

class BackendConfiguration {
  const BackendConfiguration({
    required this.mode,
    required this.dataDir,
    this.executable = '',
  });
  final String mode, dataDir, executable;
  bool get managed => mode == 'managed';
  ConfigFileStore get configFiles => ConfigFileStore(dataDir);

  factory BackendConfiguration.fromNative(Object? raw) {
    if (raw is! Map ||
        (raw['backend'] != 'managed' && raw['backend'] != 'external') ||
        raw['dataDir'] is! String ||
        (raw['dataDir'] as String).isEmpty ||
        raw['executable'] is! String) {
      throw const FormatException('请显式指定运行模式和 --data-dir。');
    }
    final result = BackendConfiguration(
      mode: raw['backend'] as String,
      dataDir: raw['dataDir'] as String,
      executable: raw['executable'] as String,
    );
    if (result.managed && result.executable.isEmpty) {
      throw const FormatException('托管启动需要 --go-executable 的绝对路径。');
    }
    if (!result.managed && result.executable.isNotEmpty) {
      throw const FormatException('外连模式不接受 --go-executable。');
    }
    return result;
  }
}

class OwnedProcessEvent {
  const OwnedProcessEvent(
    this.generation,
    this.type, {
    this.bytes = const [],
    this.exitCode = 0,
  });
  final int generation, exitCode;
  final String type;
  final List<int> bytes;
}

/// 仅管理自有资源；运行资格由 ManagedBackend 核对，不向页面公开句柄。
abstract interface class OwnedProcessPlatform {
  Stream<OwnedProcessEvent> get events;
  Future<int> start(int generation, String instanceId);
  Future<bool> alive();
  Future<void> shutdown();
  Future<void> release();
  void dispose();
}

class WindowsOwnedProcessPlatform implements OwnedProcessPlatform {
  static const channel = MethodChannel('timecue/process');
  final _events = StreamController<OwnedProcessEvent>.broadcast(sync: true);
  bool _disposed = false;

  WindowsOwnedProcessPlatform() {
    channel.setMethodCallHandler((call) async {
      if (_disposed || call.method != 'event' || call.arguments is! Map) return;
      final value = call.arguments as Map;
      final generation = int.tryParse(value['generation']?.toString() ?? '');
      if (generation == null || value['type'] is! String) return;
      _events.add(
        OwnedProcessEvent(
          generation,
          value['type'] as String,
          bytes: value['bytes'] is Uint8List
              ? value['bytes'] as Uint8List
              : const [],
          exitCode: value['exitCode'] as int? ?? 0,
        ),
      );
    });
  }

  static Future<BackendConfiguration> configuration() async =>
      BackendConfiguration.fromNative(
        await channel.invokeMethod<Object?>('configuration'),
      );
  @override
  Stream<OwnedProcessEvent> get events => _events.stream;
  @override
  Future<int> start(int generation, String instanceId) async =>
      (await channel.invokeMethod<int>('start', {
        'generation': '$generation',
        'instanceId': instanceId,
      }))!;
  @override
  Future<bool> alive() async =>
      await channel.invokeMethod<bool>('alive') == true;
  @override
  Future<void> shutdown() => channel.invokeMethod<void>('shutdown');
  @override
  Future<void> release() => channel.invokeMethod<void>('release');
  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    channel.setMethodCallHandler(null);
    unawaited(_events.close());
  }
}

enum BackendState { idle, starting, ready, failed, stopping, stopped }

/// ready、健康和自有进程共同证明运行资格；外部反馈不能绕过正式转换。
class ManagedBackend extends ChangeNotifier {
  ManagedBackend({
    required this.platform,
    required this.health,
    this.startupBudget = const Duration(seconds: 10),
    String Function()? identity,
    Duration Function()? elapsed,
  }) : _identity = identity ?? _newIdentity,
       _elapsed = elapsed {
    _subscription = platform.events.listen(_event);
  }
  final OwnedProcessPlatform platform;
  final Future<DeliveryRuntime> Function() health;
  final Duration startupBudget;
  final String Function() _identity;
  final Duration Function()? _elapsed;
  late final StreamSubscription<OwnedProcessEvent> _subscription;
  final Stopwatch _watch = Stopwatch();
  BackendState _state = BackendState.idle;
  BackendState get state => _state;
  bool get ready => _state == BackendState.ready;
  bool get canRetry =>
      _state == BackendState.failed && !_everReady && _release == null;
  String? error, instanceId;
  int? pid;
  int _generation = 0;
  bool _everReady = false, _disposed = false, _exited = false;
  bool _resourcesReleased = false;
  String _diagnostics = '';
  final List<int> _stdout = [];
  bool _receivedReady = false;
  Completer<Map<String, dynamic>>? _readyLine;
  Completer<void>? _exit;
  Completer<void>? _cancel;
  Future<void>? _startup, _release;
  VoidCallback? onInvalidated;

  static String _newIdentity() {
    final random = Random.secure();
    return List.generate(
      24,
      (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
  }

  Duration get _remaining {
    final value = startupBudget - (_elapsed?.call() ?? _watch.elapsed);
    return value.isNegative ? Duration.zero : value;
  }

  bool _current(int generation) =>
      !_disposed &&
      generation == _generation &&
      _state == BackendState.starting &&
      !_exited;

  Future<void> start() {
    if (_startup != null) return _startup!;
    if (_disposed || (_state != BackendState.idle && !canRetry)) {
      return Future<void>.value();
    }
    return _startup = _start().whenComplete(() => _startup = null);
  }

  Future<T> _bounded<T>(Future<T> Function() work) async {
    if (_state != BackendState.starting || _cancel?.isCompleted == true) {
      throw StateError('本次启动已取消。');
    }
    if (_remaining <= Duration.zero) throw TimeoutException('托管启动超时。');
    return Future.any<T>([
      work(),
      _cancel!.future.then<T>((_) => throw StateError('本次启动已取消。')),
    ]).timeout(_remaining);
  }

  Future<void> _start() async {
    final generation = ++_generation;
    _state = BackendState.starting;
    instanceId = _identity();
    pid = null;
    error = null;
    _resourcesReleased = false;
    _stdout.clear();
    _diagnostics = '';
    _exited = _receivedReady = false;
    _readyLine = Completer<Map<String, dynamic>>();
    _exit = Completer<void>();
    _cancel = Completer<void>();
    _watch.reset();
    _watch.start();
    _publish();
    try {
      final values = await _bounded(
        () => Future.wait<Object>([
          platform.start(generation, instanceId!),
          _readyLine!.future,
        ], eagerError: true),
      );
      if (!_current(generation)) return;
      pid = values[0] as int;
      final line = values[1] as Map<String, dynamic>;
      _verify(line, readyMessage: true);
      while (_current(generation)) {
        DeliveryRuntime runtime;
        try {
          runtime = await _bounded(health);
        } on FormatException {
          rethrow;
        } catch (_) {
          if (!_current(generation) || _remaining <= Duration.zero) rethrow;
          await _bounded(
            () => Future<void>.delayed(const Duration(milliseconds: 100)),
          );
          continue;
        }
        if (!_current(generation)) return;
        runtime.verifyManaged(instanceId!, pid!);
        if (!await _bounded(platform.alive)) {
          throw StateError('Go 已在就绪核验期间退出。');
        }
        if (!_current(generation)) return;
        _state = BackendState.ready;
        _everReady = true;
        _publish();
        return;
      }
    } catch (failure) {
      if (_state == BackendState.stopping ||
          _state == BackendState.stopped ||
          _disposed) {
        return;
      }
      _fail('本地服务启动失败：$failure');
      await releaseOwned();
      _publish();
    }
  }

  void _verify(Map<String, dynamic> value, {required bool readyMessage}) {
    if ((readyMessage && value['event'] != 'ready') ||
        value['address'] != 'http://127.0.0.1:10086') {
      throw const FormatException('ready 地址或事件不匹配。');
    }
    DeliveryRuntime.fromJson({
      ...value,
      'status': 'ok',
    }).verifyManaged(instanceId!, pid!);
  }

  void _event(OwnedProcessEvent event) {
    if (_disposed || event.generation != _generation) return;
    if (event.type == 'stderr') {
      _diagnostics += utf8.decode(event.bytes, allowMalformed: true);
      if (_diagnostics.length > 8192) {
        _diagnostics = _diagnostics.substring(_diagnostics.length - 8192);
      }
      return;
    }
    if (event.type == 'exit') {
      _exited = true;
      if (_exit?.isCompleted == false) _exit!.complete();
      if (_state == BackendState.starting || _state == BackendState.ready) {
        _protocolFailure(StateError('Go 已退出（代码 ${event.exitCode}）。'));
      }
      return;
    }
    if (_state != BackendState.starting && _state != BackendState.ready) return;
    try {
      if (event.type == 'error') throw const FormatException('进程输出超过接收预算。');
      if (event.type == 'stdoutEof') {
        if (!_receivedReady) throw const FormatException('ready 前 stdout 已关闭。');
        return;
      }
      if (event.type != 'stdout') return;
      if (_receivedReady && event.bytes.isNotEmpty) {
        throw const FormatException('stdout 出现重复或非协议输出。');
      }
      _stdout.addAll(event.bytes);
      if (_stdout.length > 16384) {
        throw const FormatException('ready 超过 16 KiB。');
      }
      final newline = _stdout.indexOf(10);
      if (newline < 0) return;
      if (newline != _stdout.length - 1) {
        throw const FormatException('stdout 必须只有一行 ready。');
      }
      final raw = jsonDecode(utf8.decode(_stdout.sublist(0, newline)));
      if (raw is! Map<String, dynamic>) {
        throw const FormatException('ready 不是 JSON 对象。');
      }
      _receivedReady = true;
      _readyLine!.complete(raw);
    } catch (failure) {
      _protocolFailure(failure);
    }
  }

  void invalidateRuntime(String message) {
    if (_state == BackendState.ready || _state == BackendState.starting) {
      _protocolFailure(StateError(message));
    }
  }

  void _protocolFailure(Object failure) {
    if (_readyLine?.isCompleted == false) _readyLine!.completeError(failure);
    if (_cancel?.isCompleted == false) _cancel!.complete();
    _fail('本地服务运行已失效：$failure');
    unawaited(releaseOwned().then((_) => _publish()));
  }

  void _fail(String message) {
    _state = BackendState.failed;
    error = _diagnostics.isEmpty ? message : '$message\n$_diagnostics';
    onInvalidated?.call();
    _publish();
  }

  void cancelStart() {
    if (_disposed ||
        _state == BackendState.stopped ||
        _state == BackendState.stopping) {
      return;
    }
    _state = BackendState.stopping;
    if (_cancel?.isCompleted == false) _cancel!.complete();
    _publish();
  }

  Future<void> stopOwned(Duration remaining) async {
    cancelStart();
    try {
      if (remaining > Duration.zero && !_exited) {
        final watch = Stopwatch()..start();
        await platform.shutdown().timeout(remaining);
        final left = remaining - watch.elapsed;
        if (left > Duration.zero && _exit != null) {
          await _exit!.future.timeout(left);
        }
      }
    } on TimeoutException {
      // 协作预算耗尽后由 Job 回收，不开启新的优雅等待。
    } finally {
      await releaseOwned();
      if (!_disposed) {
        _state = BackendState.stopped;
        _publish();
      }
    }
  }

  Future<void> releaseOwned() {
    if (_resourcesReleased) return Future<void>.value();
    return _release ??= platform
        .release()
        .then((_) {
          _resourcesReleased = true;
        })
        .whenComplete(() => _release = null);
  }

  void _publish() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    if (_cancel?.isCompleted == false) _cancel!.complete();
    unawaited(_subscription.cancel());
    unawaited(releaseOwned().whenComplete(platform.dispose));
    super.dispose();
  }
}
