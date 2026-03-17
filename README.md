# ecnu-ssh

轻量、可配置的 SSH wrapper，用于在 AnyConnect / OpenConnect 网络前自动处理 VPN 可达性、分流和远程端口映射。

Lightweight, configurable SSH tooling for AnyConnect-compatible and OpenConnect-based networks, with automatic VPN recovery, split tunneling, and optional reverse port forwarding.

这个仓库最初来自一套面向 ECNU 场景的自用脚本，但已经移除了学校域名、认证组、个人账号、私有主机和端口等默认值。现在它的定位是一个通用模板：只要你的环境兼容 Cisco AnyConnect 网关，且本地使用 `openconnect`，就可以按需改成自己的版本。

Originally built for a campus workflow, this repository has been sanitized for public use. It no longer ships with school-specific domains, auth groups, usernames, hosts, or ports, and is intended to be adapted to any environment that uses an AnyConnect-compatible gateway with the `openconnect` client.

## Highlights

- Auto-check VPN reachability before running `ssh`
- Recover connectivity by restarting a systemd service or calling the VPN script directly
- Keep split tunneling intact with `vpn-slice` or `uvx`
- Optionally run a systemd timer that only restarts VPN after repeated failed checks
- Optionally expose a local proxy or service to the remote machine with `ssh -R`
- Store all environment-specific values outside the repository

## 仓库结构

```text
bin/
  ecnu-ssh
  connect-campus-server.sh
  ecnu-openconnect-keepalive.sh
examples/
  ecnu-connect-campus-server.env.example
  ecnu-ssh.env.example
  ssh_config.example
systemd/
  ecnu-openconnect.service
  ecnu-openconnect-keepalive.service
  ecnu-openconnect-keepalive.timer
install.sh
```

## 依赖

建议运行环境：

- Linux
- `bash`
- `ssh`
- `sudo`
- `ip`（通常来自 `iproute2`）
- `openconnect`
- `vpn-slice` 或 `uvx`
- 可选：`systemd`

## 快速开始

### 1. 准备 VPN 配置

复制示例配置：

```bash
mkdir -p ~/.config
cp examples/ecnu-connect-campus-server.env.example ~/.config/ecnu-connect-campus-server.env
```

然后编辑 `~/.config/ecnu-connect-campus-server.env`，至少填好这些字段：

- `VPN_USER`
- `VPN_ROUTES`
- `TARGET_HOST`
- `TARGET_PORT`
- `TARGET_SSH_USER`

`VPN_ROUTES` 必须替换成你实际需要通过 VPN 访问的网段。

### 2. 准备 SSH Host 别名

如果你希望直接执行 `ecnu-ssh` 而不是每次都传主机名，可以把示例片段合并到 `~/.ssh/config`：

```bash
cat examples/ssh_config.example >> ~/.ssh/config
```

然后按你的实际主机信息修改 `HostName`、`User` 和 `Port`。

### 3. 可选配置 `ecnu-ssh`

如果你希望 `ecnu-ssh` 在未传目标主机时自动使用一个默认别名，可以复制：

```bash
cp examples/ecnu-ssh.env.example ~/.config/ecnu-ssh.env
```

然后把 `DEFAULT_HOST` 改成你在 `~/.ssh/config` 里定义的 host。

### 4. 安装脚本

```bash
sudo ./install.sh
```

只安装脚本时，上面的命令就够了。

如果你还想一并安装 systemd 单元：

```bash
sudo INSTALL_SYSTEMD=1 ./install.sh
sudo systemctl daemon-reload
```

如果你还希望启用周期 keepalive timer，再执行：

```bash
sudo systemctl enable --now ecnu-openconnect-keepalive.timer
```

## 使用方式

### 直接控制 VPN

```bash
connect-campus-server.sh connect
connect-campus-server.sh status
connect-campus-server.sh verify
connect-campus-server.sh disconnect
```

### 包装 SSH

```bash
ecnu-ssh ecnu-target
ecnu-ssh -L 8080:127.0.0.1:8080 ecnu-target
ecnu-ssh --proxy ecnu-target
```

如果你已经在 `~/.config/ecnu-ssh.env` 里设置了 `DEFAULT_HOST`，也可以直接运行：

```bash
ecnu-ssh
ecnu-ssh --proxy
```

### `--proxy` 模式

`ecnu-ssh --proxy` 会向远端追加一个反向端口映射：

