// 实验：弄清 TiDB v7.1 resource group 的 RU 语义（尤其"挂起"该怎么做）。
package main

import (
	"database/sql"
	"fmt"

	"github.com/tidb-multi-tenant/console/internal/store"
)

func main() {
	db, err := store.OpenTiDB("pepezzzz.synology.me", 24140, "root", "tidb9.0ga", "")
	if err != nil {
		fmt.Println("连接失败:", err)
		return
	}
	exec(db, "DROP RESOURCE GROUP IF EXISTS `_mt_probe`")
	fmt.Println("\n== 创建 RU=500 BURSTABLE ==")
	exec(db, "CREATE RESOURCE GROUP `_mt_probe` RU_PER_SEC = 500 BURSTABLE")

	tryRU(db, 0)   // 设计里的"挂起"方式
	tryRU(db, 1)   // 极小值
	tryRU(db, 100) // 正常值

	var ru int
	_ = db.QueryRow("SELECT ru_per_sec FROM information_schema.RESOURCE_GROUPS WHERE name='_mt_probe'").Scan(&ru)
	fmt.Printf("\n当前 RU_PER_SEC = %d\n", ru)

	exec(db, "DROP RESOURCE GROUP IF EXISTS `_mt_probe`")
	fmt.Println("已清理")
}

func tryRU(db *sql.DB, ru int) {
	_, err := db.Exec(fmt.Sprintf("ALTER RESOURCE GROUP `_mt_probe` RU_PER_SEC = %d", ru))
	if err != nil {
		fmt.Printf("  ALTER RU=%-4d ❌ %v\n", ru, err)
		return
	}
	var got int
	_ = db.QueryRow("SELECT ru_per_sec FROM information_schema.RESOURCE_GROUPS WHERE name='_mt_probe'").Scan(&got)
	fmt.Printf("  ALTER RU=%-4d ✓ 生效，读回=%d\n", ru, got)
}

func exec(db *sql.DB, q string) {
	if _, err := db.Exec(q); err != nil {
		fmt.Printf("  执行失败: %s → %v\n", q, err)
	}
}
