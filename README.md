# iShell

iShell 是一个运行在现有终端中的全屏 Shell 会话管理器。它不替代 iTerm2、Windows Terminal、PowerShell 或 Linux 终端，而是负责管理分组、连接信息和加密凭证；连接时仍使用当前终端承载交互。

支持 SSH 与 Telnet，会话数据统一加密保存到用户目录。

## 特性

- 全屏终端菜单，支持分组、会话创建、编辑、删除和排序。
- 会话密码加密保存在本地 vault；SSH 与 Telnet 均可自动填写标准密码提示。
- 支持可复用的远端初始化脚本；一个脚本可关联多个 session。
- SSH 自动接受首次主机指纹，并拒绝已记录主机的指纹变更。
- SSH 默认启用 60 秒保活；Telnet 使用 TCP 60 秒 keepalive。
- 中文、英文界面。默认跟随系统语言，也可在设置中手动指定。
- 可配置备份目录、自动备份间隔及最大保留份数。
- 支持 macOS、Windows、Linux。

## 运行环境

- Go 1.26 或更高版本
- SSH 会话需要系统提供 `ssh` 命令

## 快速开始

在项目目录构建并启动：

```sh
go build -o ishell .
./ishell
```

Windows：

```powershell
go build -o ishell.exe .
.\ishell.exe
```

## 安装到 PATH

构建完成后执行：

```sh
./ishell install
```

macOS/Linux 会将程序复制到 `~/.ishell/bin/ishell`，并把该目录写入当前 shell 的启动文件；打开新终端后可直接运行 `ishell`。Windows 会将 `~/.ishell/bin` 写入用户 PATH。移除安装可执行 `ishell uninstall`。

首次启动时可设置 vault 密码。留空时，iShell 会生成随机 key 并存入操作系统凭证存储，本地 vault 仍由 AES-256-GCM 加密。

## 数据与安全

所有分组、会话、用户名、端口和会话密码都保存在：

```text
~/.ishell/vault.json
```

vault 使用以下方式保护：

- 设置 vault 密码时，随机 salt 与密码经 Argon2id 派生密钥，再使用 AES-256-GCM 加密本地数据。
- 未设置 vault 密码时，随机 vault key 保存到系统凭证存储，数据仍由 AES-256-GCM 加密。

`~/.ishell/settings.json` 仅保存备份位置、保留策略和语言设置，不含主机、账号或密码。

连接时，SSH 密码不会出现在命令行参数或环境变量中。iShell 用带一次性令牌的本地临时通道向 `SSH_ASKPASS` 提供密码；Telnet 内置连接器同样通过该通道读取密码。

## 基本操作

| 按键 | 操作 |
| --- | --- |
| `Enter` | 打开分组、保存表单或连接会话 |
| `Esc` | 返回上一级；在根目录退出 |
| `j` / `k` 或方向键 | 移动选择 |
| `Shift+Up` / `Shift+Down` | 调整当前分组或会话在同级中的顺序 |
| `e` | 编辑选中的分组或会话 |
| `d` | 删除选中的分组或会话，随后确认 |
| `Ctrl+B` | 在备份设置页立即创建备份 |

非空分组不能直接删除，需要先删除其中的会话和子分组。

## 初始化脚本

初始化脚本是连接成功后在远端执行一次的脚本。它适合进入工作目录、加载环境变量或启动日志查看命令。

编辑 session 时，在“初始化脚本”字段按 `Enter`，可选择：

- 使用已有脚本
- 新建初始化脚本
- 不使用脚本

在脚本选择列表上按 `e` 可编辑已有脚本。脚本包含用户指定的名称、`sh` 或 `bash` 解释器和内容；同一个脚本可被多个 session 复用。脚本内容保存在加密 vault 中，因此备份会自动包含它。

编辑脚本内容时，iShell 按以下顺序选择编辑器：`$VISUAL`、`$EDITOR`、macOS/Linux 的 `vi`、Windows 的 `notepad.exe`。编辑期间使用权限为 `0600` 的临时文件，退出编辑器后立即读取并删除。

