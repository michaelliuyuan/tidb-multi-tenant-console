-- mt_console 元数据 schema（详见 docs/p0-technical-design.md §1）
CREATE SCHEMA IF NOT EXISTS mt_console;

CREATE TABLE IF NOT EXISTS mt_console.cluster (
  id              BIGINT PRIMARY KEY AUTO_RANDOM,
  name            VARCHAR(128) NOT NULL,
  tidb_host       VARCHAR(256) NOT NULL,
  tidb_port       INT NOT NULL DEFAULT 4000,
  tidb_user       VARCHAR(128) NOT NULL,
  tidb_pwd_enc    VARBINARY(512) NOT NULL,
  pd_endpoint     VARCHAR(256) NOT NULL,
  prometheus_url  VARCHAR(256),
  version         VARCHAR(32),
  status          VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at      DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_name (name)
);

CREATE TABLE IF NOT EXISTS mt_console.tenant (
  id                BIGINT PRIMARY KEY AUTO_RANDOM,
  cluster_id        BIGINT NOT NULL,
  name              VARCHAR(128) NOT NULL,
  isolation_level   ENUM('PHYSICAL','LOGICAL','HYBRID') NOT NULL DEFAULT 'HYBRID',
  label_key         VARCHAR(64),
  label_value       VARCHAR(128),
  placement_policy  VARCHAR(128),
  resource_group    VARCHAR(128),
  ru_per_sec        INT,
  priority          ENUM('LOW','MEDIUM','HIGH') NOT NULL DEFAULT 'MEDIUM',
  max_storage_gb    BIGINT,
  status            ENUM('ACTIVE','SUSPENDED','MIGRATING','DELETED') NOT NULL DEFAULT 'ACTIVE',
  retention_days    INT NOT NULL DEFAULT 30,
  created_at        DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_cluster_tenant (cluster_id, name),
  KEY idx_status (status)
);

CREATE TABLE IF NOT EXISTS mt_console.tenant_database (
  id          BIGINT PRIMARY KEY AUTO_RANDOM,
  tenant_id   BIGINT NOT NULL,
  db_name     VARCHAR(128) NOT NULL,
  UNIQUE KEY uk_tenant_db (tenant_id, db_name),
  KEY idx_tenant (tenant_id)
);

CREATE TABLE IF NOT EXISTS mt_console.tenant_user (
  id          BIGINT PRIMARY KEY AUTO_RANDOM,
  tenant_id   BIGINT NOT NULL,
  username    VARCHAR(128) NOT NULL,
  host        VARCHAR(64) NOT NULL DEFAULT '%',
  UNIQUE KEY uk_tenant_user (tenant_id, username, host),
  KEY idx_tenant (tenant_id)
);

CREATE TABLE IF NOT EXISTS mt_console.audit_log (
  id           BIGINT PRIMARY KEY AUTO_RANDOM,
  ts           DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  actor        VARCHAR(128),
  cluster_id   BIGINT,
  tenant_id    BIGINT,
  action       VARCHAR(64),
  entity_type  VARCHAR(32),
  entity_name  VARCHAR(128),
  payload      JSON,
  status       VARCHAR(16),
  message      TEXT,
  KEY idx_ts (ts),
  KEY idx_tenant (tenant_id)
);

CREATE TABLE IF NOT EXISTS mt_console.job (
  id            BIGINT PRIMARY KEY AUTO_RANDOM,
  tenant_id     BIGINT,
  op_type       VARCHAR(64) NOT NULL,
  status        ENUM('PENDING','RUNNING','SUCCEEDED','FAILED','ROLLING_BACK','ROLLED_BACK') NOT NULL DEFAULT 'PENDING',
  steps         JSON NOT NULL,
  current_step  INT NOT NULL DEFAULT 0,
  error         TEXT,
  created_at    DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_tenant (tenant_id),
  KEY idx_status (status)
);
