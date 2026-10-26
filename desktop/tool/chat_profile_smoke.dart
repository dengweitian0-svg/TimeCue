import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' show FramePhase, PlatformDispatcher;

import 'package:flutter/gestures.dart';
import 'package:flutter/foundation.dart' show kProfileMode;
import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import 'package:window_manager/window_manager.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/demo_notebook_repository.dart';
import 'package:timecue_desktop/features/chat/chat_markdown_body.dart';
import 'package:timecue_desktop/features/chat/chat_message_list.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';
import 'package:timecue_desktop/features/notebook/chat_spread.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';
import 'package:timecue_desktop/features/notebook/notebook_view_model.dart';
import 'package:timecue_desktop/theme/notebook_theme.dart';

import 'chat_acceptance_fixture.dart';

final _hostKey = GlobalKey<_ProfileHostState>();
const _profileMaximumWindowSize = Size(2048, 1400);
final _frameStages = <int, Map<String, Object?>>{};
final _frameMarkers = <Map<String, Object?>>[];
final _frameSamples = <Map<String, Object?>>[];
final _flutterErrors = <String>[];
String _samplingCase = 'startup';
String _samplingPhase = 'startup';

void _setSamplingPhase(String caseName, String phase) {
  _samplingCase = caseName;
  _samplingPhase = phase;
}

