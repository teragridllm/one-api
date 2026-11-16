# one-api macOS 菜单栏用量应用 — 设计文档

日期：2026-11-14
目标目录：`mac-plugins/one-api-menubar/`
参考形式：`/home/onehao/workspace/codex/ai-limit`（Python + rumps 菜单栏应用）

## 1. 目标

在 macOS 顶部菜单栏常驻显示 one-api「总览」数据：

- 最近 7 天每天使用的 token 总量
- 每个模型的使用量（近 7 天）

形式与 ai-limit 一致：顶部栏用「文字 + Unicode 柱状火花线」，点开是一列 NSMenu 文本行，不引入图表库。

## 2. 关键决策（已与用户确认）

| 维度 | 决策 |
|---|---|
| 技术栈 | Python 3 + rumps（`NSStatusItem` + `NSMenu`），打包 py2app + DMG |
| 数据范围 | 仅访问令牌所属账号自己的用量（`GET /api/user/dashboard`），零后端改动 |
| 顶部栏常驻内容 | 今日 tokens + 7 日火花线 |
| 明细指标 | tokens（prompt+completion）+ 请求次数（RequestCount）；Quota 默认不展示 |
| 语言 | 默认中文（zh） |
| 目录 | 当前项目 `mac-plugins/one-api-menubar/` |

## 3. 数据源与后端契约

唯一端点：`GET {base_url}/api/user/dashboard`

- 认证：Header `Authorization: Bearer <access_token>`（one-api `ValidateAccessToken` 会剥离 `Bearer ` 前缀，见 `model/user.go:337`；access_token 即「个人设置」里的 32 位令牌）。也兼容浏览器会话 Cookie，但本应用只走 Bearer。
- 无需查询参数：后端固定窗口为最近 7 天（`controller/user.go:264-266`）。
- 响应为 `LogStatistic[]`，按 `(Day, ModelName)` 分组，字段为 **PascalCase**（`model/log.go:216-223`，无 json tag）：

```json
{ "success": true, "message": "", "data": [
  { "Day": "2026-11-08", "ModelName": "gpt-4o",
    "RequestCount": 12, "Quota": 48210,
    "PromptTokens": 30000, "CompletionTokens": 9000 }
]}
```

- 无 `total_tokens` 字段：总 tokens = `PromptTokens + CompletionTokens`。
- 日志类型已由后端过滤为 `type=2`（消费）。

## 4. 架构（方案 A：逻辑层 + 薄 UI 层）

```
配置 ~/.one-api-menubar.json
   {base_url, access_token, refresh_sec, lang, bar_mode}
        │
oneapi_usage.py（纯逻辑，仅 stdlib urllib，Linux 可测）
   fetch_dashboard()  → 聚合 → 数据结构
        │
menubar/one-api-menubar.py（rumps App，AppKit 交互）
   后台线程抓取 → _pending(锁) → 主线程 timer 渲染 NSStatusItem/NSMenu
        │
缓存 ~/.one-api-menubar-cache.json（TTL 55s）
自启 ~/Library/LaunchAgents/com.oneapi.menubar.plist
```

数据流：

1. 抓取：`GET {base}/api/user/dashboard`（Bearer）。
2. 聚合：
   - 每日总 tokens = 按 `Day` 求和 `PromptTokens+CompletionTokens`；每日次数按 `Day` 求和 `RequestCount`。
   - 每模型 tokens/次数 = 跨 7 天按 `ModelName` 求和。
   - 今日 tokens = `Day == 本地今天 YYYY-MM-DD` 的当日总量。
3. 顶部栏：`⚡ {今日tokens简写} {火花线}`。火花线 7 格，字符 `▁▂▃▄▅▆▇█`，按 7 天最大值线性缩放（max→`█`，0→`▁`）。
4. 刷新：后台线程，间隔 `refresh_sec`（默认 60s，最小 30s）；结果写入带 TTL 的缓存；UI 仅在主线程 timer 更新（照搬 ai-limit 的 `_pending` 锁 + timer 交接模式）。

## 5. 界面

顶部栏（常驻）：`⚡ 1.8M ▁▃▅▂▇▃▅`

