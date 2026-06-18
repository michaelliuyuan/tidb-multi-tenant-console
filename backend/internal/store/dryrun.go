package store

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidb-multi-tenant/console/internal/model"
)

// EstimatePlacementMove 预估把目标库数据放置到「指定标签节点池」的影响：
// 受影响 Region 数 / 总量 / 目标池容量是否够 / 预估调度时长 / 告警。
// 算法见 docs/p0-technical-design.md §4。
func EstimatePlacementMove(cs *ClusterSQL, pd *PDClient, req model.DryRunRequest) (*model.DryRunResult, error) {
	if req.Voters <= 0 {
		req.Voters = 3
	}
	res := &model.DryRunResult{ReplicationFactor: req.Voters, Warnings: []string{}}

	// 1) 受影响 Region 数与总量（来自 information_schema.TIKV_REGION_STATUS）
	count, sizeMB, err := regionStats(cs, req.Databases)
	if err != nil {
		return nil, fmt.Errorf("query region status: %w（旧版本可能无 TIKV_REGION_STATUS，或需 ANALYZE）", err)
	}
	res.AffectedRegions = count
	res.TotalSizeMB = sizeMB

	// 2) 目标节点池容量（来自 PD stores，匹配 label）
	stores, err := pd.ListStores()
	if err != nil {
		return nil, fmt.Errorf("pd list stores: %w", err)
	}
	var availMB int64
	for _, s := range stores {
		if hasLabel(s.Labels, req.LabelKey, req.LabelValue) {
			res.TargetPoolCount++
			availMB += parseSizeMB(s.Available)
		}
	}
	res.TargetAvailMB = availMB

	// 3) 容量校验：目标池需承载 size * 副本数
	res.NeededMB = sizeMB * int64(req.Voters)
	res.TargetCapacityOK = res.TargetPoolCount > 0 && availMB >= res.NeededMB
	if res.TargetPoolCount == 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("没有匹配 +%s=%s 的 TiKV 节点，请先给节点打标签", req.LabelKey, req.LabelValue))
	} else if !res.TargetCapacityOK {
		res.Warnings = append(res.Warnings, fmt.Sprintf("目标池容量不足：需 %d MB，可用 %d MB（差 %d MB）", res.NeededMB, availMB, res.NeededMB-availMB))
	}

	// 4) 预估调度时长：PD region 调度并发有限，按聚合速率 ~1500 region/分钟估算
	const ratePerMin = 1500.0
	res.EstMinutes = float64(count) / ratePerMin
	if res.EstMinutes > 30 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("预估调度约 %.0f 分钟，建议灰度分批绑定（先绑少量表）", res.EstMinutes))
	}
	if res.TargetPoolCount > 0 && res.TargetPoolCount < req.Voters {
		res.Warnings = append(res.Warnings, fmt.Sprintf("目标池仅 %d 个节点，少于副本数 %d，无法满足放置约束", res.TargetPoolCount, req.Voters))
	}
	if len(res.Warnings) == 0 {
		res.Warnings = nil
	}
	return res, nil
}

// regionStats 统计目标库的 Region 数与近似总量（MB）。TIKV_REGION_STATUS.APPROXIMATE_SIZE 单位为 MB。
func regionStats(cs *ClusterSQL, dbs []string) (int, int64, error) {
	if len(dbs) == 0 {
		return 0, 0, nil
	}
	placeholders := make([]string, len(dbs))
	args := make([]any, len(dbs))
	for i, d := range dbs {
		placeholders[i] = "?"
		args[i] = d
	}
	q := fmt.Sprintf(`SELECT COUNT(*), COALESCE(SUM(APPROXIMATE_SIZE),0)
		FROM information_schema.TIKV_REGION_STATUS
		WHERE TABLE_NAME <> '' AND IS_INDEX = 0 AND DB_NAME IN (%s)`,
		strings.Join(placeholders, ","))
	var count int
	var sizeMB int64
	if err := cs.DB.QueryRow(q, args...).Scan(&count, &sizeMB); err != nil {
		return 0, 0, err
	}
	return count, sizeMB, nil
}

func hasLabel(labels []model.KV, key, value string) bool {
	if key == "" {
		return false
	}
	for _, l := range labels {
		if l.Key == key && l.Value == value {
			return true
		}
	}
	return false
}

var sizeRe = regexp.MustCompile(`(?i)([\d.]+)\s*(TiB|GiB|MiB|KiB|TB|GB|MB|KB)`)

// parseSizeMB 把 "1 TiB" / "200 GiB" 等解析为 MB（粗略，演示用）。
func parseSizeMB(s string) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "tib", "tb":
		return int64(n * 1024 * 1024)
	case "gib", "gb":
		return int64(n * 1024)
	case "mib", "mb":
		return int64(n)
	case "kib", "kb":
		return int64(n / 1024)
	}
	return 0
}
