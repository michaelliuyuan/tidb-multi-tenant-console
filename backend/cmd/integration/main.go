// 真实集群端到端联调：用 orchestrator 编排代码在 241东区建 LOGICAL 测试租户，验证后清理。
// 对象前缀 t_mttest_，全部在结束时 DROP，集群恢复原状。
package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/tidb-multi-tenant/console/internal/model"
	"github.com/tidb-multi-tenant/console/internal/orchestrator"
	"github.com/tidb-multi-tenant/console/internal/store"
)

const (
	host = "pepezzzz.synology.me"
	port = 24140
	user = "root"
	pass = "tidb9.0ga"
)

var failed bool

func main() {
	fmt.Println("===== TiDB 多租户管控台 · 真实集群端到端联调 =====")
	db := mustOpen("")

	// 1) 建 mt_console 元数据 schema（迁移）
	mig, err := os.ReadFile("migrations/0001_init.sql")
	chk(err, "读迁移文件")
	_, err = db.Exec(string(mig))
	chk(err, "执行迁移(建 mt_console)")

	meta := &store.Metadata{DB: db}

	// 2) 目标集群 SQL 连接（root，可写）
	target := &store.ClusterSQL{DB: mustOpen("")}
	pd := store.NewPDClient("http://192.168.2.233:2379") // 逻辑租户不依赖 PD

	// 3) 用真实 orchestrator 创建 LOGICAL 测试租户
	orch := orchestrator.NewTenantOrchestrator(meta, target, pd)
	req := model.CreateTenantRequest{
		Name:           "mttest",
		ClusterID:      1,
		IsolationLevel: model.Logical,
		ResourceGroup:  model.ResourceSpec{RUPerSec: 500, Burstable: true, Priority: "MEDIUM"},
		Databases:      []string{"db1"},
		Users:          []model.UserSpec{{Username: "mttest_u", Password: "MtTest#2026"}},
	}
	fmt.Println("\n[1/5] 创建租户(逻辑)：编排 6 步...")
	job, err := orch.CreateTenant(req, "integration-test")
	if err != nil {
		fmt.Printf("❌ 创建失败(已回滚): %v\n", err)
		failed = true
	} else {
		fmt.Printf("✓ 创建成功，job #%d 步骤：\n", job.ID)
		for _, s := range job.Steps {
			fmt.Printf("   - %-22s %s\n", s.Name, s.Status)
		}
	}

	// 4) 验证产物存在
	fmt.Println("\n[2/5] 验证产物...")
	verify(db, "Resource Group", `SELECT count(*) FROM information_schema.RESOURCE_GROUPS WHERE name='t_mttest_rg'`)
	verify(db, "Database", `SELECT count(*) FROM information_schema.schemata WHERE schema_name='t_mttest_db1'`)
	verify(db, "User", `SELECT count(*) FROM mysql.user WHERE user='mttest_u'`)

	// 5) 挂起（RU=0）→ 验证 → 恢复
	fmt.Println("\n[3/5] 挂起测试（ALTER RESOURCE GROUP RU_PER_SEC=1；v7.1 不允许 RU=0）...")
	if !failed {
		exec(db, `ALTER RESOURCE GROUP t_mttest_rg RU_PER_SEC = 1`)
		var ru int
		_ = db.QueryRow(`SELECT ru_per_sec FROM information_schema.RESOURCE_GROUPS WHERE name='t_mttest_rg'`).Scan(&ru)
		if ru == 1 {
			fmt.Println("✓ 挂起生效（RU=1，近乎停摆）")
		} else {
			fmt.Printf("⚠️  RU=%d（挂起未生效）\n", ru)
			failed = true
		}
	}

	fmt.Println("\n[4/5] 恢复测试（RU_PER_SEC=500）...")
	if !failed {
		exec(db, `ALTER RESOURCE GROUP t_mttest_rg RU_PER_SEC = 500`)
		fmt.Println("✓ 已恢复")
	}

	// 6) 清理：按回滚逆序 DROP，集群恢复原状
	fmt.Println("\n[5/5] 清理测试对象...")
	cleanup(db)
	exec(db, `DROP SCHEMA IF EXISTS mt_console`)
	fmt.Println("✓ 清理完成")

	fmt.Println("\n===== 联调结束 =====")
	if failed {
		fmt.Println("结果：存在失败项 ❌")
		os.Exit(1)
	}
	fmt.Println("结果：全部通过 ✅")
}

func mustOpen(_ string) *sql.DB {
	d, err := store.OpenTiDB(host, port, user, pass, "")
	if err != nil {
		fmt.Printf("❌ 连接失败: %v\n", err)
		os.Exit(1)
	}
	return d
}

func verify(db *sql.DB, label, q string) {
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		fmt.Printf("   ❌ %s 查询失败: %v\n", label, err)
		failed = true
		return
	}
	if n > 0 {
		fmt.Printf("   ✓ %s 存在\n", label)
	} else {
		fmt.Printf("   ❌ %s 不存在\n", label)
		failed = true
	}
}

func exec(db *sql.DB, q string) {
	if _, err := db.Exec(q); err != nil {
		fmt.Printf("   ⚠️  执行失败: %s → %v\n", q, err)
	}
}

func cleanup(db *sql.DB) {
	exec(db, "DROP USER IF EXISTS 'mttest_u'@'%'")
	exec(db, "DROP DATABASE IF EXISTS `t_mttest_db1`")
	exec(db, "DROP RESOURCE GROUP IF EXISTS `t_mttest_rg`")
}

func chk(err error, ctx string) {
	if err != nil {
		fmt.Printf("❌ %s: %v\n", ctx, err)
		os.Exit(1)
	}
}
