import { useEffect } from 'react';

/** 在页面中央显示业务结果，并在五秒后自动清除。 */
export function NoticeToast({ message, onDismiss }: { message: string; onDismiss: (expected: string) => void }) {
  useEffect(() => {
    const timer = window.setTimeout(() => onDismiss(message), 5000);
    return () => window.clearTimeout(timer);
  }, [message, onDismiss]);
  return <div className="notice-toast" role="status" aria-live="polite">{message}</div>;
}
