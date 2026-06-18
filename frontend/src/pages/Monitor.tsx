import { useEffect, useState } from 'react'
import { Card, Space, Segmented, Statistic, Row, Col, Empty, Spin, Typography } from 'antd'
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer } from 'recharts'
import { api, MetricSeries, MetricSummary } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text } = Typography

const COLORS = ['#1677ff', '#52c41a', '#fa8c16', '#eb2f96', '#722ed1', '#13c2c2']

export default function Monitor() {
  const { cid } = useCluster()
  const [range, setRange] = useState<'1h' | '6h' | '24h'>('1h')
  const [series, setSeries] = useState<MetricSeries[]>([])
  const [summary, setSummary] = useState<MetricSummary>({})
  const [loading, setLoading] = useState(false)
  const [allGroups, setAllGroups] = useState<string[]>([])

  useEffect(() => {
    if (!cid) return
    // 同时获取资源组列表（用于补全 RU 为 0 的组）
    api.listResourceGroups(cid).then((d: any) => {
      setAllGroups(Array.isArray(d) ? d.map((r: any) => r.name).filter(Boolean) : [])
    }).catch(() => {})

    const to = Math.floor(Date.now() / 1000)
    const h = range === '1h' ? 1 : range === '6h' ? 6 : 24
    const from = to - h * 3600
    setLoading(true)
    Promise.all([
      api.getMetricsRU(cid, from, to).catch(() => []),
      api.getMetricsSummary(cid).catch(() => ({})),
    ]).then(([s, sm]) => { setSeries(s || []); setSummary(sm || {}) })
      .finally(() => setLoading(false))
  }, [cid, range])

  // 多条时序合并为 {ts, tenantA: v, tenantB: v, ...}
  const data: Record<string, number | string>[] = []
  const byTs: Record<string, Record<string, number>> = {}
  const names: string[] = []

  // 先从 Prom 数据提取有消耗的资源组
  series.forEach(se => {
    const name = se.labels.tenant || se.labels.resource_group || 'unknown'
    if (!names.includes(name)) names.push(name)
    se.values.forEach(([ts, v]) => {
      (byTs[ts] ||= {})[name] = v
    })
  })

  // 补全所有资源组（包括 RU 为 0 的），映射到租户名
  allGroups.forEach(rg => {
    const name = rg // 直接用资源组名展示
    if (!names.includes(name)) names.push(name)
  })

  Object.keys(byTs).sort((a, b) => +a - +b).forEach(ts => {
    const row: Record<string, number | string> = { ts: +ts }
    names.forEach(n => { row[n] = byTs[ts]?.[n] ?? 0 })
    data.push(row as any)
  })

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card>
        <Space>
          <Segmented options={['1h', '6h', '24h']} value={range} onChange={v => setRange(v as any)} />
          <Text type="secondary">RU 按 resource_group 反查 tenant；metric 名随 TiDB 版本，见后端 PromQL 常量。集群请在顶部选择。</Text>
        </Space>
      </Card>

      <Row gutter={16}>
        <Col span={6}><Card><Statistic title="QPS（瞬时）" value={summary.qps ?? '-'} precision={summary.qps != null ? 1 : undefined} /></Card></Col>
        <Col span={6}><Card><Statistic title="查询 P99（秒）" value={summary.p99_s ?? '-'} precision={summary.p99_s != null ? 3 : undefined} /></Card></Col>
        <Col span={6}><Card><Statistic title="存储用量" value={fmtBytes(summary.storage_bytes)} /></Card></Col>
        <Col span={6}><Card><Statistic title="活跃租户" value={names.length} /></Card></Col>
      </Row>

      <Card title="租户 RU 消耗趋势（RU/s）">
        {!cid ? <Empty description="请先选择集群（需配置 Prometheus）" /> :
          loading ? <Spin /> :
          data.length === 0 ? <Empty description="暂无 RU 数据（确认 Prometheus 已采集 resource_group 指标）" /> :
          <ResponsiveContainer width="100%" height={320}>
            <LineChart data={data} margin={{ top: 8, right: 16, bottom: 0, left: 0 }}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="ts" tickFormatter={t => new Date(+t * 1000).toLocaleTimeString()} tick={{ fontSize: 11 }} />
              <YAxis tick={{ fontSize: 11 }} />
              <Tooltip labelFormatter={t => new Date(+t * 1000).toLocaleString()} />
              <Legend />
              {names.map((n, i) => <Line key={n} type="monotone" dataKey={n} stroke={COLORS[i % COLORS.length]} dot={false} strokeWidth={2} />)}
            </LineChart>
          </ResponsiveContainer>}
      </Card>
    </Space>
  )
}

function fmtBytes(b?: number): string {
  if (b == null) return '-'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0, v = b
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return v.toFixed(1) + ' ' + u[i]
}