下拉 NSMenu（点开）：

```
── one-api 总览 (近7天) ──
2026-11-08  ███░░  3.2M   412次
2026-11-09  █████  5.6M   730次
… 共 7 行（日期 + 柱 + tokens + 次数）
── 模型用量 (近7天) ──
gpt-4o            ████  6.1M  34%  890次
claude-3-5-sonnet ██    2.9M  16%  320次
… 按 tokens 降序；柱 = 该模型 tokens 占 7 天总量比例；百分比同列
── 最近刷新 07:17 · 共 18.4M tokens ──   (置灰不可点)
语言 (zh/en)
菜单栏显示 (今日 / 7日总量)      → bar_mode 切换
刷新间隔 (30/60/120s)
立即刷新 · 打开 one-api 控制台 · 开机自启
关于 · 退出
```

- tokens 简写：`>=1M` → `x.xM`；`>=1000` → `x.xk`；否则原值。
- 每日/每模型的行内柱用 `█░`，宽度 5（按该行占当前区块最大值缩放）。
- 置灰行用 `setEnabled_(False)`（照搬 ai-limit 的 `_noop/_disable`）。

## 6. 配置与状态文件

- 配置 `~/.one-api-menubar.json`：`base_url`、`access_token`、`refresh_sec`(默认60/最小30)、`lang`(`zh|en`，默认zh)、`bar_mode`(`today|total`)。缺 `base_url`/`access_token` 时菜单显示「请配置 one-api 地址与令牌」行。
- 缓存 `~/.one-api-menubar-cache.json`：缓存上次成功响应 + 时间戳，TTL 55s；「立即刷新」删除缓存并强制请求。
- 自启：写/删 `~/Library/LaunchAgents/com.oneapi.menubar.plist`（`RunAtLoad=true`，`KeepAlive=false`），勾选态 = plist 是否存在。
- 模板文件 `one-api-menubar.example.json` 供参考。

## 7. 错误处理

- 网络失败 / 超时：保留上次缓存数据，行尾标灰并加「(数据过时)」。
- 401 / 认证失效：菜单顶部显示「登录令牌无效，请检查 access_token」。
- `success:false` 或响应缺字段：按空数据处理，不崩溃。
- 抓取全程在后台线程，任何异常不触碰 AppKit UI。

## 8. 目录结构

```
mac-plugins/one-api-menubar/
├── oneapi_usage.py             # 纯逻辑：抓取/聚合/格式化/火花线（stdlib urllib）
├── menubar/
│   ├── one-api-menubar.py      # rumps App：NSStatusItem + NSMenu + 线程/timer/缓存
│   ├── setup.py                # py2app，LSUIElement=True
│   └── make-dmg.sh             # hdiutil 打包 .dmg
├── one-api-menubar.example.json
├── tests/
│   └── test_oneapi_usage.py    # 聚合/火花线/格式化；app 逻辑通过 stub rumps/AppKit 测试
└── README.md
```

## 9. 测试策略

- `python3 -m unittest`（Linux 即可运行）。
- 纯逻辑直测：`LogStatistic[]` → 每日聚合、每模型聚合、tokens 简写、火花线缩放（含全 0、单峰值、边界）。
- App 逻辑：用 `sys.modules` stub 掉 `rumps` 与 `AppKit`，验证刷新状态机/缓存/渲染数据构造（参照 ai-limit `tests/test_menubar_refresh.py`）。
- `.app`/DMG 需在 macOS 上产出：`cd mac-plugins/one-api-menubar/menubar && python3 setup.py py2app && bash make-dmg.sh`。

## 10. 明确不做（YAGNI）

- 不改 one-api 后端、不加新端点。
- 不做全站/多用户聚合（仅本账号）。
- 不显示 Quota/金额换算。
- 不引入图表库、httpx、Electron。
- 不做数据导出、多渠道下钻。

## 11. 环境与验证限制

当前开发环境为 Linux（Python 3.12），可编写并跑逻辑单测，但无法编译/运行 `.app`。rumps/AppKit 相关的真机运行与 DMG 打包需在 macOS 上完成。
