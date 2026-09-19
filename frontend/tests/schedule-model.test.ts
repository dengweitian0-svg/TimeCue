import { describe, expect, it } from 'vitest';
import { changeDraftType, cronType, draftRequest, emptyDraft, scheduleSummary } from '../src/schedule-model';
import type { ScheduleDraft } from '../src/contracts';

/** 构造含单次端点的草稿，验证类型切换不会丢失用户输入。 */
function singleDraft(): ScheduleDraft {
  return {
    ...emptyDraft(),
    title: '专注',
    notes: '备注',
    start: '2026-09-14T09:00',
    end: '2026-09-14T09:45',
  };
}

describe('recurring schedule model', () => {
  it('builds daily, weekly, and monthly requests without a cutoff', () => {
    const weekly = changeDraftType(singleDraft(), 'weekly');
    weekly.weekdays = [5, 1, 3];
    weekly.durationMinutes = '45';
    expect(draftRequest(weekly)).toMatchObject({
      cron: '0 9 * * 1,3,5',
      start_unix: expect.any(Number),
      end_unix: expect.any(Number),
    });
    expect(draftRequest(weekly)).not.toHaveProperty('until_unix');

    const daily = changeDraftType(singleDraft(), 'daily');
    expect(draftRequest(daily).cron).toBe('0 9 * * *');
    const monthly = changeDraftType(singleDraft(), 'monthly');
    expect(draftRequest(monthly).cron).toBe('0 9 14 * *');
  });

  it('derives time, weekday, month day, and duration when switching back to single', () => {
    const monthly = changeDraftType(singleDraft(), 'monthly');
    monthly.hour = '07';
    monthly.minute = '05';
    monthly.monthDay = '28';
    monthly.durationMinutes = '12';
    const single = changeDraftType(monthly, 'single');
    expect(single.start).toBe(`${monthly.effectiveDate}T07:05`);
    expect(single.end).toBe(`${monthly.effectiveDate}T07:17`);
    expect(single.monthDay).toBe('14');
  });

  it('accepts and summarizes normalized multi-weekday cron rules', () => {
    expect(cronType('0 9 * * 1,3,5')).toBe('weekly');
    expect(
      scheduleSummary({
        id: '1',
        title: 'multi',
        notes: '',
        firstStartLocal: '2026-09-14T09:00',
        durationMinutes: 30,
        cron: '0 9 * * 1,3,5',
        untilDateLocal: null,
        monthEndPolicy: null,
        open: true,
        revision: 1,
      }),
    ).toContain('每周一、三、五');
  });
});
