# Frontend workflow

- 前端开发以 `refactoring-ui` skill 为主要设计与检查依据；`distinctive-ai-design` 只在需要探索视觉方向时辅助使用。
- 沿用“虹桥·光谱工作台”现有组件、语义色彩与深浅主题，优先复用，不另起设计系统。
- 主要面向电脑网页端；本阶段不增加移动端重构。视觉检查覆盖 1280px 与 1440px 桌面宽度。
- 保留接口、BasePath、Cookie/CSRF 和会话恢复契约；页面专项 Mock 测试与真实接口验收分别记录。
