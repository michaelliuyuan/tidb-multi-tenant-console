# 🚀 小白安装部署手册

> 本手册面向**第一次接触本项目**的同学，手把手教你从零把系统跑起来。
>
> 只需要跟着步骤一步步操作，不需要提前懂 Go / React / TiDB 内部原理。
>
> 预计耗时：**30～60 分钟**（取决于网络下载速度）

---

## 📋 你需要提前准备什么？

在开始之前，请确认你手上有以下东西：

| 序号 | 需要什么 | 说明 | 怎么检查？ |
|:---:|---|---|---|
| 1 | **一台 TiDB 数据库** | 版本 7.1 或更高。可以是本地测试集群、也可以是远程服务器上的 | 能用 MySQL 客户端连上、执行 `SELECT version()` 不报错 |
| 2 | **TiDB 的 PD 地址** | PD 是 TiDB 的调度组件，本项目需要通过它查看存储节点拓扑 | PD 默认端口 `2379`，浏览器访问 `http://你的PD地址:2379/pd/api/v1/stores` 能看到 JSON |
| 3 | **一台电脑**（Windows / Mac / Linux 都行） | 用来下载代码、编译、运行 | 能上网就行 |
| 4 | **TiDB 的账号密码** | 建议 `root` 账号（需要有建库建表权限） | 用 MySQL 客户端能登录 |

