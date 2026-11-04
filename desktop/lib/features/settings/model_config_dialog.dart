import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app/operation_toast.dart';
import '../../data/config_file_store.dart';
import '../chat/chat_view_model.dart';

/// 管理多个官方 DeepSeek 模型，保存后在当前聊天运行中刷新本地可用性。
class ModelConfigDialog extends StatefulWidget {
  const ModelConfigDialog({super.key, required this.store, required this.chat});

  final ConfigFileStore store;
  final ChatViewModel chat;

  @override
  State<ModelConfigDialog> createState() => _ModelConfigDialogState();
}

class _ModelConfigDialogState extends State<ModelConfigDialog> {
  final _form = GlobalKey<FormState>();
  final _models = <_ModelDraft>[];
  int _nextModelId = 0;
  final _key = TextEditingController();
  bool _busy = true;
  bool _loaded = false;
  bool _visibleKey = false;
  bool _allowClose = false;
  String? _error;
  String? _message;
  List<DeepSeekModelSettings>? _pendingApplication;

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
      final settings = await widget.store.readDeepSeekModels();
      if (!mounted) return;
      for (final draft in _models) {
        draft.dispose();
      }
      _models.clear();
      for (final value in settings) {
        _models.add(_ModelDraft(_nextModelId++, value));
      }
      if (_models.isEmpty) _models.add(_ModelDraft(_nextModelId++));
      _loaded = true;
    } catch (_) {
      if (mounted) {
        _error = '模型设置未能读取，请检查本地配置后重试。';
        OperationToast.error(context, _error!);
      }
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
    if (_busy || !_loaded) return;
    if (!_form.currentState!.validate()) {
      OperationToast.error(context, '请检查模型 ID、API Key 和预算。');
      return;
    }
    final settings = [
      for (final draft in _models)
        DeepSeekModelSettings(
          model: draft.model.text.trim(),
          originalModel: draft.originalModel,
          contextWindow: _budget(draft.context.text)!,
          maxOutputTokens: _budget(draft.output.text)!,
        ),
    ];
    setState(() {
      _busy = true;
      _error = null;
      _message = '正在保存模型设置…';
    });
    try {
      await widget.store.saveDeepSeekModels(settings, apiKey: _key.text);
      if (!mounted) return;
      for (final draft in _models) {
        draft.originalModel = draft.model.text.trim();
      }
      _key.clear();
      _visibleKey = false;
      _pendingApplication = settings;
    } on ModelConfigException catch (failure) {
      if (mounted) {
        setState(() => _error = failure.message);
        OperationToast.error(context, _error!);
      }
    } catch (_) {
      if (mounted) {
        setState(() => _error = '模型配置未保存，请检查本地文件格式与读写权限后重试。');
        OperationToast.error(context, _error!);
      }
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
    final available = await widget.chat.applySavedModels(
      provider: 'deepseek',
      models: settings.map((v) => v.model),
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
    if (available) {
      OperationToast.success(context, '本地模型配置已可用');
    } else {
      OperationToast.warning(context, '设置已保存，但服务尚未确认加载。');
    }
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
    for (final draft in _models) {
      draft.dispose();
    }
    _key.dispose();
    super.dispose();
  }

  Widget _modelFields(_ModelDraft draft, int index) => Column(
    key: ValueKey('model-row-${draft.id}'),
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Row(
        children: [
          Expanded(child: Text('模型 ${index + 1}')),
          IconButton(
            tooltip: '移除模型 ${index + 1}',
            onPressed: _busy || _models.length == 1
                ? null
                : () {
                    setState(() => _models.remove(draft));
                    draft.dispose();
                    _edited('');
                  },
            icon: const Icon(Icons.remove_circle_outline),
          ),
        ],
      ),
      TextFormField(
        key: ValueKey(draft.id == 0 ? 'model-id' : 'model-id-${draft.id}'),
        controller: draft.model,
        enabled: !_busy,
        onChanged: _edited,
        decoration: const InputDecoration(labelText: '模型 ID'),
        validator: (v) => v == null || v.trim().isEmpty ? '请填写模型 ID' : null,
      ),
      ExpansionTile(
        key: ValueKey('model-advanced-${draft.id}'),
        title: const Text('高级设置'),
        tilePadding: EdgeInsets.zero,
        maintainState: true,
        children: [
          const Text('这是本地预算初值，请按所用模型的实际容量调整。'),
          TextFormField(
            key: ValueKey('model-context-${draft.id}'),
            controller: draft.context,
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
            key: ValueKey('model-output-${draft.id}'),
            controller: draft.output,
            enabled: !_busy,
            onChanged: _edited,
            keyboardType: TextInputType.number,
            decoration: const InputDecoration(labelText: '最大输出 token'),
            validator: (v) {
              final n = _budget(v ?? '');
              return n == null ||
                      n <= 0 ||
                      n >= (_budget(draft.context.text) ?? 0)
                  ? '请填写小于上下文预算的正整数'
                  : null;
            },
          ),
        ],
      ),
      const SizedBox(height: 16),
    ],
  );

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
                      key: const ValueKey('model-key'),
                      controller: _key,
                      enabled: !_busy,
                      obscureText: !_visibleKey,
                      enableSuggestions: false,
                      autocorrect: false,
                      onChanged: _edited,
                      decoration: InputDecoration(
                        labelText: 'API Key',
                        helperText: '新 Key 用于全部模型；留空保留各模型已有本地密钥。',
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
                    ),
                    const SizedBox(height: 16),
                    for (var i = 0; i < _models.length; i++)
                      _modelFields(_models[i], i),
                    TextButton.icon(
                      onPressed: _busy
                          ? null
                          : () {
                              setState(() {
                                _models.add(_ModelDraft(_nextModelId++));
                              });
                              _edited('');
                            },
                      icon: const Icon(Icons.add),
                      label: const Text('添加模型'),
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

// 每行独立持有草稿和原身份，增删行不改变其他模型的输入。
class _ModelDraft {
  _ModelDraft(this.id, [DeepSeekModelSettings? settings])
    : originalModel = settings?.originalModel,
      model = TextEditingController(text: settings?.model ?? ''),
      context = TextEditingController(
        text: '${settings?.contextWindow ?? 32768}',
      ),
      output = TextEditingController(
        text: '${settings?.maxOutputTokens ?? 8192}',
      );

  final int id;
  String? originalModel;
  final TextEditingController model;
  final TextEditingController context;
  final TextEditingController output;

  void dispose() {
    model.dispose();
    context.dispose();
    output.dispose();
  }
}
