import { useEffect, useMemo, useState } from 'react'
import {
  Spin, Empty, Space, Typography, Tag, Progress, Tooltip, Card, Row, Col,
  Statistic, Badge, Collapse, Alert, Table, Segmented,
} from 'antd'
import {
  GlobalOutlined, CloudServerOutlined, DatabaseOutlined, HddOutlined,
  ApartmentOutlined, AppstoreOutlined, UnorderedListOutlined,
} from '@ant-design/icons'
import type { TableColumnsType } from 'antd'
import { api, Store, StoreResource, Tenant, KV } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text, Title } = Typography

// ---- helpers ----

function pctUsed(cap: string, avail: string): number {
  const m = cap.match(/([\d.]+)\s*(TiB|GiB|MiB)/i)
  if (!m) return 0
  const n = parseFloat(m[1]), gb = /ti/i.test(m[2]) ? n * 1024 : /gi/i.test(m[2]) ? n : n / 1024
  const a = avail.match(/([\d.]+)\s*(TiB|GiB|MiB)/i)
  if (!a) return 0
  const na = parseFloat(a[1]), ga = /ti/i.test(a[2]) ? na * 1024 : /gi/i.test(a[2]) ? na : na / 1024
  return gb <= 0 ? 0 : Math.max(0, Math.min(100, Math.round((1 - ga / gb) * 100)))
}
function fmtB(s: string): string { const m = s.match(/([\d.]+)\s*(TiB|GiB|MiB)/i); return m ? parseFloat(m[1]).toFixed(1) + ' ' + m[2] : s }
function isTiFlash(s: Store) { return (s.labels || []).some(l => l.key === 'engine' && l.value === 'tiflash') }
function getLabel(s: Store, key: string): string {
  return (s.labels || []).find(l => l.key === key)?.value ?? ''
}
function ipOf(s: Store): string { return (s.address || '').split(':')[0] || 'unknown' }
function portOf(s: Store): number { const parts = (s.address || '').split(':'); const p = parseInt(parts[1], 10); return isNaN(p) ? 0 : p }

// IP 地址比较：将点分十进制转为数值数组逐段比较（正确处理 10.0.0.2 < 10.0.0.10）
function compareIP(a: string, b: string): number {
  const pa = a.split('.').map(n => parseInt(n, 10) || 0)
  const pb = b.split('.').map(n => parseInt(n, 10) || 0)
  for (let i = 0; i < 4; i++) {
    const da = pa[i] || 0, db = pb[i] || 0
    if (da !== db) return da - db
  }
  return 0
}

// 标签排序优先级：zone > region > rack > host > app > 其余按字母序
const LABEL_ORDER = ['zone', 'region', 'rack', 'host', 'app']
function sortLabels(labels: KV[]): KV[] {
  return [...labels].sort((a, b) => {
    const ia = LABEL_ORDER.indexOf(a.key)
    const ib = LABEL_ORDER.indexOf(b.key)
    if (ia !== -1 && ib !== -1) return ia - ib
    if (ia !== -1) return -1
    if (ib !== -1) return 1
    return a.key.localeCompare(b.key)
  })
}

// ---- hierarchical tree types ----

interface TiKVNode {
  store: Store
  res?: StoreResource
  binding?: { name: string; isolation: string; labelKey: string; labelValue: string }
}
interface HostNode { host: string; tikvs: TiKVNode[] }
interface RackNode { rack: string; hosts: HostNode[] }
interface RegionNode { region: string; racks: RackNode[] }
interface ZoneNode { zone: string; regions: RegionNode[] }
interface TopologyTree { zones: ZoneNode[] }

