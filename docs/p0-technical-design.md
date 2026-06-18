# P0 详细技术设计 — TiDB 多租户可视化管控工具

> 配套 `kb/tidb-multi-tenant-tool-design.md`（总体架构）。本文给出可直接落地的：元数据 schema DDL、REST API 规格、各操作 SQL 模板、PD API 参考、租户创建编排与回滚。

## 0. 命名与约定
- 工具自身元数据存独立 schema `mt_console`（与业务库隔离），可放在被管控 TiDB 集群内，也可放独立轻量实例。
- TiDB 内的实体命名带租户前缀防冲突：`placement policy t_{tenant}_pol`、`resource group t_{tenant}_rg`、`database t_{tenant}_{dbname}`。
- **数据访问原则**：policy / resource group / database / user 一律走 **TiDB SQL**；TiKV store 标签、PD placement rules 查看走 **PD HTTP API**；监控走 **Prometheus**。

---

## 1. 元数据 Schema（DDL）

```sql
CREATE SCHEMA IF NOT EXISTS mt_console;

-- 1.1 集群注册表（多集群）
CREATE TABLE mt_console.cluster (
  id              BIGINT PRIMARY KEY AUTO_RANDOM,
  name            VARCHAR(128) NOT NULL,
  tidb_host       VARCHAR(256) NOT NULL,          -- 4000 端口
  tidb_port       INT NOT NULL DEFAULT 4000,
  tidb_user       VARCHAR(128) NOT NULL,          -- 需 PLACEMENT + RESOURCE_GROUP_ADMIN + DDL/DCL 权限
  tidb_pwd_enc    VARBINARY(512) NOT NULL,        -- AES 加密后
  pd_endpoint     VARCHAR(256) NOT NULL,          -- http://pd:2379
  prometheus_url  VARCHAR(256),
  version         VARCHAR(32),                    -- 启动时探测：SELECT version()
  status          VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at      DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_name (name)
);

-- 1.2 租户注册表
CREATE TABLE mt_console.tenant (
  id                   BIGINT PRIMARY KEY AUTO_RANDOM,
  cluster_id           BIGINT NOT NULL,
  name                 VARCHAR(128) NOT NULL,
  isolation_level      ENUM('PHYSICAL','LOGICAL','HYBRID') NOT NULL DEFAULT 'HYBRID',
  label_key            VARCHAR(64),               -- 物理隔离用的 TiKV 标签键，如 'zone'
  label_value          VARCHAR(128),              -- 标签值，如 'tenant-a'
  placement_policy     VARCHAR(128),              -- 对应 TiDB placement policy 名
  resource_group       VARCHAR(128),              -- 对应 TiDB resource group 名
  ru_per_sec           INT,                       -- 配额
  priority             ENUM('LOW','MEDIUM','HIGH') NOT NULL DEFAULT 'MEDIUM',
  max_storage_gb       BIGINT,                    -- 软限额（仅监控告警用）
  status               ENUM('ACTIVE','SUSPENDED','MIGRATING','DELETED') NOT NULL DEFAULT 'ACTIVE',
  retention_days       INT NOT NULL DEFAULT 30,   -- 软删保留期
  created_at           DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  updated_at           DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_cluster_tenant (cluster_id, name),
  KEY idx_status (status)
) ;

-- 1.3 租户 ↔ database
CREATE TABLE mt_console.tenant_database (
  id          BIGINT PRIMARY KEY AUTO_RANDOM,
  tenant_id   BIGINT NOT NULL,
  db_name     VARCHAR(128) NOT NULL,
  UNIQUE KEY uk_tenant_db (tenant_id, db_name),
  KEY idx_tenant (tenant_id)
);

-- 1.4 租户 ↔ user（绑到该租户 resource group）
CREATE TABLE mt_console.tenant_user (
  id          BIGINT PRIMARY KEY AUTO_RANDOM,
  tenant_id   BIGINT NOT NULL,
  username    VARCHAR(128) NOT NULL,
  host        VARCHAR(64) NOT NULL DEFAULT '%',
  UNIQUE KEY uk_tenant_user (tenant_id, username, host),
  KEY idx_tenant (tenant_id)
);

-- 1.5 审计日志（append-only）
CREATE TABLE mt_console.audit_log (
  id           BIGINT PRIMARY KEY AUTO_RANDOM,
  ts           DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  actor        VARCHAR(128),        -- 控制台操作人
  cluster_id   BIGINT,
  tenant_id    BIGINT,
  action       VARCHAR(64),         -- CREATE_TENANT/SUSPEND/DELETE/ALTER_POLICY/SET_LABEL...
  entity_type  VARCHAR(32),
  entity_name  VARCHAR(128),
  payload      JSON,                -- before/after 快照
  status       VARCHAR(16),         -- success/failed
  message      TEXT,
  KEY idx_ts (ts),
  KEY idx_tenant (tenant_id)
);

-- 1.6 异步编排 Job（含回滚步骤）
CREATE TABLE mt_console.job (
  id            BIGINT PRIMARY KEY AUTO_RANDOM,
  tenant_id     BIGINT,
  op_type       VARCHAR(64) NOT NULL,             -- CREATE_TENANT/SUSPEND/MIGRATE/DELETE...
  status        ENUM('PENDING','RUNNING','SUCCEEDED','FAILED','ROLLING_BACK','ROLLED_BACK') NOT NULL DEFAULT 'PENDING',
  steps         JSON NOT NULL,                    -- [{name,status,action,sql,compensation}]
  current_step  INT NOT NULL DEFAULT 0,
  error         TEXT,
  created_at    DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_tenant (tenant_id),
  KEY idx_status (status)
);
```

