import '../contracts/notification_delivery.dart';

/// 为聊天请求核对服务身份，并在托管服务失效或宿主退出时关闭旧运行。
class ChatRuntimeGuard {
  /// 创建守卫并注入健康查询、托管就绪状态与失效回调。
  ChatRuntimeGuard({
    required this.managed,
    required Future<DeliveryRuntime> Function() health,
    required bool Function() managedReady,
    required void Function() onInvalidated,
  }) : _health = health,
       _managedReady = managedReady,
       _onInvalidated = onInvalidated;

  final bool managed;
  final Future<DeliveryRuntime> Function() _health;
  final bool Function() _managedReady;
  final void Function() _onInvalidated;
  String? _instanceId;
  bool _businessOpened = false;
  bool _invalidated = false;
  bool _closed = false;
  int _generation = 0;

  /// 返回已锚定的本地服务身份；尚未成功核对时返回 null。
  String? get instanceId => _instanceId;

  /// 标记托管服务已通过启动门禁，之后失效必须终止当前聊天运行。
  void markBusinessOpened() {
    if (!_closed && !_invalidated) _businessOpened = true;
  }

  /// 在每个聊天 HTTP/SSE 请求前核对健康状态、服务就绪和身份代次。
  Future<void> beforeRequest() async {
    _ensureUsable();
    if (managed && !_managedReady()) {
      _failManagedReadiness();
      throw StateError('自有本地服务尚未就绪。');
    }
    final generation = _generation;
    late final DeliveryRuntime runtime;
    try {
      runtime = await _health();
    } catch (_) {
      _ensureGeneration(generation);
      if (managed && _businessOpened) _invalidate();
      rethrow;
    }
    _ensureGeneration(generation);
    if (managed && !_managedReady()) {
      _failManagedReadiness();
      throw StateError('自有本地服务已失效。');
    }
    final anchored = _instanceId;
    if (anchored == null) {
      _instanceId = runtime.instanceId;
    } else if (anchored != runtime.instanceId) {
      _invalidate();
      throw StateError('聊天所属本地服务身份已变化，旧操作不会提交到新运行。');
    }
    _ensureGeneration(generation);
  }

  /// 使未完成的健康检查和后续请求失效；可安全重复调用。
  void close() {
    if (_closed) return;
    _closed = true;
    ++_generation;
  }

  /// 因托管进程退出或身份错配而终止当前聊天运行。
  void invalidateRuntime() => _invalidate();

  void _ensureUsable() {
    if (_closed || _invalidated) throw StateError('聊天所属本地服务运行已失效。');
  }

  void _ensureGeneration(int generation) {
    if (_closed || _invalidated || generation != _generation) {
      throw StateError('聊天所属本地服务运行已失效。');
    }
  }

  void _failManagedReadiness() {
    // 启动门禁前允许用户重试；曾开放业务后失效则不复用旧操作。
    if (_businessOpened) _invalidate();
  }

  void _invalidate() {
    if (_invalidated || _closed) return;
    _invalidated = true;
    ++_generation;
    _onInvalidated();
  }
}
