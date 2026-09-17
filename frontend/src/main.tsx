import '@fontsource/noto-sans-sc/400.css';
import '@fontsource/noto-sans-sc/500.css';
import '@fontsource/noto-sans-sc/700.css';
import { createRoot } from 'react-dom/client';
import { createProductionApp } from './App';
import { createWailsAdapter } from './wails-adapter';
import './styles.css';

/** 启动生产前端并在绑定不可用时显示不带平台细节的故障页面。 */
async function bootstrap(): Promise<void> {
  const root = createRoot(document.getElementById('root')!);
  try { root.render(createProductionApp(await createWailsAdapter())); }
  catch { root.render(<div className="fatal-page"><span className="empty-mark">!</span><h1>TimeCue 暂时无法连接</h1><p>桌面运行时尚未准备好，请重新启动应用。</p></div>); }
}

void bootstrap();
