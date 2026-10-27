import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';

void main() {
  test('取消投影且 finished_at 为空表示仍在收尾', () {
    final run = ChatRun.fromJson({
      'run_id': 'run',
      'session_id': 'session',
      'provider': 'test',
      'model': 'model',
      'input': '问题',
      'status': 'cancelled',
      'output': '部分正文',
      'error': null,
      'created_at': '2026-10-27T10:00:00+08:00',
      'finished_at': null,
    });
    expect(run.status, ChatRunStatus.cancelled);
    expect(run.finishedAt, isNull);
  });
  test('T01 使用 provider、四字段回执和独立 error/end', () {
    final receipt = ChatCreateRunResult.fromJson({
      'run_id': 'run',
      'status': 'failed',
      'output': '部分回复',
      'error': {'code': 'model_network_error', 'message': '网络中断'},
    });
    expect(receipt.status, ChatRunStatus.failed);
    expect(receipt.output, '部分回复');
    final text = ChatSseEvent.fromJson(
      id: '2',
      event: 'text',
      data: {'content': '正文'},
    );
    expect((text.data as ChatContentEventData).content, '正文');
    final error = ChatSseEvent.fromJson(
      id: '3',
      event: 'error',
      data: {'code': 'model_network_error', 'message': '网络中断'},
    );
    expect((error.data as ChatProtocolError).code, 'model_network_error');
    final end = ChatSseEvent.fromJson(
      id: '4',
      event: 'end',
      data: {'status': 'failed'},
    );
    expect((end.data as ChatEndEventData).status, ChatRunStatus.failed);
  });
}