/// 启动真实 Flutter Windows profile 页面并执行聊天响应性检查。
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final gateway = ChatAcceptanceGateway(historySessionCount: 4);
  final chat = ChatViewModel(gateway: gateway);
  final notebook = NotebookViewModel(DemoNotebookRepository());
  notebook.changeTab(NotebookTab.ai);
  await chat.loadHistory();
  chat.selectSession('acceptance-history-1');

  final previousFlutterError = FlutterError.onError;
  FlutterError.onError = (details) {
    _flutterErrors.add('${details.exception}\n${details.stack ?? ''}');
    previousFlutterError?.call(details);
  };
  PlatformDispatcher.instance.onError = (error, stack) {
    _flutterErrors.add('$error\n$stack');
    return true;
  };
  WidgetsBinding.instance.addPersistentFrameCallback((_) {
    final frameNumber = PlatformDispatcher.instance.frameData.frameNumber;
    final vsyncTimestamp =
        SchedulerBinding.instance.currentSystemFrameTimeStamp.inMicroseconds;
    final marker = <String, Object?>{
      'engine_frame_number': frameNumber,
      'scheduler_vsync_timestamp_us': vsyncTimestamp,
      'case': _samplingCase,
      'phase': _samplingPhase,
      'marker_index': _frameMarkers.length,
    };
    _frameMarkers.add(marker);
    if (frameNumber >= 0) _frameStages[frameNumber] = marker;
  });
  WidgetsBinding.instance.addTimingsCallback((frames) {
    for (final frame in frames) {
      final frameNumber = frame.frameNumber;
      final stage = _frameStages[frameNumber];
      _frameSamples.add({
        'frame_number': frameNumber,
        'matched_engine_frame_number': stage?['engine_frame_number'],
        'marker_index': stage?['marker_index'],
        'marker_scheduler_vsync_timestamp_us':
            stage?['scheduler_vsync_timestamp_us'],
        'vsync_start_timestamp_us': frame.timestampInMicroseconds(
          FramePhase.vsyncStart,
        ),
        'build_start_timestamp_us': frame.timestampInMicroseconds(
          FramePhase.buildStart,
        ),
        'build_finish_timestamp_us': frame.timestampInMicroseconds(
          FramePhase.buildFinish,
        ),
        'raster_start_timestamp_us': frame.timestampInMicroseconds(
          FramePhase.rasterStart,
        ),
        'raster_finish_timestamp_us': frame.timestampInMicroseconds(
          FramePhase.rasterFinish,
        ),
        'case': stage?['case'] ?? 'unclassified',
        'phase': stage?['phase'] ?? 'unclassified',
        'matched_frame_number': stage != null,
        'build_us': frame.buildDuration.inMicroseconds,
        'raster_us': frame.rasterDuration.inMicroseconds,
        'total_us': frame.totalSpan.inMicroseconds,
      });
    }
  });

  final failures = <String>[];
  final interactions = <String, int>{};
  final stream = <String, Object?>{};
  final layoutRecords = <Map<String, Object?>>[];
  var nativeDpr = 1.0;
  final gatewayNotices = {'history_during_stream': 0};
  chat.historyListenable.addListener(() {
    gatewayNotices['history_during_stream'] =
        gatewayNotices['history_during_stream']! + 1;
  });

  try {
    await windowManager.ensureInitialized();
    await windowManager.setMaximumSize(_profileMaximumWindowSize);
    await windowManager.setSize(const Size(1280, 800));
    await windowManager.focus();
    runApp(_ProfileHost(key: _hostKey, notebook: notebook, chat: chat));
    await _waitFor(() => _findMarkdownBody('history-run-1-2') != null);

    final longMarkdown = gateway.longChatMarkdown;
    _check(
      failures,
      longMarkdown.length >= 20000,
      '夹具长 Markdown 字符数 ${longMarkdown.length} 少于 20000',
    );
    final historicalBody = _findMarkdownBody('history-run-1-2');
    _check(failures, historicalBody?.data == longMarkdown, '历史长回复未按原文加载');
    stream['historical_markdown_chars'] = longMarkdown.length;
    stream['history_session_count'] = chat.historyConversations.length;
    stream['history_turn_count'] = chat.historyConversations.fold<int>(
      0,
      (count, session) => count + session.runs.length,
    );

    final nativeView = WidgetsBinding.instance.platformDispatcher.views.first;
    nativeDpr = nativeView.devicePixelRatio;
    final initialClientSize = _logicalClientSize(nativeView);
    final initialWindowSize = await windowManager.getSize();
    final windowDecoration = Size(
      (initialWindowSize.width - initialClientSize.width).clamp(0.0, 80.0),
      (initialWindowSize.height - initialClientSize.height).clamp(0.0, 100.0),
    );
    final layouts = [
      (size: const Size(1280, 800), scale: 1.0),
      (size: const Size(640, 480), scale: 1.0),
      (size: const Size(640, 480), scale: 1.6),
      (size: const Size(1440, 900), scale: 1.0),
    ];
    for (final layout in layouts) {
      final size = layout.size;
      final textScale = layout.scale;
      final scaleName = textScale == 1 ? '' : '-scale$textScale';
      final caseName = '${size.width.toInt()}x${size.height.toInt()}$scaleName';
      _setSamplingPhase(caseName, 'resize-transition');
      _hostKey.currentState!.setTextScale(textScale);
      final resizeWatch = Stopwatch()..start();
      await windowManager.setSize(
        Size(
          size.width + windowDecoration.width,
          size.height + windowDecoration.height,
        ),
      );
      await windowManager.focus();
      await _waitForClientSize(nativeView, size);
      // 尺寸稳定后再等实际 Flutter 帧完成布局和资源重排。
      await _waitForRenderedFrames(3);
      interactions['${caseName}_resize_to_client_ms'] =
          resizeWatch.elapsedMilliseconds;
      final listElement = _findWidgetElement(ChatSpread);
      _check(failures, listElement != null, '$caseName 下 ChatSpread 未挂载');
      if (size.width == 640) {
        final fieldElement = _findWidgetElement(
          TextField,
          key: const ValueKey('chat-draft-input'),
        );
        final fieldRender = fieldElement?.findRenderObject();
        _check(
          failures,
          fieldRender is RenderBox &&
              fieldRender.hasSize &&
              fieldRender.size.height > 0,
          '640x480 下生产输入区不可见',
        );
      }
      _setSamplingPhase(caseName, 'steady');
      final scrollResult = await _scrollChatMessageList(
        chat.selectedSessionId!,
      );
      _check(
        failures,
        scrollResult['moved'] == true,
        '$caseName 下真实消息滚动位置没有变化',
      );
      final animationResult = await _profileScrollAnimation(
        chat.selectedSessionId!,
      );
      _check(
        failures,
        animationResult['moved'] == true,
        '$caseName 稳态滚动动画没有实际移动内容',
      );
      layoutRecords.add({
        'case': caseName,
        'requested_client_size': [size.width, size.height],
        'requested_outer_size': [
          size.width + windowDecoration.width,
          size.height + windowDecoration.height,
        ],
        'window_maximum_tracking_size_override': [
          _profileMaximumWindowSize.width,
          _profileMaximumWindowSize.height,
        ],
        'may_extend_beyond_work_area': true,
        'window_decoration_delta': [
          windowDecoration.width,
          windowDecoration.height,
        ],
        'native_device_pixel_ratio': nativeView.devicePixelRatio,
        'native_physical_size': [
          nativeView.physicalSize.width,
          nativeView.physicalSize.height,
        ],
        'native_logical_client_size': [
          _logicalClientSize(nativeView).width,
          _logicalClientSize(nativeView).height,
        ],
        'resize_to_client_ms': resizeWatch.elapsedMilliseconds,
        'text_scale': textScale,
        'text_scale_source': textScale == 1
            ? 'native Flutter view default'
            : 'framework MediaQuery scenario; not Windows display scaling evidence',
        'resize_method': 'window_manager native window resize',
        'chat_spread_mounted': listElement != null,
        'message_scroll': scrollResult,
        'steady_scroll_animation': animationResult,
      });
    }

    _setSamplingPhase('1280x800', 'resize-transition');
    _hostKey.currentState!.setTextScale(1);
    await windowManager.setSize(
      Size(1280 + windowDecoration.width, 800 + windowDecoration.height),
    );
    await _waitForClientSize(nativeView, const Size(1280, 800));
    await _waitForRenderedFrames(3);

    // 通过生产 TextField/controller 输入，并向生产发送按钮派发真实 Flutter 指针事件。
    final local = chat.createLocalSession();
    await _waitFor(() => _messageListMounted(local.sessionId));
    const originalInput = 'Profile 原文：保留中文、punctuation 和 newline。';
    final field = _findTextField()!;
    field.controller!.value = const TextEditingValue(
      text: originalInput,
      selection: TextSelection.collapsed(offset: originalInput.length),
    );
    field.onChanged?.call(originalInput);
    const firstSendKey = ValueKey('chat-send-button');
    await _waitFor(
      () =>
          _messageListMounted(local.sessionId) && _buttonEnabled(firstSendKey),
    );
    final sendButton = _findKey(firstSendKey);
    final sendWatch = Stopwatch()..start();
    if (sendButton != null) _tap(sendButton);
    await _waitFor(() => gateway.createCalls == 1);
    interactions['send_to_gateway_ms'] = sendWatch.elapsedMilliseconds;
    _check(failures, sendButton != null, '生产发送按钮未找到');
    _check(
      failures,
      sendWatch.elapsed < const Duration(milliseconds: 700),
      '发送操作未在 700ms 内到达 Gateway',
    );
    final runId = gateway.createdRunIds.firstOrNull;
    _check(failures, runId != null, '发送没有创建 run');

    var historyStreamNoticeDelta = -1;
    var historyNoticesAtTextBaseline = 0;
    var textNoticeDelta = 0;
    var didExerciseStreamingActions = false;
    if (runId != null) {
      _setSamplingPhase('stream-1280x800', 'steady');
      gateway.emitThinking(runId, 1, '不得展示的思考原文');
      await Future<void>.delayed(const Duration(milliseconds: 25));
      var eventId = 2;
      var offset = 0;
      var visibleChunkCount = 0;
      while (offset < longMarkdown.length) {
        final end = (offset + 420).clamp(0, longMarkdown.length);
        gateway.emitText(runId, eventId++, longMarkdown.substring(offset, end));
        offset = end;
        visibleChunkCount++;
        if (visibleChunkCount == 1) {
          await Future<void>.delayed(const Duration(milliseconds: 40));
          historyNoticesAtTextBaseline =
              gatewayNotices['history_during_stream']!;
        }
        if (!didExerciseStreamingActions &&
            offset >= longMarkdown.length ~/ 2) {
          textNoticeDelta +=
              gatewayNotices['history_during_stream']! -
              historyNoticesAtTextBaseline;
          didExerciseStreamingActions = true;
          final inputWatch = Stopwatch()..start();
          final streamField = _findTextField();
          const nextDraft = '流式期间继续编辑下一条草稿';
          streamField?.controller?.value = const TextEditingValue(
            text: nextDraft,
            selection: TextSelection.collapsed(offset: nextDraft.length),
          );
          streamField?.onChanged?.call(nextDraft);
          interactions['draft_input_during_stream_ms'] =
              inputWatch.elapsedMilliseconds;

          final scrollWatch = Stopwatch()..start();
          final liveScroll = await _scrollChatMessageList(local.sessionId);
          interactions['scroll_during_stream_ms'] =
              scrollWatch.elapsedMilliseconds;
          _check(failures, liveScroll['moved'] == true, '流式期间生产消息列表滚动位置没有变化');
          stream['live_stream_scroll'] = liveScroll;

          final historyItem = _findText('项目计划 1：长内容与表格验收');
          final switchDuringStream = Stopwatch()..start();
          if (historyItem != null) _tap(historyItem);
          await _waitFor(
            () => chat.selectedSessionId == 'acceptance-history-1',
          );
          await _waitFor(() => _messageListMounted('acceptance-history-1'));
          interactions['session_switch_during_stream_ms'] =
              switchDuringStream.elapsedMilliseconds;
          _check(failures, historyItem != null, '流式期间历史会话项未找到');
          chat.selectSession(local.sessionId);
          await _waitFor(() => _messageListMounted(local.sessionId));
          // 主动切会话会合法通知历史列表；等待它们完成后再计纯正文增量。
          await Future<void>.delayed(const Duration(milliseconds: 40));
          historyNoticesAtTextBaseline =
              gatewayNotices['history_during_stream']!;
        }
        await Future<void>.delayed(const Duration(milliseconds: 22));
      }
      textNoticeDelta +=
          gatewayNotices['history_during_stream']! -
          historyNoticesAtTextBaseline;
      historyStreamNoticeDelta = textNoticeDelta;
      _check(failures, historyStreamNoticeDelta == 0, '纯正文增量触发了历史列表 notifier');
      await _waitFor(
        () =>
            local.runs.last.rawText == longMarkdown &&
            _findMarkdownBody(local.runs.last.messageId)?.data == longMarkdown,
      );
      _setSamplingPhase('stream-1280x800', 'interaction');
      gateway.finish(
        runId,
        status: ChatRunStatus.succeeded,
        output: longMarkdown,
      );
      await _waitFor(() => local.runs.isNotEmpty && local.runs.last.isTerminal);
      _check(failures, local.runs.last.input == originalInput, '发送输入原文发生变化');
      _check(failures, local.runs.last.rawText == longMarkdown, '流式最终原文不一致');
      _check(
        failures,
        !local.runs.last.rawText.contains('不得展示的思考原文'),
        'Thinking 原文泄漏进正式回复',
      );
      stream['stream_chunks'] = visibleChunkCount;
      stream['stream_duration_ms'] = sendWatch.elapsedMilliseconds;
      stream['streamed_chars'] = local.runs.last.rawText.length;
      stream['stream_original_matches'] =
          local.runs.last.rawText == longMarkdown;
      stream['stream_actions_exercised'] = didExerciseStreamingActions;
      stream['history_list_notifications_during_text_only'] =
          historyStreamNoticeDelta;
      stream['thinking_excluded'] = !local.runs.last.rawText.contains(
        '不得展示的思考原文',
      );

      if (!local.followLatest) {
        await _waitFor(() => _findText('回到最新') != null);
        _tap(_findText('回到最新')!);
        await _waitFor(
          () => local.followLatest && _messageAtLatest(local.sessionId),
        );
      }

      final finalMessageId = local.runs.last.messageId;
      await _waitFor(() {
        final body = _findMarkdownBody(finalMessageId);
        return local.runs.last.isTerminal &&
            body?.isStreaming == false &&
            body?.data == longMarkdown;
      });

      // 正文不变时，仅改变草稿；Markdown 内部缓存的 GptMarkdown 实例必须保持身份。
      final beforeMarkdown = _findDescendantWidget(
        _findElementByKey(
          ValueKey('chat-assistant-${local.runs.last.messageId}'),
        ),
        'GptMarkdown',
      );
      chat.updateDraft(local.sessionId, '只修改草稿，历史正文保持不变');
      await Future<void>.delayed(const Duration(milliseconds: 60));
      final afterMarkdown = _findDescendantWidget(
        _findElementByKey(
          ValueKey('chat-assistant-${local.runs.last.messageId}'),
        ),
        'GptMarkdown',
      );
      final cachedMarkdownStable =
          beforeMarkdown != null && identical(beforeMarkdown, afterMarkdown);
      stream['unchanged_markdown_widget_reused'] = cachedMarkdownStable;
      _check(failures, cachedMarkdownStable, '正文未变化时 GptMarkdown 实例被替换');
      interactions['unchanged_body_rebuild_check_ms'] = 60;
    }

    // 切换到真实历史列表中的会话，核验完整名称和多轮历史，再回到正在运行的本地会话。
    final historyTitle = _findText('项目计划 1：长内容与表格验收');
    final switchWatch = Stopwatch()..start();
    if (historyTitle != null) _tap(historyTitle);
    await _waitFor(() => chat.selectedSessionId == 'acceptance-history-1');
    await _waitFor(() => _messageListMounted('acceptance-history-1'));
    interactions['session_switch_ms'] = switchWatch.elapsedMilliseconds;
    _check(failures, historyTitle != null, '历史会话列表中的生产会话项未找到');
    _check(failures, chat.selectedConversation?.runs.length == 3, '多轮历史未完整保留');
    chat.selectSession(local.sessionId);
    await _waitFor(() => _messageListMounted(local.sessionId));

    // 使用生产停止按钮发送取消；终态仍由夹具 end/snapshot 事实驱动。
    chat.updateDraft(local.sessionId, '需要停止的第二轮');
    final currentField = _findTextField();
    currentField?.controller?.value = const TextEditingValue(
      text: '需要停止的第二轮',
      selection: TextSelection.collapsed(offset: 9),
    );
    currentField?.onChanged?.call('需要停止的第二轮');
    const secondSendKey = ValueKey('chat-send-button');
    await _waitFor(
      () =>
          _messageListMounted(local.sessionId) && _buttonEnabled(secondSendKey),
    );
    final currentSend = _findKey(secondSendKey);
    if (currentSend != null) _tap(currentSend);
    await _waitFor(() => gateway.createCalls == 2);
    final secondRun = gateway.createdRunIds.last;
    const stopKey = ValueKey('chat-stop-button');
    await _waitFor(() => _buttonEnabled(stopKey));
    final stopButton = _findKey(stopKey);
    final stopWatch = Stopwatch()..start();
    if (stopButton != null) _tap(stopButton);
    await _waitFor(() => gateway.cancelCalls == 1);
    interactions['stop_to_gateway_ms'] = stopWatch.elapsedMilliseconds;
    _check(failures, stopButton != null, '生产停止按钮未找到');
    _check(
      failures,
      stopWatch.elapsed < const Duration(milliseconds: 700),
      '停止操作未在 700ms 内到达 Gateway',
    );
    gateway.finish(secondRun, status: ChatRunStatus.cancelled);
    await _waitFor(() => local.runs.last.phase == ChatRunPhase.cancelled);

    // 隐藏展示期间仍接收事件但不发布 Widget 通知，恢复后以累计原文一次收敛。
    chat.updateDraft(local.sessionId, '后台接收用第三轮');
    final thirdField = _findTextField();
    thirdField?.controller?.value = const TextEditingValue(
      text: '后台接收用第三轮',
      selection: TextSelection.collapsed(offset: 9),
    );
    thirdField?.onChanged?.call('后台接收用第三轮');
    const thirdSendKey = ValueKey('chat-send-button');
    await _waitFor(
      () =>
          _messageListMounted(local.sessionId) && _buttonEnabled(thirdSendKey),
    );
    final thirdSend = _findKey(thirdSendKey);
    if (thirdSend != null) _tap(thirdSend);
    await _waitFor(() => gateway.createCalls == 3);
    final thirdRun = gateway.createdRunIds.last;
    await _waitFor(() => _messageListMounted(local.sessionId));
    var noticesBeforeHidden = 0;
    chat.addListener(() => noticesBeforeHidden++);
    chat.setPresentationVisible(false);
    await windowManager.hide();
    final nativeWindowHidden = !(await windowManager.isVisible());
    final hiddenNoticeBaseline = noticesBeforeHidden;
    gateway.emitText(thirdRun, 1, '窗口隐藏时仍收到的累计内容。');
    await _waitFor(() => local.runs.last.rawText.contains('窗口隐藏时仍收到的累计内容。'));
    final backgroundRaw = local.runs.last.rawText;
    final hiddenNotices = noticesBeforeHidden - hiddenNoticeBaseline;
    chat.setPresentationVisible(true);
    await windowManager.show();
    await windowManager.focus();
    await Future<void>.delayed(const Duration(milliseconds: 70));
    _check(failures, backgroundRaw.contains('窗口隐藏时仍收到的累计内容。'), '隐藏时未接收流式事件');
    _check(failures, hiddenNotices == 0, '隐藏期间仍通知可见界面');
    final restoredBody = _findMarkdownBody(local.runs.last.messageId);
    final restoredRenderer = _findDescendantWidget(
      _findElementByKey(
        ValueKey('chat-assistant-${local.runs.last.messageId}'),
      ),
      'GptMarkdown',
    );
    _check(
      failures,
      restoredBody?.data == backgroundRaw && restoredRenderer != null,
      '唤回后累计原文未挂载到 Markdown renderer',
    );
    stream['hidden_background_received'] = backgroundRaw.contains(
      '窗口隐藏时仍收到的累计内容。',
    );
    stream['native_window_hidden'] = nativeWindowHidden;
    stream['hidden_ui_notifications'] = hiddenNotices;
    _check(failures, nativeWindowHidden, 'profile smoke 原生窗口未能隐藏');
    gateway.finish(
      thirdRun,
      status: ChatRunStatus.succeeded,
      output: backgroundRaw,
    );
    await _waitFor(() => local.runs.last.isTerminal);

    // SSE 正文变化不得触发历史索引列表更新。
    stream['history_list_notifications_during_stream'] =
        historyStreamNoticeDelta;
    stream['original_user_input'] = local.runs.first.input;
    stream['stop_cancelled'] = local.runs[1].phase == ChatRunPhase.cancelled;
    for (final entry in interactions.entries) {
      if (entry.key.endsWith('_resize_to_client_ms')) continue;
      _check(
        failures,
        entry.value <= 700,
        '${entry.key} 响应 ${entry.value}ms 超过 700ms 预算',
      );
    }
  } catch (error, stack) {
    failures.add('profile 验收未完成：$error\n$stack');
  }
  if (!kProfileMode) failures.add('当前未以 Flutter profile 模式运行');

  _setSamplingPhase('teardown', 'interaction');
  runApp(const SizedBox.shrink());
  await Future<void>.delayed(const Duration(milliseconds: 40));
  chat.dispose();
  notebook.dispose();
  await gateway.dispose();
  // FrameTiming 回调可能批量延迟约一秒；按已保存的 frame number marker 保留末尾帧。
  _setSamplingPhase('report-flush', 'flush');
  await Future<void>.delayed(const Duration(milliseconds: 1200));

  final report = _makeReport(
    failures: failures,
    interactions: interactions,
    stream: stream,
    layouts: layoutRecords,
    nativeDpr: nativeDpr,
    gatewayNotices: gatewayNotices,
  );
  final outputPath =
      Platform.environment['TIMECUE_CHAT_PROFILE_OUTPUT'] ??
      'build/chat-profile/profile.json';
  final output = File(outputPath);
  await output.parent.create(recursive: true);
  await output.writeAsString(
    const JsonEncoder.withIndent('  ').convert(report),
  );

  await windowManager.close();
  exit((report['passed'] as bool) ? 0 : 1);
}

