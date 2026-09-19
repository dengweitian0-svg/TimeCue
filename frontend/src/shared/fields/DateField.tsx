import type { ScheduleError } from '../contracts/errors';

/** 显示日期时间输入并关联结构化字段错误。 */
export function DateField({
  label,
  name,
  value,
  min,
  max,
  dateOnly = false,
  error,
  onChange,
}: {
  label: string;
  name: string;
  value: string;
  min?: string;
  max?: string;
  dateOnly?: boolean;
  error: ScheduleError | null;
  onChange: (value: string) => void;
}) {
  const message = error?.fields[name];
  return (
    <label>
      {label} <span className="required-mark">*</span>
      <input
        type={dateOnly ? 'date' : 'datetime-local'}
        value={value}
        min={min?.slice(0, dateOnly ? 10 : 16)}
        max={max?.slice(0, dateOnly ? 10 : 16)}
        aria-invalid={Boolean(message)}
        aria-describedby={message ? `error-${name}` : undefined}
        onChange={(event) => onChange(event.target.value)}
      />
      {message && (
        <span className="field-error" id={`error-${name}`}>
          {message}
        </span>
      )}
    </label>
  );
}
