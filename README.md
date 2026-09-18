> **广告**：直播/游戏加速线路定制 联系：[tg](https://t.me/rand_xx231jnfasj_bot)
> 

# gpp

基于[sing-box](https://github.com/SagerNet/sing-box)+[wails](https://github.com/wailsapp/wails)的加速器，使用golang编写，支持windows、linux、macos

- http分流
- gui客户端
- 基于tun代理
- 自定义规则
- 使用简单

[qq交流群936204503](http://qm.qq.com/cgi-bin/qm/qr?_wv=1027&k=syMCYJm6Isz_yAxUfrQetpNGioUdpdjO&authKey=lkUyXpKkdAzUwOZYq0m%2BH5Y%2FvAU3XegyxWTm5fM1%2BxOZDdBHJUF%2BODVeNg9MraDl&noverify=0&group_code=936204503) [TG交流群](https://t.me/+3cX2FOX_owA1ODM1)
# 截图

|                                                         |                                                       |
|---------------------------------------------------------|-------------------------------------------------------|
| ![界面截图](https://imgc.cc/2024/07/06/66888d266d829.png)   | ![英雄联盟](https://imgc.cc/2024/07/06/66888d3c49609.png) |
| ![战地2042](https://imgc.cc/2024/07/06/66888d4ea1807.png) | ![绝地求生](https://imgc.cc/2024/07/06/66888d51e610d.png) |


# 使用教程

## 服务的搭建

在优质线路服务器上运行安装脚本
快速安装服务端脚本（仅支持linux）
```bash
bash <(curl -sL https://raw.githubusercontent.com/danbai225/gpp/main/server/install.sh)
```
然后执行/usr/local/gpp/run.sh start启动服务端

根据提示安装完成后会输出导入链接

# 运行客户端

[从releases下载](https://github.com/danbai225/gpp/releases)下载对应系统的客户端以管理员身份运行

点击页面上的`Game`或`Http`字样弹出节点列表窗口，在下方粘贴服务端的链接完成节点导入。
在节点列表选择你的加速节点，如何开始加速。

## 终端版客户端（gpp-tui）

不方便用 GUI 的场景可以用终端版，功能对齐、日志更全、便于排查：

```bash
go build -tags with_quic -o gpp-tui.exe ./cmd/gpp-tui
# Windows 需要以管理员身份运行（TUN 需要）
```

- **GUI 与 TUI 可以同时打开，看到的是同一份状态**（见下方"架构"）
- 与 GUI 共用同一份 `config.json`：优先可执行文件同级目录，否则 `~/.gpp/config.json`
- 参数：`-config <路径>` 指定配置文件
- 日志：`~/.gpp/gpp-tui.log`（info 级）；开启 debug 后另有 `~/.gpp/debug.log`（trace）与 `~/.gpp/sing.json`（实际生效的 sing-box 配置）
- 常用命令：`s` 开始 / `t` 停止 / `r` 重启 / `p` 测速 / `a` 自动选最快 / `i <链接>` 导入 / `x <序号>` 删除 / `w` 实时监控 / `?` 帮助
- Ctrl+C 会先停止加速再退出，不会留下虚拟网卡残留

# 架构：一个核心 + 多个前端

gpp 的隧道与全部状态只由**一个核心进程**持有，GUI 与 TUI 都是它的前端：

```
GUI (wails)  ─┐
              ├─ 本机控制面 HTTP/SSE（127.0.0.1，token 鉴权）─→ 核心：配置 + 节点 + TUN 隧道
TUI (终端)   ─┘
```

- **谁先启动谁是核心**：先启动的进程创建隧道并监听本机控制面，把连接信息写到 `~/.gpp/control.json`（仅本机可读，含随机端口与 token）；后启动的一方自动作为前端连上去。
- **状态天然同步**：加速状态、选中节点、节点延迟、实时流量、一次性提示都由核心维护，两端 1 秒内看到变化；GUI 还通过事件推送即时提示，TUI 会把另一端的事件打印成 `[核心] …`。
- **不会出现两个隧道**：所有权由 `~/.gpp/control.lock` 保证；核心异常退出留下的陈旧锁会在确认进程已退出后被下一个实例接管（误判时会提示删除该文件）。
- **退出语义**：核心退出会停止加速；前端退出不影响隧道（仍由核心持有）。GUI 作为前端退出时会明确询问"仅退出窗口 / 停止加速并退出"。
- 控制面只监听回环地址并要求 token，防止本机其他程序随意启停隧道。

# 排查常见问题

| 现象 | 处理 |
| --- | --- |
| 提示创建虚拟网卡失败 | 必须以管理员身份运行（Windows 右键“以管理员身份运行”，Linux/macOS 用 `sudo`）；注意只有**核心**进程需要管理员权限 |
| 提示无法成为核心 / 另一个核心正在运行 | 可能已经有一个 gpp（GUI 或 TUI）在运行；确认都已退出后可删除 `~/.gpp/control.lock` 后重试 |
| 提示本地端口被占用（127.0.0.1:5123） | 同上的多实例情况，或别的程序占用了该端口 |
| 提示规则集下载失败 | 首次启动需要能访问 GitHub 下载 geosite/geoip；成功下载一次后会缓存到 `~/.gpp/cache.db`，之后断网也能启动 |
| 订阅更新失败 | 会自动回退到上次成功拉取的缓存 `sub_cache.json`，检查网络或订阅地址 |
| 配置写坏了 | 启动时会自动备份为 `config.json.bad-<时间>` 并重置，不会直接崩溃 |
| 想看每条连接走了哪个节点 | `config.json` 里把 `debug` 设为 `true`，生成 trace 日志与 `sing.json`（内含实际生效的路由规则与默认出站） |

# 现在的加速逻辑（重要）

当前默认是"**全机流量进隧道，大陆/内网直连，其余走节点**"，规则顺序（自上而下，先命中先生效）：

1. `sniff` 嗅探协议与域名 → 2. 劫持 DNS → 3. **节点自身地址直连** → 4. 阻断 UDP/443（挡 QUIC）
→ 5. `geosite-cn` / `geoip-cn` / 内网 / 内置 CIDR / 内置 Steam 域名后缀 → **直连**
→ 6. 用户自定义 `rules`
→ 7. 嗅探出的 http、TCP 80/443/8080/8443（**仅当 Game 与 Http 选了不同节点**）→ 网页节点
→ 8. 其余全部 → **`route.final` = 游戏节点**

要点：

- **纯 IP + UDP 的境外游戏服务器**正是靠第 8 条被加速的（它们匹配不到任何域名规则）。
- **CN 游戏服务器走直连**（`geoip-cn` 命中），属于预期行为，不等于"没加速"。
- 第 5 条内置的 Steam 域名后缀可能让部分 Steam 系游戏服务器直连；遇到"游戏没加速"时先把 `debug` 打开看命中情况。
- `route.final` 是**显式写死**的：sing-box 在它为空时会把"第一个出站"当默认出站，一旦调整出站顺序，全机默认走向会静默改变（已有单元测试守住这条不变量）。

## 节点命名约定

节点名以 `game`/`http` 开头的被视为专用线路：`game` 前缀的节点不会出现在 Http 列表里，`http` 前缀的也不会出现在 Game 列表里。

## mac修复损坏
安装后命令行执行
```bash
sudo xattr -r -d com.apple.quarantine /Applications/gpp.app
```

# 编译

## 编译服务端

使用`golang`编译 `cmd/gpp/main.go`获得服务端可执行文件。

## 编译GUI客户端

gui的客户端需要自建构建，需要安装`wails`、`npm`和`golang`，安装方法如下

- 安装`golang`，[下载地址](https://golang.org/dl/)
- 安装`npm` [下载地址](https://nodejs.org/en/download/)
- 安装`wails`，`go install github.com/wailsapp/wails/v2/cmd/wails@latest`

使用`wails`编译（`with_quic` tag 用于 hysteria2 协议支持）

```
wails build -m -trimpath -tags webkit2_41,with_quic
```

# config解释

## 服务端

配置存放为服务端二进制文件当前目录的`config.json`

- protocol 协议
- port 端口
- addr 绑定地址
- uuid 认证用途

```json
{
  "protocol": "vless",
  "port": 5123,
  "addr": "0.0.0.0",
  "uuid":"xxx-xx-xx-xx-xxx"
}
```

## 客户端

配置存放为客户端二进制文件当前目录的`config.json`或者用户目录下`<userhome>/.gpp/config.json`

- peer_list 节点列表
- proxy_dns 代理dns
- local_dns 直连dns
- sub_addr 订阅地址
- rules [代理规则](https://sing-box.sagernet.org/zh/configuration/route/rule)

```json
{
  "peer_list": [
    {
      "name": "直连",
      "protocol": "direct",
      "port": 0,
      "addr": "direct",
      "uuid": ""
    },
    {
      "name": "hk",
      "protocol": "vless",
      "port": 5123,
      "addr": "xxx.xx.xx.xx",
      "uuid": "xxx-xxx-xx-xxx-xxx"
    }
  ],
  "proxy_dns": "8.8.8.8",
  "local_dns": "223.5.5.5",
  "sub_addr": "https://sub.com",
  "rules": [
    {
      "process_name": "C://1.exe",
      "outbound": "direct"
    },
    {
      "domain": "ipv4.ip.sb",
      "outbound": "proxy"
    }
  ]
}
```
