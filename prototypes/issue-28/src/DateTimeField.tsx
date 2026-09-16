import { useEffect, useRef, useState, type InputHTMLAttributes, type KeyboardEvent } from 'react';
import { localMinute, parseLocal } from './schedule-model';

interface DateTimeFieldProps extends Pick<InputHTMLAttributes<HTMLInputElement>, 'id' | 'aria-invalid' | 'aria-describedby'> {
  label: string; value: string; onChange: (value: string) => void; dateOnly?: boolean; min?: string; max?: string;
}

/** 使用可手动输入的文本框与主题化弹层，避免浏览器原生白色日期弹窗。 */
export function DateTimeField({ label, value, onChange, dateOnly = false, min, max, ...inputProps }: DateTimeFieldProps) {
  const [open, setOpen] = useState(false); const trigger = useRef<HTMLButtonElement>(null);
  return <div className="date-time-field">
    <input {...inputProps} type="text" value={value} placeholder={dateOnly ? 'YYYY-MM-DD' : 'YYYY-MM-DDTHH:mm'} autoComplete="off" spellCheck={false} onChange={event => onChange(event.target.value)} />
    <button ref={trigger} className="date-picker-trigger" type="button" aria-label={`选择${label}`} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)}>▦</button>
    {open && <DatePicker label={label} value={value} dateOnly={dateOnly} min={min} max={max} anchor={trigger.current!} onChange={onChange} onClose={() => setOpen(false)} />}
  </div>;
}

/** 构造本地中午日期，避免午夜时区跳变及 0–99 年的构造器特殊行为。 */
function calendarDate(year: number, month: number, day: number): Date { const date = new Date(2000, 0, 1, 12); date.setFullYear(year, month, day); return date; }
/** 格式化日期身份供网格选择、焦点和范围判断共用。 */
function dateKey(date: Date): string { return localMinute(date).slice(0, 10); }
/** 初始化时优先保留当前输入，空值按今天及允许范围定位，直到确认才回填表单。 */
function initialValue(value: string, dateOnly: boolean, min?: string, max?: string): string {
  if (parseLocal(value, dateOnly)) return value;
  const today = localMinute(new Date()).slice(0, dateOnly ? 10 : 16);
  return min && today < min ? min : max && today > max ? max : today;
}

