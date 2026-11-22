# AgentHub 项目交接

> 最后更新：2026-11-22 · 交接给下一个 agent 阅读本文件即可快速上手。

## 1. 项目定位

Wails v2（Go + Vue3/TS）桌面应用「Agent 主导的本地工具箱」。本项目是 `modstart-lib/prochub` 的 fork，已全面更名为 **agent-hub / AgentHub**。当前用途：托管本机 4 个常驻服务，统一提供启动、停止、日志、自启与状态管理；计划继续扩展更多 Agent 工具（如额度重置监控）。

## 2. 关键路径与仓库

- 工作目录：`C:\Users\wanghui\Projects\NewApps\agent-hub`（用户的私人项目池）
- 远端：`git@github.com:wanda1416/agent-hub.git`，`main` 与 `origin/main` 同步于 `a651909`
- 最新 Release：`v0.9.0-beta.1`（非 prerelease），资产 `AgentHub-0.9.0-beta.1-windows-amd64.exe` + `checksums.txt`
- 版本号：`app.go:771` → `Version: "0.9.0-beta.1"`
- 运行数据：`C:\Users\wanghui\.agenthub\data`（`client.json` 的 `dataRoot` 已指向此处，日志在其 `logs/` 下）
- 旧数据：`C:\Users\wanghui\.prochub` 仍保留作备份，可删

## 3. 改名落点（已完成，勿重复）

| 项 | 旧 | 新 |
|---|---|---|
| Go module | `prochub` | `agenthub` |
| 产品名 | ProcHub | AgentHub |
| 产物 | `ProcHub.exe` | `AgentHub.exe`（根目录与 `build/bin/`） |
| 数据目录 | `~/.prochub` | `~/.agenthub` |
| 环境变量 | `PROCHUB_DATA_ROOT` | `AGENTHUB_DATA_ROOT` |
| 自启动键 | `HKCU\...\Run\prochub` | `HKCU\...\Run\agenthub` → 新 exe 绝对路径 |
| 更新仓库 | `modstart-lib/prochub` | `wanda1416/agent-hub` |

## 4. 构建环境（本机已就绪）

- Go：`C:\Program Files\Go\bin`（**不在 PATH**，需手动加）
- Wails CLI：`C:\Users\wanghui\go\bin\wails.exe`（v2.12.0）
- 前端依赖已安装；`frontend/wailsjs`、`frontend/dist`、`node_modules` 均被 gitignore

```powershell
$env:Path = 'C:\Program Files\Go\bin;C:\Users\wanghui\go\bin;' + $env:Path
$env:GOPROXY='https://goproxy.cn,direct'
cd C:\Users\wanghui\Projects\NewApps\agent-hub
wails build -clean
```

- 产物可复现：本地构建 SHA256 与 Release 资产完全一致（`c797d5fd…3471466`）
- 发布方式：本地构建 + `gh release create`；Actions 的 Release workflow 已 `disabled_manually`，因为它依赖私有 `agenthub-pro` 克隆与 macOS 签名 secrets

## 5. 当前运行状态

```powershell
AgentHub.exe process list
```

| 进程 | 端口/挂载 |
|---|---|
| workbuddy2api | :7863 |
| trae-cn-relay | :8000 |
| dsh-web | :3080 |
| rclone-gdrive | `K:`（rclone mount Google Drive） |

日志：`AgentHub.exe process logs <id> --tail 50 --follow`

## 6. 必须知道的坑