> `audit_log`、`job` 量大，生产建议按月分区或归档。

---

## 2. REST API 规格

Base path：`/api/v1`。所有写操作异步化，返回 `job_id`，前端轮询 `/jobs/{id}` 或 WebSocket 推送进度。

### 2.1 集群
| Method | Path | 说明 |
|---|---|---|
| GET | `/clusters` | 集群列表 |
| POST | `/clusters` | 注册集群（含连通性+版本探测） |
| GET | `/clusters/{cid}` | 详情 |
| DELETE | `/clusters/{cid}` | 注销（不碰集群数据） |

### 2.2 TiKV 拓扑 / 标签（PD API 包装）
| Method | Path | 说明 |
|---|---|---|
| GET | `/clusters/{cid}/topology/stores` | 所有 store + labels（来自 PD） |
| GET | `/clusters/{cid}/topology/tree` | 按 dc>zone>host 聚合的树 |
| PUT | `/clusters/{cid}/stores/{sid}/labels` | 设置/更新标签（PD `POST /store/{sid}/label`） |
| DELETE | `/clusters/{cid}/stores/{sid}/labels/{key}` | 删除标签 |

### 2.3 放置策略 Placement Policy
| Method | Path | 说明 |
|---|---|---|
| GET | `/clusters/{cid}/placement-policies` | 列出（`INFORMATION_SCHEMA.PLACEMENT_POLICIES`） |
| POST | `/clusters/{cid}/placement-policies` | 创建 |
| PUT | `/clusters/{cid}/placement-policies/{name}` | 修改 |
| DELETE | `/clusters/{cid}/placement-policies/{name}` | 删除 |
| POST | `/clusters/{cid}/placement-policies/{name}/dry-run` | **预估**受影响 Region 数/调度量/目标容量 |

### 2.4 资源管控 Resource Group
| Method | Path | 说明 |
|---|---|---|
| GET | `/clusters/{cid}/resource-groups` | 列出（`INFORMATION_SCHEMA.RESOURCE_GROUPS`） |
| POST | `/clusters/{cid}/resource-groups` | 创建 |
| PUT | `/clusters/{cid}/resource-groups/{name}` | 修改配额 |
| DELETE | `/clusters/{cid}/resource-groups/{name}` | 删除 |
| GET | `/clusters/{cid}/resource-groups/{name}/metrics` | RU 实时/历史消耗 |

### 2.5 租户（编排核心）
| Method | Path | 说明 |
|---|---|---|
| GET | `/tenants?cluster_id=` | 租户列表 |
| POST | `/tenants` | **创建租户**（编排多步，返回 job_id） |
| GET | `/tenants/{tid}` | 详情 |
| PUT | `/tenants/{tid}` | 调整（配额/隔离级别变更） |
| POST | `/tenants/{tid}:suspend` | 挂起（resource group RU→1） |
| POST | `/tenants/{tid}:activate` | 恢复 |
| DELETE | `/tenants/{tid}` | 软删除（SUSPENDED + 保留 N 天） |

### 2.6 监控 / 审计 / Job
| Method | Path | 说明 |
|---|---|---|
| GET | `/clusters/{cid}/metrics/ru?tenant_id=&from=&to=` | 租户级 RU |
| GET | `/clusters/{cid}/metrics/qps` / `/storage` | QPS / 存储 |
| GET | `/audit-logs?cluster_id=&tenant_id=` | 审计 |
| GET | `/jobs/{jid}` | Job 进度（含每步状态） |

