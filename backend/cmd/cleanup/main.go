// 清理联调残留 + dump 原始 PD /stores JSON（排查 SetStoreLabel 标签错乱）。
package main

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tidb-multi-tenant/console/internal/store"
)

func main() {
	db := mustOpen()
	pdc := store.NewPDClient("http://pepezzzz.synology.me:24379")

	// 1) dump 原始 PD stores JSON（看标签真实结构）
	fmt.Println("== 原始 /pd/api/v1/stores（标签片段）==")
	resp, _ := http.Get("http://pepezzzz.synology.me:24379/pd/api/v1/stores")
	if resp != nil {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		s := string(b)
		// 截取第一个 store 的 labels 附近
		if i := strings.Index(s, "\"labels\""); i >= 0 {
			fmt.Println("   ...", s[i:i+min(160, len(s)-i)], "...")
		}
	}

	// 2) SQL 清理（正确名称）
	fmt.Println("\n== SQL 清理 ==")
	exec(db, "DROP USER IF EXISTS 'mttest2_u'@'%'")
	exec(db, "DROP USER IF EXISTS 't_mttest2_u'@'%'")
	exec(db, "DROP DATABASE IF EXISTS `t_mttest2_phys`")
	exec(db, "DROP RESOURCE GROUP IF EXISTS `t_mttest2_rg`")
	exec(db, "DROP PLACEMENT POLICY IF EXISTS `t_mttest2_pol`")
	exec(db, "DROP SCHEMA IF EXISTS mt_console")

	// 3) 标签清理：读取每个 TiKV store 现有标签，逐个删除（恢复原状=[]）
	fmt.Println("\n== 标签清理 ==")
	sts, _ := pdc.ListStores()
	for _, s := range sts {
		if s.RegionCount == 0 {
			continue // 跳过 TiFlash
		}
		fmt.Printf("   store %d 现有标签: %v\n", s.ID, s.Labels)
		for _, l := range s.Labels {
			if err := pdc.DeleteStoreLabel(s.ID, l.Key); err != nil {
				fmt.Printf("   ⚠️  删 store %d 标签 %s: %v\n", s.ID, l.Key, err)
			}
		}
	}
	// 复核
	sts2, _ := pdc.ListStores()
	for _, s := range sts2 {
		if s.RegionCount > 0 && len(s.Labels) > 0 {
			fmt.Printf("   ❌ store %d 仍有标签: %v\n", s.ID, s.Labels)
		}
	}
	fmt.Println("\n== 清理完成 ==")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func mustOpen() *sql.DB {
	d, err := store.OpenTiDB("pepezzzz.synology.me", 24140, "root", "tidb9.0ga", "")
	if err != nil {
		fmt.Println("连接失败:", err)
		return nil
	}
	return d
}
func exec(db *sql.DB, q string) {
	if _, err := db.Exec(q); err != nil {
		fmt.Printf("   ⚠️  %s → %v\n", q, err)
	} else {
		fmt.Printf("   ✓ %s\n", q)
	}
}
