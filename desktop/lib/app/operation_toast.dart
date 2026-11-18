import 'dart:async';

import 'package:flutter/material.dart';

enum _ToastKind { success, error, warning }

class _ToastMessage {
  _ToastMessage(this.id, this.kind, this.message);

  final int id;
  final _ToastKind kind;
  final String message;
  Timer? timer;
}

/// 在应用导航器上方显示短暂的操作结果。
class OperationToastHost extends StatefulWidget {
  const OperationToastHost({super.key, required this.child});

  final Widget child;

  @override
  State<OperationToastHost> createState() => _OperationToastHostState();
}

class _OperationToastHostState extends State<OperationToastHost> {
  final List<_ToastMessage> _messages = [];
  var _nextId = 0;

  void show(_ToastKind kind, String message) {
    if (!mounted || message.trim().isEmpty) return;
    final duration = kind == _ToastKind.success
        ? const Duration(seconds: 4)
        : const Duration(seconds: 6);
    final toast = _ToastMessage(++_nextId, kind, message);
    setState(() {
      if (_messages.length == 3) {
        _messages.removeAt(0).timer?.cancel();
      }
      _messages.add(toast);
    });
    toast.timer = Timer(duration, () => _remove(toast.id));
  }

  void _remove(int id) {
    final index = _messages.indexWhere((toast) => toast.id == id);
    if (index < 0 || !mounted) return;
    setState(() => _messages.removeAt(index).timer?.cancel());
  }

  @override
  void dispose() {
    for (final message in _messages) {
      message.timer?.cancel();
    }
    _messages.clear();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => _OperationToastScope(
    host: this,
    child: LayoutBuilder(
      builder: (context, constraints) => Stack(
        fit: StackFit.expand,
        children: [
          widget.child,
          if (_messages.isNotEmpty)
            Positioned(
              right: 24,
              bottom: 24,
              child: SizedBox(
                width: (constraints.maxWidth - 48).clamp(1.0, 420.0).toDouble(),
                child: ConstrainedBox(
                  constraints: BoxConstraints(
                    maxHeight: constraints.maxHeight > 2
                        ? (constraints.maxHeight - 48)
                              .clamp(1.0, constraints.maxHeight * .72)
                              .toDouble()
                        : constraints.maxHeight,
                  ),
                  child: SingleChildScrollView(
                    reverse: true,
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        for (final toast in _messages)
                          Padding(
                            key: ValueKey(toast.id),
                            padding: const EdgeInsets.only(top: 8),
                            child: _ToastCard(
                              toast: toast,
                              onClose: () => _remove(toast.id),
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    ),
  );
}

class _OperationToastScope extends InheritedWidget {
  const _OperationToastScope({required this.host, required super.child});

  final _OperationToastHostState host;

  @override
  bool updateShouldNotify(_OperationToastScope oldWidget) =>
      host != oldWidget.host;
}

/// 操作反馈入口。测试等未装配应用宿主的树会安全忽略调用。
class OperationToast {
  OperationToast._();

  static void success(BuildContext context, String message) =>
      _show(context, _ToastKind.success, message);

  static void error(BuildContext context, String message) =>
      _show(context, _ToastKind.error, message);

  static void warning(BuildContext context, String message) =>
      _show(context, _ToastKind.warning, message);

  static void _show(BuildContext context, _ToastKind kind, String message) {
    context.getInheritedWidgetOfExactType<_OperationToastScope>()?.host.show(
      kind,
      message,
    );
  }
}

class _ToastCard extends StatelessWidget {
  const _ToastCard({required this.toast, required this.onClose});

  final _ToastMessage toast;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    final palette = Theme.of(context).colorScheme;
    final (icon, color) = switch (toast.kind) {
      _ToastKind.success => (
        Icons.check_circle_outline,
        const Color.fromARGB(255, 86, 116, 77),
      ),
      _ToastKind.error => (Icons.error_outline, palette.error),
      _ToastKind.warning => (
        Icons.warning_amber_rounded,
        const Color.fromARGB(255, 154, 107, 37),
      ),
    };
    return Material(
      color: const Color.fromARGB(255, 255, 252, 245),
      elevation: 8,
      shadowColor: Colors.black26,
      borderRadius: BorderRadius.circular(12),
      child: Semantics(
        liveRegion: true,
        container: true,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 10, 6, 10),
          child: Row(
            mainAxisSize: MainAxisSize.max,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Semantics(
                  label: switch (toast.kind) {
                    _ToastKind.success => '成功',
                    _ToastKind.error => '失败',
                    _ToastKind.warning => '提醒',
                  },
                  child: Icon(icon, color: color, size: 20),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 2),
                  child: Text(toast.message, softWrap: true),
                ),
              ),
              Semantics(
                label: '关闭提醒',
                button: true,
                child: IconButton(
                  visualDensity: VisualDensity.compact,
                  constraints: const BoxConstraints(
                    minWidth: 36,
                    minHeight: 36,
                  ),
                  padding: const EdgeInsets.all(6),
                  onPressed: onClose,
                  icon: const Icon(Icons.close, size: 18),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