function buildHierarchy(stores: Store[]): TopologyTree {
  const zoneMap: Record<string, Record<string, Record<string, Record<string, TiKVNode[]>>>> = {}
  for (const s of stores) {
    const zone = getLabel(s, 'zone') || 'default'
    const region = getLabel(s, 'region') || 'default'
    const rack = getLabel(s, 'rack') || 'default'
    const host = ipOf(s)
    ;(zoneMap[zone] ||= {})
    ;(zoneMap[zone][region] ||= {})
    ;(zoneMap[zone][region][rack] ||= {})
    ;(zoneMap[zone][region][rack][host] ||= []).push({ store: s })
  }
  const zones: ZoneNode[] = Object.entries(zoneMap).map(([zone, regionMap]) => ({
    zone,
    regions: Object.entries(regionMap).map(([region, rackMap]) => ({
      region,
      racks: Object.entries(rackMap).map(([rack, hostMap]) => ({
        rack,
        hosts: Object.entries(hostMap)
          .map(([host, tikvs]) => ({
            host,
            tikvs: tikvs.slice().sort((a, b) => portOf(a.store) - portOf(b.store)),
          }))
          .sort((a, b) => compareIP(a.host, b.host)),
      })),
    })),
  }))
  return { zones }
}

function buildStoreTenantMap(stores: Store[], tenants: Tenant[]) {
  const map = new Map<number, { name: string; isolation: string; labelKey: string; labelValue: string }>()
  const labeled = tenants.filter(t => t.label_key && t.label_value && (t.isolation_level === 'PHYSICAL' || t.isolation_level === 'HYBRID'))
  for (const s of stores)
    for (const t of labeled)
      if ((s.labels || []).some(l => l.key === t.label_key && l.value === t.label_value))
        { map.set(s.id, { name: t.name, isolation: t.isolation_level!, labelKey: t.label_key!, labelValue: t.label_value! }); break }
  return map
}

