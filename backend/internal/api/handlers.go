// Package api 暴露 REST 接口（详见 docs/p0-technical-design.md §2）。
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/tidb-multi-tenant/console/internal/model"
	"github.com/tidb-multi-tenant/console/internal/orchestrator"
	"github.com/tidb-multi-tenant/console/internal/store"
)

type API struct {
	Meta *store.Metadata
}

func New(meta *store.Metadata) *API { return &API{Meta: meta} }

// Register 挂载所有路由。
func (a *API) Register(r *gin.Engine) {
	r.Use(cors.Default())
	v1 := r.Group("/api/v1")
	{
			v1.GET("/clusters", a.listClusters)
			v1.POST("/clusters", a.registerCluster)

			v1.GET("/clusters/:cid/topology/stores", a.listStores)
			v1.GET("/clusters/:cid/stores/:sid/resource", a.storeResource)
			v1.PUT("/clusters/:cid/stores/:sid/labels", a.setStoreLabel)

			v1.GET("/clusters/:cid/resource-groups", a.listResourceGroups)
		v1.POST("/clusters/:cid/resource-groups", a.createResourceGroup)
		v1.PUT("/clusters/:cid/resource-groups/:rgname", a.alterResourceGroup)
		v1.DELETE("/clusters/:cid/resource-groups/:rgname", a.dropResourceGroup)

		v1.GET("/clusters/:cid/users", a.listUsers)
		v1.POST("/clusters/:cid/users", a.createUser)
		v1.PUT("/clusters/:cid/users/:username", a.updateUser)
		v1.DELETE("/clusters/:cid/users/:username", a.deleteUser)
		v1.GET("/clusters/:cid/users/:username/privileges", a.listUserPrivileges)
		v1.POST("/clusters/:cid/users/:username/privileges", a.grantPrivilege)
		v1.DELETE("/clusters/:cid/users/:username/privileges", a.revokePrivilege)

		v1.GET("/clusters/:cid/databases", a.listDatabases)
		v1.GET("/clusters/:cid/databases/:dbname/tables", a.listTables)
		v1.PUT("/clusters/:cid/databases/:dbname/policy", a.bindDatabasePolicy)
		v1.PUT("/clusters/:cid/databases/:dbname/tables/:tname/policy", a.bindTablePolicy)
		v1.GET("/clusters/:cid/placement-policies", a.listPlacementPolicies)
		v1.POST("/clusters/:cid/placement-policies", a.createPlacementPolicy)
		v1.PUT("/clusters/:cid/placement-policies/:pname", a.alterPlacementPolicy)
		v1.DELETE("/clusters/:cid/placement-policies/:pname", a.dropPlacementPolicy)
		v1.GET("/clusters/:cid/placement-labels", a.listPlacementLabels)
		v1.POST("/clusters/:cid/placement-policies/dry-run", a.placementDryRun)

			v1.GET("/clusters/:cid/metrics/ru", a.metricsRU)
			v1.GET("/clusters/:cid/metrics/summary", a.metricsSummary)
			v1.GET("/clusters/:cid/health", a.clusterHealth)

			v1.GET("/tenants", a.listTenants)
		v1.POST("/tenants", a.createTenant)
		v1.DELETE("/tenants/:tid", a.deleteTenant)
		v1.PUT("/tenants/:tid", a.updateTenant)
		v1.GET("/tenants/:tid/resource", a.tenantResource)
		v1.GET("/tenants/:tid/detail", a.tenantDetail)
			v1.POST("/tenants/:tid/suspend", a.suspendTenant)
			v1.POST("/tenants/:tid/activate", a.activateTenant)
	}
}

// clusterConn 打开目标集群的 SQL + PD 连接。
func (a *API) clusterConn(cid int64) (*store.ClusterSQL, *store.PDClient, *model.Cluster, error) {
	c, err := a.Meta.GetCluster(cid)
	if err != nil {
			return nil, nil, nil, err
	}
	db, err := store.OpenTiDB(c.TiDBHost, c.TiDBPort, c.TiDBUser, c.Password, "")
	if err != nil {
			return nil, nil, nil, err
	}
	return &store.ClusterSQL{DB: db}, store.NewPDClient(c.PDEndpoint), &c, nil
}

// clusterProm 打开目标集群的 Prometheus 客户端（基于 cluster.prometheus_url）。
func (a *API) clusterProm(cid int64) (*store.PromClient, error) {
	c, err := a.Meta.GetCluster(cid)
	if err != nil {
			return nil, err
	}
	return store.NewPromClient(c.PrometheusURL), nil
}

