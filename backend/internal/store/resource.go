package store

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tidb-multi-tenant/console/internal/model"
)

// StoreResourceClient 通过 SSH 到 TiKV 节点执行 systemctl 获取资源上限。
type StoreResourceClient struct {
	SSHPassword string // TiKV 节点 SSH 密码
}

func NewStoreResourceClient(sshPassword string) *StoreResourceClient {
	return &StoreResourceClient{SSHPassword: sshPassword}
}

// FetchResource 通过 sshpass + systemctl show tikv-{port}.service 获取 MemoryLimit/CPUQuota 和当前使用量。
func (c *StoreResourceClient) FetchResource(storeAddr string) (*model.StoreResource, error) {
	// storeAddr 格式: "192.168.2.241:20160"
	host, port, ok := strings.Cut(storeAddr, ":")
	if !ok {
		return nil, fmt.Errorf("invalid store address: %s", storeAddr)
	}

	// 服务名如 tikv-20160.service
	svc := fmt.Sprintf("tikv-%s.service", port)

	// 执行: sshpass ... ssh ... "systemctl show SERVICE"
	cmd := exec.Command("sshpass",
		"-p", c.SSHPassword,
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=8",
		"-o", "PreferredAuthentications=password",
		"-o", "PubkeyAuthentication=no",
		fmt.Sprintf("tidb@%s", host),
		fmt.Sprintf("systemctl show %s --property=MemoryLimit,MemoryCurrent,CPUQuota,CPUQuotaPerSecUSec,CPUUsageNSec,IOReadBandwidthMax,IOWriteBandwidthMax", svc),
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ssh to %s failed: %w", host, err)
	}

	return parseSystemctl(string(out)), nil
}

// parseSystemctl 解析 systemctl show 输出。
func parseSystemctl(output string) *model.StoreResource {
	res := &model.StoreResource{}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "MemoryLimit":
			res.MemoryLimit = val
		case "MemoryCurrent":
			// systemd 返回的数字，如 "2147483648"
			if n, err := strconv.ParseUint(val, 10, 64); err == nil && n > 0 {
				res.MemoryCurrent = n
			}
		case "CPUQuotaPerSecUSec":
			// systemd v239+ 使用此字段，如 "2s" → 200%
			cpuStr := strings.TrimSuffix(val, "s")
			if cpuF, err := strconv.ParseFloat(cpuStr, 64); err == nil {
				res.CPUQuota = int(cpuF * 100)
			}
		case "CPUQuota":
			// 旧版 systemd，如 "200%" → 200
			cpuStr := strings.TrimSuffix(val, "%")
			if n, err := strconv.Atoi(cpuStr); err == nil {
				res.CPUQuota = n
			}
		case "CPUUsageNSec":
			// 累计 CPU 纳秒（增量需前端自行计算）
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				res.CPUUsageNSec = n
			}
		case "IOReadBandwidthMax":
			res.ReadBandwidth = val
		case "IOWriteBandwidthMax":
			res.WriteBandwidth = val
		}
	}

	if res.MemoryCurrent > 0 && res.MemoryLimit != "" && res.MemoryLimit != "infinity" {
		// 解析 MemoryLimit (如 "2147483648" 或 "2G")
		limit := parseMemoryBytes(res.MemoryLimit)
		if limit > 0 {
			res.MemoryUsagePct = float64(res.MemoryCurrent) / float64(limit) * 100
		}
	}

	res.ResourceControl = true
	return res
}

// parseMemoryBytes 解析 systemd MemoryLimit（可能是 "2G" 或 "2147483648"）。
func parseMemoryBytes(s string) uint64 {
	if s == "" || s == "infinity" {
		return 0
	}
	// 尝试纯数字
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		return n
	}
	// 尝试带后缀 2G / 512M / 1T
	s = strings.TrimSpace(s)
	multiplier := uint64(1)
	switch {
	case strings.HasSuffix(s, "T") || strings.HasSuffix(s, "TB"):
		multiplier = 1 << 40
		s = strings.TrimRight(s, "TB")
	case strings.HasSuffix(s, "G") || strings.HasSuffix(s, "GB"):
		multiplier = 1 << 30
		s = strings.TrimRight(s, "GB")
	case strings.HasSuffix(s, "M") || strings.HasSuffix(s, "MB"):
		multiplier = 1 << 20
		s = strings.TrimRight(s, "MB")
	case strings.HasSuffix(s, "K") || strings.HasSuffix(s, "KB"):
		multiplier = 1 << 10
		s = strings.TrimRight(s, "KB")
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return uint64(n * float64(multiplier))
}
