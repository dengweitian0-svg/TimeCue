import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/data/http_notebook_repository.dart';
import 'package:timecue_desktop/contracts/notification_delivery.dart';

void main() {
  test('健康 503 也核对运行身份，不能把其他运行当作暂时故障', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((request) async {
      request.response.statusCode = 503;
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'data': {
            'instanceId': 'other',
            'pid': 88,
            'error': {'code': 'database_unavailable', 'message': '数据库不可用'},
          },
        }),
      );
      await request.response.close();
    });
    final repository = HttpNotebookRepository(
      managed: true,
      base: Uri.parse('http://127.0.0.1:${server.port}/api/v1/scheduler/'),
    );
    addTearDown(() async {
      repository.dispose();
      await server.close(force: true);
    });
    repository.enableManagedRuntime('owned', pid: 77);
    var invalidations = 0;
    repository.onManagedRuntimeLost = () => invalidations++;
    await expectLater(repository.deliveryRuntime(), throwsFormatException);
    expect(invalidations, 1);
    await expectLater(repository.claimNotifications(), throwsStateError);
  });
  test('托管仓储握手前只有 health 可访问，业务读写不发送请求', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final paths = <String>[];
    server.listen((request) async {
      paths.add(request.uri.path);
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'data': {
            'status': 'ok',
            'instanceId': 'owned',
            'pid': 77,
            'apiVersion': 'v1',
            'capabilities': [
              'delivery-v1',
              'recovery-v1',
              'task-members-v1',
              'managed-lifecycle-v1',
            ],
          },
        }),
      );
      await request.response.close();
    });
    final repository = HttpNotebookRepository(
      managed: true,
      base: Uri.parse('http://127.0.0.1:${server.port}/api/v1/scheduler/'),
    );
    addTearDown(() async {
      repository.dispose();
      await server.close(force: true);
    });
    await expectLater(repository.claimNotifications(), throwsStateError);
    await expectLater(repository.loadSchedule('1'), throwsStateError);
    await expectLater(
      repository.reportNotification('old', DeliveryOutcome.sent),
      throwsStateError,
    );
    expect(paths, isEmpty);
    expect((await repository.deliveryRuntime()).instanceId, 'owned');
    expect(paths, ['/health']);
  });

  test('绑定运行身份后拒绝端口上的其他运行，失效后禁止旧结果补交', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var requests = 0;
    server.listen((request) async {
      requests++;
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'data': {
            'status': 'ok',
            'instanceId': 'foreign',
            'pid': 88,
            'apiVersion': 'v1',
            'capabilities': ['delivery-v1', 'recovery-v1'],
          },
        }),
      );
      await request.response.close();
    });
    final repository = HttpNotebookRepository(
      managed: true,
      base: Uri.parse('http://127.0.0.1:${server.port}/api/v1/scheduler/'),
    );
    addTearDown(() async {
      repository.dispose();
      await server.close(force: true);
    });
    var invalidations = 0;
    repository.onManagedRuntimeLost = () => invalidations++;
    repository.enableManagedRuntime('owned', pid: 77);
    await expectLater(repository.deliveryRuntime(), throwsFormatException);
    expect(invalidations, 1);
    await expectLater(repository.claimNotifications(), throwsStateError);
    repository.invalidateManagedRuntime();
    await expectLater(
      repository.reportNotification('old', DeliveryOutcome.sent),
      throwsStateError,
    );
    expect(requests, 1);
  });

  test('开始前的启动失败不关闭 health 客户端，允许新的显式启动握手', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((request) async {
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'data': {
            'status': 'ok',
            'instanceId': 'new',
            'apiVersion': 'v1',
            'capabilities': ['delivery-v1', 'recovery-v1'],
          },
        }),
      );
      await request.response.close();
    });
    final repository = HttpNotebookRepository(
      managed: true,
      base: Uri.parse('http://127.0.0.1:${server.port}/api/v1/scheduler/'),
    );
    addTearDown(() async {
      repository.dispose();
      await server.close(force: true);
    });
    repository.invalidateManagedRuntime();
    expect((await repository.deliveryRuntime()).instanceId, 'new');
  });

  test('运行退出后在途健康响应不能发布成新运行', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final received = Completer<void>();
    final response = Completer<void>();
    server.listen((request) async {
      received.complete();
      await response.future;
      try {
        request.response.write(
          jsonEncode({
            'data': {
              'status': 'ok',
              'instanceId': 'owned',
              'apiVersion': 'v1',
              'capabilities': ['delivery-v1', 'recovery-v1'],
            },
          }),
        );
        await request.response.close();
      } catch (_) {
        /* 客户端在退出时关闭连接。 */
      }
    });
    final repository = HttpNotebookRepository(
      managed: true,
      base: Uri.parse('http://127.0.0.1:${server.port}/api/v1/scheduler/'),
    );
    addTearDown(() async {
      repository.dispose();
      await server.close(force: true);
    });
    repository.enableManagedRuntime('owned');
    final pending = repository.deliveryRuntime();
    final rejection = expectLater(pending, throwsA(isA<Object>()));
    await received.future;
    repository.invalidateManagedRuntime();
    response.complete();
    await rejection;
  });
}