```text
远端 PROXY_REMOTE_PORT -> 本地 PROXY_LOCAL_HOST:PROXY_LOCAL_PORT
```

默认是：

```text
远端 7890 -> 本地 127.0.0.1:7890
```

适合把本机代理服务临时暴露给远端会话使用。

## 配置说明

### `connect-campus-server.sh`

常用变量：

- `VPN_HOST`: VPN 网关地址，必须配置
- `VPN_USER`: VPN 用户名，必须配置
- `VPN_AUTHGROUP`: 可选认证组；部分 AnyConnect 网关需要
- `VPN_ROUTES`: 需要走 VPN 的 CIDR 列表，必须配置
- `TARGET_HOST`: 目标主机；`verify` 和 `ssh` 需要
- `TARGET_PORT`: 目标端口，默认 `22`
- `TARGET_SSH_USER`: SSH 用户，默认当前系统用户
- `VPN_PASSWORD`: 可选；未设置时会静默提示输入
- `CONFIG_FILE`: 配置文件路径，默认 `~/.config/ecnu-connect-campus-server.env`

### `ecnu-ssh`

常用变量：

- `WRAPPER_CONFIG_FILE`: 配置文件路径，默认 `~/.config/ecnu-ssh.env`
- `CONNECT_SCRIPT`: VPN 脚本路径，默认 `/usr/local/bin/connect-campus-server.sh`
- `VPN_SERVICE`: systemd 服务名，默认 `ecnu-openconnect.service`
- `DEFAULT_HOST`: 默认 SSH host；未设置时必须显式传目标
- `PROXY_LOCAL_HOST`: 反向映射到本地的地址，默认 `127.0.0.1`
- `PROXY_LOCAL_PORT`: 本地端口，默认 `7890`
- `PROXY_REMOTE_PORT`: 远端端口，默认 `7890`

## systemd 示例

仓库内提供了这些示例单元文件：

- `systemd/ecnu-openconnect.service`
- `systemd/ecnu-openconnect-keepalive.service`
- `systemd/ecnu-openconnect-keepalive.timer`

默认假设配置文件位于：

```text
/etc/ecnu-ssh/connect-campus-server.env
```

你可以先复制示例：

```bash
sudo mkdir -p /etc/ecnu-ssh
sudo cp examples/ecnu-connect-campus-server.env.example /etc/ecnu-ssh/connect-campus-server.env
sudo chmod 600 /etc/ecnu-ssh/connect-campus-server.env
```

### keepalive timer 行为

`ecnu-openconnect-keepalive.timer` 默认每约 2 分钟触发一次巡检。

对应的 `ecnu-openconnect-keepalive.service` 不会直接拿 `connect-campus-server.sh verify` 作为判定，而是通过 `ecnu-openconnect-keepalive.sh` 用 `ping` 轻探测目标主机，并记录连续失败次数：

- `FAIL_THRESHOLD=2` 时，第一次失败只记数
- 连续第二次失败才执行 `systemctl restart ecnu-openconnect.service`
- 一旦 `ping` 恢复成功，会清空失败计数
- 默认探测目标来自 `CONFIG_FILE` 里的 `TARGET_HOST`；如果需要单独指定，可额外设置 `PROBE_HOST`

默认参数写在 unit 文件里：

- `FAIL_THRESHOLD=2`
- `STATE_DIR=/run/ecnu-openconnect-keepalive`
- `PING_COUNT=1`
- `PING_TIMEOUT=1`

如果你想调阈值，建议通过 systemd drop-in 覆盖环境变量，而不是直接修改仓库文件。

## 安全提示

- 不要把真实的 `VPN_USER`、`VPN_PASSWORD`、主机地址、端口提交到仓库。
- 如果要把 `VPN_PASSWORD` 写入配置文件，至少确保文件权限为 `600`。
- `install.sh` 只会安装示例配置，不会覆盖你现有的真实配置文件。

## 开发与校验

基础静态检查：

```bash
bash -n bin/ecnu-ssh
bash -n bin/connect-campus-server.sh
bash -n bin/ecnu-openconnect-keepalive.sh
```

如果系统装了 `shellcheck`，建议再跑一遍：

```bash
shellcheck bin/ecnu-ssh bin/connect-campus-server.sh bin/ecnu-openconnect-keepalive.sh install.sh
```

## License

MIT，见 `LICENSE`。
