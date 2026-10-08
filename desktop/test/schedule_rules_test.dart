import 'package:flutter_test/flutter_test.dart';
import 'package:timecue_desktop/contracts/notebook_repository.dart';
import 'package:timecue_desktop/contracts/schedule_rules.dart';

void main() {
  test('多时间、星期、日号排序去重并无损往返，不自行计算短月', () {
    for (final sample in [
      (ScheduleKind.daily, ['0 18 * * *', '30 9 * * *']),
      (ScheduleKind.weekly, ['0 18 * * 1,3,5', '30 9 * * 5,1,3']),
      (ScheduleKind.monthly, ['30 9 31,15,1 * *', '0 18 1,15,31 * *']),
    ]) {
      final draft = ScheduleRules.decode(
        name: '多次提醒',
        description: '',
        cron: sample.$2,
        enabled: false,
        anchor: DateTime.utc(2026, 2, 28),
      );
      expect(draft.kind, sample.$1);
      expect(draft.enabled, isFalse);
      expect(draft.rawCron, isNull);
      expect(draft.times, ['09:30', '18:00']);
      final encoded = ScheduleRules.encode(draft);
      final roundtrip = ScheduleRules.decode(
        name: draft.name,
        description: draft.description,
        cron: encoded,
        enabled: draft.enabled,
        anchor: draft.time,
      );
      expect(roundtrip.times, draft.times);
      expect(roundtrip.days, draft.days);
      if (sample.$1 == ScheduleKind.monthly) {
        expect(encoded.first, '30 9 1,15,31 * *');
      }
    }
    expect(ScheduleRules.times('18:00，9:30,09:30'), ['09:30', '18:00']);
    expect(ScheduleRules.days('7,1,7', 7), [1, 7]);
  });
  test('不同频率的旧 cron 保留原文，不能静默转为每天', () {
    for (final cron in [
      ['0 9 * * *', '0 18 * * 1,3'],
      ['0 9 * * 1,3', '0 18 31 * *'],
    ]) {
      final draft = ScheduleRules.decode(
        name: '旧规则',
        description: '',
        cron: cron,
        enabled: true,
        anchor: DateTime.utc(2026, 10, 8),
      );
      expect(draft.rawCron, cron);
      expect(ScheduleRules.encode(draft), cron);
    }
  });
  test('独立星期或日期与时间无损往返，不把组合扩展成笛卡尔积', () {
    for (final cron in [
      ['0 9 * * 1,3', '0 18 * * 2,4'],
      ['0 9 1,15 * *', '0 18 31 * *'],
    ]) {
      final draft = ScheduleRules.decode(
        name: '独立时间项',
        description: '',
        cron: cron,
        enabled: true,
        anchor: DateTime.utc(2026, 10, 8),
      );
      expect(draft.rawCron, isNull);
      expect(ScheduleRules.encode(draft), cron);
      expect(draft.slots!.length, cron.first.contains('1,3') ? 4 : 3);
    }
    final draft = ScheduleDraft(
      name: '每周',
      description: '',
      kind: ScheduleKind.weekly,
      time: DateTime.utc(2026, 10, 8),
      slots: const [
        ScheduleTimeSlot(ScheduleKind.weekly, 9, 0, day: 1),
        ScheduleTimeSlot(ScheduleKind.weekly, 18, 0, day: 3),
      ],
    );
    expect(ScheduleRules.encode(draft), ['0 9 * * 1', '0 18 * * 3']);
  });
  test('拒绝空时间列表和混合周期项，重复的同一项只编码一次', () {
    final anchor = DateTime.utc(2026, 10, 8);
    expect(
      () => ScheduleRules.encode(
        ScheduleDraft(
          name: '空',
          description: '',
          kind: ScheduleKind.daily,
          time: anchor,
          slots: const [],
        ),
      ),
      throwsFormatException,
    );
    expect(
      () => ScheduleRules.encode(
        ScheduleDraft(
          name: '混合',
          description: '',
          kind: ScheduleKind.daily,
          time: anchor,
          slots: const [ScheduleTimeSlot(ScheduleKind.weekly, 9, 0, day: 1)],
        ),
      ),
      throwsFormatException,
    );
    expect(
      ScheduleRules.encode(
        ScheduleDraft(
          name: '重复',
          description: '',
          kind: ScheduleKind.daily,
          time: anchor,
          slots: const [
            ScheduleTimeSlot(ScheduleKind.daily, 9, 0),
            ScheduleTimeSlot(ScheduleKind.daily, 9, 0),
          ],
        ),
      ),
      ['0 9 * * *'],
    );
  });
  test('拒绝秒精度、非法时间与星期，月末日号允许 31', () {
    for (final text in ['09:30:10', '24:00', '09:60', '', '09:30,']) {
      expect(() => ScheduleRules.times(text), throwsFormatException);
    }
    expect(() => ScheduleRules.days('0,1', 7), throwsFormatException);
    expect(() => ScheduleRules.days('8', 7), throwsFormatException);
    expect(ScheduleRules.days('31,1,15', 31), [1, 15, 31]);
  });
}