// ---- TiKV card ----
function TiKVCard({ node }: { node: TiKVNode }) {
  const { store, res, binding } = node
  const up = store.status_name === 'Up'
  const tiflash = isTiFlash(store)
  const diskUsed = pctUsed(store.capacity, store.available)
  const memPct = res?.memory_usage_pct ?? 0
  const cpuQ = res?.cpu_quota ?? 0

  // 资源限制显示（负值/空/infinity = 不限，常见于未配置 systemd 资源限制）
  const memLimitFmt = !res?.memory_limit || res.memory_limit === 'infinity' || res.memory_limit.startsWith('-')
    ? '不限' : (res.memory_limit_fmt || res.memory_limit)
  const cpuLimitFmt = !res?.cpu_quota || res.cpu_quota <= 0
    ? '不限' : String((res.cpu_quota / 100).toFixed(1)) + ' 核 (' + res.cpu_quota + '%)'

  const isBound = !!binding
  const canBind = up && !tiflash && !isBound

  // CPU 配额（systemd CPUQuota，如 200 = 2 核）；注意这是配额上限，不是使用率
  const cpuUsagePct = res?.cpu_usage_pct ?? 0

  return (
    <Card
      size="small"
      style={{
        flex: '1 1 260px',
        minWidth: 240,
        maxWidth: 360,
        borderColor: isBound ? '#4096ff' : canBind ? '#52c41a' : '#d9d9d9',
        borderWidth: isBound ? 2 : canBind ? 2 : 1,
        borderStyle: canBind ? 'dashed' : 'solid',
        opacity: up ? 1 : 0.6,
        boxShadow: isBound ? '0 0 8px rgba(64,150,255,0.2)' : canBind ? '0 0 8px rgba(82,196,26,0.15)' : undefined,
      }}
      styles={{ body: { padding: 10 } }}
      title={
        <Space size={4}>
          <Badge status={up ? (tiflash ? 'warning' : 'success') : 'error'} />
          <Text strong style={{ fontSize: 13 }}>#{store.id}</Text>
          {tiflash && <Tag color="orange" style={{ fontSize: 10, margin: 0, lineHeight: '16px' }}>TiFlash</Tag>}
          {isBound
            ? <Tag color="blue" style={{ fontSize: 10, margin: 0, lineHeight: '16px' }}>已绑定: {binding!.name}</Tag>
            : canBind
              ? <Tag color="green" style={{ fontSize: 10, margin: 0, lineHeight: '16px' }}>可绑定</Tag>
              : <Tag style={{ fontSize: 10, margin: 0, lineHeight: '16px' }}>空闲</Tag>}
        </Space>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        {/* 地址信息 */}
        <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11 }}>
          <Text type="secondary">{store.address}</Text>
          <Text type="secondary">{store.region_count} Regions</Text>
        </div>

        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 2 }}>
            <Text type="secondary" style={{ fontSize: 11 }}>磁盘</Text>
            <Text style={{ fontSize: 11, color: diskUsed > 80 ? '#ff4d4f' : undefined }}>{diskUsed}%</Text>
          </div>
          <Progress percent={diskUsed} size="small" strokeColor={diskUsed > 80 ? '#ff4d4f' : '#52c41a'} showInfo={false} />
          <Text type="secondary" style={{ fontSize: 10 }}>可用 {fmtB(store.available)} / {fmtB(store.capacity)}</Text>
        </div>

        {up && (
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 2 }}>
              <Text type="secondary" style={{ fontSize: 11 }}>{'内存'}</Text>
              <Space size={4}>
                {memPct > 0 && <Text style={{ fontSize: 11, color: memPct > 85 ? '#ff4d4f' : undefined }}>{Math.round(memPct)}%</Text>}
                <Tag style={{ fontSize: 9, margin: 0, lineHeight: '16px' }} color={memLimitFmt === '不限' ? 'default' : 'purple'}>{'上限: ' + memLimitFmt}</Tag>
              </Space>
            </div>
            {memPct > 0
              ? <Progress percent={Math.round(memPct)} size="small" strokeColor={memPct > 85 ? '#ff4d4f' : '#722ed1'} showInfo={false} />
              : <Text type="secondary" style={{ fontSize: 10 }}>未获取使用率</Text>}
          </div>
        )}

        {up && (
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 2 }}>
              <Text type="secondary" style={{ fontSize: 11 }}>{'CPU'}</Text>
              <Space size={4}>
                {cpuUsagePct > 0 && <Text style={{ fontSize: 11, color: cpuUsagePct > 85 ? '#ff4d4f' : undefined }}>{Math.round(cpuUsagePct)}%</Text>}
                <Tag style={{ fontSize: 9, margin: 0, lineHeight: '16px' }} color={cpuLimitFmt === '不限' ? 'default' : 'blue'}>{'上限: ' + cpuLimitFmt}</Tag>
              </Space>
            </div>
            {cpuUsagePct > 0
              ? <Progress percent={Math.min(Math.round(cpuUsagePct), 100)} size="small" strokeColor={cpuUsagePct > 85 ? '#ff4d4f' : '#1677ff'} showInfo={false} />
              : <Text type="secondary" style={{ fontSize: 10 }}>使用率需配置 Prometheus</Text>}
          </div>
        )}

        {(store.labels || []).length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 2, borderTop: '1px solid #f0f0f0', paddingTop: 4 }}>
            {sortLabels((store.labels || []).filter(l => l.key !== 'engine')).map(l => (
              <Tag key={l.key} style={{ fontSize: 10, margin: 0, lineHeight: '16px' }}
                color={isBound && l.key === binding!.labelKey ? 'blue' : 'default'}>
                {l.key}={l.value}
              </Tag>
            ))}
          </div>
        )}
      </div>
    </Card>
  )
}

// ---- Host card ----
function HostSection({ host }: { host: HostNode }) {
  const upCount = host.tikvs.filter(n => n.store.status_name === 'Up').length
  const dnCount = host.tikvs.filter(n => n.store.status_name !== 'Up').length
  return (
    <Card
      size="small"
      style={{ marginBottom: 12 }}
      title={
        <Space>
          <HddOutlined style={{ color: '#1677ff' }} />
          <Text strong>{host.host}</Text>
          <Tag color={dnCount > 0 ? 'red' : 'green'} style={{ fontSize: 11 }}>
            {upCount} UP{dnCount > 0 ? ` / ${dnCount} DOWN` : ''}
          </Tag>
          <Text type="secondary" style={{ fontSize: 12 }}>{host.tikvs.length} TiKV</Text>
        </Space>
      }
    >
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
        {host.tikvs.map(n => (
          <TiKVCard key={n.store.id} node={n} />
        ))}
      </div>
    </Card>
  )
}

