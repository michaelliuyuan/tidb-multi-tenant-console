// PHYSICAL 联调：用 orchestrator + PD API 跑通 物理隔离租户（打标签→放置策略→绑库→用户）
// + dry-run 预估（真实库）+ 清理。标签用独立 key mttest_tag（非破坏性，结束删除）。
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/tidb-multi-tenant/console/internal/model"
	"github.com/tidb-multi-tenant/console/internal/orchestrator"
	"github.com/tidb-multi-tenant/console/internal/store"
)

const (
	host = "pepezzzz.synology.me"
	port = 24140
	user = "root"
	pass = "tidb9.0ga"
	pd   = "http://pepezzzz.synology.me:24379"
	tn   = "mttest2"
	lk   = "mttest_tag" // 独立标签键，避免误删 store 原有 zone 等标签
)

var failed bool

func main() {
	fmt.Println("===== PHYSICAL 物理隔离 · 真实联调 =====")
	db := mustOpen()
	mig, _ := os.ReadFile("migrations/0001_init.sql")
	_, err := db.Exec(string(mig))
	chk(err, "建 mt_console")
	meta := &store.Metadata{DB: db}
	target := &store.ClusterSQL{DB: mustOpen()}
	pdc := store.NewPDClient(pd)

	// 选 TiKV store（排除 tiflash：region_count>0）
	stores, err := pdc.ListStores()
	chk(err, "PD list stores")
	var storeIDs []int64
	for _, s := range stores {
		if s.RegionCount > 0 {
			storeIDs = append(storeIDs, s.ID)
		}
	}
	fmt.Printf("选定 TiKV stores: %v（排除 TiFlash）\n", storeIDs)
	if len(storeIDs) < 3 {
		fmt.Printf("⚠️  TiKV store 数=%d < 3，仍继续\n", len(storeIDs))
	}

	// 预清理
	preClean(db, pdc, storeIDs)

	// 1) 创建 HYBRID 租户（物理隔离路径）
	fmt.Println("\n[1/5] 创建物理隔离租户（打标签→放置策略→资源组→绑库→用户）...")
	orch := orchestrator.NewTenantOrchestrator(meta, target, pdc)
	req := model.CreateTenantRequest{
		Name: tn, ClusterID: 1, IsolationLevel: model.Hybrid,
		LabelKey: lk, LabelValue: tn,
		Placement:     model.PlacementSpec{PrimaryRegion: "r1", Regions: "r1", Voters: 3, Followers: 2, SurvivalPreferences: "host"},
		ResourceGroup: model.ResourceSpec{RUPerSec: 500, Burstable: true, Priority: "MEDIUM"},
		Databases:     []string{"phys"},
		Users:         []model.UserSpec{{Username: tn + "_u", Password: "MtTest#2026"}},
	}
	job, err := orch.CreateTenant(req, "integration-phys")
	if err != nil {
		fmt.Printf("❌ 创建失败: %v\n", err)
		failed = true
	} else {
		fmt.Printf("✓ job #%d 步骤：\n", job.ID)
		for _, s := range job.Steps {
			fmt.Printf("   - %-22s %s\n", s.Name, s.Status)
		}
	}

	// 2) 验证
	fmt.Println("\n[2/5] 验证产物...")
	verify(db, "Placement Policy", fmt.Sprintf(`SELECT count(*) FROM information_schema.PLACEMENT_POLICIES WHERE policy_name='t_%s_pol'`, tn))
	verify(db, "Database(绑policy)", fmt.Sprintf(`SELECT count(*) FROM information_schema.schemata WHERE schema_name='t_%s_phys'`, tn))
	verify(db, "Resource Group", fmt.Sprintf(`SELECT count(*) FROM information_schema.RESOURCE_GROUPS WHERE name='t_%s_rg'`, tn))
	verify(db, "User", fmt.Sprintf(`SELECT count(*) FROM mysql.user WHERE user='%s_u'`, tn))
	// 验证 store 标签
	sts, _ := pdc.ListStores()
	tagged := 0
	for _, s := range sts {
		fmt.Printf("   store %d labels: %v\n", s.ID, s.Labels)
		for _, l := range s.Labels {
			if l.Key == lk && l.Value == tn {
				tagged++
			}
		}
	}
	fmt.Printf("   带 %s=%s 标签的 store: %d（期望 %d）\n", lk, tn, tagged, len(storeIDs))
	if tagged == 0 {
		fmt.Println("   ❌ 标签未生效")
		failed = true
	} else {
		fmt.Println("   ✓ PD 标签已生效")
	}

	// 3) dry-run 预估（真实库）
	fmt.Println("\n[3/5] dry-run 预估（真实库）...")
	if rdb := pickRealDB(db); rdb != "" {
		fmt.Printf("   目标库: %s\n", rdb)
		res, err := store.EstimatePlacementMove(target, pdc, model.DryRunRequest{
			LabelKey: lk, LabelValue: tn, Databases: []string{rdb}, Voters: 3,
		})
		if err != nil {
			fmt.Printf("   ❌ dry-run 失败: %v\n", err)
		} else {
			fmt.Printf("   ✓ 受影响 Region=%d, 总量=%dMB, 目标池(store=%d)可用=%dMB, 需=%dMB, 容量OK=%v, 预估~%.1f分钟\n",
				res.AffectedRegions, res.TotalSizeMB, res.TargetPoolCount, res.TargetAvailMB, res.NeededMB, res.TargetCapacityOK, res.EstMinutes)
		}
	} else {
		fmt.Println("   (无可 dry-run 的用户库)")
	}

	// 4) PD placement rules 查看确认规则已下发
	fmt.Println("\n[4/5] PD placement rules 查看...")
	if rules, err := pdc.PlacementRules(); err == nil {
		snip := string(rules)
		if len(snip) > 120 {
			snip = snip[:120] + "..."
		}
		fmt.Printf("   ✓ rules 返回 %d 字节: %s\n", len(rules), snip)
	} else {
		fmt.Printf("   ⚠️  %v\n", err)
	}

	// 5) 清理
	fmt.Println("\n[5/5] 清理...")
	clean(db, pdc, storeIDs)
	exec(db, "DROP SCHEMA IF EXISTS mt_console")
	fmt.Println("\n===== 结束 =====")
	if failed {
		fmt.Println("结果：有失败项 ❌")
		os.Exit(1)
	}
	fmt.Println("结果：全部通过 ✅")
}

