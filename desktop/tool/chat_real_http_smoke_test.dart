import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';
import 'package:timecue_desktop/data/http_chat_gateway.dart';
import 'package:timecue_desktop/features/chat/chat_view_model.dart';

/// 只读复核真实模型脚本创建的隔离运行，不提供聊天服务替身。
void main() {
  test('真实 Go 历史经 Flutter 网关保留模型、原始分页和终态正文', () async {
    final input = Platform.environment['TIMECUE_REAL_CHAT_INPUT'];
    expect(
      input,
      isNotNull,
      reason: '请通过 scripts/chat-real-smoke.py --flutter-test 运行',
    );
    final manifest = jsonDecode(await File(input!).readAsString()) as Map;
    final base = Uri.parse(manifest['base'] as String);
    expect(base.host, '127.0.0.1');
    final client = HttpClient()..findProxy = (_) => 'DIRECT';
    addTearDown(() => client.close(force: true));
    final health = await client.getUrl(base.resolve('/health'));
    final response = await health.close();
    final status = jsonDecode(await utf8.decoder.bind(response).join()) as Map;
    expect((status['data'] as Map)['instanceId'], manifest['instance']);
    final gateway = HttpChatGateway(baseUri: base);
    addTearDown(gateway.dispose);
    final models = await gateway.getModels();
    final sessions = await gateway.listSessions();
    final eventTypes = <ChatEventType>{};
    for (final value in manifest['runs'] as List) {
      final expected = Map<String, Object?>.from(value as Map);
      final original = ChatRun.fromJson(expected);
      final run = await gateway.getRun(original.runId);
      expect(run.provider, original.provider);
      expect(run.model, original.model);
      expect(run.status, original.status);
      expect(run.output, original.output);
      expect(run.finishedAt, original.finishedAt);
      expect(sessions, contains(run.sessionId));
      expect(
        models.any(
          (m) =>
              m.provider == run.provider && m.model == run.model && m.available,
        ),
        isTrue,
      );
      var cursor = 0;
      final expectedEvents = (manifest['events'] as Map)[run.runId] as List;
      while (true) {
        final page = await gateway.getRunEvents(
          run.runId,
          afterSeq: cursor,
          limit: 2,
        );
        for (final event in page.events) {
          expect(event.eventId, cursor + 1);
          expect(event.rawData, (expectedEvents[cursor] as Map)['data']);
          cursor = event.eventId;
          eventTypes.add(event.type);
        }
        expect(page.nextAfterSeq, cursor);
        if (!page.hasMore) break;
      }
      expect(cursor, expectedEvents.length);
      final signals = await gateway
          .runEvents(run.runId, lastEventId: cursor)
          .toList();
      expect(signals, hasLength(1));
      expect(signals.single, isA<ChatRunEventsNoContent>());
    }
    expect(
      eventTypes,
      containsAll([
        ChatEventType.start,
        ChatEventType.text,
        ChatEventType.toolStart,
        ChatEventType.toolResult,
        ChatEventType.end,
        ChatEventType.compact,
      ]),
    );
    final viewModel = ChatViewModel(gateway: gateway);
    addTearDown(viewModel.dispose);
    await viewModel.loadModels();
    await viewModel.loadHistory();
    expect(viewModel.modelsError, isNull);
    expect(viewModel.historyError, isNull);
    expect(viewModel.historyConversations, hasLength(sessions.length));
  });
}
