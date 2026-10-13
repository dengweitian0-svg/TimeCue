import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:file_selector/file_selector.dart';
import 'package:path/path.dart' as p;
import '../../app/operation_toast.dart';
import '../../data/config_file_store.dart';

/// 编辑用户覆盖值；默认基线与运行资源仍由 Go 管理。
class SettingsDialog extends StatefulWidget {
  const SettingsDialog({super.key, required this.store});
  final ConfigFileStore store;

  @override
  State<SettingsDialog> createState() => _SettingsDialogState();
}

class _SettingsDialogState extends State<SettingsDialog> {
  static const _levels = ['debug', 'info', 'warn', 'error'];
  var _form = GlobalKey<FormState>();
  final _logPath = TextEditingController();
  final _databasePath = TextEditingController();
  ConfigOverrides? _original;
  String? _level;
  String? _error;
  String? _message;
  bool _busy = true;
  bool _confirming = false;
  bool _allowClose = false;

  String? get _originalLogPath => _displayDirectory(_original?.logPath);

  String? get _originalDatabasePath {
    final value = _original?.databaseDirectory;
    if (value == null || value == ':memory:' || value.startsWith('file:')) {
      return value;
    }
    return _displayDirectory(value);
  }

  String? _displayDirectory(String? value) {
    if (value == null || value.isEmpty) return value;
    final dataDirectory = p.dirname(widget.store.configPath);
    return p.normalize(
      p.isAbsolute(value) ? value : p.join(dataDirectory, value),
    );
  }

  bool get _dirty =>
      _original != null &&
      (_level != _original!.logLevel ||
          _logPath.text != (_originalLogPath ?? '') ||
          _databasePath.text != (_originalDatabasePath ?? ''));

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _apply(ConfigOverrides values) {
    _form = GlobalKey<FormState>();
    _original = values;
    _level = values.logLevel;
    _logPath.text = _displayDirectory(values.logPath) ?? '';
    _databasePath.text = _originalDatabasePath ?? '';
  }

