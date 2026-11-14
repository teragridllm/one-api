# one-api menu-bar app

macOS 菜单栏常驻应用，展示 one-api 账号最近 7 天每日 token 总量与每个模型用量。
形式与 ai-limit 一致：顶部栏文字 + Unicode 火花线，点开是 NSMenu 文本行。

顶部栏示例：`⚡ 1.8M ▁▃▅▂▇▃▅`（今日 tokens + 7 日火花线）

## 数据源
`GET {base_url}/api/user/dashboard`，Header `Authorization: Bearer <access_token>`。
access_token 来自 one-api「个人设置」的 32 位令牌。仅显示该账号自己的用量，无需改动后端。

## 安装配置（macOS）
1. 复制 `one-api-menubar.example.json` 到 `~/.one-api-menubar.json`，填入 `base_url` 与 `access_token`。
2. 安装依赖（仅 macOS，建议 Homebrew Python）：`pip3 install rumps py2app`

## 运行（开发）
```bash
python3 menubar/one-api-menubar.py
```

## 打包 .app + DMG（仅 macOS）
```bash
cd menubar
python3 setup.py py2app
bash make-dmg.sh
```
产出 `one-api-menubar-0.1.0.dmg`（未签名，Gatekeeper 需在「系统设置 > 隐私与安全性」放行）。

## 测试（Linux 即可，纯 stdlib）
```bash
cd mac-plugins/one-api-menubar
python3 -m unittest discover -s tests -v
```

## 目录
- `oneapi_usage.py` 纯逻辑：配置 / 抓取 / 聚合 / 格式化 / 缓存（仅标准库，可跨平台测）
- `menubar/one-api-menubar.py` rumps 应用：`NSStatusItem` + `NSMenu`、后台抓取线程、定时器、开机自启