// ---- Rack section ----
function RackSection({ rack }: { rack: RackNode }) {
  return (
    <div style={{ marginBottom: 8 }}>
      <div style={{ marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
        <ApartmentOutlined style={{ color: '#722ed1' }} />
        <Text strong style={{ fontSize: 14 }}>Rack: {rack.rack}</Text>
        <Text type="secondary" style={{ fontSize: 12 }}>{rack.hosts.length} 主机</Text>
      </div>
      <div style={{ marginLeft: 24, paddingLeft: 12, borderLeft: '2px solid #f0f0f0' }}>
        {rack.hosts.map(h => <HostSection key={h.host} host={h} />)}
      </div>
    </div>
  )
}

// ---- Region section ----
function RegionSection({ region }: { region: RegionNode }) {
  return (
    <div style={{ marginBottom: 8 }}>
      <div style={{ marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
        <DatabaseOutlined style={{ color: '#13c2c2' }} />
        <Text strong style={{ fontSize: 14 }}>Region: {region.region}</Text>
        <Text type="secondary" style={{ fontSize: 12 }}>{region.racks.length} Rack</Text>
      </div>
      <div style={{ marginLeft: 24, paddingLeft: 12, borderLeft: '2px solid #f0f0f0' }}>
        {region.racks.map(r => <RackSection key={r.rack} rack={r} />)}
      </div>
    </div>
  )
}

// ---- Zone card ----
function ZoneCard({ zone, zi }: { zone: ZoneNode; zi: number }) {
  const allTikvs = zone.regions.flatMap(r => r.racks.flatMap(rk => rk.hosts.flatMap(h => h.tikvs)))
  const upC = allTikvs.filter(n => n.store.status_name === 'Up').length
  const dnC = allTikvs.filter(n => n.store.status_name !== 'Up').length
  const tfC = allTikvs.filter(n => isTiFlash(n.store)).length

  return (
    <Card
      style={{ marginBottom: 16 }}
      title={
        <Space>
          <CloudServerOutlined style={{ color: '#1677ff', fontSize: 18 }} />
          <Text strong style={{ fontSize: 16 }}>Zone: {zone.zone}</Text>
          <Tag color="blue">{zone.regions.length} Region</Tag>
          <Tag color="green">{upC} UP</Tag>
          {tfC > 0 && <Tag color="orange">{tfC} TiFlash</Tag>}
          {dnC > 0 && <Tag color="red">{dnC} DOWN</Tag>}
        </Space>
      }
    >
      <div style={{ marginLeft: 4, paddingLeft: 12, borderLeft: '2px solid #1677ff' }}>
        {zone.regions.map(r => <RegionSection key={r.region} region={r} />)}
      </div>
    </Card>
  )
}

// ---- Table view ----
function TopologyTable({ stores, resources, stm }: {
  stores: Store[]
  resources: Record<number, StoreResource>
  stm: Map<number, { name: string; isolation: string; labelKey: string; labelValue: string }>
}) {
  const rows = stores.slice().sort((a, b) => {
    const ipCmp = compareIP(ipOf(a), ipOf(b))
    if (ipCmp !== 0) return ipCmp
    return portOf(a) - portOf(b)
  })

  const columns: TableColumnsType<Store> = [
    {
      title: 'Store ID', dataIndex: 'id', width: 90, fixed: 'left',
      render: (id: number) => <Text strong>#{id}</Text>,
    },
    {
      title: '地址', dataIndex: 'address', width: 180,
      render: (addr: string) => <Text copyable style={{ fontSize: 13 }}>{addr}</Text>,
    },
    {
      title: '状态', dataIndex: 'status_name', width: 80,
      render: (s: string) => {
        const up = s === 'Up'
        const tf = false
        return <Badge status={up ? 'success' : 'error'} text={up ? 'UP' : s} />
      },
    },
    {
      title: '类型', width: 70,
      render: (_: unknown, r: Store) => isTiFlash(r)
        ? <Tag color="orange" style={{ margin: 0 }}>TiFlash</Tag>
        : <Tag color="green" style={{ margin: 0 }}>TiKV</Tag>,
    },
    {
      title: 'Zone', width: 80,
      render: (_: unknown, r: Store) => getLabel(r, 'zone') || <Text type="secondary">-</Text>,
    },
    {
      title: 'Region', width: 80,
      render: (_: unknown, r: Store) => getLabel(r, 'region') || <Text type="secondary">-</Text>,
    },
    {
      title: 'Rack', width: 80,
      render: (_: unknown, r: Store) => getLabel(r, 'rack') || <Text type="secondary">-</Text>,
    },
    {
      title: 'Host', width: 90,
      render: (_: unknown, r: Store) => getLabel(r, 'host') || <Text type="secondary">-</Text>,
    },
    {
      title: '磁盘', width: 120,
      render: (_: unknown, r: Store) => {
        const pct = pctUsed(r.capacity, r.available)
        return (
          <div>
            <Text style={{ fontSize: 12, color: pct > 80 ? '#ff4d4f' : undefined }}>{pct}%</Text>
            <Progress percent={pct} size="small" strokeColor={pct > 80 ? '#ff4d4f' : '#52c41a'} showInfo={false} />
            <Text type="secondary" style={{ fontSize: 10 }}>{fmtB(r.available)} / {fmtB(r.capacity)}</Text>
          </div>
        )
      },
    },
    {
      title: 'Regions', dataIndex: 'region_count', width: 80,
      render: (n: number) => <Text type="secondary">{n}</Text>,
    },
    {
      title: '内存', width: 110,
      render: (_: unknown, r: Store) => {
        const res = resources[r.id]
        if (!res || !res.memory_usage_pct) return <Text type="secondary" style={{ fontSize: 11 }}>-</Text>
        const pct = Math.round(res.memory_usage_pct)
        return (
          <Space size={4}>
            <Text style={{ fontSize: 12, color: pct > 85 ? '#ff4d4f' : undefined }}>{pct}%</Text>
          </Space>
        )
      },
    },
    {
      title: 'CPU', width: 130,
      render: (_: unknown, r: Store) => {
        const res = resources[r.id]
        if (!res) return <Text type="secondary" style={{ fontSize: 11 }}>-</Text>
        const pct = res.cpu_usage_pct ?? 0
        const quota = res.cpu_quota && res.cpu_quota > 0 ? `${(res.cpu_quota / 100).toFixed(1)} 核` : '不限'
        return (
          <div>
            {pct > 0
              ? <Text style={{ fontSize: 12, color: pct > 85 ? '#ff4d4f' : undefined }}>{Math.round(pct)}%</Text>
              : <Text type="secondary" style={{ fontSize: 11 }}>N/A</Text>}
            <Text type="secondary" style={{ fontSize: 10, marginLeft: 4 }}>/ {quota}</Text>
          </div>
        )
      },
    },
    {
      title: '租户绑定', width: 120,
      render: (_: unknown, r: Store) => {
        const b = stm.get(r.id)
        return b ? <Tag color="blue" style={{ margin: 0 }}>{b.name}</Tag> : null
      },
    },
    {
      title: '标签', width: 200,
      render: (_: unknown, r: Store) => (
        <Space size={2} wrap>
          {sortLabels((r.labels || []).filter(l => l.key !== 'engine')).map(l => (
            <Tag key={l.key} style={{ fontSize: 10, margin: 0, lineHeight: '16px' }}
              color={stm.get(r.id)?.labelKey === l.key ? 'blue' : 'default'}>
              {l.key}={l.value}
            </Tag>
          ))}
        </Space>
      ),
    },
  ]

  return (
    <Card>
      <Table
        columns={columns}
        dataSource={rows}
        rowKey="id"
        size="small"
        scroll={{ x: 1400 }}
        pagination={false}
      />
    </Card>
  )
}

// ---- main component ----
export default function Topology() {
  const { cid } = useCluster()
  const [stores, setStores] = useState<Store[]>([])
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(false)
  const [resources, setResources] = useState<Record<number, StoreResource>>({})
  const [viewMode, setViewMode] = useState<'card' | 'table'>('card')
  useEffect(() => {
    if (!cid) return
    setLoading(true); setResources({})
    Promise.all([api.listStores(cid), api.listTenants(cid).catch(() => [] as Tenant[])])
      .then(([stores, tenants]) => {
        setStores(stores); setTenants(tenants)
        Promise.allSettled(stores.filter(s => s.status_name === 'Up').map(s =>
          api.getStoreResource(cid, s.id).then(r => ({ id: s.id, r }))
        )).then(rs => {
          const m: Record<number, StoreResource> = {}
          rs.forEach(r => { if (r.status === 'fulfilled') m[r.value.id] = r.value.r })
          setResources(m)
        })
      }).finally(() => setLoading(false))
  }, [cid])

  const tree = useMemo(() => buildHierarchy(stores), [stores])
  const stm = useMemo(() => buildStoreTenantMap(stores, tenants), [stores, tenants])

  // attach resources + bindings to tree
  useMemo(() => {
    const attach = (nodes: TiKVNode[]) => {
      for (const n of nodes) {
        n.res = resources[n.store.id]
        const b = stm.get(n.store.id)
        n.binding = b ? { name: b.name, isolation: b.isolation, labelKey: b.labelKey, labelValue: b.labelValue } : undefined
      }
    }
    for (const z of tree.zones)
      for (const r of z.regions)
        for (const rk of r.racks)
          for (const h of rk.hosts)
            attach(h.tikvs)
  }, [tree, resources, stm])

  const totalStores = stores.length
  const upC = stores.filter(s => s.status_name === 'Up').length
  const dnC = stores.filter(s => s.status_name !== 'Up').length
  const tfC = stores.filter(isTiFlash).length
  const totalZones = tree.zones.length
  const totalRegions = tree.zones.reduce((a, z) => a + z.regions.length, 0)
  const boundC = stores.filter(s => stm.has(s.id)).length
  const canBindC = stores.filter(s => s.status_name === 'Up' && !isTiFlash(s) && !stm.has(s.id)).length

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card>
        <Row gutter={24}>
          <Col><Statistic title="Zone" value={totalZones} prefix={<GlobalOutlined />} /></Col>
          <Col><Statistic title="Region" value={totalRegions} prefix={<CloudServerOutlined />} /></Col>
          <Col><Statistic title="TiKV 节点" value={totalStores} prefix={<DatabaseOutlined />} /></Col>
          <Col>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>状态</Text>
              <div style={{ marginTop: 4 }}>
                <Space>
                  <Badge status="success" text={`${upC} UP`} />
                  {tfC > 0 && <Badge status="warning" text={`${tfC} TiFlash`} />}
                  {dnC > 0 && <Badge status="error" text={`${dnC} DOWN`} />}
                  <div style={{ width: 1, height: 12, background: '#d9d9d9' }} />
                  <Badge color="#4096ff" text={`${boundC} 已绑定`} />
                  <Badge color="#52c41a" text={`${canBindC} 可绑定`} />
                </Space>
              </div>
            </div>
          </Col>
        </Row>
      </Card>

      {!cid ? (
        <Card><Empty description="请先选择集群" /></Card>
      ) : loading ? (
        <div style={{ textAlign: 'center', padding: 80 }}><Spin size="large" /></div>
      ) : totalStores === 0 ? (
        <Card><Empty description="无 TiKV 节点" /></Card>
      ) : (
        <>
          {totalZones === 1 && viewMode === 'card' && (
            <Alert
              type="warning"
              showIcon
              message="当前仅 1 个 Zone，无法实现跨机房高可用。建议至少 3 个 Zone。"
            />
          )}
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <Segmented
              value={viewMode}
              onChange={v => setViewMode(v as 'card' | 'table')}
              options={[
                { label: '卡片视图', value: 'card', icon: <AppstoreOutlined /> },
                { label: '表格视图', value: 'table', icon: <UnorderedListOutlined /> },
              ]}
            />
          </div>
          {viewMode === 'card' ? (
            tree.zones.map((z, zi) => (
              <ZoneCard key={z.zone} zone={z} zi={zi} />
            ))
          ) : (
            <TopologyTable stores={stores} resources={resources} stm={stm} />
          )}
        </>
      )}
    </Space>
  )
}
