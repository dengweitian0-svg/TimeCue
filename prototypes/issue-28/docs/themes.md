# 主题与媒体背景扩展

## 配色独立于背景

`Settings.theme` 选择 `peach`、`mint`、`sky`、`lavender`。`Settings.background` 独立描述背景。本期存储适配器只接受 `{ kind: 'solid' }`；页面不展示未启用的图片/视频上传入口。

`Background` 是页面外层唯一背景容器。未来增加媒体渲染与存储格式支持时，页面组件结构保持不变。现在收到未启用媒体配置时，容器明确回退纯色。

## 语义变量映射

Figma 集合为“TimeCue · 原始值”“TimeCue · 配色”“TimeCue · 尺度”，共 80 个变量。配色集合含四个模式，语义变量引用原始值。CSS 名称写入 Figma Web code syntax。

| Figma 变量 | CSS | 用途 |
| --- | --- | --- |
| color/bg | --bg | 应用背景 |
| color/surface | --surface | 卡片与窗口标题栏 |
| color/soft | --soft | 选中、信息提示、禁用底色 |
| color/accent / hover | --accent / --hover | 主要按钮与悬停 |
| color/text / muted | --text / --muted | 正文与辅助文字 |
| color/border | --border | 轻描边 |
| color/success / successBg | --success / --successBg | 保存成功 |
| color/error / errorBg | --error / --errorBg | 失败反馈 |
| color/focus | --focus | 键盘焦点 |
| color/onAccent | --onAccent | 强调色上的白色文字 |
| space/4…48、radius/12…28 | --space-*、--radius-* | 间距与圆角 |

`src/themes.ts` 是代码侧配色来源。四主题正文、辅助文字、主要按钮和反馈文字均有 ≥4.5:1 的自动对比度检查。晴空蓝辅助色调整为 `#566979`，Figma 原始变量同步更新。

中文采用 Noto Sans SC，字体本地打包。小闹钟 SVG 是可编辑品牌插图，保留暖桃固有色。Windows 模拟通知与托盘使用独立系统配色。

## 媒体背景的后续约定

| 事项 | 约定 |
| --- | --- |
| 资源引用 | `resource` 使用由平台适配器解析的受控资源 ID；不让页面读取任意本地文件。真实来源、访问权限与 URL 生命周期由适配器负责。 |
| 裁切 | `fit: cover` 默认居中覆盖，可选 `contain`；保持宽高比，不拉伸。裁切/位置可在未来背景配置中扩展。 |
| 遮罩 | `overlay` 范围 0–1，由背景层提供主题色遮罩；卡片继续使用语义 surface，保证文字对比。 |
| 加载与失败 | 加载前、错误、资源被删除或不支持格式时回退当前配色的 bg；不阻挡设置和确认操作。可记录一次可恢复 WARN。 |
| 视频播放 | 默认 muted、playsInline；不播放声音。尊重 `prefers-reduced-motion`，启用减少动态效果时使用海报或纯色。 |
| 生命周期 | 窗口隐藏/最小化、页面不可见、应用停止时暂停解码与播放；恢复后按用户偏好继续。组件卸载时移除事件监听、停止媒体并释放 object URL。 |
| 预算 | 限制体积、分辨率与解码资源；超预算回退纯色。重试次数与加载时间受限，不使用无限重试。 |
| 安全 | 后续适配器校验 MIME、大小与协议；外部地址与本地资源采用统一引用，不拼接可执行 HTML。 |

启用媒体属于后续范围，需要扩展适配器存储校验和背景渲染实现，以及增加加载失败、减少动态效果和释放资源测试。本期只交付契约、回退容器与上述约定。
