import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../data/config_file_store.dart';
import '../chat/chat_view_model.dart';

/// 配置单个官方 DeepSeek 模型，保存后在当前聊天运行中刷新本地可用性。
class ModelConfigDialog extends StatefulWidget {
  const ModelConfigDialog({super.key, required this.store, required this.chat});

  final ConfigFileStore store;
  final ChatViewModel chat;

  @override
  State<ModelConfigDialog> createState() => _ModelConfigDialogState();
}

class _ModelConfigDialogState extends State<ModelConfigDialog> {
  final _form = GlobalKey<FormState>();
  final _model = TextEditingController();
  final _key = TextEditingController();
  final _context = TextEditingController(text: '32768');
  final _output = TextEditingController(text: '8192');
  bool _busy = true;
  bool _loaded = false;
  bool _visibleKey = false;
  bool _allowClose = false;
  String? _error;
  String? _message;
  DeepSeekModelSettings? _pendingApplication;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final settings = await widget.store.readDeepSeekModel();
      if (!mounted) return;
      if (settings != null) {
        _model.text = settings.model;
        _context.text = settings.contextWindow.toString();
        _output.text = settings.maxOutputTokens.toString();
      }
      _loaded = true;
    } catch (_) {
      if (mounted) _error = '模型设置未能读取，请检查本地配置后重试。';
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _edited(String _) => setState(() {
    _error = null;
    _message = null;
    _pendingApplication = null;
  });

  int? _budget(String text) =>
      RegExp(r'^[0-9]+$').hasMatch(text) ? int.tryParse(text) : null;

  Future<void> _save() async {
    if (_busy || !_loaded || !_form.currentState!.validate()) return;
    final settings = DeepSeekModelSettings(
      model: _model.text.trim(),
      contextWindow: _budget(_context.text)!,
      maxOutputTokens: _budget(_output.text)!,
    );
    setState(() {
      _busy = true;
      _error = null;
      _message = '正在保存模型设置…';
    });
    try {
      await widget.store.saveDeepSeekModel(settings, apiKey: _key.text);
      if (!mounted) return;
      _key.clear();
      _visibleKey = false;
      _pendingApplication = settings;
    } on FormatException catch (failure) {
      if (mounted) setState(() => _error = failure.message);
    } catch (_) {
      if (mounted) setState(() => _error = '保存失败，请检查数据目录的文件权限后重试。');
    } finally {
      if (mounted) {
        setState(() {
          _busy = false;
          _message = null;
        });
      }
    }
    if (mounted && _pendingApplication != null) await _apply();
  }

  Future<void> _apply() async {
    final settings = _pendingApplication;
    if (_busy || settings == null) return;
    setState(() {
      _busy = true;
      _error = null;
      _message = '设置已保存，正在等待本地服务加载…';
    });
    final available = await widget.chat.applySavedModel(
      provider: 'deepseek',
      model: settings.model,
    );
    if (!mounted) return;
    setState(() {
      _busy = false;
      if (available) {
        _pendingApplication = null;
        _message = '本地模型已可用，可以继续对话。认证、余额和网络结果以实际发送为准。';
      } else {
        _message = '设置已保存，但服务尚未确认加载。可重试加载，无需再次保存。';
      }
    });
  }

  void _close() {
    if (_busy || _allowClose) return;
    setState(() => _allowClose = true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.of(context).pop();
    });
  }

  @override
  void dispose() {
    _model.dispose();
    _key.dispose();
    _context.dispose();
    _output.dispose();
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
        title: const Text('DeepSeek 模型设置'),
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
                  const Text('提供方：DeepSeek'),
                  const Text('https://api.deepseek.com'),
                  const SizedBox(height: 20),
                  if (_loaded) ...[
                    TextFormField(
                      key: const ValueKey('model-id'),
                      controller: _model,
                      enabled: !_busy,
                      onChanged: _edited,
                      decoration: const InputDecoration(labelText: '模型 ID'),
                      validator: (v) =>
                          v == null || v.trim().isEmpty ? '请填写模型 ID' : null,
                    ),
                    const SizedBox(height: 16),
                    TextFormField(
                      key: const ValueKey('model-key'),
                      controller: _key,
                      enabled: !_busy,
                      obscureText: !_visibleKey,
                      enableSuggestions: false,
                      autocorrect: false,
                      onChanged: _edited,
                      decoration: InputDecoration(
                        labelText: 'API Key',
                        helperText: '本次保存需填写 Key，仅保存在当前设备。',
                        suffixIcon: IconButton(
                          tooltip: _visibleKey ? '隐藏密钥' : '显示密钥',
                          onPressed: _busy
                              ? null
                              : () =>
                                    setState(() => _visibleKey = !_visibleKey),
                          icon: Icon(
                            _visibleKey
                                ? Icons.visibility_off
                                : Icons.visibility,
                          ),
                        ),
                      ),
                      validator: (v) =>
                          v == null || v.trim().isEmpty ? '请填写 API Key' : null,
                    ),
                    const SizedBox(height: 16),
                    ExpansionTile(
                      title: const Text('高级设置'),
                      tilePadding: EdgeInsets.zero,
                      maintainState: true,
                      children: [
                        const Text('这是本地预算初值，请按所用模型的实际容量调整。'),
                        TextFormField(
                          key: const ValueKey('model-context'),
                          controller: _context,
                          enabled: !_busy,
                          onChanged: _edited,
                          keyboardType: TextInputType.number,
                          decoration: const InputDecoration(labelText: '上下文预算'),
                          validator: (v) {
                            final n = _budget(v ?? '');
                            return n == null || n <= 0 || n > 2147483647
                                ? '请填写正整数，最多 2147483647'
                                : null;
                          },
                        ),
                        TextFormField(
                          key: const ValueKey('model-output'),
                          controller: _output,
                          enabled: !_busy,
                          onChanged: _edited,
                          keyboardType: TextInputType.number,
                          decoration: const InputDecoration(
                            labelText: '最大输出 token',
                          ),
                          validator: (v) {
                            final n = _budget(v ?? '');
                            return n == null ||
                                    n <= 0 ||
                                    n >= (_budget(_context.text) ?? 0)
                                ? '请填写小于上下文预算的正整数'
                                : null;
                          },
                        ),
                      ],
                    ),
                  ],
                  if (_error != null)
                    Text(
                      _error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  if (!_loaded && !_busy)
                    TextButton(onPressed: _load, child: const Text('重试读取')),
                  if (_message != null) Text(_message!),
                ],
              ),
            ),
          ),
        ),
        actions: [
          TextButton(onPressed: _busy ? null : _close, child: const Text('关闭')),
          if (_pendingApplication != null)
            FilledButton(
              onPressed: _busy ? null : _apply,
              child: const Text('重试加载'),
            )
          else
            FilledButton(
              onPressed: _busy || !_loaded ? null : _save,
              child: const Text('保存并应用'),
            ),
        ],
      ),
    ),
  );
}
