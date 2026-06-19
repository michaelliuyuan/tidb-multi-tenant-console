# TiDB 多租户可视化管控平台

> 把 TiDB 的 **物理隔离（Placement Rule in SQL + TiKV 标签）** 与 **逻辑隔离（Resource Control / RU 资源组）**，封装成以「租户」为一等公民的可视化管控台，替代繁琐的手工运维。

一个 Go 后端（Gin）+ React 前端（Ant Design）的多租户 TiDB 资源池管控平台。面向 DBA / 平台运维，提供租户生命周期编排、TiKV 拓扑可视化、放置策略 dry-run 预估、Prometheus 监控仪表盘、用户与权限管理等能力。

> 📘 **新手入门？** 请先阅读 [**小白安装部署手册 (DEPLOY.md)**](DEPLOY.md) —— 手把手从零开始，含环境安装、配置、启动、生产部署和常见问题排查。

---

## 目录

- [核心特性](#核心特性)
- [系统架构](#系统架构)
- [技术栈](#技术栈)
- [目录结构](#目录结构)
- [环境要求](#环境要求)
- [快速开始（开发模式）](#快速开始开发模式)
- [生产部署](#生产部署)
- [配置说明](#配置说明)
- [API 参考](#api-参考)
- [数据模型](#数据模型)
- [设计原则](#设计原则)
- [路线图](#路线图)
- [安全说明](#安全说明)

---

## 核心特性

### 租户管理
- **三种隔离级别**：`PHYSICAL`（Placement Rule 物理隔离）/ `LOGICAL`（Resource Control RU 限制）/ `HYBRID`（物理 + 逻辑混合）
- **原子化编排**：租户创建为 6 步原子事务（建库 → placement policy → resource group → 绑定 → 建用户 → 授权），任一步失败自动补偿回滚
- **租户生命周期**：挂起（RU→1 资源降级）/ 恢复 / 删除（幂等清理）
- **租户详情视图**：关联数据库（大小/表数）+ 承载 TiKV 实例（按租户标签过滤）

### 集群管理
- **添加集群**：页面表单录入集群连接信息，提交前自动做 TiDB 连通性探测（`SELECT version()` 提取真实版本）+ PD 可达性检查；支持「跳过探测」直接存档
- **多集群支持**：顶部全局集群切换器，所有页面共享；集群配置持久化到元数据库，重启不丢
- **集群健康探测**：TiDB / PD / Prometheus 三端可达性 + 版本能力探测（placement / resource control 可用性）

### TiKV 拓扑与调度
- **拓扑可视化**：PD stores 实时列表，按 zone / region 聚合，展示容量 / 可用空间 / Region 数
- **Store 标签管理**：通过 PD HTTP API 给 TiKV store 打标签（`label_key=label_value`），驱动 Placement Rule 调度
- **Store 资源监控**：单 store 的内存 / CPU 配额与使用、读写带宽、block cache

### 放置策略（Placement Rule in SQL）
- **策略 CRUD**：约束式（`CONSTRAINTS` / `LEADER_CONSTRAINTS` / `FOLLOWER_CONSTRAINTS`）/ 区域式（`PRIMARY_REGION` / `REGIONS`）/ 生存偏好三种语法
- **dry-run 预估**：提交前预估受影响 Region 数、数据搬迁量、目标节点池容量是否充足、预计调度时长
- **绑定管理**：库级 / 表级 placement policy 绑定与解绑

### 资源管控（Resource Control）
- **资源组 CRUD**：RU_PER_SEC / PRIORITY / BURSTABLE 配置，实时 `ALTER RESOURCE GROUP`
- **租户 RU 调整**：在线调整租户的资源组配额

### 监控仪表盘
- **RU 趋势**：基于 Prometheus PromQL，租户级 RU 消耗时序（recharts 可视化）
- **QPS / P99 延迟 / 存储用量**：集群与租户级汇总指标

### 用户与权限
- **用户管理**：建用户 / 改密 / 绑定资源组 / 删除
- **权限管理**：库表级 GRANT / REVOKE，支持通配符

---

## 系统架构

```
┌─────────────────────────────────────────────────────────┐
│                    浏览器 (React SPA)                     │
│  拓扑 │ 租户管理 │ 放置策略 │ 资源管控 │ 监控 │ 用户权限   │
└────────────────────────┬────────────────────────────────┘
                         │ HTTP /api/v1
┌────────────────────────▼────────────────────────────────┐
│              Backend (Go + Gin, :8088)                  │
│  ┌──────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ REST API │  │ Orchestrator │  │  Static (SPA)    │   │
│  │ (handlers)│  │ (6步原子编排) │  │ frontend/dist    │   │
│  └────┬─────┘  └──────┬───────┘  └──────────────────┘   │
│       │               │                                   │
│  ┌────▼───────────────▼────┐  ┌────────────────────┐     │
│  │  store: TiDB SQL client │  │ store: PD HTTP API │     │
│  │  (元数据 + 目标集群 DDL) │  │ (stores / labels)  │     │
│  └─────────────────────────┘  └────────────────────┘     │
└─────────────────────────────────────────────────────────┘
          │                              │              │
   ┌──────▼──────┐              ┌────────▼───┐   ┌──────▼──────┐
   │  TiDB 集群   │              │    PD       │   │ Prometheus  │
   │ (被管控)     │              │ (调度/拓扑) │   │  (指标)     │
   │              │              │             │   │             │
   │ mt_console.* │              │             │   │             │
   │ (元数据)     │              │             │   │             │
   └──────────────┘              └─────────────┘   └─────────────┘
```

**关键设计**：后端同时承担「元数据存储」（`mt_console.*` schema 存租户/集群/审计）与「目标集群操作」（通过 TiDB SQL 执行 DDL、通过 PD HTTP API 操作拓扑）。元数据库可以指向被管控集群本身，也可以是独立的元数据库。

---

## 技术栈

| 层 | 技术 | 版本 |
|---|---|---|
| 后端 | Go + Gin | Go 1.21+ / Gin 1.9 |
| 前端 | React + TypeScript + Ant Design 5 | React 18 |
| 构建 | Vite 5 | tsc + vite |
| 数据库 | TiDB（MySQL 协议兼容） | v7.1+ |
| 调度 | PD HTTP API | /pd/api/v1 |
| 监控 | Prometheus | PromQL |
| 图表 | recharts | — |

---

## 目录结构

```
tidb-multi-tenant-console/
├── README.md                         # 本文件
├── .gitignore
├── docs/
│   └── p0-technical-design.md        # 详细技术设计（元数据 schema / REST API / SQL 模板 / PD API / 编排回滚 / 权限矩阵）
├── prototype/
│   └── tenant-wizard.html            # 可交互原型（浏览器直接打开）
├── backend/                          # Go API Server
│   ├── main.go                       # 入口：加载配置 → 迁移 → 预注册集群 → 启动 HTTP + 静态服务
│   ├── config.example.yaml           # 配置模板（复制为 config.yaml 使用）
│   ├── go.mod / go.sum
│   ├── migrations/
│   │   └── 0001_init.sql             # mt_console 元数据 schema（6 张表）
│   └── internal/
│       ├── model/                    # 领域模型（Cluster / Tenant / 请求结构体）
│       ├── store/                    # TiDB SQL + PD HTTP API + Prometheus 客户端
│       ├── orchestrator/             # 租户创建 6 步原子编排 + 回滚
│       └── api/                      # REST handlers（Gin 路由）
└── frontend/                         # React 管控台
    ├── package.json
    ├── vite.config.ts                # 开发代理 /api → :8088
    └── src/
        ├── App.tsx                   # 路由 + 布局
        ├── cluster-context.tsx       # 全局集群选择器 + 添加集群
        ├── api/client.ts             # API 客户端（axios）+ TypeScript 类型
        └── pages/                    # 各功能页（Topology / Tenants / Placement / ...）
```

---

## 环境要求

### 被管控的 TiDB 集群
- **TiDB**：v7.1+（依赖 Placement Rule in SQL、Resource Control；v7.1 起 RU_PER_SEC 不允许为 0）
- **PD**：HTTP API 可达（默认 :2379），用于拓扑查看与 store 标签
- **账号**：建议可写账号（如 root）——租户编排、DDL、打标签均需写权限；只读账号仅能查看
- **Prometheus**（可选）：采集了 TiDB / TiKV 指标，用于监控仪表盘

### 开发机
- **Go** 1.21+
- **Node.js** 18+ / npm
- 网络可达 TiDB（4000）、PD（2379）、Prometheus（9090）

---

## 快速开始（开发模式）

### 1. 克隆

```bash
git clone https://github.com/michaelliuyuan/tidb-multi-tenant-console.git
cd tidb-multi-tenant-console
```

### 2. 配置后端

```bash
cd backend
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，填入你的 TiDB 集群连接信息（详见 [配置说明](#配置说明)）：

```yaml
server:
  addr: ":8088"

metadata:
  host: "127.0.0.1"
  port: 4000
  user: "root"
  password: "<你的密码>"
  db: ""

clusters:
  - name: "my-cluster"
    tidb_host: "127.0.0.1"
    tidb_port: 4000
    tidb_user: "root"
    tidb_password: "<你的密码>"
    pd_endpoint: "http://127.0.0.1:2379"
    prometheus_url: "http://127.0.0.1:9090"
```

> 元数据库 `db` 留空即可，首次启动会自动创建 `mt_console` schema 并执行迁移。

### 3. 启动后端

```bash
go mod tidy
go run .
```

后端启动后会：
1. 连接元数据库
2. 自动执行 `migrations/0001_init.sql`（创建 `mt_console` schema + 6 张表）
3. 把 `config.yaml` 中的集群预注册到 `mt_console.cluster` 表
4. 监听 `:8088`

看到 `TiDB 多租户管控台 listening on :8088` 即成功。

### 4. 启动前端

新开终端：

```bash
cd frontend
npm install
npm run dev
```

前端开发服务器在 `:5180`，已配置代理把 `/api` 转发到后端 `:8088`。浏览器打开 <http://localhost:5180> 即可。

> 开发模式下前后端分离运行；生产部署见下文。

---

## 生产部署

生产环境推荐单二进制部署：后端编译为静态二进制，同时托管前端构建产物（SPA）。

### 方式一：源码构建（本机有 Go + Node）

#### 1. 构建前端

```bash
cd frontend
npm install
npm run build      # 产物在 frontend/dist/（index.html + assets/）
```

#### 2. 构建后端

```bash
cd backend
go build -o console-server .
```

#### 3. 运行

```bash
cd backend
./console-server   # 读取同目录 config.yaml，监听 :8088，自动托管 ../frontend/dist
```

后端会优先查找 `../frontend/dist`（相对于二进制的上层目录），找不到则回退 `frontend/dist`，作为 SPA 静态服务（含 history 路由 fallback）。打开 <http://服务器:8088> 即完整应用。

### 方式二：交叉编译部署到 Linux（本机 Windows → 远程 Linux）

若开发机是 Windows、目标服务器是 Linux 且无 Go/Node 环境：

```bash
# 1. 本机交叉编译后端（静态二进制）
cd backend
$env:CGO_ENABLED=0; $env:GOOS='linux'; $env:GOARCH='amd64'
go build -o console-server .

# 2. 本机构建前端
cd ../frontend
npm install
npm run build

# 3. 上传到服务器（scp / rsync / pscp 等）
scp backend/console-server  user@server:/opt/tidb-console/
scp -r frontend/dist        user@server:/opt/tidb-console/frontend/

# 4. 服务器上准备配置（不要上传含密码的 config.yaml，用 example 重新填）
ssh user@server
cd /opt/tidb-console
cp /path/to/config.example.yaml config.yaml
vi config.yaml                  # 填入生产集群连接信息

# 5. 启动
./console-server
# 或后台运行：
nohup ./console-server > console.log 2>&1 &
```

目录结构（服务器）：

```
/opt/tidb-console/
├── console-server          # 后端二进制
├── config.yaml             # 配置（含密码，chmod 600）
├── migrations/             # 迁移 SQL（首次启动自动执行）
│   └── 0001_init.sql
└── frontend/
    └── dist/               # 前端构建产物
        ├── index.html
        ├── logo.png
        └── assets/
```

> `migrations/` 目录必须与 `console-server` 同级，首次启动会读取 `migrations/0001_init.sql` 自动建表。

#### 重启 / 升级

```bash
# 重启
pkill console-server && nohup ./console-server > console.log 2>&1 &

# 升级后端：原子替换二进制
mv console-server console-server.bak
mv console-server.new console-server
chmod +x console-server
./restart.sh
```

---

## 配置说明

配置文件 `backend/config.yaml`（从 `config.example.yaml` 复制）：

| 字段 | 说明 | 默认 / 示例 |
|---|---|---|
| `server.addr` | 后端监听地址 | `:8088` |
| `metadata.host` | 元数据库主机 | `127.0.0.1` |
| `metadata.port` | 元数据库端口 | `4000` |
| `metadata.user` | 元数据库用户（需可建 schema/表） | `root` |
| `metadata.password` | 元数据库密码 | — |
| `metadata.db` | 元数据库名（留空自动创建 `mt_console`） | `""` |
| `clusters[].name` | 集群显示名（唯一） | `my-cluster` |
| `clusters[].tidb_host` | TiDB 主机 | — |
| `clusters[].tidb_port` | TiDB 端口 | `4000` |
| `clusters[].tidb_user` | TiDB 用户（建议可写） | `root` |
| `clusters[].tidb_password` | TiDB 密码 | — |
| `clusters[].pd_endpoint` | PD HTTP API 地址 | `http://pd:2379` |
| `clusters[].prometheus_url` | Prometheus 地址（可选） | `http://prom:9090` |

> `config.yaml` 中的集群在启动时**幂等 upsert** 到数据库（按 name 去重）。运行时通过页面「添加集群」功能新增的集群也会写入同一张表，重启后保留。

---

## API 参考

所有接口前缀 `/api/v1`。集群/租户的 ID 是 `AUTO_RANDOM BIGINT`（>2^53），JSON 中统一序列化为**字符串**，前端不可做数值运算。

### 集群

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/clusters` | 列出所有集群 |
| POST | `/clusters` | 添加集群（提交前自动 TiDB/PD 连通性探测） |
| GET | `/clusters/:cid/topology/stores` | TiKV store 拓扑（PD stores） |
| GET | `/clusters/:cid/stores/:sid/resource` | 单 store 资源占用 |
| PUT | `/clusters/:cid/stores/:sid/labels` | 给 store 打标签 |
| GET | `/clusters/:cid/health` | 集群健康探测（TiDB/PD/Prom + 能力） |
| GET | `/clusters/:cid/metrics/ru` | RU 趋势时序 |
| GET | `/clusters/:cid/metrics/summary` | QPS/P99/存储汇总 |

### 租户

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/tenants?cluster_id=:cid` | 列出租户 |
| POST | `/tenants` | 创建租户（6 步原子编排） |
| PUT | `/tenants/:tid` | 更新租户（RU/优先级） |
| DELETE | `/tenants/:tid` | 删除租户（幂等清理） |
| GET | `/tenants/:tid/detail` | 租户详情（关联库 + 承载 TiKV） |
| GET | `/tenants/:tid/resource` | 租户资源（RG + stores + policy） |
| POST | `/tenants/:tid/suspend` | 挂起（RU→1） |
| POST | `/tenants/:tid/activate` | 恢复 |

### 放置策略

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/clusters/:cid/placement-policies` | 列出策略 |
| POST | `/clusters/:cid/placement-policies` | 创建策略 |
| PUT | `/clusters/:cid/placement-policies/:pname` | 修改策略 |
| DELETE | `/clusters/:cid/placement-policies/:pname` | 删除策略 |
| GET | `/clusters/:cid/placement-labels` | 可用标签 |
| POST | `/clusters/:cid/placement-policies/dry-run` | dry-run 预估 |
| PUT | `/clusters/:cid/databases/:dbname/policy` | 库绑定 policy |
| PUT | `/clusters/:cid/databases/:dbname/tables/:tname/policy` | 表绑定 policy |

### 资源组 / 用户 / 数据库

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/clusters/:cid/resource-groups` | 列出/创建资源组 |
| PUT/DELETE | `/clusters/:cid/resource-groups/:rgname` | 修改/删除资源组 |
| GET/POST | `/clusters/:cid/users` | 列出/创建用户 |
| PUT/DELETE | `/clusters/:cid/users/:username` | 修改/删除用户 |
| GET/POST/DELETE | `/clusters/:cid/users/:username/privileges` | 权限查询/授予/撤销 |
| GET | `/clusters/:cid/databases` | 数据库列表（含大小/表数/policy） |
| GET | `/clusters/:cid/databases/:dbname/tables` | 表列表 |

---

## 数据模型

元数据存储在 `mt_console` schema（首次启动自动创建），共 6 张表：

| 表 | 用途 |
|---|---|
| `cluster` | 被管控的 TiDB 集群连接信息 |
| `tenant` | 租户聚合根（隔离级别 / 标签 / policy / RG） |
| `tenant_database` | 租户关联的数据库 |
| `tenant_user` | 租户关联的数据库用户 |
| `audit_log` | 操作审计（actor/action/status/payload） |
| `job` | 编排任务（6 步状态机） |

完整 DDL 见 [`backend/migrations/0001_init.sql`](backend/migrations/0001_init.sql)，详细设计见 [`docs/p0-technical-design.md`](docs/p0-technical-design.md)。

---

## 设计原则

1. **能走标准 SQL 的绝不绕过** —— policy / resource group / database / user 全走 TiDB SQL，不绕过权限体系
2. **不绕过 PD 直接改 TiKV** —— 仅通过 PD HTTP API 设标签，尊重调度器
3. **租户操作原子化** —— 多步编排 + 失败补偿回滚 + 幂等，杜绝半成品租户
4. **placement 变更安全** —— dry-run 预估 Region 调度量 + 目标池容量校验 + 灰度绑定

---

## 路线图

- [x] **P0**：TiKV 拓扑 + store 标签 + 租户创建编排（PHYSICAL/LOGICAL/HYBRID）+ policy/RG 查看 + 挂起/恢复
- [x] **P1**：placement dry-run 预估 + Prometheus 监控仪表盘 + 集群连通性/能力探测
- [x] **P1+**：添加集群 UI + 租户详情关联视图 + 用户/权限管理 + 数据库/表 policy 绑定
- [ ] **P2**：声明式 YAML（GitOps）+ 多集群统一视图 + 审计页面 + 密码加密存储（当前明文，见安全说明）

---

## 安全说明

> ⚠️ 当前版本面向**内网/测试环境**，密码在数据库中以明文存储（`tidb_pwd_enc` 列预留了加密位但未实现加解密）。生产部署前请：

- 给 `mt_console.cluster.tidb_pwd_enc` 实现加密存储（参考 `store/tidb.go` 的 `GetCluster`/`UpsertCluster`）
- 限制后端端口访问范围（防火墙 / 反向代理 + 鉴权）
- `config.yaml` 设置 `chmod 600`，不要提交到版本库（已被 `.gitignore` 排除）
- TiDB 账号遵循最小权限原则

---

## 文档

- [`docs/p0-technical-design.md`](docs/p0-technical-design.md) —— 元数据 schema、REST API、SQL 模板、PD API 用法、6 步编排与回滚、权限矩阵
- [`prototype/tenant-wizard.html`](prototype/tenant-wizard.html) —— 可交互原型（浏览器直接打开）

## 相关链接

- TiDB Resource Control：<https://docs.pingcap.com/tidb/stable/tidb-resource-control>
- Placement Rules in SQL：<https://docs.pingcap.com/tidb/stable/placement-rules-in-sql>

## License

本项目仅供内部使用。
