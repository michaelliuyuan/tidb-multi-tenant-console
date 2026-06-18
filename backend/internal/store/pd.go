package store

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tidb-multi-tenant/console/internal/model"
)

// PDClient 包装 PD HTTP API（/pd/api/v1）。
// 仅用于只读拓扑查看 + store 标签写入；placement rules 由 SQL 侧管理。
type PDClient struct {
	Endpoint string // 如 http://pd:2379
	HTTP     *http.Client
}

func NewPDClient(endpoint string) *PDClient {
	return &PDClient{Endpoint: strings.TrimRight(endpoint, "/"), HTTP: http.DefaultClient}
}

// Version 探测 PD /pd/api/v1/version，用于添加集群时的连通性检查。
func (p *PDClient) Version() (string, error) {
	resp, err := p.HTTP.Get(p.Endpoint + "/pd/api/v1/version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("pd status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)), nil
}

// storesResp 对应 PD /pd/api/v1/stores 结构（仅取需要字段）。
type storesResp struct {
	Stores []struct {
		Store struct {
			ID            int64      `json:"id"`
			Address       string     `json:"address"`
			StatusAddress string     `json:"status_address"`
			Labels        []model.KV `json:"labels"`
			StateName     string     `json:"state_name"` // 状态在 store 对象，非 status
		} `json:"store"`
		Status struct {
			Capacity    string `json:"capacity"`
			Available   string `json:"available"`
			RegionCount int    `json:"region_count"`
		} `json:"status"`
	} `json:"stores"`
}

func (p *PDClient) ListStores() ([]model.Store, error) {
	resp, err := p.HTTP.Get(p.Endpoint + "/pd/api/v1/stores")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var sr storesResp
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}
	out := make([]model.Store, 0, len(sr.Stores))
	for _, s := range sr.Stores {
		labels := s.Store.Labels
		if labels == nil {
			labels = []model.KV{} // 避免 JSON 输出 null（前端 .find/.map 会崩）
		}
		out = append(out, model.Store{
			ID:            s.Store.ID,
			Address:       s.Store.Address,
			StatusAddress: s.Store.StatusAddress,
			Labels:        labels,
			StatusName:    s.Store.StateName,
			Capacity:      s.Status.Capacity,
			Available:     s.Status.Available,
			RegionCount:   s.Status.RegionCount,
		})
	}
	return out, nil
}

// SetStoreLabel 对 store 打标签：POST /pd/api/v1/store/{id}/label，body 为 map 格式 {"<key>":"<value>"}。
// 注意：该 PD 把 body 当作 labelKey→labelValue 的 map 解析（{"key":..,"value":..} 会被误建成两个标签）。
func (p *PDClient) SetStoreLabel(id int64, key, value string) error {
	body := fmt.Sprintf(`{%q: %q}`, key, value)
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/pd/api/v1/store/%d/label", p.Endpoint, id),
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pd set label failed: %s: %s", resp.Status, string(b))
	}
	return nil
}

// DeleteStoreLabel 删除标签：DELETE /pd/api/v1/store/{id}/label，body 为标签键的 JSON 字符串 "<k>"。
func (p *PDClient) DeleteStoreLabel(id int64, key string) error {
	body := fmt.Sprintf("%q", key) // JSON 字符串，如 "zone"
	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/pd/api/v1/store/%d/label", p.Endpoint, id),
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("pd del label failed: %s", resp.Status)
	}
	return nil
}

// PlacementRules 查看 PD 实际下发的 placement rules（只读，用于校验/展示）。
func (p *PDClient) PlacementRules() (json.RawMessage, error) {
	resp, err := p.HTTP.Get(p.Endpoint + "/pd/api/v1/config/placement-rule")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