- SSH：在远端执行脚本后进入所选解释器的交互 shell。
- Telnet：自动登录后将脚本逐行发送到远端终端。

初始化脚本拥有远端账号的同等权限，应只关联可信内容。Telnet 的脚本内容和密码都会以明文在网络中传输。

## 会话协议

创建会话时用左右方向键选择协议。

### SSH

- 默认端口为 `22`。
- 默认追加 `ServerAliveInterval=60` 和 `ServerAliveCountMax=3`。
- 使用 `StrictHostKeyChecking=accept-new`：首次连接自动写入正常的 `known_hosts`，主机指纹后续发生变化时会拒绝连接。
- 仍兼容 SSH key、ssh-agent、`~/.ssh/config` 与 SSH 别名。

### Telnet

- 默认端口为 `23`。
- 内置 Telnet 连接器会识别常见的 `login:`、`username:`、`password:` 提示，并自动填入保存的用户名和密码。
- Telnet 协议本身以明文传输凭证和终端流量，仅应在受信任网络或兼容旧系统时使用。优先使用 SSH。

## 备份

在“设置”中配置以下内容：

- 备份目录
- 自动备份间隔（小时）；设为 `0` 关闭自动备份
- 最多保留备份数；设为 `0` 表示全部保留

“设置”中的“备份与恢复”入口还可配置 WebDAV 地址、远端路径、用户名和密码。WebDAV 凭证与其他 session 数据一样保存在加密 vault 中，不会写入明文 `settings.json`。建议使用 HTTPS。

iShell 会在启动时检查一次备份，并在运行期间每分钟检查是否到期。每份备份保存为：

```text
<备份目录>/<主机名>-<WebDAV 用户名>-YYYYMMDDHHMMSS-<标签>.zip
```

每份备份 zip 仅包含公开的 `metadata.json`（备份格式版本、vault 版本、创建时间、来源主机、app 版本和 salt）以及加密的 `vault.enc`（AES-GCM nonce 和密文），不会生成明文凭证。创建备份时，iShell 先在内存中解密本地 vault，再用独立的备份密码和随机 salt 重新加密 zip；该密码保存在加密 vault 中以支持自动备份，也用于跨设备恢复。超过保留数量时，只会清理符合 iShell 备份命名规则的旧备份。

当前不提供 cron、Windows 任务计划程序或其他后台调度；iShell 退出后不会继续执行自动备份。

### WebDAV 同步

每次本地备份成功后，iShell 将加密的 zip 推送到 WebDAV，远端目录结构与本地一致。启动 iShell 时，以及打开“备份与恢复”时，程序会后台检查远端是否存在本地缺少的备份；发现后会下载到本地备份目录，并按最大保留数清理旧备份。

WebDAV 使用 Basic Authentication。请优先使用 `https://` 地址，避免在网络中暴露凭证。

### 恢复备份

在“备份与恢复”的“备份目录或 `vault.json`”字段输入 zip 文件路径，或输入旧版备份目录/`vault.json` 文件路径，按 `Ctrl+R` 后确认即可恢复。程序会先读取备份 metadata，按格式版本选择恢复流程；新格式 zip 要求输入备份密码。验证失败不会覆盖当前数据；恢复成功后，数据会立即使用当前设备的 vault 保护方式重新加密，并替换当前分组、会话、脚本库和 WebDAV 配置。

## 语言

默认语言为“自动（跟随系统）”。

- macOS 读取 `AppleLocale`
- Windows 读取当前 UI language
- Linux 读取 `LANG`、`LC_ALL`、`LC_MESSAGES`

可在“设置”中切换为自动、中文或 English。手动选择优先于系统语言。

## 开发验证

```sh
go test ./...
go vet ./...
go build .
```

项目也会进行 Windows 与 Linux 交叉构建验证。