func pickRealDB(db *sql.DB) string {
	rows, err := db.Query(`SELECT table_schema, count(*) FROM information_schema.tables
		WHERE table_schema NOT IN ('INFORMATION_SCHEMA','METRICS_SCHEMA','PERFORMANCE_SCHEMA','mysql') GROUP BY table_schema ORDER BY count(*) DESC`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var c int
		_ = rows.Scan(&s, &c)
		if c > 0 {
			return s
		}
	}
	return ""
}

func preClean(db *sql.DB, pdc *store.PDClient, storeIDs []int64) {
	exec(db, fmt.Sprintf("DROP USER IF EXISTS '%s_u'@'%%'", tn))
	exec(db, fmt.Sprintf("DROP DATABASE IF EXISTS `t_%s_phys`", tn))
	exec(db, fmt.Sprintf("DROP RESOURCE GROUP IF EXISTS `t_%s_rg`", tn))
	exec(db, fmt.Sprintf("DROP PLACEMENT POLICY IF EXISTS `t_%s_pol`", tn))
	for _, id := range storeIDs {
		_ = pdc.DeleteStoreLabel(id, lk)
	}
}
func clean(db *sql.DB, pdc *store.PDClient, storeIDs []int64) {
	preClean(db, pdc, storeIDs)
}

func mustOpen() *sql.DB {
	d, err := store.OpenTiDB(host, port, user, pass, "")
	if err != nil {
		fmt.Println("连接失败:", err)
		os.Exit(1)
	}
	return d
}
func verify(db *sql.DB, label, q string) {
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		fmt.Printf("   ❌ %s: %v\n", label, err)
		failed = true
		return
	}
	if n > 0 {
		fmt.Printf("   ✓ %s\n", label)
	} else {
		fmt.Printf("   ❌ %s 不存在\n", label)
		failed = true
	}
}
func exec(db *sql.DB, q string) {
	if _, err := db.Exec(q); err != nil {
		fmt.Printf("   ⚠️  %s → %v\n", strings.ReplaceAll(q, "\n", " "), err)
	}
}
func chk(err error, ctx string) {
	if err != nil {
		fmt.Printf("❌ %s: %v\n", ctx, err)
		os.Exit(1)
	}
}
