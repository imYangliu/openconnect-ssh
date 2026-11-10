# Doctor 诊断

`och doctor` 输出分阶段检查结果，默认文本，可使用 `--json`。

## Sub-features

- `doctor-passive` 验证本地配置和后台快照，不初始化 VPN。
- `doctor-route` 通过运行中后台的同一 Router.Resolve 检查规则、DNS 方式和实际目标地址。
- `doctor-vpn-target` 分别报告 VPN 初始化、认证要求与目标 TCP 结果。
- `doctor-native` 复用原生 SSH 认证和转发探测，检查本地或远端临时监听及正确一侧的目标。
- `doctor-output` 支持版本化 JSON、配置错误报告、非零失败退出码和超时取消。

## How to get to it (user POV)

运行 `och doctor` 读取状态。使用 `--via NAME HOST PORT`、`--policy NAME HOST PORT`、`--ssh NAME [TARGET]` 或 `--forward NAME` 主动检查。`--json --timeout 10s` 可控制输出和网络检查预算。

## Driving it with isolated tests

`go test ./internal/routing ./internal/daemon ./internal/forward` 包含路由决策与拨号一致性、DNS 失败、登录要求与目标拒绝区分、前后台配置不一致、旧诊断协议、超时和真实 SSH 正反向临时监听探测。`TestCompiledCLIDoctorReportsActualChecks` 驱动编译后的 CLI，检查文本、JSON、无效配置、缺失后台、纯直连、密钥拒绝和占用端口。全部使用临时 HOME、回环 SSH/TCP/Unix socket 与注入 VPN 后端；不使用系统 ssh、真实凭据或个人配置。

## Gotchas

后台 ready 不是目标可达证明。端口检查不是预留，运行中的转发本身也会使端口检查失败。远端监听拒绝不能区分占用与权限。仅 TCP 建连，不执行应用协议或远端 shell。隔离测试不证明真实校园 VPN、所有认证流程或外部 SSH 服务配置均可用。