class _ProfileHost extends StatefulWidget {
  const _ProfileHost({super.key, required this.notebook, required this.chat});

  final NotebookViewModel notebook;
  final ChatViewModel chat;

  @override
  State<_ProfileHost> createState() => _ProfileHostState();
}

class _ProfileHostState extends State<_ProfileHost> {
  double _textScale = 1;

  void setTextScale(double value) => setState(() => _textScale = value);

  @override
  Widget build(BuildContext context) => MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: NotebookTheme.data,
    home: Scaffold(
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: LayoutBuilder(
            builder: (context, constraints) {
              final width = (constraints.maxWidth - 64).clamp(260.0, 1120.0);
              final height = (constraints.maxHeight - 108).clamp(160.0, 720.0);
              return MediaQuery(
                data: MediaQuery.of(
                  context,
                ).copyWith(textScaler: TextScaler.linear(_textScale)),
                child: Center(
                  child: SizedBox(
                    width: width,
                    height: height,
                    child: NotebookFrame(
                      model: widget.notebook,
                      onClose: () {},
                      onManage: () {},
                      onTab: widget.notebook.changeTab,
                      spread: ChatSpread(
                        model: widget.notebook,
                        chat: widget.chat,
                      ),
                    ),
                  ),
                ),
              );
            },
          ),
        ),
      ),
    ),
  );
}

