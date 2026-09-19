import type { ScheduleError } from '../contracts/errors';

/** 显示下拉输入并关联结构化字段错误。 */
export function SelectField({
  label,
  name,
  value,
  options,
  error,
  onChange,
}: {
  label: string;
  name: string;
  value: string;
  options: string[];
  error: ScheduleError | null;
  onChange: (value: string) => void;
}) {
  const message = error?.fields[name];
  return (
    <label className="select-field">
      <span className="field-label">
        {label} <span className="required-mark">*</span>
      </span>
      <select
        value={value}
        aria-invalid={Boolean(message)}
        onChange={(event) => onChange(event.target.value)}
      >
        <option value="">请选择</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
      {message && <span className="field-error">{message}</span>}
    </label>
  );
}