// metricsRU 返回按 resource_group（反查为 tenant）的 RU 消耗时序。
func (a *API) metricsRU(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	step, _ := strconv.Atoi(c.DefaultQuery("step", "60"))
	if to == 0 {
			to = nowUnix()
	}
	if from == 0 {
			from = to - 3600 // 默认近 1 小时
	}
	prom, err := a.clusterProm(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	series, err := prom.QueryRange(store.PromQLRUByGroup, unixTime(from), unixTime(to), step)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	rg2t, _ := a.Meta.TenantRGMap(cid)
	for i := range series {
			if rg := series[i].Labels["resource_group"]; rg != "" {
				if t, ok := rg2t[rg]; ok {
					series[i].Labels["tenant"] = t
				} else {
					series[i].Labels["tenant"] = rg
				}
			}
	}
	c.JSON(200, series)
}

// metricsSummary 瞬时汇总：RU 速率 / QPS / P99 / 存储。
func (a *API) metricsSummary(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	prom, err := a.clusterProm(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	summary := gin.H{}
	if s, err := prom.Query(store.PromQLQPS); err == nil {
			summary["qps"] = sumSeries(s)
	}
	if s, err := prom.Query(store.PromQLP99); err == nil {
			summary["p99_s"] = sumSeries(s)
	}
	if s, err := prom.Query(store.PromQLStorage); err == nil {
			summary["storage_bytes"] = lastValue(s)
	}
	c.JSON(200, summary)
}

// clusterHealth 连通性 + 版本能力探测（P1-c 联调前置）。
func (a *API) clusterHealth(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	cs, pd, cl, err := a.clusterConn(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	h := gin.H{"name": cl.Name}
	// TiDB 连通 + 版本
	var version string
	if e := cs.DB.QueryRow(`SELECT version()`).Scan(&version); e == nil {
			h["tidb_ok"] = true
			h["version"] = version
	} else {
			h["tidb_ok"] = false
			h["tidb_error"] = e.Error()
	}
	// 能力探测：信息 schema 是否含 placement / resource group 相关表
	if rows, e := cs.DB.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema='INFORMATION_SCHEMA' AND table_name IN ('PLACEMENT_POLICIES','RESOURCE_GROUPS')`); e == nil {
			caps := []string{}
			for rows.Next() {
				var t string
				_ = rows.Scan(&t)
				caps = append(caps, t)
			}
			rows.Close()
			h["capabilities"] = caps
	}
	// PD 可达
	if _, e := pd.ListStores(); e == nil {
			h["pd_ok"] = true
	} else {
			h["pd_ok"] = false
			h["pd_error"] = e.Error()
	}
	// Prometheus 可达
	if pc, e := a.clusterProm(cid); e == nil {
			if _, e := pc.Query("up"); e == nil {
				h["prom_ok"] = true
			} else {
				h["prom_ok"] = false
			}
	}
	c.JSON(200, h)
}

// ----- helpers -----

func nowUnix() int64             { return time.Now().Unix() }
func unixTime(u int64) time.Time { return time.Unix(u, 0) }

func sumSeries(s []store.Series) float64 {
	var sum float64
	for _, se := range s {
			if len(se.Values) > 0 {
				sum += se.Values[len(se.Values)-1][1]
			}
	}
	return sum
}

func lastValue(s []store.Series) float64 {
	for _, se := range s {
			if len(se.Values) > 0 {
				return se.Values[len(se.Values)-1][1]
			}
	}
	return 0
}

func (a *API) listClusters(c *gin.Context) {
	out, err := a.Meta.ListClusters()
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	c.JSON(200, out)
}

// extractTiDBVersion 从 SELECT version() 的返回值中提取真实 TiDB 版本。
// 输入形如 "8.0.11-TiDB-v7.1.9-0.0"，提取出 "7.1.9"；匹配失败则原样返回。
var tidbVerRe = regexp.MustCompile(`TiDB-v([0-9]+\.[0-9]+\.[0-9]+)`)

func extractTiDBVersion(raw string) string {
	m := tidbVerRe.FindStringSubmatch(raw)
	if len(m) >= 2 {
		return m[1]
	}
	return strings.TrimSpace(raw)
}

func (a *API) registerCluster(c *gin.Context) {
	var req model.CreateClusterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.TiDBPort <= 0 {
		req.TiDBPort = 4000
	}

	version := ""
	// 连通性探测：提交前实测 TiDB/PD 能否连上，连不通就拒绝入库（除非 skip_probe）。
	if !req.SkipProbe {
		// TiDB SQL 探测：SELECT version()
		db, err := store.OpenTiDB(req.TiDBHost, req.TiDBPort, req.TiDBUser, req.TiDBPassword, "")
		if err != nil {
			c.JSON(400, gin.H{"error": "TiDB 连接失败: " + err.Error(), "field": "tidb"})
			return
		}
		if err := db.QueryRow(`SELECT version()`).Scan(&version); err != nil {
			db.Close()
			c.JSON(400, gin.H{"error": "TiDB 查询版本失败: " + err.Error(), "field": "tidb"})
			return
		}
		db.Close()
		// SELECT version() 返回形如 "8.0.11-TiDB-v7.1.9-0.0"，取 "TiDB-v" 之后的版本段。
		version = extractTiDBVersion(version)

		// PD 探测：GET /pd/api/v1/version（非必填，失败仅警告不阻断）
		if req.PDEndpoint != "" {
			pd := store.NewPDClient(req.PDEndpoint)
			if _, err := pd.Version(); err != nil {
				// PD 连不上只警告，不阻断——用户可能还没开放 PD 端口转发
				log.Printf("[warn] register cluster %s: PD %s 探测失败: %v", req.Name, req.PDEndpoint, err)
			}
		}
	}

	cl := model.Cluster{
		Name:          req.Name,
		TiDBHost:      req.TiDBHost,
		TiDBPort:      req.TiDBPort,
		TiDBUser:      req.TiDBUser,
		PDEndpoint:    req.PDEndpoint,
		PrometheusURL: req.PrometheusURL,
		Version:       version,
		Status:        "active",
	}
	id, err := a.Meta.UpsertCluster(cl, req.TiDBPassword)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	cl.ID = id
	c.JSON(201, cl)
}

func (a *API) listStores(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	_, pd, _, err := a.clusterConn(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	stores, err := pd.ListStores()
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	c.JSON(200, stores)
}

func (a *API) setStoreLabel(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	sid, _ := strconv.ParseInt(c.Param("sid"), 10, 64)
	_, pd, cl, err := a.clusterConn(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	var body model.KV
	_ = c.ShouldBindJSON(&body)
	if err := pd.SetStoreLabel(sid, body.Key, body.Value); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	a.Meta.Audit("api", cid, 0, "SET_LABEL", "store", strconv.FormatInt(sid, 10), "success", body.Key+"="+body.Value)
	_ = cl
	c.JSON(200, gin.H{"ok": true})
}

// burstableBool 将 TiDB v7.1 的 BURSTABLE varchar 'YES'/'NO' 映射为 int 1/0。
func burstableBool(s string) int {
	if strings.ToUpper(strings.TrimSpace(s)) == "YES" {
		return 1
	}
	return 0
}

func (a *API) listResourceGroups(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	rows, err := cs.DB.Query(`SELECT NAME,RU_PER_SEC,PRIORITY,BURSTABLE FROM information_schema.RESOURCE_GROUPS`)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	defer rows.Close()
	type rg struct {
			Name      string `json:"name"`
			RUPerSec  int    `json:"ru_per_sec"`
			Priority  string `json:"priority"`
			Burstable int    `json:"burstable"`
	}
	var out []rg
	for rows.Next() {
			var r rg
			var burstableStr string
			_ = rows.Scan(&r.Name, &r.RUPerSec, &r.Priority, &burstableStr)
			r.Burstable = burstableBool(burstableStr)
			out = append(out, r)
	}
	c.JSON(200, out)
}

func (a *API) createResourceGroup(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	var req model.CreateResourceGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var parts []string
	if req.RUPerSec != nil {
		parts = append(parts, fmt.Sprintf("RU_PER_SEC = %d", *req.RUPerSec))
	} else {
		parts = append(parts, "RU_PER_SEC = UNLIMITED")
	}
	if req.Burstable {
		parts = append(parts, "BURSTABLE")
	}
	if req.Priority != "" {
		parts = append(parts, fmt.Sprintf("PRIORITY = %s", req.Priority))
	}
	query := fmt.Sprintf("CREATE RESOURCE GROUP IF NOT EXISTS `%s` %s",
		strings.ReplaceAll(req.Name, "`", ""), strings.Join(parts, " "))
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "CREATE_RESOURCE_GROUP", "resource_group", req.Name, "success", query)
	c.JSON(201, gin.H{"ok": true, "name": req.Name, "sql": query})
}

func (a *API) alterResourceGroup(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	rgname := c.Param("rgname")
	var req model.AlterResourceGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var parts []string
	if req.RUPerSec != nil {
		parts = append(parts, fmt.Sprintf("RU_PER_SEC = %d", *req.RUPerSec))
	}
	if req.Priority != "" {
		parts = append(parts, fmt.Sprintf("PRIORITY = %s", req.Priority))
	}
	if req.Burstable != nil {
		if *req.Burstable {
			parts = append(parts, "BURSTABLE")
		} else {
			parts = append(parts, "BURSTABLE = OFF")
		}
	}
	if len(parts) == 0 {
		c.JSON(400, gin.H{"error": "至少需要指定一个修改字段"})
		return
	}
	query := fmt.Sprintf("ALTER RESOURCE GROUP `%s` %s",
		strings.ReplaceAll(rgname, "`", ""), strings.Join(parts, " "))
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "ALTER_RESOURCE_GROUP", "resource_group", rgname, "success", query)
	c.JSON(200, gin.H{"ok": true, "name": rgname, "sql": query})
}

func (a *API) dropResourceGroup(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	rgname := c.Param("rgname")
	if rgname == "default" {
		c.JSON(400, gin.H{"error": "default 资源组不可删除"})
		return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	query := fmt.Sprintf("DROP RESOURCE GROUP IF EXISTS `%s`", strings.ReplaceAll(rgname, "`", ""))
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.Meta.Audit("api", cid, 0, "DROP_RESOURCE_GROUP", "resource_group", rgname, "success", query)
	c.JSON(200, gin.H{"ok": true})
}

// ----- 用户管理 -----

func (a *API) listUsers(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	rows, err := cs.DB.Query(`SELECT User, Host FROM mysql.user WHERE User NOT IN ('root','mysql.infoschema') ORDER BY User`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []model.DBUser
	for rows.Next() {
		var u model.DBUser
		_ = rows.Scan(&u.User, &u.Host)
		out = append(out, u)
	}
	if out == nil {
		out = []model.DBUser{}
	}
	c.JSON(200, out)
}

func (a *API) createUser(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	var req model.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if req.Host == "" {
		req.Host = "%"
	}
	rgClause := ""
	if req.ResourceGroup != "" {
		rgClause = fmt.Sprintf(" RESOURCE GROUP `%s`", strings.ReplaceAll(req.ResourceGroup, "`", ""))
	}
	query := fmt.Sprintf("CREATE USER '%s'@'%s' IDENTIFIED BY '%s'%s",
		strings.ReplaceAll(req.User, "'", "''"),
		strings.ReplaceAll(req.Host, "'", "''"),
		strings.ReplaceAll(req.Password, "'", "''"),
		rgClause)
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "CREATE_USER", "user", req.User+"@"+req.Host, "success", query)
	c.JSON(201, gin.H{"ok": true, "user": req.User, "host": req.Host})
}

func (a *API) updateUser(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	username := c.Param("username")
	host := c.DefaultQuery("host", "%")
	var req model.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var queries []string
	if req.Password != nil {
		q := fmt.Sprintf("ALTER USER '%s'@'%s' IDENTIFIED BY '%s'",
			strings.ReplaceAll(username, "'", "''"),
			strings.ReplaceAll(host, "'", "''"),
			strings.ReplaceAll(*req.Password, "'", "''"))
		queries = append(queries, q)
	}
	if req.ResourceGroup != nil {
		rg := strings.ReplaceAll(*req.ResourceGroup, "`", "")
		var q string
		if rg == "" || rg == "default" {
			q = fmt.Sprintf("ALTER USER '%s'@'%s' RESOURCE GROUP `default`",
				strings.ReplaceAll(username, "'", "''"), strings.ReplaceAll(host, "'", "''"))
		} else {
			q = fmt.Sprintf("ALTER USER '%s'@'%s' RESOURCE GROUP `%s`",
				strings.ReplaceAll(username, "'", "''"), strings.ReplaceAll(host, "'", "''"), rg)
		}
		queries = append(queries, q)
	}
	if len(queries) == 0 {
		c.JSON(400, gin.H{"error": "至少指定一个修改项（password 或 resource_group）"})
		return
	}
	for _, q := range queries {
		if _, err := cs.DB.Exec(q); err != nil {
			c.JSON(500, gin.H{"error": err.Error(), "sql": q})
			return
		}
	}
	a.Meta.Audit("api", cid, 0, "ALTER_USER", "user", username+"@"+host, "success", strings.Join(queries, "; "))
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) deleteUser(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	username := c.Param("username")
	host := c.DefaultQuery("host", "%")
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	query := fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'",
		strings.ReplaceAll(username, "'", "''"), strings.ReplaceAll(host, "'", "''"))
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.Meta.Audit("api", cid, 0, "DROP_USER", "user", username+"@"+host, "success", query)
	c.JSON(200, gin.H{"ok": true})
}

// parseGrant 将 SHOW GRANTS 的输出行解析为结构化权限项。
// 示例输入: "GRANT SELECT, INSERT ON `test`.* TO 'u1'@'%'"
func parseGrant(grant string, user, host string) model.PrivilegeEntry {
	p := model.PrivilegeEntry{
		Grantee: fmt.Sprintf("'%s'@'%s'", user, host),
	}
	// 提取权限部分：GRANT xxx ON `db`.tbl TO ...
	s := strings.TrimSpace(grant)
	if strings.HasPrefix(strings.ToUpper(s), "GRANT ") {
		s = s[6:]
	}
	// 分割 ON
	parts := strings.SplitN(s, " ON ", 2)
	if len(parts) == 2 {
		p.PrivilegeType = strings.TrimSpace(parts[0])
		// 解析 db.table 部分，如 `test`.*  或 *.*
		dbTbl := strings.TrimSpace(parts[1])
		dbTbl = strings.Split(dbTbl, " TO ")[0]
		dbTbl = strings.Trim(dbTbl, "`")
		// 分割 db 和 table
		dbParts := strings.SplitN(dbTbl, ".", 2)
		if len(dbParts) == 2 {
			p.TableSchema = strings.Trim(dbParts[0], "`")
			p.TableName = strings.Trim(dbParts[1], "`")
		}
	} else {
		p.PrivilegeType = s
	}
	return p
}

func (a *API) listUserPrivileges(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	username := c.Param("username")
	host := c.DefaultQuery("host", "%")
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// 使用 SHOW GRANTS FOR 解析权限（比 information_schema.USER_PRIVILEGES 更通用兼容）
	rows, err := cs.DB.Query(fmt.Sprintf("SHOW GRANTS FOR '%s'@'%s'",
		strings.ReplaceAll(username, "'", "''"), strings.ReplaceAll(host, "'", "''")))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var grants []string
	for rows.Next() {
		var g string
		_ = rows.Scan(&g)
		grants = append(grants, g)
	}
	// 解析 GRANT 语句为结构化权限列表
	var out []model.PrivilegeEntry
	for _, g := range grants {
		out = append(out, parseGrant(g, username, host))
	}
	if out == nil {
		out = []model.PrivilegeEntry{}
	}
	c.JSON(200, gin.H{"grants": grants, "parsed": out})
}

func (a *API) grantPrivilege(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	username := c.Param("username")
	host := c.DefaultQuery("host", "%")
	var req model.GrantPrivilegeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	tbl := req.Table
	if tbl == "" {
		tbl = "*"
	}
	query := fmt.Sprintf("GRANT %s ON `%s`.%s TO '%s'@'%s'",
		req.Privileges,
		strings.ReplaceAll(req.Database, "`", ""),
		tbl,
		strings.ReplaceAll(username, "'", "''"),
		strings.ReplaceAll(host, "'", "''"))
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "GRANT", "user", username+"@"+host, "success", query)
	c.JSON(200, gin.H{"ok": true, "sql": query})
}

