import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:tray_manager/tray_manager.dart' as tray;
import 'package:window_manager/window_manager.dart';

/// 窗口与托盘的副作用只经此窄契约执行，业务规则仍归 Go。
abstract interface class DesktopPlatform {
  Future<void> initialize(DesktopLifecycle lifecycle);
  Future<List<DesktopLaunchRequest>> ready();
  Future<void> createTray();
  Future<bool> hasTray();
  Future<void> show();
  Future<void> hide();
  Future<void> beginExit();
  Future<void> removeTray();
  Future<void> destroy();
  void detach();
}

class DesktopLaunchRequest {
  const DesktopLaunchRequest({
    required this.id,
    this.payload,
    this.login = false,
    this.startHidden = false,
  });
  final String id;
  final String? payload;
  final bool login, startHidden;

  factory DesktopLaunchRequest.fromMap(Object? value) {
    if (value is! Map ||
        value['version'] != 1 ||
        value['id'] is! String ||
        (value['id'] as String).isEmpty ||
        value['arguments'] is! List) {
      throw const FormatException('无效启动请求。');
    }
    String? payload;
    var login = false, hidden = false;
    for (final argument in value['arguments'] as List) {
      if (argument == '--login-start') {
        login = true;
      } else if (argument == '--start-hidden') {
        hidden = true;
      } else if (argument is String &&
          argument.startsWith('--activation-payload=')) {
        if (payload != null) throw const FormatException('重复激活参数。');
        payload = argument.substring('--activation-payload='.length);
      } else {
        throw const FormatException('运行中的目录、连接及模式不能通过启动参数更改。');
      }
    }
    final source = payload != null
        ? 'notification'
        : (login ? 'login' : 'open');
    if (value['source'] != source) throw const FormatException('启动来源不匹配。');
    return DesktopLaunchRequest(
      id: value['id'] as String,
      payload: payload,
      login: login,
      startHidden: hidden,
    );
  }
}

enum DesktopState { initializing, visible, hidden, exiting, disposed }

/// 唯一宿主的正式转换；隐藏不释放应用，退出只消费一个共享预算。
class DesktopLifecycle extends ChangeNotifier {
  DesktopLifecycle(
    this.platform, {
    this.exitBudget = const Duration(seconds: 5),
    this.elapsed,
  });
  final DesktopPlatform platform;
  final Duration exitBudget;
  // 测试可注入单调耗时，生产使用 Stopwatch，不受系统时钟调整影响。
  final Duration Function()? elapsed;
  DesktopState _state = DesktopState.initializing;
  bool _trayReady = false;
  String? _error, _windowError;
  DesktopState get state => _state;
  bool get trayReady => _trayReady;
  String? get error => _error;
  bool get windowFailed => _windowError != null && _error == _windowError;
  Future<void> Function(String payload)? onActivate;
  VoidCallback? stopNewWork;
  Future<void> Function(Duration budget)? drain;
  Future<void> Function(Duration budget)? stopOwnedProcess;
  Future<void> Function()? finalizeOwnedProcess;
  VoidCallback? releaseResources;
  Future<void>? _initialization, _trayWork, _exit;
  Future<void> _windowWork = Future<void>.value();
  bool _ready = false, _disposed = false, _wantVisible = true;
  int _windowRevision = 0;
  DesktopLaunchRequest? _pendingActivation;
  bool _pendingOpen = false;
  final Set<String> _requests = {};
  bool get stopping => _state == DesktopState.exiting || _disposed;

  Future<void> initialize() => _initialization ??= _initialize();

  Future<void> _initialize() async {
    try {
      await platform.initialize(this);
      if (stopping) return;
      await retryTray();
      if (stopping) return;
      final launches = await platform.ready();
      if (stopping) return;
      // ready 回应中的旧缓冲不能覆盖等待回应期间到达的新目标。
      final latestActivation = _pendingActivation;
      final latestOpen = _pendingOpen;
      _pendingActivation = null;
      _pendingOpen = false;
      for (final request in launches) {
        await accept(request, initial: true);
      }
      if (latestActivation != null) _pendingActivation = latestActivation;
      _pendingOpen = _pendingOpen || latestOpen;
      _ready = true;
      if (_pendingOpen || _pendingActivation != null) _wantVisible = true;
      if (!_wantVisible && _trayReady) {
        await hideWindow();
      } else {
        await openWindow();
      }
      final activation = _pendingActivation;
      _pendingActivation = null;
      _pendingOpen = false;
      if (activation?.payload case final payload?) await activate(payload);
    } catch (failure) {
      if (stopping) return;
      _ready = true;
      _trayReady = false;
      _report('桌面入口初始化失败：$failure');
      await openWindow();
    }
  }

  Future<void> retryTray() {
    if (stopping) return Future<void>.value();
    return _trayWork ??= _createTray().whenComplete(() => _trayWork = null);
  }

