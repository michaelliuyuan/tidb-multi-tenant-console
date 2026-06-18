package store

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// PromClient 包装 Prometheus HTTP API（/api/v1/query、/api/v1/query_range）。
type PromClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewPromClient(base string) *PromClient {
	return &PromClient{BaseURL: base, HTTP: http.DefaultClient}
}

// Series 一条时序：标签 + 采样点（[unix秒, 值]）。
type Series struct {
	Labels map[string]string `json:"labels"`
	Values [][2]float64      `json:"values"` // [timestamp, value]
}

// promResp Prometheus 标准响应结构（仅取 matrix/vector 用到的字段）。
type promResp struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	} `json:"data"`
}

// QueryRange 执行 range 查询，返回 matrix 多条时序。
func (p *PromClient) QueryRange(query string, start, end time.Time, stepSec int) ([]Series, error) {
	if p == nil || p.BaseURL == "" {
		return nil, fmt.Errorf("prometheus url 未配置")
	}
	v := url.Values{}
	v.Set("query", query)
	v.Set("start", strconv.FormatInt(start.Unix(), 10))
	v.Set("end", strconv.FormatInt(end.Unix(), 10))
	v.Set("step", strconv.Itoa(stepSec))
	resp, err := p.HTTP.PostForm(p.BaseURL+"/api/v1/query_range", v)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var pr promResp
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prom error: %s", pr.Error)
	}
	var out []Series
	for _, raw := range pr.Data.Result {
		// matrix: {"metric":{...}, "values":[[ts,"val"],...]}
		var item struct {
			Metric map[string]string `json:"metric"`
			Values []([]json.Number) `json:"values"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		s := Series{Labels: item.Metric}
		for _, pair := range item.Values {
			if len(pair) != 2 {
				continue
			}
			ts, _ := pair[0].Float64()
			val, _ := pair[1].Float64()
			s.Values = append(s.Values, [2]float64{ts, val})
		}
		out = append(out, s)
	}
	return out, nil
}

// Query 执行瞬时查询，返回 vector 各标签的当前值。
func (p *PromClient) Query(query string) ([]Series, error) {
	if p == nil || p.BaseURL == "" {
		return nil, fmt.Errorf("prometheus url 未配置")
	}
	v := url.Values{}
	v.Set("query", query)
	resp, err := p.HTTP.PostForm(p.BaseURL+"/api/v1/query", v)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var pr promResp
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prom error: %s", pr.Error)
	}
	var out []Series
	for _, raw := range pr.Data.Result {
		var item struct {
			Metric map[string]string `json:"metric"`
			Value  []json.Number     `json:"value"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		s := Series{Labels: item.Metric}
		if len(item.Value) == 2 {
			ts, _ := item.Value[0].Float64()
			val, _ := item.Value[1].Float64()
			s.Values = append(s.Values, [2]float64{ts, val})
		}
		out = append(out, s)
	}
	return out, nil
}

// 默认 PromQL。注意：TiDB 资源管控 RU 的 metric 名随版本变化，
// 接入真实集群时请按目标版本 Grafana 核对（以下以 v7.1.9 实测为准）。
// v7.1.9 实测可用的 resource_group 指标：
//   - resource_manager_client_token_request_resource_group（gauge，RU token 消耗量，含 resource_group label）
//   - tidb_session_resource_group_query_total（counter，按 RG 的查询总数）
//   - resource_manager_client_resource_group_status（gauge，RG 状态）
const (
	// RU token 消耗（按 resource_group 聚合，gauge 类型直接取值）
	PromQLRUByGroup = `sum by (resource_group) (resource_manager_client_token_request_resource_group)`
	// QPS（按 instance）
	PromQLQPS = `sum by (instance) (rate(tidb_server_query_total[5m]))`
	// 查询 P99 延迟
	PromQLP99 = `histogram_quantile(0.99, sum by (le) (rate(tidb_server_handle_query_duration_seconds_bucket[5m])))`
	// TiKV 存储用量
	PromQLStorage = `sum(tikv_engine_size_bytes)`
)

var _ = io.EOF
