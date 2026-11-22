# 额度数据源部署（Gemini / Antigravity / Grok）

> 适用范围：Windows 10/11，AgentHub v0.9.x。
> 目标：让「额度」页拿到真实数据，同时不在 AgentHub 内打包或复制第三方实现。

## 1. 架构

AgentHub 只读三类数据源，不接管 Google / xAI 登录，也不要求复制 Cookie 或 OAuth 凭据：

```
gemini-web2api-go  (127.0.0.1:8083)  ─┐
Antigravity-Manager (127.0.0.1:8045) ─┼─→ AgentHub internal/quota ─→ 额度页
GrokUsagePet usage.json (本地快照)    ─┘
```

| 组件 | 作用 | 地址 / 路径 | 获取方式 | 许可 |
|---|---|---|---|---|
| [gemini-web2api-go](https://github.com/LymtIcsh/gemini-web2api-go) | Gemini 网页额度，本地 HTTP 服务 | `http://127.0.0.1:8083` | 源码构建 | 见仓库 |
| [Antigravity-Manager](https://github.com/lbjlaq/Antigravity-Manager) | 读取 Antigravity 登录态并暴露额度 API | `http://127.0.0.1:8045` | 官方 Release 安装 | CC BY-NC-SA 4.0，仅本地个人使用 |
| [GrokUsagePet](https://github.com/MGMTopia/grok-usage-pet) | 生成 Grok / Grok Bot 额度快照 | `%LOCALAPPDATA%\GrokUsagePet\usage.json` | Release zip 或源码 | 见仓库 |

统一前置条件：

- Git 使用 SSH 克隆，克隆目录建议放在 `C:\Users\wanghui\Githubs\ai-tools\` 下。
- Go 1.24+、Wails CLI（仅构建 AgentHub 时需要）。
- Node.js ≥ 22.15（Gemini 服务的浏览器工作进程需要）。
- Python 3.12+（仅 Grok 源码方式需要）。

## 2. Gemini Web（gemini-web2api-go）

### 2.1 安装

```powershell
cd C:\Users\wanghui\Githubs\ai-tools
git clone git@github.com:LymtIcsh/gemini-web2api-go.git
cd gemini-web2api-go
cd browser-worker; npm ci; npx camoufox fetch; cd ..
go build -trimpath -o gemini-web2api-go.exe .
```

说明：

- `npm ci` 安装 `browser-worker` 的 Node 依赖，`npx camoufox fetch` 下载 Camoufox 浏览器。
- 缺少这一步时添加账号会报 `ERR_MODULE_NOT_FOUND: Cannot find package 'camoufox'`。
- 首次启动会生成自己的 `config.json`，已有配置不会覆盖；仓库不包含任何账号或 Cookie。

### 2.2 启动

```powershell
.\gemini-web2api-go.exe
```

默认监听 `127.0.0.1:8083`。也可以在面板或 `config.json` 中确认：

| 字段 | 说明 |
|---|---|
| `host` / `port` | Windows 首次配置为 `127.0.0.1` / `8083` |
| `admin_token` | 管理密码，空值表示管理接口不鉴权 |
| `BROWSER_WORKER_PATH` | 默认 `browser-worker/worker.mjs` |

### 2.3 添加 Gemini 账号

1. 打开 `http://127.0.0.1:8083/admin`，用 `admin_token` 登录。
2. 「账号池」→ 添加账号 → 在弹出的 Camoufox 窗口完成 Google 登录。
3. 回到面板点「确认登录」，账号状态变为已登录。
4. 一个账号会提供 5 小时与周两个额度池；AgentHub 只展示周池。

### 2.4 验证

```powershell
$t = '<admin_token>'
Invoke-RestMethod http://127.0.0.1:8083/admin/api/accounts -Headers @{ Authorization = "Bearer $t" }
```

## 3. Antigravity（Antigravity-Manager）

### 3.1 前提

本机已安装并登录 **Antigravity IDE**。Antigravity 本身不缓存额度，只保存登录态；额度需要由工具带登录态实时请求上游。

### 3.2 安装

1. 从 [Releases](https://github.com/lbjlaq/Antigravity-Manager/releases/latest) 下载 `Antigravity.Tools_<version>_x64-setup.exe`。
2. 静默安装：`.\Antigravity.Tools_5.0.4_x64-setup.exe /S`。
3. 默认安装目录：`%LOCALAPPDATA%\Antigravity Tools\antigravity-tools.exe`，占用约 110MB。

首次启动会自动识别 Antigravity IDE 的登录账号，并在 `127.0.0.1:8045` 启动本地 API。

### 3.3 获取 API Key

桌面版把代理 API Key 存在：

```
%USERPROFILE%\.antigravity_tools\gui_config.json
```

读取其中的 `proxy.api_key`。该 Key 就是 AgentHub 配置里的 `adminToken`。

### 3.4 鉴权差异（v5.0.4）

- v5 使用 `Authorization: Bearer <api_key>`（`x-api-key` 同样可用）。
- 旧版只认 `x-admin-token`；用错会返回 `401`。
- AgentHub 的适配器同时发送三种头，兼容新旧版本。
- 单账号 `/api/accounts/{id}/quota` 在 v5 会阻塞十余秒，AgentHub 优先读取 `/api/accounts` 内联的 `quota.quota_groups`。
- 主动刷新使用 `POST /api/accounts/refresh`。

### 3.5 验证

```powershell
$k = '<proxy.api_key>'
Invoke-RestMethod http://127.0.0.1:8045/api/accounts -Headers @{ Authorization = "Bearer $k" }
```

### 3.6 许可

Antigravity-Manager 为 **CC BY-NC-SA 4.0**。AgentHub 只在本地通过 HTTP API 读取额度，不分发、不打包、不复制其实现；请仅用于个人非商业场景。

## 4. Grok（GrokUsagePet）

### 4.1 方式 A：官方 Release（推荐，无需 Python）

1. 下载 `GrokUsagePet-v<version>-Windows-x64.zip` 并解压运行。
2. 用 `grok login` 登录 SuperGrok；需要 Grok Bot 时保持对应登录态。
3. 数据写入 `%LOCALAPPDATA%\GrokUsagePet\usage.json`，桌面应用会定期刷新。

### 4.2 方式 B：源码 + 定时抓取（无 GUI）

```powershell
cd C:\Users\wanghui\Githubs\ai-tools
git clone git@github.com:MGMTopia/grok-usage-pet.git
cd grok-usage-pet
python -m pip install -r requirements.txt
python fetch_usage.py --watch --interval 300 --quiet
```

- 每 300 秒刷新一次 `usage.json`；`CURSOR` / `Codex` 相关字段 AgentHub 不读取。
- 快照 `status=failed` 时 AgentHub 保留旧值并标为 `stale`，不会清零。

## 5. 接入 AgentHub

### 5.1 配置

在 AgentHub 数据目录的 `config.json` 中确认（路径通常为 `C:\Users\wanghui\.agenthub\data\config.json`）：

```json
{
  "quota": {
    "enabled": true,
    "intervalMinutes": 5,
    "alertThresholds": [80, 90, 100],
    "gemini": {
      "enabled": true,
      "baseURL": "http://127.0.0.1:8083",
      "adminToken": "<gemini admin_token>"
    },
    "antigravity": {
      "enabled": true,
      "baseURL": "http://127.0.0.1:8045",
      "adminToken": "<antigravity proxy.api_key>"
    },
    "grok": {
      "enabled": true,
      "snapshotPath": "%LOCALAPPDATA%\\GrokUsagePet\\usage.json",
      "planKind": "auto"
    }
  }
}
```

也可以通过额度页的设置区直接修改并保存。

### 5.2 交给「进程」托管

把三个数据源加入进程列表（额度页与进程页共用同一份 `config.json`）：

| id | command | args | workingDir |
|---|---|---|---|
| `gemini-web2api` | `...\gemini-web2api-go\gemini-web2api-go.exe` | — | `...\gemini-web2api-go` |
| `antigravity-tools` | `%LOCALAPPDATA%\Antigravity Tools\antigravity-tools.exe` | — | `%LOCALAPPDATA%\Antigravity Tools` |
| `grok-usage-pet` | `python.exe` | `fetch_usage.py --watch --interval 300 --quiet` | `...\grok-usage-pet` |

三者都建议 `autoStart=true`、`autoRestart=true`、`restartPolicy=on_failure`。

**不要手工在外部启动这些服务。** AgentHub 只接管自己启动的子进程（按持久化的 PID、启动时间、exe 路径校验），外部实例不会被识别，还会造成端口占用导致托管实例启动失败。

### 5.3 验证

1. 进程页三个服务显示运行中。
2. 额度页点「立即刷新」，数据源状态应为 `ok`。
3. 持久化快照可检查：`C:\Users\wanghui\.agenthub\data\quota-state.json`。

## 6. 常见问题

| 现象 | 原因与处理 |
|---|---|
| 添加 Gemini 账号报 `ERR_MODULE_NOT_FOUND: camoufox` | 未安装 `browser-worker` 依赖：执行 `npm ci` 与 `npx camoufox fetch` |
| Antigravity 接口 401 | 用了 `x-admin-token`；v5 需 `Authorization: Bearer <api_key>` |
| 进程页显示「失败 / 已停止」但端口在监听 | 存在外部手工实例；停掉外部实例，由 AgentHub 重新启动 |
| 额度页只有周池 | 有意设计：5 小时池不展示、不写历史、不参与提醒 |
| 数据源变 `stale` | 最近一次采集失败，旧数值保留；检查服务与 token |
| 百分比显示 `--` | 上游未返回可确认的百分比，不按 0 处理 |
| Antigravity 账号未出现 | 先在 Antigravity IDE 完成 Google 登录，再重启 Antigravity Tools |

## 7. 安全注意

- 三个服务都应只监听 `127.0.0.1`，不要暴露到局域网或公网。
- `admin_token` / `api_key` 以明文保存在本地 `config.json` 与 `gui_config.json` 中，不要提交到仓库或分享截图。
- AgentHub 不读取、不保存 Google 或 xAI 的账号密码，也不接管登录流程。