func (a *API) revokePrivilege(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	username := c.Param("username")
	host := c.DefaultQuery("host", "%")
	var req model.GrantPrivilegeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	tbl := req.Table
	if tbl == "" {
		tbl = "*"
	}
	query := fmt.Sprintf("REVOKE %s ON `%s`.%s FROM '%s'@'%s'",
		req.Privileges,
		strings.ReplaceAll(req.Database, "`", ""),
		tbl,
		strings.ReplaceAll(username, "'", "''"),
		strings.ReplaceAll(host, "'", "''"))
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "REVOKE", "user", username+"@"+host, "success", query)
	c.JSON(200, gin.H{"ok": true})
}

// ----- 数据库管理 -----

func (a *API) listDatabases(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	rows, err := cs.DB.Query(`SELECT s.SCHEMA_NAME, COALESCE(s.TIDB_PLACEMENT_POLICY_NAME,''),
		COALESCE(t.cnt,0), COALESCE(t.sz,0)
		FROM information_schema.schemata s
		LEFT JOIN (
			SELECT TABLE_SCHEMA, COUNT(*) as cnt, SUM(DATA_LENGTH+INDEX_LENGTH) as sz
			FROM information_schema.TABLES GROUP BY TABLE_SCHEMA
		) t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
		WHERE s.SCHEMA_NAME NOT IN ('information_schema','mysql','performance_schema','metrics_schema','sys','INFORMATION_SCHEMA','METRICS_SCHEMA','PERFORMANCE_SCHEMA')
		ORDER BY s.SCHEMA_NAME`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []model.DatabaseInfo
	for rows.Next() {
		var d model.DatabaseInfo
		var pol sql.NullString
		_ = rows.Scan(&d.Name, &pol, &d.TableCount, &d.SizeMB)
		d.PlacementPolicy = pol.String
		d.SizeMB = d.SizeMB / (1 << 20) // B → MiB
		out = append(out, d)
	}
	if out == nil {
		out = []model.DatabaseInfo{}
	}
	c.JSON(200, out)
}

func (a *API) listTables(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	dbname := strings.ReplaceAll(c.Param("dbname"), "`", "")
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	rows, err := cs.DB.Query(`SELECT TABLE_NAME, TABLE_SCHEMA, COALESCE(TIDB_PLACEMENT_POLICY_NAME,''),
		COALESCE(TABLE_ROWS,0), COALESCE(DATA_LENGTH+INDEX_LENGTH,0), COALESCE(ENGINE,'')
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, dbname)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []model.TableInfo
	for rows.Next() {
		var t model.TableInfo
		var pol sql.NullString
		_ = rows.Scan(&t.Name, &t.Schema, &pol, &t.RowCount, &t.SizeMB, &t.Engine)
		t.PlacementPolicy = pol.String
		t.SizeMB = t.SizeMB / (1 << 20)
		out = append(out, t)
	}
	if out == nil {
		out = []model.TableInfo{}
	}
	c.JSON(200, out)
}

func (a *API) bindDatabasePolicy(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	dbname := strings.ReplaceAll(c.Param("dbname"), "`", "")
	var req model.BindPolicyRequest
	_ = c.ShouldBindJSON(&req)
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var query string
	if req.Policy == "" || req.Policy == "default" {
		query = fmt.Sprintf("ALTER DATABASE `%s` PLACEMENT POLICY=default", dbname)
	} else {
		query = fmt.Sprintf("ALTER DATABASE `%s` PLACEMENT POLICY=`%s`", dbname, strings.ReplaceAll(req.Policy, "`", ""))
	}
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "BIND_DB_POLICY", "database", dbname, "success", query)
	c.JSON(200, gin.H{"ok": true, "sql": query})
}

func (a *API) bindTablePolicy(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	dbname := strings.ReplaceAll(c.Param("dbname"), "`", "")
	tname := strings.ReplaceAll(c.Param("tname"), "`", "")
	var req model.BindPolicyRequest
	_ = c.ShouldBindJSON(&req)
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var query string
	if req.Policy == "" || req.Policy == "default" {
		query = fmt.Sprintf("ALTER TABLE `%s`.%s PLACEMENT POLICY=default", dbname, "`"+tname+"`")
	} else {
		query = fmt.Sprintf("ALTER TABLE `%s`.%s PLACEMENT POLICY=`%s`", dbname, "`"+tname+"`", strings.ReplaceAll(req.Policy, "`", ""))
	}
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "BIND_TABLE_POLICY", "table", dbname+"."+tname, "success", query)
	c.JSON(200, gin.H{"ok": true, "sql": query})
}