---

## 3. 操作 SQL 模板（TiDB）

> 占位符 `{...}` 由后端注入并**参数化/转义**。统一前缀 `t_{tenant}_`。

### 3.1 Placement Policy
```sql
-- 物理/HYBRID：约束到租户独占节点池（纯约束式）
-- 重要：TiDB v7.1 不允许 PRIMARY_REGION/REGIONS（糖语法）与 *_CONSTRAINTS 混用，
--       故按 label 做物理隔离时只用约束式 + VOTERS 计数 + 生存偏好。
CREATE PLACEMENT POLICY t_{tenant}_pol
    VOTERS               = {voters}            -- 默认 3
    LEADER_CONSTRAINTS   = "[+{label_key}={label_value}]"
    FOLLOWER_CONSTRAINTS = "[+{label_key}={label_value}]"
    VOTER_CONSTRAINTS    = "[+{label_key}={label_value}]"
    SURVIVAL_PREFERENCES = "[{survival_csv}]";  -- 例 "[zone, host]"

-- 调整：
ALTER PLACEMENT POLICY t_{tenant}_pol FOLLOWER_CONSTRAINTS = "[+zone=tenant-a]";
DROP   PLACEMENT POLICY IF EXISTS t_{tenant}_pol;

-- 仅逻辑隔离租户：可不建 policy（共享默认），仅靠 resource group
```
约束语法：`"[+zone=zone1, +disk=ssd]"` 正向、`"[-zone=zone2]"` 负向。可用 `INFORMATION_SCHEMA.PLACEMENT_POLICIES` 反查已建策略。

### 3.2 绑定 policy 到 database / table
```sql
CREATE DATABASE t_{tenant}_{db} PLACEMENT POLICY = t_{tenant}_pol;
ALTER  DATABASE t_{tenant}_{db} PLACEMENT POLICY = t_{tenant}_pol;   -- 已存在库补绑
ALTER  DATABASE t_{tenant}_{db} PLACEMENT POLICY = NULL;             -- 解绑

-- 单表覆盖（粒度更细）
ALTER TABLE t_{tenant}_{db}.t1 PLACEMENT POLICY = t_{tenant}_pol;
```

### 3.3 Resource Group
```sql
CREATE RESOURCE GROUP t_{tenant}_rg
    RU_PER_SEC = {ru_per_sec}            -- 必填，配额
    BURSTABLE                              -- 允许短时超出（按需）
    PRIORITY   = {LOW|MEDIUM|HIGH}
    BACKGROUND = (                          -- 可选：后台任务（BR/统计）独立 RU
        TASK_TYPE = "br", RU_PER_SEC = {bg_ru}
    );

ALTER RESOURCE GROUP t_{tenant}_rg RU_PER_SEC = {new_ru};
-- 挂起 = 配额降到极小值（v7.1 实测：RU_PER_SEC=0 报 "unknown resource group mode"，0 语义=无限；故用 1 近乎停摆）
ALTER RESOURCE GROUP t_{tenant}_rg RU_PER_SEC = 1;
DROP  RESOURCE GROUP IF EXISTS t_{tenant}_rg;   -- 前提：无 user 绑定
```
反查：`INFORMATION_SCHEMA.RESOURCE_GROUPS`、消耗 `INFORMATION_SCHEMA.RESOURCE_GROUPS_USAGE`（如版本支持）。

### 3.4 用户绑定 resource group
```sql
CREATE USER '{user}'@'{host}' IDENTIFIED BY '{pwd}' RESOURCE GROUP t_{tenant}_rg;
ALTER USER '{user}'@'{host}' RESOURCE GROUP t_{tenant}_rg;
-- 回退默认组
ALTER USER '{user}'@'{host}' RESOURCE GROUP `default`;
```
> TiDB 默认组 `default`。用户级 RU 追溯：通过 `RESOURCE_GROUP` 字段关联租户。

---

## 4. PD HTTP API 参考（标签 / 调度）

> 经 `pd_endpoint`（如 `http://pd:2379`）。只读为主；写标签需谨慎。

