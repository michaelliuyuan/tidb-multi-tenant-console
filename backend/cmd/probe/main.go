// 联调探测：用本项目 store 包连真实 TiDB，验证读路径（连通/版本/能力/已有 RG与policy/Region 视图/PD stores）。
// 只读，不在共享集群留任何对象。
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tidb-multi-tenant/console/internal/store"
)

func main() {
	host := envOr("TIDB_HOST", "120.131.12.7")
	port := atoi(envOr("TIDB_PORT", "6000"))
	user := envOr("TIDB_USER", "readonly")
	pass := envOr("TIDB_PASS", "readonly@123")
	pd := os.Getenv("PD_ENDPOINT")

	fmt.Printf("== 连接 %s@%s:%d ==\n", user, host, port)
	db, err := store.OpenTiDB(host, port, user, pass, "")
	if err != nil {
		fmt.Printf("❌ 连接失败: %v\n", err)
		return
	}
	fmt.Println("✓ TiDB 连通")

	var ver string
	if err := db.QueryRow(`SELECT version()`).Scan(&ver); err == nil {
		fmt.Printf("✓ 版本: %s\n", ver)
	}

	// 能力：信息表存在性
	rows, err := db.Query(`SELECT table_name FROM information_schema.tables
		WHERE table_schema='INFORMATION_SCHEMA' AND table_name IN
		('PLACEMENT_POLICIES','RESOURCE_GROUPS','TIKV_REGION_STATUS','TIKV_REGION_PEERS')`)
	if err == nil {
		var caps []string
		for rows.Next() {
			var t string
			rows.Scan(&t)
			caps = append(caps, t)
		}
		rows.Close()
		fmt.Printf("✓ 能力(info表): %s\n", strings.Join(caps, ", "))
	}

	// 已有 resource group
	if names, err := col(db, `SELECT name FROM information_schema.RESOURCE_GROUPS`); err == nil {
		fmt.Printf("✓ Resource Groups(%d): %s\n", len(names), trunc(names))
	} else {
		fmt.Printf("⚠️  RESOURCE_GROUPS 查询失败: %v\n", err)
	}

	// 已有 placement policy
	if names, err := col(db, `SELECT policy_name FROM information_schema.PLACEMENT_POLICIES`); err == nil {
		fmt.Printf("✓ Placement Policies(%d): %s\n", len(names), trunc(names))
	} else {
		fmt.Printf("⚠️  PLACEMENT_POLICIES 查询失败: %v\n", err)
	}

	// Region 视图是否可查（不扫全量，limit 1）
	if _, err := db.Query(`SELECT 1 FROM information_schema.TIKV_REGION_STATUS LIMIT 1`); err == nil {
		fmt.Println("✓ TIKV_REGION_STATUS 可查（dry-run 预估可用）")
	} else {
		fmt.Printf("⚠️  TIKV_REGION_STATUS 不可查: %v\n", err)
	}

	// PD stores
	if pd != "" {
		fmt.Printf("\n== PD %s ==\n", pd)
		pc := store.NewPDClient(pd)
		if sts, err := pc.ListStores(); err == nil {
			fmt.Printf("✓ TiKV stores: %d\n", len(sts))
			for i, s := range sts {
				if i >= 5 {
					break
				}
				lab := ""
				for _, l := range s.Labels {
					lab += l.Key + "=" + l.Value + " "
				}
				fmt.Printf("   store %d %s region=%d [%s]\n", s.ID, s.Address, s.RegionCount, lab)
			}
		} else {
			fmt.Printf("❌ PD 不可达: %v\n", err)
		}
	} else {
		fmt.Println("\n(i) 未提供 PD_ENDPOINT，跳过拓扑/标签探测")
	}
	fmt.Println("\n== 探测完成 ==")
}

func col(db *sql.DB, q string) ([]string, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return out, err
		}
		out = append(out, s)
	}
	return out, nil
}
func trunc(s []string) string {
	if len(s) == 0 {
		return "(无)"
	}
	if len(s) > 8 {
		return strings.Join(s[:8], ", ") + " ..."
	}
	return strings.Join(s, ", ")
}
func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
