import type { Theme, WindowThemeColors } from './contracts';

export type { Theme, WindowThemeColors } from './contracts';

/** 四套主题的语义颜色，逐项复用 Issue #28 原型定义。 */
export const themes = {
  peach: {
    name: '奶油桃',
    bg: '#FFF8F0',
    surface: '#FFFDFA',
    soft: '#FBEBDD',
    accent: '#B95332',
    hover: '#974026',
    text: '#45342D',
    muted: '#79645A',
    border: '#E8D6C7',
    success: '#336C50',
    successBg: '#E6F2E8',
    error: '#A33438',
    errorBg: '#FBE7E6',
    focus: '#7654A3',
  },
  mint: {
    name: '薄荷绿',
    bg: '#F2F9F3',
    surface: '#FCFEFB',
    soft: '#E0F0E5',
    accent: '#326D55',
    hover: '#24513F',
    text: '#293F35',
    muted: '#596F63',
    border: '#C9DECE',
    success: '#286447',
    successBg: '#DFEFE1',
    error: '#A33438',
    errorBg: '#FAE8E6',
    focus: '#6545A0',
  },
  sky: {
    name: '晴空蓝',
    bg: '#F1F7FC',
    surface: '#FCFDFF',
    soft: '#DFEDF8',
    accent: '#356B95',
    hover: '#265274',
    text: '#2C3C4B',
    muted: '#566979',
    border: '#CBDAE8',
    success: '#30674D',
    successBg: '#E1F1E9',
    error: '#A33438',
    errorBg: '#FBE7EB',
    focus: '#7150A0',
  },
  lavender: {
    name: '薰衣草紫',
    bg: '#F8F4FC',
    surface: '#FEFCFF',
    soft: '#EEE5F6',
    accent: '#795598',
    hover: '#604178',
    text: '#40334C',
    muted: '#746280',
    border: '#DECEE9',
    success: '#30674D',
    successBg: '#E5F2EC',
    error: '#A33438',
    errorBg: '#FBE7ED',
    focus: '#386A91',
  },
} as const satisfies Record<Theme, Record<string, string>>;

/** 返回设置页稳定展示顺序，避免依赖对象枚举顺序。 */
export const themeOrder: Theme[] = ['peach', 'mint', 'sky', 'lavender'];

/** 将语义颜色转换为页面共用的 CSS 自定义属性。 */
export function themeStyle(theme: Theme): Record<string, string> {
  return Object.fromEntries(Object.entries(themes[theme]).filter(([name]) => name !== 'name').map(([name, value]) => [`--${name}`, value]));
}

/** 从当前主题提取原生标题栏所需的最小语义颜色集合。 */
export function windowThemeColors(theme: Theme): WindowThemeColors {
  return { surface: themes[theme].surface, text: themes[theme].text };
}
