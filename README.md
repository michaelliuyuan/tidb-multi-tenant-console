# TiDB 多租户可视化管控工具

把 TiDB 的**物理隔离（Placement Rule in SQL + TiKV 标签）**与**逻辑隔离（Resource Control / RU 资源组）**，
封装成以「租户」为一等公民的可视化管控台，替代繁琐的手工运维。

## 背景
- 逻辑隔离 Resource Control：<https://pingkai.cn/docs/tidb/stable/tidb-resource-control-ru-groups>
- 物理隔离 Placement Rule in SQL：<https://pingkai.cn/docs/tidb/stable/placement-rules-in-sql>

## 文档
- [`docs/p0-technical-design.md`](docs/p0-technical-design.md) — 元数据 schema、REST API、SQL 模板、PD API、编排与回滚、权限矩阵
- 配套总体架构：`../kb/tidb-multi-tenant-tool-design.md`

## 结构
```
tidb-multi-tenant-console/
├── docs/p0-technical-design.md   # ① 详细技术设计
├── prototype/tenant-wizard.html  # ② 可交互原型（浏览器直接打开）
├── backend/                      # ③ Go API Server（编排 + TiDB SQL + PD API）
│   ├── main.go  config.yaml  migrations/  internal/{model,store,orchestrator,api}
└── frontend/                     # ③ React 管控台（拓扑/租户/创建向导）
    └── src/{App,api,pages}
```

## 快速开始
```bash
# 后端
cd backend && cp config.yaml config.local.yaml   # 改连接信息
go mod tidy && go run .                           # :8088

# 前端
cd frontend && npm install && npm run dev         # :5180
```

## MVP 范围
- [x] TiKV 拓扑查看（PD stores，按 zone 聚合）+ store 标签管理（P0）
- [x] 租户创建向导（6 步原子化编排，失败回滚）— PHYSICAL / LOGICAL / HYBRID（P0）
- [x] placement policy + resource group 查看（P0）
- [x] 租户挂起（RU→1）/ 恢复（P0）
- [x] **placement dry-run 预估**（受影响 Region 数 / 目标池容量 / 调度时长 / 告警）（P1）
- [x] **Prometheus 监控仪表盘**（租户级 RU 趋势 + QPS / P99 / 存储）（P1）
- [x] **集群连通性 + 版本能力探测**（联调前置：TiDB/PD/Prom 可达性 + placement/resource control 能力）（P1）
- [ ] 声明式 YAML / 多集群统一视图 / 审计页面（P2）

## 设计原则
1. **能走标准 SQL 的绝不绕过** — policy/resource group/database/user 全走 TiDB SQL。
2. **不绕过 PD 直接改 TiKV** — 仅通过 PD HTTP API 设标签。
3. **租户操作原子化** — 多步编排 + 失败补偿回滚 + 幂等。
4. **placement 变更安全** — dry-run 预估 Region 调度 + 灰度绑定（P1）。