> 💡 **没有 TiDB？** 可以用 [TiUP](https://docs.pingcap.com/tidb/stable/quick-start-with-tidb) 在本地一键装一个测试集群：
> ```bash
> curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
> source ~/.bash_profile
> tiup playground v7.5.0 --db 1 --pd 1 --kv 3
> ```
> 等 5 分钟就能在本地跑起一个 TiDB（端口 4000、PD 端口 2379）。

---

## 🧩 第一部分：安装必备软件

就像做饭前要先买锅碗瓢盆一样，我们需要先在电脑上装好基础工具。

### 1.1 安装 Go 语言环境（后端用）

> Go 是后端的编程语言，相当于「发动机」。

**Windows：**
1. 打开 <https://go.dev/dl/>
2. 下载 **go1.21.x Windows-amd64.msi**（例如 `go1.21.13.windows-amd64.msi`）
3. 双击安装，一路「Next」，默认装到 `C:\Program Files\Go`
4. 安装完成后，打开 **命令提示符**（按 `Win+R`，输入 `cmd`，回车），输入：
   ```
   go version
   ```
   如果显示 `go version go1.21.x windows/amd64`，就说明安装成功了 ✅

**Mac / Linux：**
```bash
# Mac（用 Homebrew）
brew install go

# Linux
wget https://go.dev/dl/go1.21.13.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.21.13.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin  # 加到 ~/.bashrc 里永久生效
go version
```

---

### 1.2 安装 Node.js（前端用）

> Node.js 是前端的构建工具，相当于「装修工具」。

1. 打开 <https://nodejs.org/>
2. 下载 **LTS 版本**（带「LTS」标记的，比如 20.x.x）
3. Windows 双击 `.msi` 安装，一路 Next
4. 安装完成后，在命令提示符输入：
   ```
   node --version
   npm --version
   ```
   能看到版本号（如 `v20.18.0` 和 `10.8.2`）就说明成功了 ✅

---

### 1.3 安装 Git（下载代码用）

> Git 是代码版本管理工具，我们用它把代码从 GitHub 下载到本地。

1. 打开 <https://git-scm.com/downloads>
2. 下载对应系统的版本，Windows 双击安装，一路 Next
3. 在命令提示符输入：
   ```
   git --version
   ```
   显示 `git version 2.x.x` 即成功 ✅

> ⚠️ **Mac 用户**：如果系统自带的 git 版本太旧，建议用 `brew install git` 安装新版。

---

### ✅ 检查清单

继续下一步之前，请在命令提示符中依次运行以下命令，**全部不报错**才说明环境就绪：

```bash
go version      # 应显示 go1.21 以上
node --version  # 应显示 v18 以上
npm --version   # 应显示版本号
git --version   # 应显示 git version 2.x
```

---

## 📥 第二部分：下载项目代码

打开命令提示符（Windows）或终端（Mac/Linux），找一个你喜欢的目录（比如桌面），执行：

```bash
# 进入你想存放代码的目录（示例用 Desktop）
cd Desktop

# 从 GitHub 克隆代码（就是把整个项目下载到本地）
git clone https://github.com/michaelliuyuan/tidb-multi-tenant-console.git

# 进入项目目录
cd tidb-multi-tenant-console
```

下载完成后，你的目录结构应该是这样的：

```
tidb-multi-tenant-console/        ← 你现在在这里
├── README.md                     ← 项目说明文档
├── DEPLOY.md                     ← 就是本文件 👈
├── backend/                      ← 后端代码（Go）
│   ├── main.go                   ← 后端入口
│   ├── config.example.yaml       ← 配置文件模板
│   ├── go.mod / go.sum           ← Go 依赖描述
│   └── migrations/               ← 数据库建表脚本
├── frontend/                     ← 前端代码（React）
│   ├── package.json
│   ├── vite.config.ts
│   └── src/                      ← 前端源码
├── docs/                         ← 设计文档
└── prototype/                    ← 可交互原型
```

---

## ⚙️ 第三部分：配置后端连接信息

这一步告诉后端「你的 TiDB 数据库在哪、账号密码是什么」。

### 3.1 复制配置模板

```bash
cd backend
copy config.example.yaml config.yaml    # Windows
# cp config.example.yaml config.yaml   # Mac/Linux
```

### 3.2 编辑配置文件

用任何文本编辑器（记事本、VS Code、Notepad++ 都行）打开 `backend/config.yaml`，把 `<...>` 占位符换成你的真实信息：

```yaml
server:
  addr: ":8088"                          # ← 后端监听端口，一般不用改

# 元数据库：存这个管控台自己用的数据（租户信息、审计日志等）
metadata:
  host: "127.0.0.1"                      # ← 换成你的 TiDB 服务器 IP
  port: 4000                             # ← TiDB 端口（默认 4000）
  user: "root"                           # ← TiDB 用户名
  password: "你的真实密码"                 # ← ⚠️ 换成你的 TiDB 密码
  db: ""                                 # ← 留空！程序会自动建库

# 要管控的 TiDB 集群（可以写多个）
clusters:
  - name: "my-cluster"                   # ← 给集群起个名字，随意
    tidb_host: "127.0.0.1"              # ← TiDB 服务器 IP
    tidb_port: 4000                     # ← TiDB 端口
    tidb_user: "root"                   # ← TiDB 用户名
    tidb_password: "你的真实密码"          # ← ⚠️ 换成你的 TiDB 密码
    pd_endpoint: "http://127.0.0.1:2379" # ← PD 地址
    prometheus_url: "http://127.0.0.1:9090" # ← Prometheus 地址（没有就删掉这行）

auth:
  admin_token: "随便写一串你喜欢的字符"     # ← 管理令牌，自己定一个
```

> 💡 **不知道某项填什么？**
> - `metadata` 和 `clusters` 的 TiDB 地址通常是一样的（指向同一个集群）
> - `pd_endpoint` 就是 PD 的地址，默认 `http://IP:2379`
> - `prometheus_url` 如果你没装 Prometheus 监控，直接删掉这一行，不影响主功能
> - `db` 留空就好，程序首次启动会自动创建一个叫 `mt_console` 的库

> ⚠️ **安全提醒**：`config.yaml` 里有你的数据库密码，**绝对不要**把它上传到 GitHub！项目已经用 `.gitignore` 自动排除了它。

---

## 🏗️ 第四部分：启动后端

### 4.1 下载 Go 依赖

```bash
cd backend    # 确保你在 backend 目录
go mod tidy   # 自动下载所有依赖包（第一次会比较慢，耐心等 2～5 分钟）
```

> 💡 国内网络慢的话，先设置 Go 代理加速：
> ```bash
> go env -w GOPROXY=https://goproxy.cn,direct
> go mod tidy
> ```

### 4.2 启动后端

```bash
go run .
```

看到类似这样的输出就说明成功了：

```
TiDB 多租户管控台 listening on :8088
```

> 🎉 此时后端已经在 `8088` 端口运行了。它会自动：
> 1. 连接你的 TiDB 数据库
> 2. 创建 `mt_console` 库和 6 张元数据表
> 3. 把配置文件里的集群信息注册进来
> 4. 开放 API 接口

**先别关这个窗口**，后端需要一直运行。接下来开一个**新的命令提示符窗口**启动前端。

---

## 🖥️ 第五部分：启动前端

### 5.1 打开新窗口，进入前端目录

新开一个命令提示符（保持后端窗口不关），执行：

```bash
cd Desktop/tidb-multi-tenant-console/frontend
```

### 5.2 安装前端依赖

```bash
npm install    # 下载前端依赖包（第一次约 1～3 分钟）
```

> 💡 国内网络慢的话，先切换 npm 镜像：
> ```bash
> npm config set registry https://registry.npmmirror.com
> npm install
> ```

### 5.3 启动前端开发服务器

```bash
npm run dev
```

看到类似这样的输出：

```
  VITE v5.x.x  ready in 800 ms

  ➜  Local:   http://localhost:5180/
```

### 5.4 打开浏览器

用浏览器（推荐 Chrome）打开：

> 🌐 **http://localhost:5180**

如果看到管控台界面，恭喜你，部署成功了！🎉🎉🎉

---

## 🎯 第六部分：验证是否正常工作

### 6.1 检查集群连接

1. 页面顶部应该能看到你在 `config.yaml` 里配的集群名（比如 `my-cluster`）
2. 点击集群名，进入 **拓扑** 页面
3. 如果能看到 TiKV 节点列表（store ID、状态、容量），说明 PD 连接正常 ✅

### 6.2 尝试创建一个租户

1. 点击左侧菜单 **租户管理**
2. 点击 **创建租户**
3. 填写租户名（比如 `test-tenant`），选择隔离级别（可以先选 `LOGICAL` 最简单）
4. 点击 **确定**
5. 如果创建成功、列表里出现了新租户，说明后端编排功能完全正常 ✅

### 6.3 查看监控（可选）

如果你配了 Prometheus，点击 **监控** 页面，应该能看到 RU 趋势图、QPS 等图表。

---

## 🚀 第七部分：生产环境部署（正式上线用）

> 开发模式下前后端是分开运行的（前端 5180、后端 8088）。
> 正式上线时，我们希望只跑一个程序、用浏览器直接访问，不需要记端口号。

### 方式一：在本机构建（推荐，最简单）

适合你的电脑能上网、装了 Go 和 Node.js 的情况。

#### 第一步：构建前端

```bash
cd tidb-multi-tenant-console/frontend
npm install        # 如果已经装过可以跳过
npm run build      # 构建生产版本，产物输出到 frontend/dist/
```

构建完成后，`frontend/dist/` 目录下会生成 `index.html` 和 `assets/` 文件夹。

#### 第二步：编译后端

```bash
cd ../backend
go build -o console-server .     # Windows 会生成 console-server.exe
```

#### 第三步：运行

```bash
./console-server     # Linux/Mac
console-server.exe   # Windows
```

后端会自动托管前端页面。打开浏览器访问 **http://localhost:8088** 就是完整的应用了！

---

### 方式二：在 Windows 编译，部署到 Linux 服务器

适合开发机是 Windows、服务器是 Linux 的情况。

#### 第一步：交叉编译后端（在 Windows 上操作）

```powershell
cd backend

# 设置环境变量，让 Go 编译 Linux 版本
$env:CGO_ENABLED=0
$env:GOOS="linux"
$env:GOARCH="amd64"

go build -o console-server .
```

这会生成一个 Linux 版的二进制文件 `console-server`。

#### 第二步：构建前端（还是在 Windows 上）

```powershell
cd ../frontend
npm install
npm run build
```

#### 第三步：上传到服务器

用 `scp` 或 WinSCP / Xftp 等工具，把以下文件传到服务器 `/opt/tidb-console/`：

```
需要上传的文件：
├── backend/console-server              ← 后端程序
├── backend/migrations/                 ← 数据库建表脚本（整个文件夹）
│   └── 0001_init.sql
└── frontend/dist/                      ← 前端构建产物（整个文件夹）
```

服务器上的最终目录结构：

```
/opt/tidb-console/
├── console-server          ← 后端程序
├── config.yaml             ← 配置文件（在服务器上新建，见下一步）
├── migrations/
│   └── 0001_init.sql       ← 建表脚本
└── frontend/
    └── dist/               ← 前端页面
        ├── index.html
        └── assets/
```

#### 第四步：在服务器上创建配置文件

SSH 登录服务器：

```bash
ssh 用户名@服务器IP
cd /opt/tidb-console
```

创建配置文件（参考 [第三部分](#️-第三部分配置后端连接信息) 的模板）：

```bash
cat > config.yaml << 'EOF'
server:
  addr: ":8088"

metadata:
  host: "127.0.0.1"
  port: 4000
  user: "root"
  password: "你的TiDB密码"
  db: ""

clusters:
  - name: "prod-cluster"
    tidb_host: "127.0.0.1"
    tidb_port: 4000
    tidb_user: "root"
    tidb_password: "你的TiDB密码"
    pd_endpoint: "http://127.0.0.1:2379"

auth:
  admin_token: "你的管理令牌"
EOF
```

给配置文件设置权限（只有主人能读写，保护密码安全）：

```bash
chmod 600 config.yaml
```

#### 第五步：启动！

```bash
chmod +x console-server

# 前台运行（先测试一下能不能跑）
./console-server

# 确认没问题后，后台运行（关掉 SSH 也不退）
nohup ./console-server > console.log 2>&1 &
```

打开浏览器访问 **http://服务器IP:8088** 🎉

#### 第六步：设为开机自启（可选但推荐）

创建 systemd 服务文件：

```bash
sudo cat > /etc/systemd/system/tidb-console.service << 'EOF'
[Unit]
Description=TiDB Multi-Tenant Console
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/tidb-console
ExecStart=/opt/tidb-console/console-server
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable tidb-console
sudo systemctl start tidb-console
sudo systemctl status tidb-console    # 查看运行状态
```

以后服务器重启会自动启动，可以用以下命令管理：

```bash
sudo systemctl start tidb-console     # 启动
sudo systemctl stop tidb-console      # 停止
sudo systemctl restart tidb-console   # 重启
sudo systemctl status tidb-console    # 查看状态
journalctl -u tidb-console -f         # 查看实时日志
```

---

## 🔧 第八部分：日常运维操作

### 更新到新版本

```bash
cd /opt/tidb-console

# 1. 停服务
sudo systemctl stop tidb-console

# 2. 备份旧版本
mv console-server console-server.bak

# 3. 放入新版本二进制
mv console-server.new console-server
chmod +x console-server

# 4. 启动
sudo systemctl start tidb-console
```

### 查看日志

```bash
# systemd 方式
journalctl -u tidb-console -f

# nohup 方式
tail -f /opt/tidb-console/console.log
```

### 修改配置后重启

```bash
vi /opt/tidb-console/config.yaml    # 编辑
sudo systemctl restart tidb-console  # 重启生效
```

---

## ❓ 常见问题排查

### Q1: `go mod tidy` 卡住不动 / 报网络错误

**原因**：默认 Go 仓库服务器在国外，国内访问慢。

**解决**：设置国内代理：
```bash
go env -w GOPROXY=https://goproxy.cn,direct
go mod tidy
```

---

### Q2: `npm install` 很慢或报错

**原因**：同上，npm 默认源在国外。

**解决**：切换淘宝镜像：
```bash
npm config set registry https://registry.npmmirror.com
npm install
```

---

### Q3: 后端启动报 `connection refused` 或数据库连接失败

**原因**：后端连不上 TiDB 数据库。

**排查步骤**：
1. 确认 TiDB 在运行：用 MySQL 客户端试连一下
   ```bash
   mysql -h 127.0.0.1 -P 4000 -u root -p
   ```
2. 检查 `config.yaml` 里的 `host`、`port`、`user`、`password` 是否正确
3. 检查防火墙是否放行了 4000 端口
4. 如果 TiDB 在远程服务器，确认 `host` 填的是服务器 IP 而不是 `127.0.0.1`

---

### Q4: 前端页面打开是白屏

**排查步骤**：
1. 按 `F12` 打开浏览器开发者工具，看 Console 标签有没有红色报错
2. 确认后端在运行（`http://localhost:8088/api/v1/clusters` 能返回 JSON）
3. 确认前端代理配置正确：`frontend/vite.config.ts` 里的 proxy 指向 `8088`
4. 清浏览器缓存后重试（`Ctrl + Shift + R`）

---

### Q5: 拓扑页面看不到 TiKV 节点

**原因**：后端连不上 PD。

**排查步骤**：
1. 浏览器直接访问 `http://你的PD地址:2379/pd/api/v1/stores`
2. 如果打不开，说明 PD 没启动或端口不通
3. 检查 `config.yaml` 的 `pd_endpoint` 地址是否正确

---

### Q6: 监控页面没有数据

**原因**：没配 Prometheus，或 Prometheus 没有采集 TiDB 指标。

**解决**：
- 如果没有 Prometheus，删掉 `config.yaml` 中的 `prometheus_url` 行，监控功能会优雅降级（不报错，只是图表为空）
- 如果有 Prometheus，确认它正在采集 TiDB / TiKV 的指标（参考 [TiDB 监控部署文档](https://docs.pingcap.com/tidb/stable/tidb-monitoring-framework)）

---

### Q7: 创建租户失败

**原因**：可能是 TiDB 账号权限不够，或者 TiDB 版本太低。

**排查步骤**：
1. 确认 TiDB 版本 ≥ 7.1：`SELECT version();` 或 `SELECT tidb_version();`
2. 确认账号有足够权限（建议用 `root`）
3. 查看后端日志中的具体报错信息

---

### Q8: 生产部署后访问 8088 端口打不开

**原因**：服务器防火墙没放行端口。

**解决**：
```bash
# Linux 开放端口
sudo firewall-cmd --add-port=8088/tcp --permanent
sudo firewall-cmd --reload

# 或者用 iptables
sudo iptables -A INPUT -p tcp --dport 8088 -j ACCEPT
```

---

### Q9: 端口 8088 被占用

```bash
# 查看谁占了 8088
# Linux:
lsof -i :8088
# Windows:
netstat -ano | findstr :8088

# 解决方案：
# 方案一：杀掉占用端口的进程
# 方案二：修改 config.yaml 的 server.addr 为其他端口，如 ":9099"
```

---

## 📞 遇到问题怎么办？

1. **先看本手册的「常见问题排查」**——大部分问题都有答案
2. **查看后端日志**——错误信息通常就在那里
3. **检查配置文件**——90% 的问题都是配置填错了
4. **在 GitHub 提 Issue**：<https://github.com/michaelliuyuan/tidb-multi-tenant-console/issues>

---

## 📎 附录：快速命令速查表

| 操作 | 命令 |
|---|---|
| 克隆代码 | `git clone https://github.com/michaelliuyuan/tidb-multi-tenant-console.git` |
| 安装 Go 依赖 | `cd backend && go mod tidy` |
| 启动后端（开发） | `cd backend && go run .` |
| 安装前端依赖 | `cd frontend && npm install` |
| 启动前端（开发） | `cd frontend && npm run dev` |
| 构建前端 | `cd frontend && npm run build` |
| 编译后端 | `cd backend && go build -o console-server .` |
| 交叉编译 Linux | `$env:CGO_ENABLED=0; $env:GOOS='linux'; $env:GOARCH='amd64'; go build -o console-server .` |
| 后台运行（Linux） | `nohup ./console-server > console.log 2>&1 &` |
| 设为系统服务 | `sudo systemctl enable tidb-console` |
| 查看服务状态 | `sudo systemctl status tidb-console` |
| 查看日志 | `journalctl -u tidb-console -f` |
| 停止服务 | `sudo systemctl stop tidb-console` |
| 重启服务 | `sudo systemctl restart tidb-console` |

---

## 🗺️ 部署流程总览图

```
准备 TiDB 集群
      │
      ▼
安装 Go + Node.js + Git
      │
      ▼
git clone 下载代码
      │
      ▼
编辑 config.yaml（填 TiDB 连接信息）
      │
      ▼
┌─────────────────┐
│  开发模式        │
│  后端: go run .  │
│  前端: npm run dev│
│  访问 :5180      │
└────────┬────────┘
         │
         │ 需要上线？
         ▼
┌──────────────────────┐
│  生产模式              │
│  前端: npm run build   │
│  后端: go build        │
│  运行: ./console-server│
│  访问 :8088            │
└────────┬─────────────┘
         │
         │ 部署到服务器？
         ▼
┌──────────────────────────┐
│  服务器部署                │
│  交叉编译 → 上传 → systemd  │
│  访问 http://服务器:8088    │
└──────────────────────────┘
```

---

> 📖 更详细的技术文档请看 [README.md](README.md) 和 [docs/p0-technical-design.md](docs/p0-technical-design.md)