func (a *API) listPlacementPolicies(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// Bug#1 fix: TiDB v7.1 的 PLACEMENT_POLICIES 表没有 SURVIVAL_PREFERENCES 列，
	// 该列在更高版本才引入。这里先尝试带该列的查询，失败后回退到不含该列的查询。
	queryFull := `SELECT POLICY_ID, CATALOG_NAME, POLICY_NAME,
		COALESCE(PRIMARY_REGION,''), COALESCE(REGIONS,''),
		COALESCE(CONSTRAINTS,''), COALESCE(LEADER_CONSTRAINTS,''),
		COALESCE(FOLLOWER_CONSTRAINTS,''), COALESCE(LEARNER_CONSTRAINTS,''),
		COALESCE(SCHEDULE,''),
		COALESCE(FOLLOWERS,0), COALESCE(LEARNERS,0),
		COALESCE(SURVIVAL_PREFERENCES,'')
		FROM information_schema.PLACEMENT_POLICIES`
	queryFallback := `SELECT POLICY_ID, CATALOG_NAME, POLICY_NAME,
		COALESCE(PRIMARY_REGION,''), COALESCE(REGIONS,''),
		COALESCE(CONSTRAINTS,''), COALESCE(LEADER_CONSTRAINTS,''),
		COALESCE(FOLLOWER_CONSTRAINTS,''), COALESCE(LEARNER_CONSTRAINTS,''),
		COALESCE(SCHEDULE,''),
		COALESCE(FOLLOWERS,0), COALESCE(LEARNERS,0)
		FROM information_schema.PLACEMENT_POLICIES`

	rows, err := cs.DB.Query(queryFull)
	if err != nil {
		// 回退到不含 SURVIVAL_PREFERENCES 的查询（TiDB v7.1 等旧版本）
		rows, err = cs.DB.Query(queryFallback)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	defer rows.Close()

	// 检测实际列数以决定如何 scan
	cols, _ := rows.Columns()
	hasSurvivalPref := len(cols) == 13

	var out []model.PlacementPolicy
	for rows.Next() {
		var p model.PlacementPolicy
		if hasSurvivalPref {
			if err := rows.Scan(&p.PolicyID, &p.CatalogName, &p.PolicyName,
				&p.PrimaryRegion, &p.Regions, &p.Constraints, &p.LeaderConstraints,
				&p.FollowerConstraints, &p.LearnerConstraints, &p.Schedule,
				&p.Followers, &p.Learners, &p.SurvivalPreferences); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
		} else {
			if err := rows.Scan(&p.PolicyID, &p.CatalogName, &p.PolicyName,
				&p.PrimaryRegion, &p.Regions, &p.Constraints, &p.LeaderConstraints,
				&p.FollowerConstraints, &p.LearnerConstraints, &p.Schedule,
				&p.Followers, &p.Learners); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
		}
		out = append(out, p)
	}
	c.JSON(200, out)
}

// getPlacementPolicyByName 从 information_schema 读取单个策略的当前完整配置，
// 用于 ALTER 前的 overlay（Bug#2 fix: ALTER 是全量替换，需先取当前值再 merge 提交改动）。
func (a *API) getPlacementPolicyByName(cs *store.ClusterSQL, pname string) (*model.PlacementPolicy, error) {
	queryFull := `SELECT POLICY_ID, CATALOG_NAME, POLICY_NAME,
		COALESCE(PRIMARY_REGION,''), COALESCE(REGIONS,''),
		COALESCE(CONSTRAINTS,''), COALESCE(LEADER_CONSTRAINTS,''),
		COALESCE(FOLLOWER_CONSTRAINTS,''), COALESCE(LEARNER_CONSTRAINTS,''),
		COALESCE(SCHEDULE,''),
		COALESCE(FOLLOWERS,0), COALESCE(LEARNERS,0),
		COALESCE(SURVIVAL_PREFERENCES,'')
		FROM information_schema.PLACEMENT_POLICIES WHERE POLICY_NAME = ?`
	queryFallback := `SELECT POLICY_ID, CATALOG_NAME, POLICY_NAME,
		COALESCE(PRIMARY_REGION,''), COALESCE(REGIONS,''),
		COALESCE(CONSTRAINTS,''), COALESCE(LEADER_CONSTRAINTS,''),
		COALESCE(FOLLOWER_CONSTRAINTS,''), COALESCE(LEARNER_CONSTRAINTS,''),
		COALESCE(SCHEDULE,''),
		COALESCE(FOLLOWERS,0), COALESCE(LEARNERS,0)
		FROM information_schema.PLACEMENT_POLICIES WHERE POLICY_NAME = ?`

	var p model.PlacementPolicy
	err := cs.DB.QueryRow(queryFull, pname).Scan(
		&p.PolicyID, &p.CatalogName, &p.PolicyName,
		&p.PrimaryRegion, &p.Regions, &p.Constraints, &p.LeaderConstraints,
		&p.FollowerConstraints, &p.LearnerConstraints, &p.Schedule,
		&p.Followers, &p.Learners, &p.SurvivalPreferences)
	if err != nil {
		// 回退到不含 SURVIVAL_PREFERENCES 的查询
		err = cs.DB.QueryRow(queryFallback, pname).Scan(
			&p.PolicyID, &p.CatalogName, &p.PolicyName,
			&p.PrimaryRegion, &p.Regions, &p.Constraints, &p.LeaderConstraints,
			&p.FollowerConstraints, &p.LearnerConstraints, &p.Schedule,
			&p.Followers, &p.Learners)
		if err != nil {
			return nil, err
		}
	}
	return &p, nil
}

func (a *API) createPlacementPolicy(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	var req model.CreatePlacementPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cs, _, cl, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	query, err := store.BuildCreatePlacementPolicySQL(req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "CREATE_PLACEMENT_POLICY", "policy", req.Name, "success", query)
	_ = cl
	c.JSON(201, gin.H{"ok": true, "name": req.Name, "sql": query})
}

func (a *API) alterPlacementPolicy(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	pname := c.Param("pname")
	var req model.AlterPlacementPolicyRequest
	// Bug#3 fix: name 从 URL 路径参数获取，body 中可省略
	if err := c.ShouldBindJSON(&req); err != nil {
		// 如果 body 为空或绑定失败，用路径参数继续
		req = model.AlterPlacementPolicyRequest{}
	}
	req.Name = pname
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Bug#2 fix: ALTER PLACEMENT POLICY 是全量替换，不能只提交改动字段。
	// 先读取当前策略的完整配置，再用提交的改动 overlay，最后构建完整的 ALTER 语句。
	existing, err := a.getPlacementPolicyByName(cs, pname)
	if err != nil {
		c.JSON(404, gin.H{"error": fmt.Sprintf("placement policy %s not found: %v", pname, err)})
		return
	}

	// overlay：只有非空/非零的提交字段覆盖现有值
	overlayReq := model.CreatePlacementPolicyRequest{Name: pname}
	overlayReq.PrimaryRegion = existing.PrimaryRegion
	overlayReq.Regions = existing.Regions
	overlayReq.Schedule = existing.Schedule
	overlayReq.Constraints = existing.Constraints
	overlayReq.LeaderConstraints = existing.LeaderConstraints
	overlayReq.FollowerConstraints = existing.FollowerConstraints
	overlayReq.LearnerConstraints = existing.LearnerConstraints
	overlayReq.SurvivalPreferences = existing.SurvivalPreferences
	overlayReq.Followers = &existing.Followers
	overlayReq.Learners = &existing.Learners

	// 用提交的值覆盖（仅覆盖非空字段）
	if req.PrimaryRegion != "" {
		overlayReq.PrimaryRegion = req.PrimaryRegion
	}
	if req.Regions != "" {
		overlayReq.Regions = req.Regions
	}
	if req.Schedule != "" {
		overlayReq.Schedule = req.Schedule
	}
	if req.Constraints != "" {
		overlayReq.Constraints = req.Constraints
	}
	if req.LeaderConstraints != "" {
		overlayReq.LeaderConstraints = req.LeaderConstraints
	}
	if req.FollowerConstraints != "" {
		overlayReq.FollowerConstraints = req.FollowerConstraints
	}
	if req.LearnerConstraints != "" {
		overlayReq.LearnerConstraints = req.LearnerConstraints
	}
	if req.SurvivalPreferences != "" {
		overlayReq.SurvivalPreferences = req.SurvivalPreferences
	}
	if req.Followers != nil {
		overlayReq.Followers = req.Followers
	}
	if req.Learners != nil {
		overlayReq.Learners = req.Learners
	}

	// 用合并后的完整配置生成 ALTER（复用 CREATE 的 builder 逻辑）
	query, err := store.BuildCreatePlacementPolicySQL(overlayReq)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	// 把 CREATE 替换为 ALTER
	query = strings.Replace(query, "CREATE PLACEMENT POLICY", "ALTER PLACEMENT POLICY", 1)

	if _, err := cs.DB.Exec(query); err != nil {
		c.JSON(500, gin.H{"error": err.Error(), "sql": query})
		return
	}
	a.Meta.Audit("api", cid, 0, "ALTER_PLACEMENT_POLICY", "policy", pname, "success", query)
	c.JSON(200, gin.H{"ok": true, "name": pname, "sql": query})
}

func (a *API) dropPlacementPolicy(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	pname := c.Param("pname")
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	query := fmt.Sprintf("DROP PLACEMENT POLICY IF EXISTS `%s`", strings.ReplaceAll(pname, "`", ""))
	if _, err := cs.DB.Exec(query); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "still in use") {
			c.JSON(409, gin.H{"error": fmt.Sprintf("放置策略 %s 正在被表/数据库使用，无法删除。请先解除绑定（ALTER ... PLACEMENT POLICY=default）", pname)})
		} else {
			c.JSON(500, gin.H{"error": errMsg})
		}
		return
	}
	a.Meta.Audit("api", cid, 0, "DROP_PLACEMENT_POLICY", "policy", pname, "success", query)
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) listPlacementLabels(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	rows, err := cs.DB.Query(`SHOW PLACEMENT LABELS`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []model.PlacementLabel
	for rows.Next() {
		var l model.PlacementLabel
		var valsJSON string
		if err := rows.Scan(&l.Key, &valsJSON); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		// valsJSON 格式如 ["us-east-1","us-west-1"]，尝试 JSON 解析
		if err := json.Unmarshal([]byte(valsJSON), &l.Values); err != nil {
			l.Values = []string{valsJSON}
		}
		out = append(out, l)
	}
	c.JSON(200, out)
}

func (a *API) placementDryRun(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	var req model.DryRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
	}
	cs, pd, _, err := a.clusterConn(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	res, err := store.EstimatePlacementMove(cs, pd, req)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	c.JSON(200, res)
}

func (a *API) listTenants(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Query("cluster_id"), 10, 64)
	out, err := a.Meta.ListTenants(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	if out == nil {
		out = []model.Tenant{}
	}
	c.JSON(200, out)
}

func (a *API) createTenant(c *gin.Context) {
	var req model.CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
	}
	if req.LabelKey == "" {
			req.LabelKey = "zone"
	}
	cs, pd, _, err := a.clusterConn(req.ClusterID)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	orch := orchestrator.NewTenantOrchestrator(a.Meta, cs, pd)
	job, err := orch.CreateTenant(req, "api")
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error(), "job": job})
			return
	}
	c.JSON(201, job)
}

