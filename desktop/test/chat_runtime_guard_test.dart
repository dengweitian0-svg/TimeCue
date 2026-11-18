import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/app/chat_runtime_guard.dart';
import 'package:timecue_desktop/contracts/notification_delivery.dart';

void main() {
  test('managed startup gate can be retried before business opens', () async {
    var ready = false;
    var healthCalls = 0;
    var invalidations = 0;
    final guard = ChatRuntimeGuard(
      managed: true,
      health: () async {
        healthCalls++;
        return const DeliveryRuntime('owned');
      },
      managedReady: () => ready,
      onInvalidated: () => invalidations++,
    );

    await expectLater(guard.beforeRequest(), throwsA(isA<StateError>()));
    expect(healthCalls, 0);
    expect(invalidations, 0);
    ready = true;
    guard.markBusinessOpened();
    await guard.beforeRequest();
    expect(guard.instanceId, 'owned');
    expect(healthCalls, 1);
  });

  test('health responses after exit cannot anchor an identity', () async {
    final response = Completer<DeliveryRuntime>();
    var invalidations = 0;
    final guard = ChatRuntimeGuard(
      managed: false,
      health: () => response.future,
      managedReady: () => false,
      onInvalidated: () => invalidations++,
    );

    final request = guard.beforeRequest();
    guard.close();
    response.complete(const DeliveryRuntime('late-instance'));
    await expectLater(request, throwsA(isA<StateError>()));
    expect(guard.instanceId, isNull);
    expect(invalidations, 0);
  });

  test('first concurrent health identity remains anchored', () async {
    final responses = [
      Completer<DeliveryRuntime>(),
      Completer<DeliveryRuntime>(),
    ];
    var calls = 0;
    var invalidations = 0;
    final guard = ChatRuntimeGuard(
      managed: false,
      health: () => responses[calls++].future,
      managedReady: () => true,
      onInvalidated: () => invalidations++,
    );

    final first = guard.beforeRequest();
    final second = guard.beforeRequest();
    responses[0].complete(const DeliveryRuntime('first'));
    await first;
    responses[1].complete(const DeliveryRuntime('second'));
    await expectLater(second, throwsA(isA<StateError>()));
    expect(guard.instanceId, 'first');
    expect(invalidations, 1);
  });

  test('managed service loss after opening invalidates old requests', () async {
    var ready = true;
    var invalidations = 0;
    final guard = ChatRuntimeGuard(
      managed: true,
      health: () async => const DeliveryRuntime('owned'),
      managedReady: () => ready,
      onInvalidated: () => invalidations++,
    );
    guard.markBusinessOpened();
    await guard.beforeRequest();
    ready = false;

    await expectLater(guard.beforeRequest(), throwsA(isA<StateError>()));
    expect(invalidations, 1);
    ready = true;
    await expectLater(guard.beforeRequest(), throwsA(isA<StateError>()));
    expect(invalidations, 1);
  });

  test(
    'managed readiness loss during health await invalidates runtime',
    () async {
      final response = Completer<DeliveryRuntime>();
      var ready = true;
      var invalidations = 0;
      final guard = ChatRuntimeGuard(
        managed: true,
        health: () => response.future,
        managedReady: () => ready,
        onInvalidated: () => invalidations++,
      );
      guard.markBusinessOpened();

      final request = guard.beforeRequest();
      ready = false;
      response.complete(const DeliveryRuntime('owned'));
      await expectLater(request, throwsA(isA<StateError>()));
      expect(guard.instanceId, isNull);
      expect(invalidations, 1);
    },
  );

  test(
    'health failure after managed startup invalidates runtime once',
    () async {
      final response = Completer<DeliveryRuntime>();
      var invalidations = 0;
      final guard = ChatRuntimeGuard(
        managed: true,
        health: () => response.future,
        managedReady: () => true,
        onInvalidated: () => invalidations++,
      );
      guard.markBusinessOpened();

      final request = guard.beforeRequest();
      response.completeError(StateError('health unavailable'));
      await expectLater(request, throwsA(isA<StateError>()));
      expect(invalidations, 1);
      await expectLater(guard.beforeRequest(), throwsA(isA<StateError>()));
      expect(invalidations, 1);
    },
  );

  test(
    'health failure after close cannot cause another invalidation',
    () async {
      final response = Completer<DeliveryRuntime>();
      var invalidations = 0;
      final guard = ChatRuntimeGuard(
        managed: true,
        health: () => response.future,
        managedReady: () => true,
        onInvalidated: () => invalidations++,
      );
      guard.markBusinessOpened();

      final request = guard.beforeRequest();
      guard.close();
      response.completeError(StateError('late health failure'));
      await expectLater(request, throwsA(isA<StateError>()));
      expect(invalidations, 0);
      await expectLater(guard.beforeRequest(), throwsA(isA<StateError>()));
      expect(invalidations, 0);
    },
  );

  test('external mode still checks and anchors runtime identity', () async {
    var instance = 'external-a';
    var invalidations = 0;
    final guard = ChatRuntimeGuard(
      managed: false,
      health: () async => DeliveryRuntime(instance),
      managedReady: () => false,
      onInvalidated: () => invalidations++,
    );
    await guard.beforeRequest();
    instance = 'external-b';
    await expectLater(guard.beforeRequest(), throwsA(isA<StateError>()));
    expect(guard.instanceId, 'external-a');
    expect(invalidations, 1);
  });
}