Map<String, Object?> _makeReport({
  required List<String> failures,
  required Map<String, int> interactions,
  required Map<String, Object?> stream,
  required List<Map<String, Object?>> layouts,
  required double nativeDpr,
  required Map<String, int> gatewayNotices,
}) {
  final cases = <String, Object?>{};
  final preFrameworkStartupEvidence = _classifyPreFrameworkStartupPrefix(
    _frameSamples,
    _frameMarkers,
  );
  final byPhase = <String, Map<String, List<Map<String, Object?>>>>{};
  for (final frame in _frameSamples) {
    final phase = frame['phase']! as String;
    final caseName = frame['case']! as String;
    byPhase
        .putIfAbsent(phase, () => <String, List<Map<String, Object?>>>{})
        .putIfAbsent(caseName, () => <Map<String, Object?>>[])
        .add(frame);
  }
  Map<String, Object?> summarize(List<Map<String, Object?>> frames) {
    final build = frames.map((frame) => frame['build_us']! as int).toList()
      ..sort();
    final raster = frames.map((frame) => frame['raster_us']! as int).toList()
      ..sort();
    final total = frames.map((frame) => frame['total_us']! as int).toList()
      ..sort();
    return {
      'frame_count': frames.length,
      'build_us': _stats(build),
      'raster_us': _stats(raster),
      'total_us': _stats(total),
    };
  }

  final steadyGroups = byPhase['steady'] ?? const {};
  final expectedSteadyCases = <String>{
    ...layouts.map((layout) => layout['case']! as String),
    'stream-1280x800',
  };
  final targetFindings = <String>[];
  for (final caseName in expectedSteadyCases) {
    final frames = steadyGroups[caseName] ?? const <Map<String, Object?>>[];
    final metrics = summarize(frames);
    final buildStats = metrics['build_us']! as Map<String, int>;
    final rasterStats = metrics['raster_us']! as Map<String, int>;
    final buildTargetMet = buildStats['p95']! <= 16667;
    final rasterTargetMet = rasterStats['p95']! <= 16667;
    final frameTargetMet =
        frames.length >= 20 && buildTargetMet && rasterTargetMet;
    cases[caseName] = {
      ...metrics,
      'minimum_frame_count': 20,
      'frame_target_us': 16667,
      'frame_target_met': frameTargetMet,
    };
    if (frames.length < 20) {
      failures.add('$caseName 仅收集到 ${frames.length} 个稳态帧，至少需要 20 帧');
    }
    if (frames.isNotEmpty && !buildTargetMet) {
      targetFindings.add(
        '$caseName build p95=${buildStats['p95']}us，高于 16667us 帧目标。',
      );
    }
    if (frames.isNotEmpty && !rasterTargetMet) {
      targetFindings.add(
        '$caseName raster p95=${rasterStats['p95']}us，高于 16667us 帧目标。',
      );
    }
  }

  final phaseSummaries = <String, Object?>{};
  for (final phase in [
    'startup',
    'resize-transition',
    'interaction',
    'flush',
    'pre_framework_startup',
    'unclassified',
  ]) {
    final groups = byPhase[phase];
    if (groups == null) continue;
    phaseSummaries[phase] = {
      for (final entry in groups.entries) entry.key: summarize(entry.value),
    };
  }
  List<Map<String, Object?>> selectSteadyFrames({required bool streamOnly}) =>
      steadyGroups.entries
          .where((entry) => entry.key.startsWith('stream-') == streamOnly)
          .expand((entry) => entry.value)
          .toList();
  final layoutSteadyFrames = selectSteadyFrames(streamOnly: false);
  final streamSteadyFrames = selectSteadyFrames(streamOnly: true);
  final coldFrames = [
    ...?byPhase['startup']?.values.expand((frames) => frames),
    ...?byPhase['pre_framework_startup']?.values.expand((frames) => frames),
  ];
  final unclassified = _frameSamples
      .where((frame) => frame['matched_frame_number'] != true)
      .where((frame) => frame['phase'] != 'pre_framework_startup')
      .toList();
  if (_flutterErrors.isNotEmpty) {
    failures.add('捕获到 FlutterError：${_flutterErrors.first}');
  }
  if (_frameSamples.isEmpty) failures.add('没有收集到 FrameTiming');
  if (unclassified.isNotEmpty) {
    failures.add('${unclassified.length} 个 FrameTiming 未能映射到引擎 frameNumber');
  }
  return {
    'schema_version': 4,
    'test_id': 'timecue-chat-profile-v1',
    'created_at': DateTime.now().toUtc().toIso8601String(),
    'passed': failures.isEmpty,
    'failures': failures,
    'environment': {
      'os': Platform.operatingSystem,
      'os_version': Platform.operatingSystemVersion,
      'dart_version': Platform.version,
      'profile_mode': kProfileMode,
      'build_mode':
          'flutter build windows --profile --target=tool/chat_profile_smoke.dart',
      'device_pixel_ratio': nativeDpr,
      'native_window_maximum_tracking_size_override': [
        _profileMaximumWindowSize.width,
        _profileMaximumWindowSize.height,
      ],
      'window_may_extend_beyond_work_area': true,
      'native_physical_size': [
        WidgetsBinding
            .instance
            .platformDispatcher
            .views
            .first
            .physicalSize
            .width,
        WidgetsBinding
            .instance
            .platformDispatcher
            .views
            .first
            .physicalSize
            .height,
      ],
      'native_input_automation':
          'Flutter framework pointer events and TextEditingController; no native OS keyboard events',
    },
    'content': stream,
    'layouts': layouts,
    'performance': {
      'response_budget': {'interaction_ms': 700},
      'frame_target': {
        'build_p95_us': 16667,
        'raster_p95_us': 16667,
        'minimum_samples': 20,
        'gates_exit_code': false,
      },
      'cold_start': {
        'frame_count': coldFrames.length,
        ...summarize(coldFrames),
        'budget_applies': false,
      },
      'frame_target_us': 16667,
      'frame_target_met': cases.values.every(
        (item) => (item as Map)['frame_target_met'] == true,
      ),
      'target_findings': targetFindings,
      'frame_target_is_diagnostic_not_exit_gate': true,
      'cases': cases,
      'transitions_and_other_phases': phaseSummaries,
      'layout_steady_aggregate': summarize(layoutSteadyFrames),
      'stream_steady_aggregate': summarize(streamSteadyFrames),
      'frame_classification': {
        'method':
            'FrameTiming.frameNumber matched to PlatformDispatcher.frameData.frameNumber captured in persistent frame callback',
        'sample_count': _frameSamples.length,
        'normal_matched_count': _frameSamples
            .where((frame) => frame['matched_frame_number'] == true)
            .length,
        'pre_framework_count': preFrameworkStartupEvidence.length,
        'unclassified_normal_count': unclassified.length,
        'pre_framework_startup_evidence': preFrameworkStartupEvidence,
        'engine_warmup_or_missing_markers': unclassified
            .map(
              (frame) => {
                'frame_number': frame['frame_number'],
                'vsync_start_timestamp_us': frame['vsync_start_timestamp_us'],
                'build_start_timestamp_us': frame['build_start_timestamp_us'],
              },
            )
            .toList(),
      },
      'frame_markers': _frameMarkers,
      'raw_frame_samples': _frameSamples,
      'interactions_ms': interactions,
    },
    'functional': {
      'production_composer_send': interactions.containsKey(
        'send_to_gateway_ms',
      ),
      'production_stop': interactions.containsKey('stop_to_gateway_ms'),
      'production_session_switch': interactions.containsKey(
        'session_switch_ms',
      ),
      'production_scroll': layouts.every(
        (layout) => (layout['message_scroll'] as Map)['moved'] == true,
      ),
      'history_notifier': gatewayNotices,
    },
    'manual_windows_checks': {
      'status': 'pending',
      'required': [
        'Windows 原生中文输入法选词与 Enter',
        '跨显示器与 100/125/150/200% OS DPI',
        '系统键盘和鼠标手工操作',
      ],
      'native_hide_show':
          stream['native_window_hidden'] == true &&
          stream['hidden_background_received'] == true,
      'tray_lifecycle_tested': false,
      'automated_profile_is_not_evidence_for_these': true,
    },
    'flutter_errors': _flutterErrors,
  };
}

