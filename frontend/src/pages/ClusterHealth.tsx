import { useEffect, useState } from 'react'
import { Card, Button, Descriptions, Tag, Alert, Space, Typography } from 'antd'
import { api, ClusterHealth } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text } = Typography

// 联调前置：探测 TiDB 连通/版本、PD 可达、Prometheus 可达、placement/resource_control 能力。
export default function ClusterHealthPage() {
  const { cid } = useCluster()
  const [h, setH] = useState<ClusterHealth | null>(null)
  const [loading, setLoading] = useState(false)

  const probe = async (id: string) => {
    setLoading(true); setH(null)
    try { setH(await api.getClusterHealth(id)) } catch { setH(null) }
    finally { setLoading(false) }
  }

  // cid 变化时自动探测一次
  useEffect(() => { if (cid) probe(cid) }, [cid])

  const hasCap = (c: string) => h?.capabilities?.includes(c)

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card title="集群连通性 + 版本能力探测（联调前置）"
        extra={cid ? <Button size="small" loading={loading} onClick={() => probe(cid)}>重新探测</Button> : undefined}>
        {!cid
          ? <Alert type="info" showIcon message="请在顶部选择集群后探测。" />
          : loading && !h
            ? <Text type="secondary">探测中…</Text>
            : null}
      </Card>

      {h && (
        <Card title={`集群：${h.name}`}>
          <Descriptions bordered column={1} size="small">
            <Descriptions.Item label="TiDB 连通"><Tag color={h.tidb_ok ? 'green' : 'red'}>{h.tidb_ok ? '✓ 正常' : '✗ 失败'}</Tag>
              {h.tidb_error && <Text type="danger"> {h.tidb_error}</Text>}</Descriptions.Item>
            <Descriptions.Item label="TiDB 版本">{h.version || '-'}</Descriptions.Item>
            <Descriptions.Item label="PD 可达"><Tag color={h.pd_ok ? 'green' : 'red'}>{h.pd_ok ? '✓ 正常' : '✗ 失败'}</Tag>
              {h.pd_error && <Text type="danger"> {h.pd_error}</Text>}</Descriptions.Item>
            <Descriptions.Item label="Prometheus 可达"><Tag color={h.prom_ok ? 'green' : 'red'}>{h.prom_ok ? '✓ 正常' : '✗ 未配置/不可达'}</Tag></Descriptions.Item>
            <Descriptions.Item label="Placement Rule 能力"><Tag color={hasCap('PLACEMENT_POLICIES') ? 'green' : 'default'}>{hasCap('PLACEMENT_POLICIES') ? '✓ 可用' : '✗ 不支持（需 v5.3+）'}</Tag></Descriptions.Item>
            <Descriptions.Item label="Resource Control 能力"><Tag color={hasCap('RESOURCE_GROUPS') ? 'green' : 'default'}>{hasCap('RESOURCE_GROUPS') ? '✓ 可用' : '✗ 不支持（需 v7.1+）'}</Tag></Descriptions.Item>
          </Descriptions>
          {!h.tidb_ok && <Alert type="error" showIcon style={{ marginTop: 12 }} message="TiDB 不可达：检查 host/port/账号网络。" />}
          {h.tidb_ok && !hasCap('PLACEMENT_POLICIES') && <Alert type="warning" showIcon style={{ marginTop: 12 }} message="该版本不支持 Placement Rule in SQL，物理隔离能力不可用。" />}
          {h.tidb_ok && !hasCap('RESOURCE_GROUPS') && <Alert type="warning" showIcon style={{ marginTop: 12 }} message="该版本不支持 Resource Control，资源配额能力不可用。" />}
        </Card>
      )}
    </Space>
  )
}
