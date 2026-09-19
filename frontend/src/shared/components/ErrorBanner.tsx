import type { ScheduleError } from '../contracts/errors';

/** 展示结构化错误及首个字段级提示。 */
export function ErrorBanner({ error }: { error: ScheduleError }) {
  const fieldMessage = Object.values(error.fields)[0];
  return (
    <div className="error-banner" role="alert">
      <span className="error-icon">!</span>
      <div>
        <strong>{error.message}</strong>
        {fieldMessage && <p>{fieldMessage}</p>}
      </div>
    </div>
  );
}