Map<String, int> _stats(List<int> sorted) => {
  'count': sorted.length,
  'p50': _percentile(sorted, .50),
  'p95': _percentile(sorted, .95),
  'max': sorted.isEmpty ? 0 : sorted.last,
};

List<Map<String, Object?>> _classifyPreFrameworkStartupPrefix(
  List<Map<String, Object?>> samples,
  List<Map<String, Object?>> markers,
) {
  final firstNormalMarkerIndex = markers.indexWhere(
    (marker) =>
        (marker['engine_frame_number']! as int) >= 0 &&
        (marker['scheduler_vsync_timestamp_us']! as int) > 0,
  );
  if (firstNormalMarkerIndex < 0) return const [];
  final firstNormalMarker = markers[firstNormalMarkerIndex];
  final zeroStartupMarkers = markers
      .take(firstNormalMarkerIndex)
      .where(
        (marker) =>
            marker['phase'] == 'startup' &&
            marker['scheduler_vsync_timestamp_us'] == 0,
      )
      .toList();
  if (zeroStartupMarkers.isEmpty) return const [];
  final baseline = zeroStartupMarkers.last;
  final baselineFrame = baseline['engine_frame_number']! as int;
  final firstNormalFrame = firstNormalMarker['engine_frame_number']! as int;
  final firstNormalVsync =
      firstNormalMarker['scheduler_vsync_timestamp_us']! as int;
  final firstMatchedSampleIndex = samples.indexWhere(
    (sample) => sample['matched_frame_number'] == true,
  );
  if (firstMatchedSampleIndex < 0) return const [];

  final evidence = <Map<String, Object?>>[];
  for (var index = 0; index < firstMatchedSampleIndex; index++) {
    final sample = samples[index];
    if (sample['matched_frame_number'] == true) continue;
    final frameNumber = sample['frame_number']! as int;
    final rawVsync = sample['vsync_start_timestamp_us']! as int;
    final isInitialFramePrefix =
        frameNumber > baselineFrame &&
        frameNumber < firstNormalFrame &&
        frameNumber <
            (samples[firstMatchedSampleIndex]['frame_number']! as int);
    final isBeforeFirstNormalVsync = rawVsync < firstNormalVsync;
    if (!isInitialFramePrefix || !isBeforeFirstNormalVsync) continue;

    sample
      ..['case'] = 'pre-framework-startup'
      ..['phase'] = 'pre_framework_startup'
      ..['pre_framework_startup'] = true
      ..['startup_classification_reason'] =
          'FrameTiming ID lies strictly between zero-time startup marker and first nonzero framework marker; raw vsync precedes that marker.';
    evidence.add({
      'sample_index': index,
      'frame_number': frameNumber,
      'matched_frame_number': false,
      'baseline_zero_timestamp_marker': baseline,
      'first_nonzero_framework_marker': firstNormalMarker,
      'raw_vsync_start_timestamp_us': rawVsync,
      'first_nonzero_marker_scheduler_vsync_timestamp_us': firstNormalVsync,
      'initial_prefix_before_first_matched_timing': true,
      'classification': 'pre_framework_startup',
    });
  }
  return evidence;
}

