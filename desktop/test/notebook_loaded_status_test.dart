import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/features/notebook/notebook_spreads.dart';

void main() {
  testWidgets('已加载提醒不能显示为已经提醒', (tester) async {
    final entry = TriggerSummary(
      id: '901',
      scheduleId: '900',
      name: '等待展示',
      plannedAt: DateTime.utc(2026, 10, 9, 9),
      delivery: 'loaded',
    );
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () => showTriggerDetails(context, entry),
              child: const Text('详情'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('详情'));
    await tester.pumpAndSettle();
    expect(find.textContaining('待投递'), findsOneWidget);
    expect(find.textContaining('已提醒'), findsNothing);
  });
}
