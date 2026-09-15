import { createRoot } from 'react-dom/client';
import { App } from './App';
import { Controls } from './Controls';
import { MockAdapter, STORAGE_KEY } from './adapter';
import { Controller } from './controller';
import './styles.css';

const adapter = new MockAdapter(window.localStorage);
/** 让控制台与产品页签共享模拟平台结果，持久写入仍仅发生在适配器。 */
function onStorage(event: StorageEvent) { if (event.key === STORAGE_KEY) adapter.externalChanged(); }
window.addEventListener('storage', onStorage);
const controller = new Controller(adapter);
createRoot(document.getElementById('root')!).render(window.location.pathname === '/controls' ? <Controls adapter={adapter} /> : <App controller={controller} />);
/** 热更新时释放旧订阅，避免一个事件被重复处理。 */
function dispose() { controller.dispose(); window.removeEventListener('storage', onStorage); }
if (import.meta.hot) import.meta.hot.dispose(dispose);