  Future<void> _createTray() async {
    _trayReady = false;
    _publish();
    try {
      await platform.createTray();
      final available = await platform.hasTray();
      if (stopping) return;
      if (!available) throw StateError('系统未接受托盘图标。');
      _trayReady = true;
      _error = null;
      _publish();
    } catch (failure) {
      if (stopping) return;
      _report('托盘不可用，窗口保持可见：$failure');
      await openWindow();
    }
  }

  Future<void> trayLost() async {
    if (stopping) return;
    _trayReady = false;
    await openWindow();
    await retryTray();
  }

  Future<void> accept(
    DesktopLaunchRequest request, {
    bool initial = false,
  }) async {
    if (stopping || !_requests.add(request.id)) return;
    // 传输去重只保留最近请求，业务目标的去重继续由 D6 管理。
    if (_requests.length > 128) _requests.remove(_requests.first);
    if (!_ready) {
      if (request.payload != null) _pendingActivation = request;
      if (!request.login && !initial) _pendingOpen = true;
      if (initial) _wantVisible = !request.startHidden && !request.login;
      return;
    }
    if (request.payload case final payload?) {
      await activate(payload);
    } else if (!request.login) {
      await openWindow();
    }
  }

  Future<void> activate(String payload) async {
    if (stopping) return;
    if (!_ready) {
      _pendingActivation = DesktopLaunchRequest(
        id: 'callback',
        payload: payload,
      );
      return;
    }
    await openWindow();
    if (!stopping) await onActivate?.call(payload);
  }

  Future<void> openWindow() => _visibility(true);
  Future<void> hideWindow() => _visibility(false);

  Future<void> _visibility(bool visible) {
    if (stopping) return Future<void>.value();
    _wantVisible = visible;
    final revision = ++_windowRevision;
    final next = _windowWork.then((_) async {
      if (stopping || revision != _windowRevision) return;
      try {
        if (!visible) {
          final available = _trayReady && await platform.hasTray();
          if (stopping || revision != _windowRevision) return;
          if (!available) {
            _trayReady = false;
            await platform.show();
            if (stopping || revision != _windowRevision) return;
            _state = DesktopState.visible;
            _report('托盘不可用，窗口保持可见。请重试托盘或明确退出。');
            return;
          }
          await platform.hide();
        } else {
          await platform.show();
        }
        if (stopping || revision != _windowRevision) return;
        _state = visible ? DesktopState.visible : DesktopState.hidden;
        // 成功重试只清除本次窗口错误，不能遮蔽独立的托盘故障。
        if (_error == _windowError) _error = null;
        _windowError = null;
        _publish();
      } catch (failure) {
        if (stopping || revision != _windowRevision) return;
        _windowError = '窗口操作失败：$failure';
        _report(_windowError!);
        // 隐藏结果未知时尽力显示，不能发布隐藏成功。
        if (!visible) {
          try {
            await platform.show();
            if (!stopping && revision == _windowRevision) {
              _state = DesktopState.visible;
            }
          } catch (_) {
            // 原生宿主仍保留手动启动唤回入口。
          }
          _publish();
        }
      }
    });
    _windowWork = next;
    return next;
  }

  Future<void> requestExit() {
    if (_exit case final pending?) return pending;
    final completion = Completer<void>();
    _exit = completion.future;
    // 先登记 Future，通知监听重入也只能观察同一退出。
    unawaited(
      _requestExit().then(
        completion.complete,
        onError: completion.completeError,
      ),
    );
    return completion.future;
  }

  Future<void> _requestExit() async {
    if (_disposed) return;
    _state = DesktopState.exiting;
    ++_windowRevision;
    _pendingActivation = null;
    _publish();
    final watch = Stopwatch()..start();
    Duration remaining() {
      final value = exitBudget - (elapsed?.call() ?? watch.elapsed);
      return value.isNegative ? Duration.zero : value;
    }

    Future<void> bounded(Future<void> Function(Duration budget)? action) async {
      final budget = remaining();
      if (action == null || budget <= Duration.zero) return;
      try {
        await action(budget).timeout(budget);
      } catch (failure) {
        _error = '退出清理未完成：$failure';
      }
    }

    try {
      stopNewWork?.call();
    } catch (failure) {
      _error = '停止新工作失败：$failure';
    }
    await bounded((_) => platform.beginExit());
    await bounded(drain);
    await bounded(stopOwnedProcess);
    await bounded((_) => _windowWork);
    await bounded((_) async {
      await _trayWork;
    });
    try {
      // 自有 Job 回收不属于优雅等待，零预算也必须执行。
      await finalizeOwnedProcess?.call();
    } catch (failure) {
      _error = '自有进程最终回收失败：$failure';
    }
    try {
      releaseResources?.call();
    } catch (failure) {
      _error = '资源释放失败：$failure';
    }
    platform.detach();
    await bounded((_) => platform.removeTray());
    // 窗口销毁属于退出提交；清理预算耗尽也必须执行。
    await platform.destroy();
  }

