import { useEffect, useMemo, useState } from 'react'
import {
  Spin, Empty, Space, Typography, Tag, Progress, Tooltip, Card, Row, Col,
  Statistic, Badge, Collapse, Alert,
} from 'antd'
import {
  GlobalOutlined, CloudServerOutlined, DatabaseOutlined, HddOutlined,
  ApartmentOutlined,
} from '@ant-design/icons'
import { api, Store, StoreResource, Tenant } from '../api/client'
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
        hosts: Object.entries(hostMap).map(([host, tikvs]) => ({ host, tikvs })),
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
                {cpuQ > 0 && <Text style={{ fontSize: 11 }}>{cpuQ}%</Text>}
                <Tag style={{ fontSize: 9, margin: 0, lineHeight: '16px' }} color={cpuLimitFmt === '不限' ? 'default' : 'blue'}>{'上限: ' + cpuLimitFmt}</Tag>
              </Space>
            </div>
            {cpuQ > 0
              ? <Progress percent={Math.min(cpuQ, 100)} size="small" strokeColor="#1677ff" showInfo={false} />
              : <Text type="secondary" style={{ fontSize: 10 }}>{'未获取使用率'}</Text>}
          </div>
        )}

        {(store.labels || []).length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 2, borderTop: '1px solid #f0f0f0', paddingTop: 4 }}>
            {(store.labels || []).filter(l => l.key !== 'engine').map(l => (
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

// ---- main component ----
export default function Topology() {
  const { cid } = useCluster()
  const [stores, setStores] = useState<Store[]>([])
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(false)
  const [resources, setResources] = useState<Record<number, StoreResource>>({})

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
          {totalZones === 1 && (
            <Alert
              type="warning"
              showIcon
              message="当前仅 1 个 Zone，无法实现跨机房高可用。建议至少 3 个 Zone。"
            />
          )}
          {tree.zones.map((z, zi) => (
            <ZoneCard key={z.zone} zone={z} zi={zi} />
          ))}
        </>
      )}
    </Space>
  )
}