1. **重启 AgentHub 不再需要先停子进程**：子进程身份（PID + 创建时间 + exe 路径）持久化在 `~/.agenthub/data/runtime-state.json`，启动时校验后直接接管存活进程，AutoStart 不会重复拉起。退出 AgentHub 也不再 `StopAll`，子进程继续运行等待下次接管。要真正停止服务，仍用 `AgentHub.exe process stop <id>`。
   - 子进程 stdout/stderr 直接写入 `~/.agenthub/data/streams/<id>/{stdout,stderr}/*.log`，AgentHub 再 tail 到日志面板。旧实现的匿名管道会随 App 退出断裂，Node 进程（dsh-web）在写日志时会触发 EPIPE 崩溃；文件句柄不依赖父进程，已实测重启后 90 秒以上仍存活。
   - 重启后日志从文件末尾继续，运行期不断档；日志面板只显示本次启动后的新行，历史行仍在 stream 文件里。
   - 接管进程退出后不会按重启策略自动拉起（没有退出码，无法区分正常/异常），状态变为 stopped，需手动启动。
   - PID 校验失败（进程已退出、PID 被复用、exe 路径变化）会清掉旧记录并正常重新启动。
   - **首次从旧版本迁移时**，已在运行的子进程仍是管道输出，需要按老流程停一次再启动，之后才会切到文件输出。
2. **Windows Defender 误报**：曾把 exe 判为 `Behavior:Win32/Persistence.A!ml`（新路径 + Run 键触发），该条目**不可还原**（`0x80508014`）。已加排除目录 `...\agent-hub`、`...\prochub`，排除进程仍写作旧的 `ProcHub.exe`；若新名 `AgentHub.exe` 再次被拦，需补这条排除并重新放行。
3. **更新后端**：`app.go` 的 `baseURL` 仍指 `open.tecmz.com/open_app/AgentHub`（该应用不存在，404 后自动回退 GitHub Releases）；`WebsiteGitee` 已置空。已实测 `update check` 显示 `来源: github`。
4. **发布资产命名必须匹配** `AgentHub-<version>-<os>-<arch>.exe`，否则更新器解析不到；更新器默认跳过 prerelease。
5. **rclone 依赖 AgentHub 托管**：关掉 AgentHub 不再中断 `K:`（子进程被保留），但显式 `process stop rclone-gdrive` 仍会掉。Google Drive 桌面版已废弃卸载，不要再用。
6. 之前 Google Drive 的 Dokan 挂载死掉曾导致 Explorer 僵尸（桌面/任务栏消失），只能注销/重启恢复——这也是迁到 rclone 的原因。
7. `checksums.txt` 已被替换为本项目发布清单，不再是上游 0.8.x 内容。
8. 本机 Windows 上 `go test ./...` 有两个既有失败，与本次改动无关：`internal/updater` 引用了仅 linux 定义的 `relaunchAfterInstall`；`internal/cli` 的 0600 权限断言在 Windows 不成立。验证改动请跑 `go test ./internal/process ./internal/platform ./internal/control ./internal/service`。

## 7. 下一步（用户已确认方向，尚未动手）

把 agent-hub 从「只跑进程」扩展为**工具集**，首个新模块：常驻 **watcher**，监控 `https://aigazz.com/zh/resets/antigravity` 这类「额度重置」公告。

已定决策：

- 通知走 **Windows 托盘/Toast**，不引第三方推送
- **内置 watcher 模块**（不先做外挂进程）
- 监控四个品类：Antigravity / Codex / Claude / Grok Bot
- 数据源主用 `aihot.news`，`aigazz.com` 作镜像回退；60~120s 轮询 + 抖动 + `If-Modified-Since` + 失败退避；优先抓「已宣布待生效」状态；状态持久化去重
- 进程配置增加 `kind: process | watcher`，作为后续工具的挂载点
- 命名保持 agenthub，暂不再改

## 8. 交接注意事项

- 重启 AgentHub 前不需要停进程；改 exe 构建/替换时进程保留，重新打开即接管
- `wails build` 会自动生成 `frontend/wailsjs`，不要手改
- git 提交作者使用 `wanghui <wanghui@users.noreply.github.com>`（仓库未配置全局身份）
- 改动前先 `git status`，当前工作区应保持干净（`main == origin/main`）
- 运行态文件 `~/.agenthub/data/runtime-state.json` 只存运行中进程的身份；删除它只会导致下次启动不接管，不会杀进程
- 子进程原始输出在 `~/.agenthub/data/streams/<id>/{stdout,stderr}/*.log`，每次 spawn 新建文件并只保留最近 2 份