/** 在可着色的原生模态容器内编辑临时选择，取消不会覆盖原字段。 */
function DatePicker({ label, value, dateOnly, min, max, anchor, onChange, onClose }: Required<Pick<DateTimeFieldProps, 'label' | 'value' | 'dateOnly' | 'onChange'>> & Pick<DateTimeFieldProps, 'min' | 'max'> & { anchor: HTMLElement; onClose: () => void }) {
  const initial = initialValue(value, dateOnly, min, max);
  const [day, setDay] = useState(initial.slice(0, 10)); const [time, setTime] = useState(initial.slice(11) || '00:00');
  const [month, setMonth] = useState(() => calendarDate(Number(initial.slice(0, 4)), Number(initial.slice(5, 7)) - 1, 1));
  const dialog = useRef<HTMLDialogElement>(null); const today = dateKey(new Date());
  const chosen = dateOnly ? day : `${day}T${time}`;
  const valid = !!parseLocal(chosen, dateOnly) && (!min || chosen >= min) && (!max || chosen <= max);
  const start = calendarDate(month.getFullYear(), month.getMonth(), 1 - ((month.getDay() + 6) % 7));
  const days = Array.from({ length: 42 }, (_, index) => calendarDate(start.getFullYear(), start.getMonth(), start.getDate() + index));

  /** 限制弹层在视口内，并根据字段位置优先向下或向上展开。 */
  function position() {
    const node = dialog.current!; const rect = anchor.getBoundingClientRect(); const width = node.offsetWidth; const height = node.offsetHeight;
    node.style.left = `${Math.max(12, Math.min(rect.right - width, window.innerWidth - width - 12))}px`;
    node.style.top = `${Math.max(12, Math.min(rect.bottom + 8 + height <= window.innerHeight - 12 ? rect.bottom + 8 : rect.top - height - 8, window.innerHeight - height - 12))}px`;
  }
  useEffect(() => {
    const node = dialog.current!; node.showModal(); position();
    node.querySelector<HTMLButtonElement>('[data-day][tabindex="0"]')?.focus();
    window.addEventListener('resize', position);
    return () => { window.removeEventListener('resize', position); node.close(); if (anchor.isConnected) anchor.focus(); };
  }, []);
  useEffect(() => { dialog.current?.querySelectorAll<HTMLElement>('.time-options [aria-pressed="true"]').forEach(node => node.scrollIntoView({ block: 'nearest' })); }, [time]);

  /** 日期按钮只判断日期上下界，精确分钟边界由确认按钮和应用最终校验共同保护。 */
  function unavailable(date: Date): boolean { const key = dateKey(date); return date.getFullYear() < 1 || date.getFullYear() > 9999 || !!(min && key < min.slice(0, 10)) || !!(max && key > max.slice(0, 10)); }
  /** 翻月保留待确认日期，不因浏览月份而改写表单。 */
  function moveMonth(offset: number) { const next = calendarDate(month.getFullYear(), month.getMonth() + offset, 1); if (next.getFullYear() >= 1 && next.getFullYear() <= 9999) setMonth(next); }
  /** 箭头逐日或逐周移动，PageUp/Down 翻月，Shift 组合翻年并夹取月末。 */
  function moveDay(event: KeyboardEvent<HTMLButtonElement>, date: Date) {
    let next: Date;
    const offsets: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7, Home: -((date.getDay() + 6) % 7), End: 6 - ((date.getDay() + 6) % 7) };
    if (event.key in offsets) next = calendarDate(date.getFullYear(), date.getMonth(), date.getDate() + offsets[event.key]);
    else if (event.key === 'PageUp' || event.key === 'PageDown') { const target = calendarDate(date.getFullYear(), date.getMonth() + (event.key === 'PageUp' ? -1 : 1) * (event.shiftKey ? 12 : 1), 1); const last = calendarDate(target.getFullYear(), target.getMonth() + 1, 0).getDate(); next = calendarDate(target.getFullYear(), target.getMonth(), Math.min(date.getDate(), last)); }
    else return;
    event.preventDefault(); event.stopPropagation(); if (unavailable(next)) return;
    const key = dateKey(next); setDay(key); setMonth(calendarDate(next.getFullYear(), next.getMonth(), 1));
    requestAnimationFrame(() => dialog.current?.querySelector<HTMLButtonElement>(`[data-day="${key}"]`)?.focus());
  }
  /** 弹层自行消费 Esc，避免触发表单放弃确认；Tab 始终在弹层内循环。 */
  function keys(event: KeyboardEvent<HTMLDialogElement>) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); return; }
    if (event.key === 'Enter' && event.target instanceof HTMLInputElement) { event.preventDefault(); event.stopPropagation(); if (valid) apply(); return; }
    if (event.key !== 'Tab') return;
    const items = [...dialog.current!.querySelectorAll<HTMLElement>('button:not(:disabled):not([tabindex="-1"]), input:not(:disabled)')];
    const first = items[0]; const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }
  /** 用户确认后仅回填字段，保存日程仍由既有控制器负责。 */
  function apply() { if (valid) { onChange(chosen); onClose(); } }
  /** 今天按钮只调整临时选择，不绕过字段范围约束。 */
  function chooseToday() { const date = new Date(); if (!unavailable(date)) { setDay(today); setMonth(calendarDate(date.getFullYear(), date.getMonth(), 1)); } }
  /** 时间列只保留一个 Tab 停靠点，方向键调整选择并同步滚动位置。 */
  function moveTime(event: KeyboardEvent<HTMLButtonElement>, index: number, number: number) {
    const max = index === 0 ? 23 : 59;
    const next = event.key === 'ArrowUp' ? Math.max(0, number - 1) : event.key === 'ArrowDown' ? Math.min(max, number + 1) : event.key === 'Home' ? 0 : event.key === 'End' ? max : null;
    if (next === null) return;
    event.preventDefault(); event.stopPropagation(); const text = String(next).padStart(2, '0');
    setTime(index === 0 ? `${text}:${time.split(':')[1]}` : `${time.split(':')[0]}:${text}`);
    requestAnimationFrame(() => dialog.current?.querySelector<HTMLButtonElement>(`[data-time="${index}-${text}"]`)?.focus());
  }
  const focusDay = days.some(date => dateKey(date) === day && !unavailable(date)) ? day : dateKey(days.find(date => !unavailable(date) && date.getMonth() === month.getMonth()) ?? days[0]);
  return <dialog ref={dialog} className={`date-picker ${dateOnly ? 'date-only' : ''}`} aria-label={`选择${label}`} onKeyDown={keys} onCancel={event => { event.preventDefault(); event.stopPropagation(); onClose(); }} onClick={event => { const rect = event.currentTarget.getBoundingClientRect(); if (event.target === event.currentTarget && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)) onClose(); }}>
    <header className="picker-header"><strong>{label}</strong><button type="button" aria-label="关闭日期选择" onClick={onClose}>×</button></header>
    <div className="picker-body"><section className="picker-calendar" aria-label="日期">
      <div className="picker-month"><button type="button" aria-label="上个月" onClick={() => moveMonth(-1)}>‹</button><label><input aria-label="年份" type="number" min={1} max={9999} value={month.getFullYear()} onChange={event => { const year = Number(event.target.value); if (year >= 1 && year <= 9999) setMonth(calendarDate(year, month.getMonth(), 1)); }} />年</label><span aria-live="polite">{month.getMonth() + 1}月</span><button type="button" aria-label="下个月" onClick={() => moveMonth(1)}>›</button></div>
      <table role="grid" aria-label={`${month.getFullYear()}年${month.getMonth() + 1}月`}><thead><tr>{['一', '二', '三', '四', '五', '六', '日'].map(name => <th key={name} scope="col">{name}</th>)}</tr></thead><tbody>{Array.from({ length: 6 }, (_, row) => <tr key={row}>{days.slice(row * 7, row * 7 + 7).map(date => { const key = dateKey(date); return <td key={key} aria-selected={key === day}><button type="button" data-day={key} tabIndex={key === focusDay ? 0 : -1} disabled={unavailable(date)} aria-label={`${date.getFullYear()}年${date.getMonth() + 1}月${date.getDate()}日`} aria-current={key === today ? 'date' : undefined} className={`${date.getMonth() !== month.getMonth() ? 'other-month' : ''} ${key === day ? 'selected' : ''}`} onClick={() => { setDay(key); setMonth(calendarDate(date.getFullYear(), date.getMonth(), 1)); }} onKeyDown={event => moveDay(event, date)}>{date.getDate()}</button></td>; })}</tr>)}</tbody></table>
    </section>{!dateOnly && <section className="picker-time" aria-label="时间">{(['小时', '分钟'] as const).map((label, index) => <div className="time-column" key={label}><strong>{label}</strong><div className="time-options" role="group" aria-label={label}>{Array.from({ length: index === 0 ? 24 : 60 }, (_, number) => { const text = String(number).padStart(2, '0'); return <button type="button" key={text} data-time={`${index}-${text}`} tabIndex={time.split(':')[index] === text ? 0 : -1} aria-label={`${text}${label}`} aria-pressed={time.split(':')[index] === text} onKeyDown={event => moveTime(event, index, number)} onClick={() => setTime(index === 0 ? `${text}:${time.split(':')[1]}` : `${time.split(':')[0]}:${text}`)}>{text}</button>; })}</div></div>)}</section>}</div>
    <p className="picker-selection" aria-live="polite">{chosen.replace('T', ' ')}{!valid && ' · 超出可选范围'}</p>
    <footer className="picker-footer"><button type="button" onClick={() => { onChange(''); onClose(); }}>清除</button><button type="button" disabled={unavailable(new Date())} onClick={chooseToday}>今天</button><button type="button" className="picker-confirm" disabled={!valid} onClick={apply}>确定</button></footer>
  </dialog>;
}
