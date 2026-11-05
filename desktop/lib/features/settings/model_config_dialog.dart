import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app/operation_toast.dart';
import '../../data/config_file_store.dart';
import '../chat/chat_view_model.dart';

/// 编辑 Chat Completions 连接，保存后在当前进程刷新本地模型可用性。
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
  final _provider = TextEditingController(text: 'deepseek');
  final _baseUrl = TextEditingController(text: 'https://api.deepseek.com');
  List<ModelConnectionSettings> _connections = [];
  ModelConnectionSettings? _source;
  int _selectedConnection = -1;
  String _preset = 'deepseek';
  (String, String) _savedConnection = ('deepseek', 'https://api.deepseek.com');
  bool _busy = true;
  bool _loaded = false;
  bool _visibleKey = false;
  bool _allowClose = false;
  bool _confirming = false;
  final _keyStatus = <String, bool>{};
  List<(String?, String, String, String)> _savedDrafts = [];
  String? _error;
  String? _message;
  ({String provider, Set<String> models})? _pendingApplication;

  List<(String?, String, String, String)> get _drafts => [
    for (final draft in _models)
      (
        draft.originalModel,
        draft.model.text,
        draft.context.text,
        draft.output.text,
      ),
  ];

  bool get _dirty =>
      _loaded &&
      (_key.text.isNotEmpty ||
          (_provider.text, _baseUrl.text) != _savedConnection ||
          !_sameDrafts(_drafts, _savedDrafts));

  bool _sameDrafts(
    List<(String?, String, String, String)> left,
    List<(String?, String, String, String)> right,
  ) =>
      left.length == right.length &&
      Iterable<int>.generate(left.length).every((i) => left[i] == right[i]);

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
      final connections = await widget.store.readModelConnections();
      if (!mounted) return;
      _connections = connections;
      final selected = widget.chat.selectedModel;
      final index = connections.indexWhere(
        (v) =>
            v.provider == selected?.provider &&
            v.models.any((m) => m.model == selected?.model),
      );
      _setConnection(
        index >= 0
            ? index
            : connections.isEmpty
            ? -1
            : 0,
      );
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

  void _setConnection(int index) {
    _selectedConnection = index;
    _source = index < 0 ? null : _connections[index];
    _provider.text = _source?.provider ?? 'deepseek';
    _baseUrl.text = _source?.baseUrl ?? 'https://api.deepseek.com';
    _preset =
        _provider.text == 'deepseek' &&
            _baseUrl.text == 'https://api.deepseek.com'
        ? 'deepseek'
        : 'custom';
    for (final draft in _models) {
      draft.dispose();
    }
    _models.clear();
    _nextModelId = 0;
    _keyStatus.clear();
    for (final model in _source?.models ?? <ChatModelSettings>[]) {
      _models.add(_ModelDraft(_nextModelId++, model));
      _keyStatus['${_source!.provider}\u0000${model.model}'] =
          model.hasLocalKey;
    }
    if (_models.isEmpty) _models.add(_ModelDraft(_nextModelId++));
    _key.clear();
    _visibleKey = false;
    _savedConnection = (_provider.text, _baseUrl.text);
    _savedDrafts = _drafts;
    _pendingApplication = null;
    _error = null;
    _message = null;
  }

  Future<void> _selectConnection(int? index) async {
    if (_busy || _confirming || index == null || index == _selectedConnection) {
      return;
    }
    if (!await _confirmDiscard() || !mounted) return;
    setState(() => _setConnection(index));
  }

  void _edited(String _) => setState(() {
    _error = null;
    _message = null;
    _pendingApplication = null;
  });

  int? _budget(String text) =>
      RegExp(r'^[0-9]+$').hasMatch(text) ? int.tryParse(text) : null;

  Future<void> _save() async {
    if (_busy || _confirming || _allowClose || !_loaded) return;
    if (!_form.currentState!.validate()) {
      OperationToast.error(context, '请检查模型 ID、API Key 和预算。');
      return;
    }
    final models = [
      for (final draft in _models)
        ChatModelSettings(
          model: draft.model.text.trim(),
          originalModel: draft.originalModel,
          contextWindow: _budget(draft.context.text)!,
          maxOutputTokens: _budget(draft.output.text)!,
        ),
    ];
    final settings = ModelConnectionSettings(
      provider: _provider.text.trim(),
      baseUrl: _baseUrl.text.trim(),
      models: models,
      sourceIndex: _source?.sourceIndex,
      sourceProvider: _source?.sourceProvider,
    );
    setState(() {
      _busy = true;
      _error = null;
      _message = '正在保存模型设置…';
    });
    try {
      final expected = await widget.store.saveModelConnection(
        settings,
        apiKey: _key.text,
      );
      if (!mounted) return;
      for (final draft in _models) {
        draft.originalModel = draft.model.text.trim();
        _keyStatus['${settings.provider}\u0000${draft.originalModel}'] = true;
      }
      final index = _source?.sourceIndex ?? _connections.length;
      _source = ModelConnectionSettings(
        provider: settings.provider,
        baseUrl: settings.baseUrl,
        sourceIndex: index,
        sourceProvider: settings.provider,
        models: [
          for (final model in models)
            ChatModelSettings(
              model: model.model,
              originalModel: model.model,
              contextWindow: model.contextWindow,
              maxOutputTokens: model.maxOutputTokens,
              hasLocalKey: true,
            ),
        ],
      );
      _connections = [..._connections];
      if (index == _connections.length) {
        _connections.add(_source!);
      } else {
        _connections[index] = _source!;
      }
      _selectedConnection = index;
      _savedConnection = (_provider.text, _baseUrl.text);
      _savedDrafts = _drafts;
      _key.clear();
      _visibleKey = false;
      _pendingApplication = (provider: settings.provider, models: expected);
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
    if (_busy || _confirming || _allowClose || settings == null) return;
    setState(() {
      _busy = true;
      _error = null;
      _message = '设置已保存，正在等待本地服务加载…';
    });
    final applied = await widget.chat.applySavedModels(
      provider: settings.provider,
      models: settings.models,
    );
    if (!mounted) return;
    final available = applied && widget.chat.acceptingOperations;
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

  Future<void> _close() async {
    if (_busy || _confirming || _allowClose) return;
    if (!await _confirmDiscard() || !mounted) return;
    setState(() => _allowClose = true);
    final route = ModalRoute.of(context);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && route?.isCurrent == true) Navigator.of(context).pop();
    });
  }

  Future<bool> _confirmDiscard() async {
    if (_dirty) {
      _confirming = true;
      bool discard;
      try {
        discard =
            await showDialog<bool>(
              context: context,
              builder: (context) => AlertDialog(
                scrollable: true,
                title: const Text('舍弃未保存的修改？'),
                content: const Text('模型设置尚未保存，关闭后将保留原有配置。'),
                actions: [
                  TextButton(
                    onPressed: () => Navigator.pop(context, false),
                    child: const Text('继续编辑'),
                  ),
                  FilledButton(
                    onPressed: () => Navigator.pop(context, true),
                    child: const Text('舍弃修改'),
                  ),
                ],
              ),
            ) ==
            true;
      } finally {
        _confirming = false;
      }
      if (!mounted || !discard) return false;
    }
    return true;
  }

  @override
  void dispose() {
    for (final draft in _models) {
      draft.dispose();
    }
    _key.dispose();
    _provider.dispose();
    _baseUrl.dispose();
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
      Text(
        _keyStatus['${_provider.text.trim()}\u0000${draft.model.text.trim()}'] ==
                true
            ? '已配置本地密钥'
            : _keyStatus['${_provider.text.trim()}\u0000${draft.model.text.trim()}'] ==
                  false
            ? '未配置本地密钥，请填写新 Key。'
            : '目标模型需有本地密钥，否则请填写新 Key。',
      ),
      ExpansionTile(
        key: ValueKey('model-advanced-${draft.id}'),
        title: const Text('高级设置'),
        tilePadding: EdgeInsets.zero,
        maintainState: true,
        enabled: !_busy,
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
      child: Focus(
        autofocus: true,
        child: AlertDialog(
          scrollable: true,
          title: const Text('模型连接设置'),
          content: SizedBox(
            width: 480,
            child: Form(
              key: _form,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (_busy) const LinearProgressIndicator(),
                  if (_loaded) ...[
                    if (_connections.isNotEmpty)
                      InputDecorator(
                        decoration: const InputDecoration(labelText: '当前连接'),
                        child: DropdownButtonHideUnderline(
                          child: DropdownButton<int>(
                            value: _selectedConnection,
                            isExpanded: true,
                            isDense: true,
                            items: [
                              for (var i = 0; i < _connections.length; i++)
                                DropdownMenuItem(
                                  value: i,
                                  child: Text(_connections[i].provider),
                                ),
                              const DropdownMenuItem(
                                value: -1,
                                child: Text('新建连接'),
                              ),
                            ],
                            onChanged: _busy ? null : _selectConnection,
                          ),
                        ),
                      ),
                    DropdownButtonFormField<String>(
                      key: ValueKey(
                        'connection-preset-$_selectedConnection-$_preset',
                      ),
                      initialValue: _preset,
                      isExpanded: true,
                      decoration: const InputDecoration(labelText: '服务商预设'),
                      items: const [
                        DropdownMenuItem(
                          value: 'deepseek',
                          child: Text('DeepSeek'),
                        ),
                        DropdownMenuItem(value: 'custom', child: Text('自定义')),
                      ],
                      onChanged: _busy
                          ? null
                          : (value) {
                              if (value == null) return;
                              _preset = value;
                              if (value == 'deepseek') {
                                _provider.text = 'deepseek';
                                _baseUrl.text = 'https://api.deepseek.com';
                              }
                              _edited('');
                            },
                    ),
                    const Text('仅支持 Chat Completions；预设可修改，模型 ID 手动填写。'),
                    TextFormField(
                      key: const ValueKey('model-provider'),
                      controller: _provider,
                      enabled: !_busy,
                      onChanged: _edited,
                      decoration: const InputDecoration(
                        labelText: 'provider（连接身份）',
                      ),
                      validator: (v) =>
                          v == null || v.trim().isEmpty ? '请填写 provider' : null,
                    ),
                    TextFormField(
                      key: const ValueKey('model-base-url'),
                      controller: _baseUrl,
                      enabled: !_busy,
                      onChanged: _edited,
                      decoration: const InputDecoration(
                        labelText: '服务地址（Base URL）',
                        helperText: '填写服务根地址（含所需路径前缀），不要加 /chat/completions。',
                        helperMaxLines: 4,
                      ),
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
                        helperText: '新 Key 用于全部模型；留空保留各模型已有本地密钥。',
                        helperMaxLines: 4,
                        errorMaxLines: 3,
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
          actions: [
            TextButton(
              onPressed: _busy ? null : _close,
              child: const Text('关闭'),
            ),
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
