import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/chat_contracts.dart';

void main() {
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
