import type { ScheduleError } from '../contracts/errors';

/** 显示指定字段的结构化错误文本。 */
export function FieldError({
  error,
  name,
}: {
  error: ScheduleError | null;
  name: string;
}) {
  const message = error?.fields[name];
  return message ? (
    <span className="field-error" id={`error-${name}`}>
      {message}
    </span>
  ) : null;
}