  Future<void> _load() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final values = await widget.store.read();
      if (mounted) setState(() => _apply(values));
    } catch (failure) {
      if (mounted) {
        final message = '读取设置失败：$failure';
        setState(() => _error = message);
        OperationToast.error(context, '设置未能读取，请查看弹窗中的错误并重试。');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _edited() => setState(() {
    _error = null;
    _message = null;
  });

  Future<void> _save() async {
    if (_busy || !_dirty) return;
    if (!_form.currentState!.validate()) {
      OperationToast.error(context, '请检查设置字段并修正错误。');
      return;
    }
    await _write(
      () => widget.store.update(
        logLevel: _level != _original!.logLevel ? _level : null,
        logPath: _logPath.text != (_originalLogPath ?? '')
            ? _logPath.text
            : null,
        databaseDirectory: _databasePath.text != (_originalDatabasePath ?? '')
            ? _databasePath.text
            : null,
      ),
      '设置已保存',
    );
  }

  Future<void> _write(
    Future<ConfigSaveResult> Function() operation,
    String message,
  ) async {
    setState(() {
      _busy = true;
      _error = null;
      _message = null;
    });
    try {
      final result = await operation();
      if (!mounted) return;
      final resultMessage = result.pathsChanged
          ? '$message。路径更改将在下次启动生效，已有数据不会自动搬迁。'
          : '$message。日志级别由本地服务自动加载。';
      setState(() {
        _apply(result.overrides);
        _message = resultMessage;
      });
      OperationToast.success(
        context,
        result.pathsChanged
            ? message.contains('恢复')
                  ? '已恢复默认设置，路径将在下次启动时生效'
                  : '设置已保存，路径将在下次启动时生效'
            : message.contains('恢复')
            ? '已恢复默认设置'
            : '设置已保存',
      );
    } catch (failure) {
      if (mounted) {
        final error = message.contains('恢复')
            ? '恢复默认设置失败：$failure'
            : '保存设置失败：$failure';
        setState(() => _error = error);
        OperationToast.error(context, '设置操作未完成，请查看弹窗中的错误。');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<bool> _confirm(String title, String body, String action) async {
    _confirming = true;
    try {
      return await showDialog<bool>(
            context: context,
            builder: (context) => AlertDialog(
              title: Text(title),
              content: Text(body),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(context, false),
                  child: const Text('取消'),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(context, true),
                  child: Text(action),
                ),
              ],
            ),
          ) ==
          true;
    } finally {
      _confirming = false;
    }
  }

  Future<void> _restore() async {
    if (_busy || _confirming) return;
    final confirmed = await _confirm(
      '恢复默认设置？',
      '日志级别、日志目录和数据库目录将恢复默认配置。未保存的修改会被舍弃，已有数据不会自动搬迁。',
      '恢复默认',
    );
    if (!mounted || !confirmed) return;
    await _write(widget.store.restoreRuntimeDefaults, '已恢复默认设置');
  }

  Future<void> _close() async {
    if (_busy || _confirming || _allowClose) return;
    if (_dirty) {
      final confirmed = await _confirm(
        '舍弃未保存的修改？',
        '设置尚未保存，关闭后将保留原有配置。',
        '舍弃修改',
      );
      if (!mounted || !confirmed) return;
    }
    setState(() => _allowClose = true);
    // 让 PopScope 的关闭许可先同步到路由，再执行关闭。
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.of(context).pop();
    });
  }

  String? _validatePath(String? value, String? original) {
    if ((value ?? '') == (original ?? '')) return null;
    if (value == null || value.trim().isEmpty) {
      return '请填写路径；使用默认配置请点击「恢复默认设置」';
    }
    if (value.contains('\u0000')) return '路径不能包含空字符';
    return null;
  }

  Future<void> _selectDirectory(TextEditingController controller) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
      _message = null;
    });
    try {
      final currentDirectory = controller.text;
      final selected = await getDirectoryPath(
        initialDirectory: p.isAbsolute(currentDirectory)
            ? currentDirectory
            : p.dirname(widget.store.configPath),
      );
      if (!mounted || selected == null) return;
      controller.text = p.normalize(selected);
      _edited();
    } catch (failure) {
      if (mounted) {
        setState(() => _error = '选择目录失败：$failure');
        OperationToast.error(context, '无法打开目录选择器，请查看弹窗中的错误。');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget _directoryField({
    required Key key,
    required String label,
    required String hint,
    required TextEditingController controller,
    required String? original,
  }) => TextFormField(
    key: key,
    controller: controller,
    enabled: !_busy,
    readOnly: true,
    onTap: () => _selectDirectory(controller),
    decoration: InputDecoration(
      labelText: label,
      hintText: hint,
      suffixIcon: IconButton(
        tooltip: '选择$label',
        onPressed: _busy ? null : () => _selectDirectory(controller),
        icon: const Icon(Icons.folder_open_outlined),
      ),
    ),
    validator: (value) => _validatePath(value, original),
  );

  @override
  void dispose() {
    _logPath.dispose();
    _databasePath.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => PopScope<void>(
    canPop: _allowClose,
    onPopInvokedWithResult: (didPop, _) {
      if (!didPop) _close();
    },
    child: CallbackShortcuts(
      bindings: {const SingleActivator(LogicalKeyboardKey.escape): _close},
      child: AlertDialog(
        title: Row(
          children: [
            const Icon(Icons.settings_outlined),
            const SizedBox(width: 12),
            const Expanded(child: Text('设置')),
            IconButton(
              tooltip: '关闭设置',
              onPressed: _busy ? null : _close,
              icon: const Icon(Icons.close),
            ),
          ],
        ),
        content: SizedBox(
          width: 480,
          child: SingleChildScrollView(
            child: Form(
              key: _form,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (_busy) const LinearProgressIndicator(),
                  const Text('日志与数据'),
                  const SizedBox(height: 8),
                  const Text('日志级别会自动加载；日志目录和数据库目录在下次启动生效。'),
                  const SizedBox(height: 24),
                  if (_original != null) ...[
                    DropdownButtonFormField<String>(
                      key: ValueKey(_original),
                      initialValue: _level ?? 'default',
                      isExpanded: true,
                      decoration: const InputDecoration(labelText: '日志级别'),
                      items: [
                        if (_original!.logLevel == null)
                          const DropdownMenuItem(
                            value: 'default',
                            child: Text('使用默认配置'),
                          ),
                        if (_level != null && !_levels.contains(_level))
                          DropdownMenuItem(value: _level, child: Text(_level!)),
                        for (final level in _levels)
                          DropdownMenuItem(value: level, child: Text(level)),
                      ],
                      onChanged: _busy
                          ? null
                          : (value) {
                              _level = value == 'default' ? null : value;
                              _edited();
                            },
                    ),
                    const SizedBox(height: 20),
                    _directoryField(
                      key: const ValueKey('settings-log-path'),
                      controller: _logPath,
                      label: '日志目录',
                      hint: '未设置，使用默认配置',
                      original: _originalLogPath,
                    ),
                    const SizedBox(height: 20),
                    _directoryField(
                      key: const ValueKey('settings-database-path'),
                      controller: _databasePath,
                      label: '数据库目录',
                      hint: '未设置，使用默认配置',
                      original: _originalDatabasePath,
                    ),
                    const SizedBox(height: 16),
                    const Text('更改数据库目录不会搬迁已有日程数据。'),
                    const SizedBox(height: 20),
                  ],
                  if (_error != null) ...[
                    const SizedBox(height: 16),
                    Text(
                      _error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                    if (_original == null)
                      TextButton(
                        onPressed: _busy ? null : _load,
                        child: const Text('重试读取'),
                      ),
                  ],
                  if (_message != null) ...[
                    const SizedBox(height: 16),
                    Text(_message!, key: const ValueKey('settings-result')),
                  ],
                ],
              ),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: _busy || _original == null ? null : _restore,
            child: const Text('恢复默认设置'),
          ),
          TextButton(onPressed: _busy ? null : _close, child: const Text('关闭')),
          FilledButton(
            onPressed: _busy || !_dirty ? null : _save,
            child: const Text('保存设置'),
          ),
        ],
      ),
    ),
  );
}
