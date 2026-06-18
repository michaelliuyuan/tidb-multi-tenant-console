// Package store 提供对 TiDB（SQL）与元数据的访问。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	"github.com/tidb-multi-tenant/console/internal/model"
)

// Metadata 操作 mt_console.* 元数据表。
type Metadata struct {
	DB *sql.DB
}

// OpenTiDB 打开一个 TiDB 连接（用于元数据库或目标集群）。
func OpenTiDB(host string, port int, user, password, db string) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true",
		user, password, host, port, db)
	d, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		return nil, err
	}
	return d, nil
}

// ----- Cluster -----

func (m *Metadata) ListClusters() ([]model.Cluster, error) {
	rows, err := m.DB.Query(`SELECT id,name,tidb_host,tidb_port,tidb_user,pd_endpoint,prometheus_url,version,status FROM mt_console.cluster`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Cluster
	for rows.Next() {
		var c model.Cluster
		if err := rows.Scan(&c.ID, &c.Name, &c.TiDBHost, &c.TiDBPort, &c.TiDBUser, &c.PDEndpoint, &c.PrometheusURL, &c.Version, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (m *Metadata) GetCluster(id int64) (model.Cluster, error) {
	var c model.Cluster
	var pwd []byte
	err := m.DB.QueryRow(`SELECT id,name,tidb_host,tidb_port,tidb_user,tidb_pwd_enc,pd_endpoint,prometheus_url,version,status FROM mt_console.cluster WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.TiDBHost, &c.TiDBPort, &c.TiDBUser, &pwd, &c.PDEndpoint, &c.PrometheusURL, &c.Version, &c.Status)
	// NOTE: pwd 解密略，P0 直接回填供连接；生产应解密 tidb_pwd_enc
	c.Password = string(pwd) // 简化：见 Security 注记
	return c, err
}

func (m *Metadata) UpsertCluster(c model.Cluster, pwdEnc string) (int64, error) {
	res, err := m.DB.Exec(`INSERT INTO mt_console.cluster(name,tidb_host,tidb_port,tidb_user,tidb_pwd_enc,pd_endpoint,prometheus_url,version,status)
		VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE tidb_host=VALUES(tidb_host),tidb_port=VALUES(tidb_port),tidb_user=VALUES(tidb_user),
		tidb_pwd_enc=VALUES(tidb_pwd_enc),pd_endpoint=VALUES(pd_endpoint),prometheus_url=VALUES(prometheus_url),version=VALUES(version)`,
		c.Name, c.TiDBHost, c.TiDBPort, c.TiDBUser, pwdEnc, c.PDEndpoint, c.PrometheusURL, c.Version, c.Status)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = m.DB.QueryRow(`SELECT id FROM mt_console.cluster WHERE name=?`, c.Name).Scan(&id)
	}
	return id, nil
}

// ----- Tenant -----

func (m *Metadata) CreateTenant(t *model.Tenant) error {
	res, err := m.DB.Exec(`INSERT INTO mt_console.tenant(cluster_id,name,isolation_level,label_key,label_value,placement_policy,resource_group,ru_per_sec,priority,status,retention_days)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		t.ClusterID, t.Name, t.IsolationLevel, t.LabelKey, t.LabelValue, t.PlacementPolicy, t.ResourceGroup, t.RUPerSec, t.Priority, t.Status, t.RetentionDays)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	for _, d := range t.Databases {
		if _, err := m.DB.Exec(`INSERT IGNORE INTO mt_console.tenant_database(tenant_id,db_name) VALUES(?,?)`, t.ID, d); err != nil {
			return err
		}
	}
	for _, u := range t.Users {
		if _, err := m.DB.Exec(`INSERT IGNORE INTO mt_console.tenant_user(tenant_id,username,host) VALUES(?,?,?)`, t.ID, u.Username, u.Host); err != nil {
			return err
		}
	}
	return nil
}

func (m *Metadata) ListTenants(clusterID int64) ([]model.Tenant, error) {
	rows, err := m.DB.Query(`SELECT id,cluster_id,name,isolation_level,label_key,label_value,placement_policy,resource_group,ru_per_sec,priority,status FROM mt_console.tenant WHERE cluster_id=? AND status<>'DELETED'`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Tenant
	for rows.Next() {
		var t model.Tenant
		var lk, lv sql.NullString
		if err := rows.Scan(&t.ID, &t.ClusterID, &t.Name, &t.IsolationLevel, &lk, &lv, &t.PlacementPolicy, &t.ResourceGroup, &t.RUPerSec, &t.Priority, &t.Status); err != nil {
			return nil, err
		}
		t.LabelKey = lk.String
		t.LabelValue = lv.String
		out = append(out, t)
	}
	return out, nil
}

// GetTenantRG 返回租户的 cluster_id / resource_group / ru_per_sec（suspend/activate 用）。
func (m *Metadata) GetTenantRG(id int64) (clusterID int64, rg string, ru int, err error) {
	err = m.DB.QueryRow(`SELECT cluster_id, resource_group, ru_per_sec FROM mt_console.tenant WHERE id=?`, id).
		Scan(&clusterID, &rg, &ru)
	return
}

// GetTenantDatabases 返回租户关联的数据库列表。
func (m *Metadata) GetTenantDatabases(tenantID int64) ([]string, error) {
	rows, err := m.DB.Query(`SELECT db_name FROM mt_console.tenant_database WHERE tenant_id=?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dbs []string
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, err
		}
		dbs = append(dbs, db)
	}
	return dbs, nil
}

// GetTenantLabel 返回租户的 label_key / label_value（用于查找关联的 store）。
func (m *Metadata) GetTenantLabel(tenantID int64) (labelKey, labelValue string, err error) {
	err = m.DB.QueryRow(`SELECT label_key, label_value FROM mt_console.tenant WHERE id=?`, tenantID).
		Scan(&labelKey, &labelValue)
	return
}

// UpdateTenantRU 更新租户的 RU 配额和优先级（元数据）。
func (m *Metadata) UpdateTenantRU(id int64, ruPerSec int, priority string) error {
	if priority != "" {
		_, err := m.DB.Exec(`UPDATE mt_console.tenant SET ru_per_sec=?, priority=? WHERE id=?`, ruPerSec, priority, id)
		return err
	}
	_, err := m.DB.Exec(`UPDATE mt_console.tenant SET ru_per_sec=? WHERE id=?`, ruPerSec, id)
	return err
}

// UpdateTenantLabel 更新租户的 label_value（元数据，影响 store 匹配）。
func (m *Metadata) UpdateTenantLabel(id int64, labelValue string) error {
	_, err := m.DB.Exec(`UPDATE mt_console.tenant SET label_value=? WHERE id=?`, labelValue, id)
	return err
}

// GetTenant 返回租户完整元数据（含 label_key/label_value/isolation_level 等，供详情视图使用）。
func (m *Metadata) GetTenant(id int64) (model.Tenant, error) {
	var t model.Tenant
	var lk, lv sql.NullString
	err := m.DB.QueryRow(`SELECT id,cluster_id,name,isolation_level,label_key,label_value,placement_policy,resource_group,ru_per_sec,priority,status FROM mt_console.tenant WHERE id=?`, id).
		Scan(&t.ID, &t.ClusterID, &t.Name, &t.IsolationLevel, &lk, &lv, &t.PlacementPolicy, &t.ResourceGroup, &t.RUPerSec, &t.Priority, &t.Status)
	if err != nil {
		return t, err
	}
	t.LabelKey = lk.String
	t.LabelValue = lv.String
	return t, nil
}

func (m *Metadata) UpdateTenantStatus(id int64, status model.TenantStatus, ruPerSec int) error {
	if ruPerSec >= 0 {
		_, err := m.DB.Exec(`UPDATE mt_console.tenant SET status=?, ru_per_sec=? WHERE id=?`, status, ruPerSec, id)
		return err
	}
	_, err := m.DB.Exec(`UPDATE mt_console.tenant SET status=? WHERE id=?`, status, id)
	return err
}

// TenantRGMap 返回 cluster 下 resource_group → tenant 名称 映射（用于把 Prom 的 rg label 反查回租户）。
func (m *Metadata) TenantRGMap(clusterID int64) (map[string]string, error) {
	rows, err := m.DB.Query(`SELECT name, resource_group FROM mt_console.tenant WHERE cluster_id=? AND status<>'DELETED' AND resource_group<>''`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, rg string
		if err := rows.Scan(&name, &rg); err != nil {
			return nil, err
		}
		out[rg] = name
	}
	return out, nil
}

// ----- Job -----

func (m *Metadata) CreateJob(j *model.Job) error {
	steps, _ := json.Marshal(j.Steps)
	res, err := m.DB.Exec(`INSERT INTO mt_console.job(tenant_id,op_type,status,steps,current_step) VALUES(?,?,?,?,?)`,
		j.TenantID, j.OpType, "RUNNING", string(steps), 0)
	if err != nil {
		return err
	}
	j.ID, _ = res.LastInsertId()
	return nil
}

func (m *Metadata) FinishJob(id int64, status string, errMsg string, steps []model.JobStep) error {
	stepsJSON, _ := json.Marshal(steps)
	_, err := m.DB.Exec(`UPDATE mt_console.job SET status=?, error=?, steps=?, updated_at=NOW(3) WHERE id=?`, status, errMsg, string(stepsJSON), id)
	return err
}

func (m *Metadata) Audit(actor string, clusterID, tenantID int64, action, entityType, entityName, status, msg string) {
	_, _ = m.DB.Exec(`INSERT INTO mt_console.audit_log(actor,cluster_id,tenant_id,action,entity_type,entity_name,status,message) VALUES(?,?,?,?,?,?,?,?)`,
		actor, clusterID, tenantID, action, entityType, entityName, status, msg)
}

// ----- ClusterSQL：对目标集群执行 DDL/DCL -----

// ClusterSQL 封装对某个被管控集群的 SQL 操作（placement/resource group/database/user）。
type ClusterSQL struct {
	DB *sql.DB
}

// DSN helper for password field in model（P0 简化：明文回填）
// 见 model.Cluster.Password。

// Exec 执行任意（多语句）SQL。
func (c *ClusterSQL) Exec(query string, args ...any) error {
	_, err := c.DB.Exec(query, args...)
	return err
}

// CreatePlacementPolicy 见 docs/p0-technical-design.md §3.1
// 约束式（label 隔离用）。注：TiDB v7.1 不允许 PRIMARY_REGION/REGIONS（糖语法）与
// *_CONSTRAINTS 混用，故多租户按 label 物理隔离时只用约束式 + VOTERS 计数 + 生存偏好。
// constraints: [0]=leader/follower 约束、[1]=voter 约束（通常相同）。
func (c *ClusterSQL) CreatePlacementPolicy(name string, voters int, survival string, constraints []string) (string, string) {
	q := fmt.Sprintf(`CREATE PLACEMENT POLICY %s
		VOTERS = %d,
		LEADER_CONSTRAINTS = "%s",
		FOLLOWER_CONSTRAINTS = "%s",
		VOTER_CONSTRAINTS = "%s",
		SURVIVAL_PREFERENCES = "[%s]"`,
		qname(name), voters, constraints[0], constraints[0], constraints[1], survival)
	comp := fmt.Sprintf(`DROP PLACEMENT POLICY IF EXISTS %s`, qname(name))
	return q, comp
}

// BuildCreatePlacementPolicySQL 根据文档支持的全部放置选项生成 CREATE PLACEMENT POLICY 语句。
// 常规选项（PRIMARY_REGION/REGIONS/SCHEDULE）与 CONSTRAINTS 互斥。
func BuildCreatePlacementPolicySQL(req model.CreatePlacementPolicyRequest) (string, error) {
	var parts []string
	keyword := "CREATE"
	if req.Schedule == "" {
		req.Schedule = ""
	}
	hasAdvanced := req.Constraints != "" || req.LeaderConstraints != "" ||
		req.FollowerConstraints != "" || req.LearnerConstraints != "" ||
		req.SurvivalPreferences != ""
	hasBasic := req.PrimaryRegion != "" || req.Regions != ""
	if hasBasic && hasAdvanced {
		return "", fmt.Errorf("PRIMARY_REGION/REGIONS/SCHEDULE 不能与 CONSTRAINTS 系列选项同时指定")
	}
	if req.PrimaryRegion != "" {
		parts = append(parts, fmt.Sprintf("PRIMARY_REGION = %s", sqlStr(req.PrimaryRegion)))
	}
	if req.Regions != "" {
		parts = append(parts, fmt.Sprintf("REGIONS = %s", sqlStr(req.Regions)))
	}
	if req.Schedule != "" {
		parts = append(parts, fmt.Sprintf("SCHEDULE = %s", sqlStr(req.Schedule)))
	}
	if req.Constraints != "" {
		parts = append(parts, fmt.Sprintf("CONSTRAINTS = %s", sqlStr(req.Constraints)))
	}
	if req.LeaderConstraints != "" {
		parts = append(parts, fmt.Sprintf("LEADER_CONSTRAINTS = %s", sqlStr(req.LeaderConstraints)))
	}
	if req.FollowerConstraints != "" {
		parts = append(parts, fmt.Sprintf("FOLLOWER_CONSTRAINTS = %s", sqlStr(req.FollowerConstraints)))
	}
	if req.LearnerConstraints != "" {
		parts = append(parts, fmt.Sprintf("LEARNER_CONSTRAINTS = %s", sqlStr(req.LearnerConstraints)))
	}
	if req.SurvivalPreferences != "" {
		parts = append(parts, fmt.Sprintf("SURVIVAL_PREFERENCES = %s", sqlStr(req.SurvivalPreferences)))
	}
	if req.Followers != nil {
		parts = append(parts, fmt.Sprintf("FOLLOWERS = %d", *req.Followers))
	}
	if req.Learners != nil {
		parts = append(parts, fmt.Sprintf("LEARNERS = %d", *req.Learners))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("至少需要指定一个放置选项")
	}
	return fmt.Sprintf("%s PLACEMENT POLICY %s %s", keyword, qname(req.Name), strings.Join(parts, ", ")), nil
}

// BuildAlterPlacementPolicySQL 生成 ALTER PLACEMENT POLICY 语句（字段同 CREATE，不含 keyword 差异）。
func BuildAlterPlacementPolicySQL(req model.AlterPlacementPolicyRequest) (string, error) {
	var parts []string
	hasAdvanced := req.Constraints != "" || req.LeaderConstraints != "" ||
		req.FollowerConstraints != "" || req.LearnerConstraints != "" ||
		req.SurvivalPreferences != ""
	hasBasic := req.PrimaryRegion != "" || req.Regions != ""
	if hasBasic && hasAdvanced {
		return "", fmt.Errorf("PRIMARY_REGION/REGIONS/SCHEDULE 不能与 CONSTRAINTS 系列选项同时指定")
	}
	if req.PrimaryRegion != "" {
		parts = append(parts, fmt.Sprintf("PRIMARY_REGION = %s", sqlStr(req.PrimaryRegion)))
	}
	if req.Regions != "" {
		parts = append(parts, fmt.Sprintf("REGIONS = %s", sqlStr(req.Regions)))
	}
	if req.Schedule != "" {
		parts = append(parts, fmt.Sprintf("SCHEDULE = %s", sqlStr(req.Schedule)))
	}
	if req.Constraints != "" {
		parts = append(parts, fmt.Sprintf("CONSTRAINTS = %s", sqlStr(req.Constraints)))
	}
	if req.LeaderConstraints != "" {
		parts = append(parts, fmt.Sprintf("LEADER_CONSTRAINTS = %s", sqlStr(req.LeaderConstraints)))
	}
	if req.FollowerConstraints != "" {
		parts = append(parts, fmt.Sprintf("FOLLOWER_CONSTRAINTS = %s", sqlStr(req.FollowerConstraints)))
	}
	if req.LearnerConstraints != "" {
		parts = append(parts, fmt.Sprintf("LEARNER_CONSTRAINTS = %s", sqlStr(req.LearnerConstraints)))
	}
	if req.SurvivalPreferences != "" {
		parts = append(parts, fmt.Sprintf("SURVIVAL_PREFERENCES = %s", sqlStr(req.SurvivalPreferences)))
	}
	if req.Followers != nil {
		parts = append(parts, fmt.Sprintf("FOLLOWERS = %d", *req.Followers))
	}
	if req.Learners != nil {
		parts = append(parts, fmt.Sprintf("LEARNERS = %d", *req.Learners))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("至少需要指定一个放置选项")
	}
	return fmt.Sprintf("ALTER PLACEMENT POLICY %s %s", qname(req.Name), strings.Join(parts, ", ")), nil
}

// sqlStr 生成 SQL 字符串字面量（单引号包裹，转义内部单引号）。
func sqlStr(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// CreateResourceGroup 见 §3.3
func (c *ClusterSQL) CreateResourceGroup(name string, ru int, burstable bool, priority string) (string, string) {
	burst := ""
	if burstable {
		burst = " BURSTABLE"
	}
	if priority == "" {
		priority = "MEDIUM"
	}
	q := fmt.Sprintf(`CREATE RESOURCE GROUP %s RU_PER_SEC = %d%s PRIORITY = %s`, qname(name), ru, burst, priority)
	comp := fmt.Sprintf(`DROP RESOURCE GROUP IF EXISTS %s`, qname(name))
	return q, comp
}

// CreateDatabaseWithPolicy 见 §3.2
func (c *ClusterSQL) CreateDatabaseWithPolicy(db, policy string) (string, string) {
	if policy == "" {
		q := fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s`, qname(db))
		comp := fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, qname(db))
		return q, comp
	}
	q := fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s PLACEMENT POLICY = %s`, qname(db), qname(policy))
	comp := fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, qname(db))
	return q, comp
}

// CreateUserWithResourceGroup 见 §3.4
func (c *ClusterSQL) CreateUserWithResourceGroup(user, host, pwd, rg string) (string, string) {
	q := fmt.Sprintf(`CREATE USER '%s'@'%s' IDENTIFIED BY '%s' RESOURCE GROUP %s`, user, host, pwd, qname(rg))
	comp := fmt.Sprintf(`DROP USER IF EXISTS '%s'@'%s'`, user, host)
	return q, comp
}

// qname 反引号包裹标识符，防关键字/冲突。
func qname(s string) string {
	s = strings.ReplaceAll(s, "`", "")
	return "`" + s + "`"
}