int _percentile(List<int> sorted, double percentile) {
  if (sorted.isEmpty) return 0;
  final index = (sorted.length - 1) * percentile;
  final lower = index.floor().clamp(0, sorted.length - 1);
  final upper = index.ceil().clamp(0, sorted.length - 1);
  final fraction = index - lower;
  return (sorted[lower] + (sorted[upper] - sorted[lower]) * fraction).round();
}

Future<void> _waitFor(bool Function() condition) async {
  final watch = Stopwatch()..start();
  while (!condition()) {
    if (watch.elapsed > const Duration(seconds: 8)) {
      throw TimeoutException('profile smoke 等待操作响应超时');
    }
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
}

Future<void> _waitForRenderedFrames(int count) async {
  for (var frame = 0; frame < count; frame++) {
    await SchedulerBinding.instance.endOfFrame;
  }
}

void _check(List<String> failures, bool condition, String message) {
  if (!condition) failures.add(message);
}

TextField? _findTextField() {
  final element = _findWidgetElement(
    TextField,
    key: const ValueKey('chat-draft-input'),
  );
  return element?.widget as TextField?;
}

Element? _findKey(Key key) => _findElementByKey(key);

bool _buttonEnabled(Key key) {
  final element = _findKey(key);
  return element?.widget is IconButton &&
      (element!.widget as IconButton).onPressed != null;
}

Element? _findText(String value) {
  Element? found;
  _walk((element) {
    if (found == null &&
        element.widget is Text &&
        (element.widget as Text).data == value) {
      found = element;
    }
  });
  return found;
}

bool _messageListMounted(String sessionId) =>
    _findElementByKey(ValueKey('chat-message-list-$sessionId')) != null;

bool _messageAtLatest(String sessionId) {
  final state = _messageScrollState(sessionId);
  if (state == null || !state.position.hasContentDimensions) return false;
  return (state.position.maxScrollExtent - state.position.pixels).abs() < 48;
}

ChatMarkdownBody? _findMarkdownBody(String messageId) {
  final element = _findWidgetElement(
    ChatMarkdownBody,
    key: ValueKey('chat-assistant-$messageId'),
  );
  return element?.widget as ChatMarkdownBody?;
}

Element? _findWidgetElement(Type type, {Key? key}) {
  Element? found;
  _walk((element) {
    if (found == null &&
        element.widget.runtimeType == type &&
        (key == null || element.widget.key == key)) {
      found = element;
    }
  });
  return found;
}

Element? _findElementByKey(Key key) {
  Element? found;
  _walk((element) {
    if (found == null && element.widget.key == key) found = element;
  });
  return found;
}

Widget? _findDescendantWidget(Element? parent, String typeName) {
  if (parent == null) return null;
  Widget? found;
  parent.visitChildElements((element) {
    if (found == null) {
      if (element.widget.runtimeType.toString() == typeName) {
        found = element.widget;
      } else {
        found = _findDescendantWidget(element, typeName);
      }
    }
  });
  return found;
}

void _walk(void Function(Element) visitor) {
  final root = WidgetsBinding.instance.rootElement;
  if (root == null) return;
  void visit(Element element) {
    visitor(element);
    element.visitChildElements(visit);
  }

  visit(root);
}

void _tap(Element element) {
  final render = element.findRenderObject();
  if (render is! RenderBox || !render.hasSize) return;
  final position = render.localToGlobal(render.size.center(Offset.zero));
  final binding = GestureBinding.instance;
  binding.handlePointerEvent(
    PointerDownEvent(pointer: 71, position: position, buttons: kPrimaryButton),
  );
  binding.handlePointerEvent(PointerUpEvent(pointer: 71, position: position));
}

Future<Map<String, Object?>> _scrollChatMessageList(String sessionId) async {
  final state = _messageScrollState(sessionId);
  if (state == null || !state.position.hasContentDimensions) {
    return {'found': false, 'moved': false, 'before': 0.0, 'after': 0.0};
  }
  final position = state.position;
  final before = position.pixels;
  final forwardRoom = position.maxScrollExtent - before;
  final backwardRoom = before - position.minScrollExtent;
  final deltaY = forwardRoom >= backwardRoom ? 300.0 : -300.0;
  final element = state.context as StatefulElement;
  final render = element.findRenderObject();
  if (render is! RenderBox || !render.hasSize) {
    return {'found': true, 'moved': false, 'before': before, 'after': before};
  }
  final pointerPosition = render.localToGlobal(render.size.center(Offset.zero));
  final watch = Stopwatch()..start();
  GestureBinding.instance.handlePointerEvent(
    PointerScrollEvent(
      position: pointerPosition,
      scrollDelta: Offset(0, deltaY),
    ),
  );
  await _waitFor(() => (position.pixels - before).abs() >= 1);
  final after = position.pixels;
  return {
    'found': true,
    'before': before,
    'after': after,
    'min_extent': position.minScrollExtent,
    'max_extent': position.maxScrollExtent,
    'scroll_delta_y': deltaY,
    'response_ms': watch.elapsedMilliseconds,
    'moved': (after - before).abs() >= 1,
  };
}

Future<Map<String, Object?>> _profileScrollAnimation(String sessionId) async {
  final state = _messageScrollState(sessionId);
  if (state == null || !state.position.hasContentDimensions) {
    return {'found': false, 'moved': false, 'elapsed_ms': 0};
  }
  final position = state.position;
  final before = position.pixels;
  final distanceToEnd = position.maxScrollExtent - before;
  final distanceToStart = before - position.minScrollExtent;
  final target = distanceToEnd >= distanceToStart
      ? position.maxScrollExtent
      : position.minScrollExtent;
  final watch = Stopwatch()..start();
  await position.animateTo(
    target,
    duration: const Duration(milliseconds: 1200),
    curve: Curves.linear,
  );
  final after = position.pixels;
  return {
    'found': true,
    'before': before,
    'after': after,
    'target': target,
    'elapsed_ms': watch.elapsedMilliseconds,
    'moved': (after - before).abs() >= 1,
  };
}

ScrollableState? _messageScrollState(String sessionId) {
  final list = _findWidgetElement(
    ChatMessageList,
    key: ValueKey('chat-message-list-$sessionId'),
  );
  if (list == null) return null;
  ScrollableState? found;
  void visit(Element element) {
    if (found != null) return;
    if (element.widget.runtimeType == Scrollable &&
        element is StatefulElement &&
        element.state is ScrollableState &&
        (element.state as ScrollableState).position.axis == Axis.vertical) {
      found = element.state as ScrollableState;
      return;
    }
    element.visitChildElements(visit);
  }

  visit(list);
  return found;
}

Size _logicalClientSize(FlutterView view) => Size(
  view.physicalSize.width / view.devicePixelRatio,
  view.physicalSize.height / view.devicePixelRatio,
);

Future<void> _waitForClientSize(FlutterView view, Size target) async {
  final watch = Stopwatch()..start();
  Size? previous;
  var stableCount = 0;
  while (watch.elapsed < const Duration(seconds: 8)) {
    final actual = _logicalClientSize(view);
    final close =
        (actual.width - target.width).abs() < 1.2 &&
        (actual.height - target.height).abs() < 1.2;
    if (close &&
        previous != null &&
        (actual.width - previous.width).abs() < 0.5 &&
        (actual.height - previous.height).abs() < 0.5) {
      stableCount++;
      if (stableCount >= 3) return;
    } else {
      stableCount = 0;
    }
    previous = actual;
    await Future<void>.delayed(const Duration(milliseconds: 30));
  }
  throw TimeoutException(
    '窗口客户区未稳定到目标尺寸：目标 $target，实际 ${_logicalClientSize(view)}',
  );
}