  void _report(String message) {
    _error = message;
    _publish();
  }

  void _publish() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _state = DesktopState.disposed;
    ++_windowRevision;
    platform.detach();
    super.dispose();
  }
}

/// 插件负责展示适配；原生通道只承担就绪、转交和实际托盘核验。
class WindowsDesktopPlatform
    with WindowListener, tray.TrayListener
    implements DesktopPlatform {
  static const channel = MethodChannel('timecue/desktop');
  DesktopLifecycle? _lifecycle;
  bool _attached = false;

  @override
  Future<void> initialize(DesktopLifecycle lifecycle) async {
    if (!Platform.isWindows) throw UnsupportedError('桌面宿主仅支持 Windows。');
    _lifecycle = lifecycle;
    _attached = true;
    channel.setMethodCallHandler((call) async {
      final host = _lifecycle;
      if (host == null || host.stopping) return;
      switch (call.method) {
        case 'launch':
          try {
            await host.accept(DesktopLaunchRequest.fromMap(call.arguments));
          } catch (failure) {
            host._report('启动请求无法处理：$failure');
            await host.openWindow();
          }
        case 'trayLost':
          await host.trayLost();
      }
    });
    await windowManager.ensureInitialized();
    if (lifecycle.stopping) return;
    windowManager.addListener(this);
    tray.trayManager.addListener(this);
    await windowManager.setPreventClose(true);
    if (lifecycle.stopping) {
      detach();
      return;
    }
    await windowManager.waitUntilReadyToShow();
  }

  @override
  Future<List<DesktopLaunchRequest>> ready() async {
    final requests = await channel.invokeListMethod<Object?>('ready') ?? [];
    return requests.map(DesktopLaunchRequest.fromMap).toList();
  }

  @override
  Future<void> createTray() async {
    final host = _lifecycle;
    if (host == null || host.stopping) return;
    // 先核对资源，避免插件将图标加载失败误报成功。
    final icon = File(
      '${File(Platform.resolvedExecutable).parent.path}/data/flutter_assets/assets/tray/app_icon.ico',
    );
    if (!await icon.exists()) throw StateError('缺少托盘图标资源。');
    if (host.stopping) return;
    await tray.trayManager.setIcon('assets/tray/app_icon.ico');
    if (host.stopping) return;
    await tray.trayManager.setToolTip('TimeCue');
    if (host.stopping) return;
    await tray.trayManager.setContextMenu(
      tray.Menu(
        items: [
          tray.MenuItem(key: 'open', label: '打开 TimeCue'),
          tray.MenuItem.separator(),
          tray.MenuItem(key: 'exit', label: '退出 TimeCue'),
        ],
      ),
    );
  }

  @override
  Future<bool> hasTray() async =>
      await channel.invokeMethod<bool>('hasTray') == true;
  @override
  Future<void> show() async {
    await windowManager.setSkipTaskbar(false);
    // window_manager.show 仅排队 ShowWindowAsync，不能立即据此裁定失败。
    // 原生宿主同步显示/恢复并核验后，再通过原插件请求焦点。
    if (await channel.invokeMethod<bool>('showWindow') != true) {
      throw StateError('窗口显示未生效。');
    }
    await windowManager.focus();
  }

  @override
  Future<void> hide() async {
    await windowManager.hide();
    if (await windowManager.isVisible()) throw StateError('窗口隐藏未生效。');
  }

  @override
  Future<void> beginExit() async {
    await channel.invokeMethod<void>('beginExit');
  }

  @override
  Future<void> removeTray() => tray.trayManager.destroy();
  @override
  Future<void> destroy() async {
    await channel.invokeMethod<void>('destroy');
  }

  @override
  void onWindowClose() {
    unawaited(_lifecycle?.hideWindow());
  }

  @override
  void onTrayIconMouseDown() {
    unawaited(_lifecycle?.openWindow());
  }

  @override
  void onTrayIconRightMouseDown() {
    unawaited(
      tray.trayManager.popUpContextMenu().catchError((Object failure) {
        _lifecycle?._report('托盘菜单无法打开：$failure');
        return _lifecycle?.openWindow();
      }),
    );
  }

  @override
  void onTrayMenuItemClick(tray.MenuItem menuItem) {
    if (menuItem.key == 'open') unawaited(_lifecycle?.openWindow());
    if (menuItem.key == 'exit') unawaited(_lifecycle?.requestExit());
  }

  @override
  void detach() {
    if (!_attached) return;
    _attached = false;
    windowManager.removeListener(this);
    tray.trayManager.removeListener(this);
    channel.setMethodCallHandler(null);
    _lifecycle = null;
  }
}
