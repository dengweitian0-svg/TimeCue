import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';

import 'fixtures/chat_fixtures.dart';

void main() {
  group('chat DTOs', () {
    test('session_name is required and maps verbatim to name', () {
      final chinese = ChatSession.fromJson(
        ChatFixtures.session(name: '安排明天的阅读', runIds: ['r1']),
      );
      expect(chinese.name, '安排明天的阅读');
      expect(chinese.runIds, ['r1']);

      expect(ChatSession.fromJson(ChatFixtures.session(name: '')).name, '');
      final longName = '长名称' * 200;
      expect(
        ChatSession.fromJson(ChatFixtures.session(name: longName)).name,
        longName,
      );

      final missing = ChatFixtures.session()..remove('session_name');
      expect(
        () => ChatSession.fromJson(missing),
        throwsA(isA<ChatContractFormatException>()),
      );
      final wrongType = ChatFixtures.session()..['session_name'] = null;
      expect(
        () => ChatSession.fromJson(wrongType),
        throwsA(isA<ChatContractFormatException>()),
      );
    });

    test(
      'run required fields and nullable fields retain protocol distinctions',
      () {
        final succeeded = ChatRun.fromJson(ChatFixtures.succeededRun());
        expect(succeeded.input, '用户原始输入');
        expect(succeeded.status, ChatRunStatus.succeeded);
        expect(succeeded.output, '最终回复');
        expect(
          succeeded.finishedAt?.toUtc(),
          DateTime.utc(2026, 10, 17, 1, 0, 1),
        );

        final failed = ChatRun.fromJson(
          ChatFixtures.terminalRun(status: 'failed'),
        );
        expect(failed.output, isNull);
        expect(failed.error?.code, 'upstream_failed');
        final cancelled = ChatRun.fromJson(
          ChatFixtures.terminalRun(status: 'cancelled'),
        );
        expect(cancelled.output, '已确认检查点');
        expect(cancelled.error, isNull);
        expect(ChatRun.fromJson(ChatFixtures.runningRun()).finishedAt, isNull);

        for (final (field, value) in <(String, Object?)>[
          ('input', 42),
          ('output', 42),
          ('created_at', 'not-a-date'),
          ('status', 'unknown'),
        ]) {
          final invalid = ChatFixtures.succeededRun()..[field] = value;
          expect(
            () => ChatRun.fromJson(invalid),
            throwsA(isA<ChatContractFormatException>()),
          );
        }
        final missingInput = ChatFixtures.succeededRun()..remove('input');
        expect(
          () => ChatRun.fromJson(missingInput),
          throwsA(isA<ChatContractFormatException>()),
        );
        final inconsistent = ChatFixtures.succeededRun()
          ..['finished_at'] = null;
        expect(
          () => ChatRun.fromJson(inconsistent),
          throwsA(isA<ChatContractFormatException>()),
        );
      },
    );

    test('response DTOs reject missing fields and invalid enums', () {
      expect(ChatCreateRunResult.fromJson({'run_id': 'r-1'}).runId, 'r-1');
      expect(
        () => ChatCreateRunResult.fromJson({}),
        throwsA(isA<ChatContractFormatException>()),
      );
      expect(
        () => ChatCancelRunResult.fromJson({
          'run_id': 'r-1',
          'status': 'unknown',
        }),
        throwsA(isA<ChatContractFormatException>()),
      );
      expect(
        ChatDeleteSessionResult.fromJson({
          'session_id': 's-1',
          'status': 'deleted',
        }).status,
        'deleted',
      );
      expect(
        ChatSessionIds.fromJson(ChatFixtures.listSessionsResponse()).values,
        ['550e8400-e29b-41d4-a716-446655440000'],
      );
      expect(
        () => ChatSessionIds.fromJson({
          'sessions': ['valid', null],
        }),
        throwsA(isA<ChatContractFormatException>()),
      );
    });
  });

  group('chat SSE and HTTP errors', () {
    test(
      'event ID differs from tool call ID and tool name comes from function.name',
      () {
        final frame = ChatFixtures.interleavedFailureFrames[3];
        final fields = ChatFixtures.decodeFrameForContractTest(frame);
        final event = ChatSseEvent.fromJson(
          id: fields.id,
          event: fields.event,
          data: fields.data,
        );
        final tool = event.data! as ChatToolEventData;
        expect(event.eventId, 4);
        expect(tool.id, 'call-a');
        expect(tool.id, isNot('${event.eventId}'));
        expect(tool.function.name, 'search_schedules');
        expect(tool.function.args.name, 'spoofed');
        expect(tool.function.args.value, {'q': '日期'});
      },
    );

    test(
      'fixed interleaved, repeated and reconnect frames keep source identity and order',
      () {
        final events = ChatFixtures.interleavedFailureFrames
            .map((frame) => ChatFixtures.decodeFrameForContractTest(frame))
            .map(
              (frame) => ChatSseEvent.fromJson(
                id: frame.id,
                event: frame.event,
                data: frame.data,
              ),
            )
            .toList();
        expect(events.map((event) => event.eventId), [
          1,
          2,
          3,
          4,
          5,
          6,
          7,
          8,
          9,
        ]);
        final calls = events
            .where((event) => event.type == ChatEventType.tool)
            .map((event) => event.data! as ChatToolEventData)
            .toList();
        expect(calls.map((call) => call.id), ['call-a', 'call-b', 'call-c']);
        expect(calls.map((call) => call.function.name), [
          'search_schedules',
          'get_calendar',
          'search_schedules',
        ]);
        expect(events[1].data, isA<ChatContentEventData>());
        expect(events.last.data, isA<ChatEndEventData>());
        expect((events.last.data! as ChatEndEventData).stopReason, 'aborted');

        final replay = ChatFixtures.decodeFrameForContractTest(
          ChatFixtures.reconnectReplayFrames.first,
        );
        final duplicate = ChatSseEvent.fromJson(
          id: replay.id,
          event: replay.event,
          data: replay.data,
        );
        expect(duplicate.eventId, 7);
        expect((duplicate.data! as ChatToolEventData).id, 'call-c');

        final normal = ChatFixtures.normalSuccessFrames
            .map(ChatFixtures.decodeFrameForContractTest)
            .map(
              (frame) => ChatSseEvent.fromJson(
                id: frame.id,
                event: frame.event,
                data: frame.data,
              ),
            )
            .toList();
        expect(normal.last.type, ChatEventType.end);
        expect((normal.last.data! as ChatEndEventData).stopReason, 'stop');

        final multipleTools = ChatFixtures.multipleToolFrames
            .map(ChatFixtures.decodeFrameForContractTest)
            .map(
              (frame) => ChatSseEvent.fromJson(
                id: frame.id,
                event: frame.event,
                data: frame.data,
              ),
            )
            .toList();
        final toolCalls = multipleTools
            .map((event) => event.data! as ChatToolEventData)
            .toList();
        expect(toolCalls.map((call) => call.id), [
          'call-one',
          'call-two',
          'call-three',
        ]);
        expect(toolCalls[0].function.name, toolCalls[2].function.name);
        expect(toolCalls[0].id, isNot(toolCalls[2].id));

        final repeatedFrame = ChatFixtures.decodeFrameForContractTest(
          ChatFixtures.duplicateToolFrame,
        );
        final repeated = ChatSseEvent.fromJson(
          id: repeatedFrame.id,
          event: repeatedFrame.event,
          data: repeatedFrame.data,
        );
        expect(repeated.eventId, 10);
        expect((repeated.data! as ChatToolEventData).id, 'call-one');

        final cancelled = ChatFixtures.decodeFrameForContractTest(
          ChatFixtures.cancelledEndFrame,
        );
        expect(
          (ChatSseEvent.fromJson(
                    id: cancelled.id,
                    event: cancelled.event,
                    data: cancelled.data,
                  ).data!
                  as ChatEndEventData)
              .stopReason,
          'cancelled',
        );
      },
    );

    test(
      'strict event shape and HTTP error mapping preserve server details',
      () {
        expect(
          () => ChatSseEvent.fromJson(id: '0', event: 'start', data: const {}),
          throwsA(isA<ChatContractFormatException>()),
        );
        expect(
          () =>
              ChatSseEvent.fromJson(id: '1', event: 'unknown', data: const {}),
          throwsA(isA<ChatContractFormatException>()),
        );
        expect(
          () => ChatSseEvent.fromJson(
            id: '1',
            event: 'text',
            data: {'content': 1, 'error': null},
          ),
          throwsA(isA<ChatContractFormatException>()),
        );

        final errorJson =
            jsonDecode('{"error":{"code":"session_busy","message":"繁忙"}}')
                as Map<String, Object?>;
        final error = ChatHttpError.fromJson(409, errorJson);
        expect(error.kind, ChatHttpFailureKind.conflict);
        expect(error.statusCode, 409);
        expect(error.message, '繁忙');
        expect(
          ChatHttpError.fromJson(
            409,
            ChatFixtures.httpError(code: 'run_terminal'),
          ).kind,
          ChatHttpFailureKind.runTerminal,
        );
        expect(
          ChatHttpError.fromJson(
            503,
            ChatFixtures.httpError(code: 'event_buffer_full'),
          ).kind,
          ChatHttpFailureKind.eventBufferFull,
        );
        expect(
          ChatHttpError.fromJson(
            500,
            ChatFixtures.httpError(code: 'internal_error'),
          ).kind,
          ChatHttpFailureKind.internal,
        );
        expect(
          ChatHttpError.fromJson(404, {
            'error': {'code': 'run_not_found', 'message': 'missing'},
          }).kind,
          ChatHttpFailureKind.notFound,
        );
      },
    );

    test('fixture exposes byte chunks that may split UTF-8 characters', () {
      final chunks = ChatFixtures.utf8Chunks(
        ChatFixtures.interleavedFailureFrames[3],
        chunkSize: 2,
      );
      expect(chunks.length, greaterThan(5));
      expect(chunks.every((chunk) => chunk.isNotEmpty), isTrue);
      expect(ChatFixtures.noContentResponse, (statusCode: 204, body: ''));
    });
  });
}
