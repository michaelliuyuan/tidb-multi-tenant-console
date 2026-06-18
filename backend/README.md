# TiDB 多租户管控台 — Backend (Go)

把 TiDB **物理隔离（Placement Rule in SQL + TiKV 标签）** 与 **逻辑隔离（Resource Control / RU 资源组）** 封装成以「租户」为中心的 REST API。

## 目录结构
```
backend/
├── main.go                      # 入口：加载配置 → 连元数据 → 迁移 → 注册集群 → 启动 Gin
├── config.yaml                  # 配置（元数据库 + 预注册集群）
├── migrations/0001_init.sql     # mt_console 元数据 schema
└── internal/
    ├── model/model.go           # 领域模型（Cluster/Tenant/Job/...）
    ├── store/
    │   ├── tidb.go              # 元数据 CRUD + 目标集群 SQL 操作（placement/rg/db/user 模板）
    │   └── pd.go                # PD HTTP API（stores / store label / placement rules）
    ├── orchestrator/tenant.go   # 租户创建 6 步编排 + 失败补偿回滚
    └── api/handlers.go          # REST 路由（/api/v1/*）
```

## 运行
```bash
cd backend
cp config.yaml config.local.yaml   # 改元数据库与集群连接信息
go mod tidy
go run .                           # 默认 :8088
```
> 需要可写账号（见 `docs/p0-technical-design.md` §8 权限矩阵）。P0 元数据密码为明文回填，生产应加密 `tidb_pwd_enc`。

## 核心 API（节选）
| Method | Path | 说明 |
|---|---|---|
| GET | `/api/v1/clusters/:cid/topology/stores` | TiKV store + 标签（PD） |
| PUT | `/api/v1/clusters/:cid/stores/:sid/labels` | 打标签 |
| GET | `/api/v1/clusters/:cid/placement-policies` | 列出（INFORMATION_SCHEMA） |
| GET | `/api/v1/clusters/:cid/resource-groups` | 列出 |
| POST | `/api/v1/tenants` | **创建租户**（6 步编排，失败回滚） |
| POST | `/api/v1/tenants/:tid/suspend` | 挂起（RU→1，v7.1 不支持 0） |

完整规格见 `../docs/p0-technical-design.md`。

## 创建租户示例
```bash
curl -X POST localhost:8088/api/v1/tenants -H 'Content-Type: application/json' -d '{
  "name": "acme", "cluster_id": 1, "isolation_level": "HYBRID",
  "label_key": "zone", "store_ids": [1,2,3],
  "placement": {"primary_region":"east","regions":"east,west","voters":3,"followers":2,"survival_preferences":"zone,host"},
  "resource_group": {"ru_per_sec": 2000, "burstable": true, "priority":"MEDIUM"},
  "databases": ["core","log"],
  "users": [{"username":"app_rw","password":"***"}]
}'
```
成功返回编排 `job`；失败时已执行步骤逆序回滚，tenant 标记 DELETED，并写审计日志。
