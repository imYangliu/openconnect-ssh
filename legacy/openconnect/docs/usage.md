# OCH 使用指南

本文带你安装 OCH、配置 VPN，并通过 SSH 登录目标主机。示例中的 `vpn.example.com`、`server.internal.example` 和账号均为占位内容，请替换为自己的信息。

## 安装 OCH

### 安装预编译版

预编译包支持 macOS Apple Silicon 和 Linux x86_64，不需要 Go。macOS 需先安装 Homebrew。Debian/Ubuntu 安装脚本会通过 apt-get 安装依赖。准备好 VPN 网关地址、VPN 账号和密码；需要 SSH 时，还需目标地址和 SSH 登录凭据。

在普通用户终端运行：

```bash
curl -fsSL https://raw.githubusercontent.com/imYangliu/openconnect-ssh/v0.1.0-alpha.2/install.sh -o och-install.sh
OCH_VERSION=v0.1.0-alpha.2 bash och-install.sh
och --help
```

安装脚本会下载匹配平台的发布包，并在需要时请求 sudo 授权。最后一条命令应显示 OCH 命令列表。发布文件见 [v0.1.0-alpha.2](https://github.com/imYangliu/openconnect-ssh/releases/tag/v0.1.0-alpha.2)。这是预发布版，因此示例显式指定版本，不依赖安装器默认的 `latest`。

其他 Linux 发行版需自行安装 OpenConnect、OpenSSH 客户端、iproute2、sudo，以及下载和解压工具。依赖就绪后运行：

```bash
OCH_VERSION=v0.1.0-alpha.2 bash och-install.sh --no-deps
```

### 从源码安装

需要 Git、Go 1.24+ 和 make。先准备上述运行依赖，再运行：

```bash
git clone https://github.com/imYangliu/openconnect-ssh.git
cd openconnect-ssh
make build
sudo make install
```

默认安装到 macOS 的 `/opt/homebrew` 或 Linux 的 `/usr/local`。如需安装到用户目录，在源码目录运行：

```bash
make install PREFIX="$HOME/.local"
export PATH="$HOME/.local/bin:$PATH"
```

将 PATH 设置加入你的 shell 启动文件，供后续终端使用。用户目录安装只免去安装文件时的管理员权限，建立或断开 VPN 仍需 sudo。

## 配置并完成首次 SSH 连接

1. 在交互式终端运行 `och setup`。不要使用 `sudo och setup`，配置应属于日常使用 SSH 的用户。
2. 按提示填写 VPN 网关，例如 `vpn.example.com`，再填写 VPN 用户。认证组由 VPN 管理员提供，不需要时留空。
3. 输入 VPN 密码。输入时终端不显示密码。已有密码时，回车保留。
4. 在 SSH 地址处填写 `server.internal.example`。SSH 用户在后续提示中单独填写。
5. 确认托管 SSH 别名，例如 `och-target`，再填写 SSH 用户和端口。默认端口是 `22`。
6. 保存后，记录向导输出的 `ssh <别名>` 命令。

检查本机配置：

```bash
och doctor
```

成功时输出 `配置、依赖检查通过`。该检查不登录 VPN，也不测试远端主机。

在同一终端授权并连接：

```bash
sudo -v
ssh och-target
```

将 `och-target` 替换为向导输出的别名。首次连接时，按你的 SSH 使用流程核对主机指纹，并使用 SSH 密钥或 SSH 密码登录。VPN 密码不代替 SSH 登录凭据。进入远端 shell 后，首次连接完成。

OCH 先尝试目标 TCP 端口。如果不可达，OCH 检查已有 VPN，必要时启动 OpenConnect，再等待目标端口恢复。SSH 会话结束后，VPN 继续运行。

## 复用已有 SSH 别名

假设你已有以下 SSH 配置：

```sshconfig
Host lab
  HostName server.internal.example
  User researcher
  Port 2222
  IdentityFile ~/.ssh/id_ed25519_lab
```

运行 `och setup`，在 SSH 地址处输入 `lab`。向导通过 `ssh -G lab` 提取主机、用户和端口；这个示例会建议新别名 `och-lab`。确认后，使用向导打印的新别名连接。

向导不复制 `IdentityFile`、`ProxyJump` 或端口转发等其他选项。如需专用密钥，可以在 `~/.ssh/config` 中保留 OCH 的 Include，并添加：

```sshconfig
Host och-lab
  IdentityFile ~/.ssh/id_ed25519_lab
```

不要把长期维护的自定义设置写入 `~/.ssh/och.config`，该文件会被向导覆盖。托管文件当前只保存一个 Host；再次配置其他目标会替换原托管 Host。

## 只连接 VPN

需要仅让指定网段走 VPN 时，先阅读[分流指南](split-tunneling.md)。OCH 当前的额外路由设置不保证默认路由保持不变。

首次运行 `och setup` 时，在 SSH 地址处输入 `-`。配置完成后运行：

```bash
sudo -v
och vpn connect
och vpn status
```

`status` 显示 OCH 管理的 OpenConnect 进程状态，不代表所有内网服务都可达。再访问实际需要的内网服务，确认连接可用。未配置 SSH 目标时，不使用 `och vpn verify`。

重新运行向导并输入 `-` 会跳过 SSH 配置步骤，但不会清除之前保存的 SSH 字段或托管文件。

## 断开 VPN

退出依赖该 VPN 的 SSH 会话后运行：

```bash
sudo -v
och vpn disconnect
och vpn status
```

`disconnect` 请求终止 OCH 管理的 VPN 进程。等待进程退出后，`status` 应显示未连接。关闭 VPN 会影响其他使用同一 VPN 的连接。

## 更新 OCH

更新到指定发布版时，下载对应版本的安装脚本，并显式指定版本。例如重新安装本文版本：

```bash
OCH_VERSION=v0.1.0-alpha.2 bash och-install.sh --update
```

安装其他版本时，同时替换脚本下载地址中的标签和 `OCH_VERSION`。默认目录下安装的 CLI 也支持：

```bash
och update
```

该命令从主分支下载安装脚本，默认查询 GitHub 的最新正式发布版。预发布版需通过 `OCH_VERSION` 显式指定，例如 `OCH_VERSION=v0.1.0-alpha.2 och update`。自定义安装位置时需再次传入原来的 `PREFIX`，以及使用过的 `BIN_DIR`、`LIBEXEC_DIR` 和 `CONFIG_DIR`。

源码安装可在更新源码后重新运行 `make build` 和安装命令。如果可执行文件位置发生变化，重新运行 `och setup`，更新 SSH 配置中的绝对路径。

## 排查连接问题

先运行 `och doctor`，再根据现象检查：

| 现象 | 操作 |
| --- | --- |
| 找不到 `och` | 检查安装前缀的 `bin` 是否在 PATH 中。默认是 `/opt/homebrew/bin` 或 `/usr/local/bin`。 |
| 缺少 OpenConnect、SSH 或 sudo | 安装缺少的依赖，再运行 `och doctor`。 |
| 提示 sudo 授权失败 | 在发起连接的同一终端运行 `sudo -v`，然后重试。也可以自行配置 `SUDO_ASKPASS`。 |
| VPN 启动失败 | 运行 `och vpn logs`，检查网关、账号、认证组及服务端返回的错误。 |
| VPN 已启动，但目标仍不可达 | 确认目标地址和端口，再检查路由、DNS、远端主机及 SSH 服务。 |
| SSH 提示 `Permission denied` | 检查 SSH 用户和密钥。已有别名中的专用密钥不会被向导自动复制。 |
| 提示 secret 文件权限不安全 | 运行 `chmod 600 ~/.config/och/secrets.env`。自定义路径时替换为实际文件。 |

已配置 SSH 目标时，可以单独测试其端口：

```bash
och vpn verify
```

成功时输出 `目标端口可达`。该命令不启动 VPN，也不验证 SSH 登录。

`och vpn logs` 显示最近 80 行日志，默认文件是 `~/.config/och/run/openconnect.log`。首次连接前日志可能尚未创建。向他人提供日志前，检查并遮盖账号、内部地址等信息。

使用 `[vpn].helper` 时，VPN 状态、凭据和日志由外部 helper 管理，`och vpn logs` 不提供其日志。路由和 DNS 的平台差异见[配置说明](configuration.md)。

## 运行隔离验证

在源码仓库根目录运行：

```bash
python3 .agents/skills/verify/scripts/verify.py
```

该命令构建 CLI，使用临时 HOME、虚构凭据和本地 TCP 服务验证数据转发，再运行 `make check`。它不连接真实 VPN，也不修改系统网络或个人 SSH 配置。终端会输出证据目录，目录中的 `report.json` 和命令日志在临时配置清理后保留。

已覆盖的功能与验证缺口见 [Feature Map](../.agents/skills/verify/features/README.md)。模拟 VPN 启动通过不代表真实 VPN 登录或系统分流已经验证。

## 查看和移除安装文件

默认文件位置如下：

| 文件 | 用途 |
| --- | --- |
| `<安装前缀>/bin/och` | 命令行程序 |
| `<安装前缀>/libexec/och/macos-vpnc-route-wrapper.sh` | macOS 路由和 DNS 脚本 |
| `/etc/och/ssh_config.example` | 发布版安装脚本安装的 SSH 示例 |
| `~/.config/och/config.toml` | VPN 和 SSH 配置 |
| `~/.config/och/secrets.env` | 明文 VPN 密码，权限为 `0600` |
| `~/.config/och/run/` | VPN 日志、PID 和锁文件 |
| `~/.ssh/och.config` | 向导生成的 SSH Host |

OCH 没有卸载子命令。卸载时按以下顺序操作：

1. 运行 `sudo -v` 和 `och vpn disconnect`，确认 VPN 进程已退出。
2. 从 `~/.ssh/config` 移除 `Include ~/.ssh/och.config`，以及你自行添加的 OCH 别名设置。保留其他 SSH 配置。
3. 删除 `~/.ssh/och.config`。
4. 删除实际安装前缀中的 `bin/och` 和 `libexec/och/macos-vpnc-route-wrapper.sh`。系统目录可能需要 sudo。
5. 如不再保留账号配置，删除 `~/.config/och/`。如使用自定义配置或密码路径，分别处理对应文件。
6. 如由发布版安装脚本安装，删除 `/etc/och/ssh_config.example`，或自定义 `CONFIG_DIR` 中的该文件。

OpenConnect 和 OpenSSH 可能被其他程序使用，无需随 OCH 一起卸载。