func (a *API) deleteTenant(c *gin.Context) {
	tid, _ := strconv.ParseInt(c.Param("tid"), 10, 64)
	t, err := a.Meta.GetTenant(tid)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(404, gin.H{"error": "tenant not found"})
		} else {
			c.JSON(500, gin.H{"error": err.Error()})
		}
		return
	}
	cs, pd, _, err := a.clusterConn(t.ClusterID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	orch := orchestrator.NewTenantOrchestrator(a.Meta, cs, pd)
	if err := orch.DeleteTenant(t, "api"); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) updateTenant(c *gin.Context) {
	tid, _ := strconv.ParseInt(c.Param("tid"), 10, 64)
	var req model.UpdateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	t, err := a.Meta.GetTenant(tid)
	if err != nil {
		c.JSON(404, gin.H{"error": "tenant not found"})
		return
	}
	cs, _, _, err := a.clusterConn(t.ClusterID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 修改 Resource Group（ALTER RESOURCE GROUP）
	if t.ResourceGroup != "" {
		var parts []string
		if req.RUPerSec != nil {
			parts = append(parts, fmt.Sprintf("RU_PER_SEC = %d", *req.RUPerSec))
		}
		if req.Priority != "" {
			parts = append(parts, fmt.Sprintf("PRIORITY = %s", req.Priority))
		}
		if req.Burstable != nil {
			if *req.Burstable {
				parts = append(parts, "BURSTABLE")
			} else {
				parts = append(parts, "BURSTABLE = OFF")
			}
		}
		if len(parts) > 0 {
			query := fmt.Sprintf("ALTER RESOURCE GROUP `%s` %s", t.ResourceGroup, strings.Join(parts, ", "))
			if _, err := cs.DB.Exec(query); err != nil {
				c.JSON(500, gin.H{"error": err.Error(), "sql": query})
				return
			}
			a.Meta.Audit("api", t.ClusterID, tid, "UPDATE_TENANT", "tenant", t.Name, "success", query)
		}
	}

	// 更新元数据
	ru := t.RUPerSec
	if req.RUPerSec != nil {
		ru = *req.RUPerSec
	}
	prio := t.Priority
	if req.Priority != "" {
		prio = req.Priority
	}
	_ = a.Meta.UpdateTenantRU(tid, ru, prio)

	// 更新 label_value（影响 store 匹配）
	if req.LabelValue != nil {
		_ = a.Meta.UpdateTenantLabel(tid, *req.LabelValue)
		a.Meta.Audit("api", t.ClusterID, tid, "UPDATE_TENANT_LABEL", "tenant", t.Name, "success", "label_value="+*req.LabelValue)
	}

	c.JSON(200, gin.H{"ok": true})
}

func (a *API) tenantResource(c *gin.Context) {
	tid, _ := strconv.ParseInt(c.Param("tid"), 10, 64)
	t, err := a.Meta.GetTenant(tid)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(404, gin.H{"error": "tenant not found"})
		} else {
			c.JSON(500, gin.H{"error": err.Error()})
		}
		return
	}
	cs, pd, _, err := a.clusterConn(t.ClusterID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	detail := model.TenantResourceDetail{
		IsolationLevel: string(t.IsolationLevel),
	}

	// Resource Group 配置
	if t.ResourceGroup != "" {
		var rg model.ResourceGroupDetail
		// TiDB v7.1: BURSTABLE 是 varchar(3) 'YES'/'NO'，按字符串扫描再映射
		var burstableStr string
		err := cs.DB.QueryRow(`SELECT NAME, RU_PER_SEC, PRIORITY, BURSTABLE FROM information_schema.RESOURCE_GROUPS WHERE NAME = ?`, t.ResourceGroup).
			Scan(&rg.Name, &rg.RUPerSec, &rg.Priority, &burstableStr)
		if err == nil {
			rg.Burstable = burstableBool(burstableStr)
			detail.ResourceGroup = &rg
		} else {
			// fallback: 不含 BURSTABLE 列
			err = cs.DB.QueryRow(`SELECT NAME, RU_PER_SEC, PRIORITY FROM information_schema.RESOURCE_GROUPS WHERE NAME = ?`, t.ResourceGroup).
				Scan(&rg.Name, &rg.RUPerSec, &rg.Priority)
			if err == nil {
				detail.ResourceGroup = &rg
			}
		}
	}

	// TiKV 实例（物理隔离时按 label 过滤）
	stores, err := pd.ListStores()
	if err == nil {
		byLabel := t.LabelKey != "" && t.LabelValue != ""
		matched := make([]model.Store, 0)
		for _, s := range stores {
			if !byLabel {
				continue // 逻辑隔离不展示 TiKV
			}
			for _, l := range s.Labels {
				if l.Key == t.LabelKey && l.Value == t.LabelValue {
					matched = append(matched, s)
					break
				}
			}
		}
		detail.Stores = matched
		if byLabel {
			detail.StoreMatchMode = "label"
		} else {
			detail.StoreMatchMode = "all"
		}
	}

	// Placement Policy 详情
	if t.PlacementPolicy != "" && t.IsolationLevel != model.Logical {
		var p model.PlacementPolicy
		var pr, rg sql.NullString
		err := cs.DB.QueryRow(`SELECT POLICY_NAME,
			COALESCE(PRIMARY_REGION,''), COALESCE(REGIONS,''),
			COALESCE(CONSTRAINTS,''), COALESCE(LEADER_CONSTRAINTS,''),
			COALESCE(FOLLOWER_CONSTRAINTS,''), COALESCE(LEARNER_CONSTRAINTS,''),
			COALESCE(SCHEDULE,''), COALESCE(FOLLOWERS,0), COALESCE(LEARNERS,0)
			FROM information_schema.PLACEMENT_POLICIES WHERE POLICY_NAME = ?`, t.PlacementPolicy).
			Scan(&p.PolicyName, &pr, &rg, &p.Constraints, &p.LeaderConstraints,
				&p.FollowerConstraints, &p.LearnerConstraints, &p.Schedule,
				&p.Followers, &p.Learners)
		if err == nil {
			p.PrimaryRegion = pr.String
			p.Regions = rg.String
			detail.PlacementPolicy = &p
		}
	}

	c.JSON(200, detail)
}
// 注意：TiDB v7.1 不允许 RU_PER_SEC=0（报 "unknown resource group mode"），0 在语义上=无限。
// 因此挂起用 1（实际近乎停摆），恢复时还原元数据中的原配额。
const suspendResourceRUMin = 1