| 用途 | Method | Path | Body / 备注 |
|---|---|---|---|
| 列出所有 store | GET | `/pd/api/v1/stores` | 含 `labels[]`、`status`、`capacity`、`available`、`region_count` |
| 单个 store | GET | `/pd/api/v1/store/{id}` | |
| **设置/更新标签** | POST | `/pd/api/v1/store/{id}/label` | body 为 **map** `{"zone":"tenant-a"}`（labelKey→value）。⚠️ 实测 `{"key":..,"value":..}` 会被当 map 误建成两个标签，勿用 |
| 删除标签 | DELETE | `/pd/api/v1/store/{id}/label` | body 为标签键的 **JSON 字符串** `"zone"`（不是路径 `/label/{key}`，会 404） |
| placement rules 全量 | GET | `/pd/api/v1/config/placement-rule` | 查看 PD 实际下发的规则 |
| 按 group | GET | `/pd/api/v1/config/placement-rule/{group}` | |
| Region 调度统计 | GET | `/pd/api/v1/operators` / `/scheduler` | dry-run 预估依据 |
| 集群版本/健康 | GET | `/pd/api/v1/version` / `/pd/api/v1/health` | |

**dry-run 预估算法（place policy 变更）**：
1. 取该 policy 绑定的所有 table 的 Region 数（`tikv_region_status` / PD `regions/keyspace`）。
2. 比对目标标签节点池的 `available` 容量是否 ≥ Region 总大小。
3. 估算调度时长 ≈ Region 数 / PD 调度并发（默认 ~2048 inflight）。
4. 输出：`{affected_regions, est_minutes, target_capacity_ok, warnings[]}`。

---

## 5. 租户创建编排（含回滚）

创建一个 HYBRID 租户 = 6 步事务性编排，任一失败沿已执行步补偿回滚。`job.steps` 结构：

```
[1] ensure_store_labels   (PD)     comp: 无（标签可保留，幂等）
[2] create_placement_policy (SQL)  comp: DROP PLACEMENT POLICY IF EXISTS ...
[3] create_resource_group   (SQL)  comp: DROP RESOURCE GROUP IF EXISTS ...
[4] create_database(+bind)  (SQL)  comp: DROP DATABASE ...（确认空库）
[5] create_users(+bind rg)  (SQL)  comp: DROP USER ...
[6] write_metadata          (mt_console) comp: DELETE tenant/*_user/_db rows
```

**幂等**：每步带 `IF NOT EXISTS` / `IF EXISTS`，可安全重试。**灰度**：先建 policy+rg，绑定到 1 张表观察调度，再全库绑定。

**挂起/恢复/删除**：
- suspend → `ALTER RESOURCE GROUP ... RU_PER_SEC=1`（v7.1 不允许 0；保留原值于元数据，恢复时还原）；可选同时 `PLACEMENT POLICY=NULL` 解绑迁移。
- delete → 软删：先 suspend，`tenant.status=DELETED`，到 `retention_days` 后由定时任务物理 DROP（二次确认）。

---

## 6. 监控指标（Prometheus）

| 指标 | 用途 |
|---|---|
| `tidb_server_resource_group_ru` | 按 resource group 的 RU 消耗 → 反查租户 |
| `tidb_request_unit` | RU 细分 |
| `tikv_engine_size` / `tikv_store_size_bytes` | 存储用量 |
| `pd_scheduler_status` / `tikv_region_count` | Region 均衡 / 调度进度 |
| `tidb_session_schema_last_repl_info`（或 cluster_info） | 集群拓扑 |

前端通过 `prometheus_url` 的 `/api/v1/query_range` 拉取，按 `resource_group` label 聚合回租户。

---

## 7. 版本能力探测（启动时）

`SELECT version();` 解析后启用能力：
- Placement Rule in SQL：v5.3 GA（v5.0 实验性）→ 否则禁用物理隔离选项。
- Resource Control：v7.1 GA（v6.6 实验）→ 否则禁用配额字段、降级为仅拓扑+policy。
- `INFORMATION_SCHEMA.PLACEMENT_POLICIES` / `RESOURCE_GROUPS`：v6.x+。

---

## 8. 权限矩阵（连接 TiDB 的服务账号）

```sql
-- 工具服务账号所需（最小集，按集群授予）
GRANT SELECT ON *.*;                         -- 拓扑/INFORMATION_SCHEMA 探测
GRANT PLACEMENT ON *.*;                      -- 建/改/删 placement policy、绑库表
GRANT RESOURCE_GROUP_ADMIN ON *.*;           -- resource group CRUD + 绑用户
GRANT CREATE,DROP,ALTER ON *.*;              -- 建/删库表
GRANT CREATE USER ON *.*;                    -- 建用户
-- mt_console 元数据库
GRANT ALL PRIVILEGES ON mt_console.* TO 'mt_console'@'%';
```

> PD 标签写入还需要 PD 客户端权限（PD 默认无鉴权；开启 client-urls 鉴权时需对应凭证）。
