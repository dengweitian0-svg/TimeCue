import type { ButtonHTMLAttributes, CSSProperties, ReactNode } from 'react';
import type { BackgroundConfig } from './contracts';
import { themeStyle, type Theme } from './themes';

/** 复用一致的按钮焦点、加载与禁用语义。 */
export function Button({ secondary, busy, children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { secondary?: boolean; busy?: boolean }) {
  return <button {...props} className={`${secondary ? 'button secondary' : 'button'} ${props.className ?? ''}`} disabled={props.disabled || busy} aria-busy={busy || undefined}>{busy ? '正在保存…' : children}</button>;
}
/** 展示品牌角色，装饰插图不干扰辅助技术。 */
export function Mascot({ size = 144 }: { size?: number }) { return <img className="mascot" src="/clock.svg" width={size} height={size} alt="" aria-hidden="true" />; }
/** 使用语义反馈颜色，并将动态结果提供给屏幕阅读器。 */
export function Feedback({ children, kind = 'info' }: { children: ReactNode; kind?: 'info' | 'error' | 'success' }) { return <div className={`feedback ${kind}`} role={kind === 'error' ? 'alert' : 'status'}>{children}</div>; }
/** 背景扩展仅由此容器承接；未启用的媒体配置安全回退到主题纯色。 */
export function Background({ theme, config, children }: { theme: Theme; config: BackgroundConfig; children: ReactNode }) {
  return <div className="app-background" data-background={config.kind === 'solid' ? 'solid' : 'solid-fallback'} data-theme={theme} style={themeStyle(theme) as CSSProperties}>{children}</div>;
}