func (a *API) suspendTenant(c *gin.Context) {
	tid, _ := strconv.ParseInt(c.Param("tid"), 10, 64)
	cid, rg, ru, err := a.Meta.GetTenantRG(tid)
	if err != nil {
			tenantNotFound(c, err)
			return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	if _, err := cs.DB.Exec(fmt.Sprintf("ALTER RESOURCE GROUP `%s` RU_PER_SEC = %d", rg, suspendResourceRUMin)); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	_ = a.Meta.UpdateTenantStatus(tid, model.Suspended, ru) // 保留原 ru 供恢复
	a.Meta.Audit("api", cid, tid, "SUSPEND", "tenant", "", "success", fmt.Sprintf("RU %d→%d", ru, suspendResourceRUMin))
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) activateTenant(c *gin.Context) {
	tid, _ := strconv.ParseInt(c.Param("tid"), 10, 64)
	cid, rg, ru, err := a.Meta.GetTenantRG(tid)
	if err != nil {
			tenantNotFound(c, err)
			return
	}
	cs, _, _, err := a.clusterConn(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	restore := ru
	if restore < suspendResourceRUMin {
			restore = 1000 // 兜底：元数据无配额时给默认
	}
	if _, err := cs.DB.Exec(fmt.Sprintf("ALTER RESOURCE GROUP `%s` RU_PER_SEC = %d", rg, restore)); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}
	_ = a.Meta.UpdateTenantStatus(tid, model.Active, restore)
	a.Meta.Audit("api", cid, tid, "ACTIVATE", "tenant", "", "success", fmt.Sprintf("RU→%d", restore))
	c.JSON(200, gin.H{"ok": true})
}

// tenantDetail 返回租户关联视图：绑定的数据库（含大小）+ 承载数据的 TiKV 实例。
//
// 关联逻辑：
//   - databases：来自元数据表 mt_console.tenant_database；大小由目标集群 information_schema.TABLES 汇总（只读安全）。
//   - stores：从 PD /pd/api/v1/stores 取全量后过滤。
//     PHYSICAL/HYBRID：按 tenant 的 label_key=label_value 过滤（物理隔离池）。
//     LOGICAL：无物理亲和，返回全量 store 并标记 shared=true。
func (a *API) tenantDetail(c *gin.Context) {
	tid, _ := strconv.ParseInt(c.Param("tid"), 10, 64)

	t, err := a.Meta.GetTenant(tid)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(404, gin.H{"error": "tenant not found"})
		} else {
			c.JSON(500, gin.H{"error": err.Error()})
		}
		return
	}

	// 关联数据库（元数据）
	dbNames, err := a.Meta.GetTenantDatabases(tid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 连接目标集群 + PD
	cs, pd, _, err := a.clusterConn(t.ClusterID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 数据库大小（按 schema 汇总，Go 侧过滤到租户库）
	sizeMB := map[string]int64{}
	tblCnt := map[string]int{}
	if rows, e := cs.DB.Query(`SELECT TABLE_SCHEMA, COALESCE(SUM(DATA_LENGTH+INDEX_LENGTH),0), COUNT(*) FROM information_schema.TABLES GROUP BY TABLE_SCHEMA`); e == nil {
		for rows.Next() {
			var schema string
			var size int64
			var cnt int64
			if rows.Scan(&schema, &size, &cnt) == nil {
				sizeMB[schema] = size / (1 << 20) // B → MiB
				tblCnt[schema] = int(cnt)
			}
		}
		rows.Close()
	}

	type dbInfo struct {
		Name       string `json:"name"`
		SizeMB     int64  `json:"size_mb"`
		TableCount int    `json:"table_count"`
	}
	databases := make([]dbInfo, 0, len(dbNames))
	for _, d := range dbNames {
		databases = append(databases, dbInfo{Name: d, SizeMB: sizeMB[d], TableCount: tblCnt[d]})
	}

	// 关联 TiKV store
	stores, err := pd.ListStores()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	byLabel := t.LabelKey != "" && t.LabelValue != ""
	matched := make([]model.Store, 0)
	for _, s := range stores {
		if !byLabel {
			matched = append(matched, s)
			continue
		}
		for _, l := range s.Labels {
			if l.Key == t.LabelKey && l.Value == t.LabelValue {
				matched = append(matched, s)
				break
			}
		}
	}

	c.JSON(200, gin.H{
		"tenant":            t,
		"databases":         databases,
		"stores":            matched,
		"store_match_mode":  map[bool]string{true: "label", false: "all"}[byLabel],
		"store_shared_note": map[bool]string{true: "", false: "逻辑隔离：无物理亲和，数据分布在全部 TiKV 节点"}[byLabel],
		"total_store_count": len(stores),
	})
}

// storeResource 获取单个 TiKV 实例的资源上限。
// 通过 sshpass SSH 到 TiKV 节点执行 systemctl cat tikv-{port}.service 获取 MemoryLimit/CPUQuota。
func (a *API) storeResource(c *gin.Context) {
	cid, _ := strconv.ParseInt(c.Param("cid"), 10, 64)
	sid, _ := strconv.ParseInt(c.Param("sid"), 10, 64)

	cl, err := a.Meta.GetCluster(cid)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}

	// 从 PD stores 中找到对应 store 的 address（用于提取 host:port）
	pd := store.NewPDClient(cl.PDEndpoint)
	stores, err := pd.ListStores()
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}

	var storeAddr string
	for _, s := range stores {
			if s.ID == sid {
				storeAddr = s.Address
				break
			}
	}
	if storeAddr == "" {
			c.JSON(404, gin.H{"error": "store not found"})
			return
	}

	// 用集群密码作为 TiKV 节点 SSH 密码
	src := store.NewStoreResourceClient(cl.Password)
	res, err := src.FetchResource(storeAddr)
	if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
	}

	c.JSON(200, gin.H{
			"store_id":         sid,
			"memory_limit":     res.MemoryLimit,
			"memory_limit_fmt": formatMemoryFmt(res.MemoryLimit),
			"memory_current":   res.MemoryCurrent,
			"memory_usage_pct": res.MemoryUsagePct,
			"cpu_quota":        res.CPUQuota,
			"cpu_usage_nsec":   res.CPUUsageNSec,
			"read_bandwidth":   res.ReadBandwidth,
			"write_bandwidth":  res.WriteBandwidth,
			"block_cache_size": res.BlockCacheSize,
			"resource_control": res.ResourceControl,
	})
}

var _ = http.StatusOK

// tenantNotFound 按 id 查不到租户时返回干净的 404（sql.ErrNoRows），其它错误仍 500。
func tenantNotFound(c *gin.Context, err error) {
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "tenant not found"})
	} else {
		c.JSON(500, gin.H{"error": err.Error()})
	}
}

func formatMemoryFmt(s string) string {
	if s == "" || s == "infinity" {
			return "不限制"
	}
	// 如果是纯数字（字节），转为人类可读
	n, err := strconv.ParseUint(s, 10, 64)
	if err == nil {
			if n >= 1<<30 {
				return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
			}
			if n >= 1<<20 {
				return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
			}
			return fmt.Sprintf("%d B", n)
	}
	return s
}
