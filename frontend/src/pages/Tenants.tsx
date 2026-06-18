import { useEffect, useState } from 'react'
import {
  Table, Tag, Button, Space, Popconfirm, message, Alert, Modal, Form,
  InputNumber, Select, Switch, Descriptions, Card, Row, Col, Typography, Spin,
} from 'antd'
import { EditOutlined, DeleteOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { api, Tenant, TenantDetail, TenantResourceDetail } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text } = Typography

const isoColor: Record<string, string> = { PHYSICAL: 'purple', LOGICAL: 'blue', HYBRID: 'geekblue' }
const statusColor: Record<string, string> = { ACTIVE: 'green', SUSPENDED: 'orange', MIGRATING: 'gold', DELETED: 'red' }

function fmtSize(mb: number): string {
  if (!mb) return '—'
  if (mb >= 1024) return (mb / 1024).toFixed(2) + ' GiB'
  return mb + ' MiB'
}
function fmtB(s: string): string { const m = s.match(/([\d.]+)\s*(TiB|GiB|MiB)/i); return m ? parseFloat(m[1]).toFixed(1) + ' ' + m[2] : s }
function pctUsed(cap: string, avail: string): number {
  const m = cap.match(/([\d.]+)\s*(TiB|GiB|MiB)/i)
  if (!m) return 0
  const n = parseFloat(m[1]), gb = /ti/i.test(m[2]) ? n * 1024 : /gi/i.test(m[2]) ? n : n / 1024
  const a = avail.match(/([\d.]+)\s*(TiB|GiB|MiB)/i)
  if (!a) return 0
  const na = parseFloat(a[1]), ga = /ti/i.test(a[2]) ? na * 1024 : /gi/i.test(a[2]) ? na : na / 1024
  return gb <= 0 ? 0 : Math.max(0, Math.min(100, Math.round((1 - ga / gb) * 100)))
}

export default function Tenants() {
  const nav = useNavigate()
  const { cid } = useCluster()
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(false)
  const [detail, setDetail] = useState<Record<string, TenantDetail>>({})
  const [detailLoading, setDetailLoading] = useState<Record<string, boolean>>({})
  const [resource, setResource] = useState<Record<string, TenantResourceDetail>>({})

  // edit modal
  const [editOpen, setEditOpen] = useState(false)
  const [editTenant, setEditTenant] = useState<Tenant | null>(null)
  const [editForm] = Form.useForm()
  const [editSubmitting, setEditSubmitting] = useState(false)

  useEffect(() => {
    if (!cid) return
    setLoading(true)
    api.listTenants(cid).then((ts: Tenant[]) => {
      setTenants(ts)
      // 预加载每个租户的关联数据库和 TiKV 实例
      ts.forEach(t => {
        api.getTenantDetail(t.id).then(d => {
          setDetail(s => ({ ...s, [String(t.id)]: d }))
        }).catch(() => {})
        api.getTenantResource(t.id).then((r: any) => {
          if (r) setResource(s => ({ ...s, [String(t.id)]: r }))
        }).catch(() => {})
      })
    }).finally(() => setLoading(false))
  }, [cid])

  const refresh = () => cid && api.listTenants(cid).then(setTenants)

  const onExpand = (expanded: boolean, t: Tenant) => {
    if (!expanded) return
    const key = String(t.id)
    if (detail[key]) return
    setDetailLoading(s => ({ ...s, [key]: true }))
    Promise.all([
      api.getTenantDetail(t.id),
      api.getTenantResource(t.id).catch(() => null),
    ]).then(([d, r]) => {
      setDetail(s => ({ ...s, [key]: d }))
      if (r) setResource(s => ({ ...s, [key]: r }))
    }).catch(e => message.error('加载详情失败：' + (e?.message || e)))
      .finally(() => setDetailLoading(s => ({ ...s, [key]: false })))
  }

  const openEdit = (t: Tenant) => {
    setEditTenant(t)
    editForm.setFieldsValue({
      ru_per_sec: t.ru_per_sec,
      priority: t.priority,
      burstable: true,
    })
    setEditOpen(true)
  }

  const handleEdit = async () => {
    try {
      const v = await editForm.validateFields()
      setEditSubmitting(true)
      await api.updateTenant(editTenant!.id, {
        ru_per_sec: v.ru_per_sec,
        priority: v.priority,
        burstable: v.burstable,
      })
      message.success('租户配置已更新')
      setEditOpen(false)
      refresh()
      // 刷新展开的详情
      const key = String(editTenant!.id)
      if (detail[key]) {
        setDetail(s => { const ns = { ...s }; delete ns[key]; return ns })
      }
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(e?.response?.data?.error || e?.message || '更新失败')
    } finally {
      setEditSubmitting(false)
    }
  }

  const handleDelete = async (t: Tenant) => {
    try {
      await api.deleteTenant(t.id)
      message.success(`租户 ${t.name} 已删除`)
      refresh()
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '删除失败')
    }
  }

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button type="primary" disabled={!cid} onClick={() => nav('/tenants/new')}>+ 创建租户</Button>
      </Space>
      {!cid ? (
        <Alert type="info" showIcon message="请在顶部选择集群后查看租户。" />
      ) : (
        <Table rowKey="id" loading={loading} dataSource={tenants} pagination={false}
          expandable={{ onExpand, expandedRowRender: (t) => (
            <TenantAssoc
              detail={detail[String(t.id)]}
              resource={resource[String(t.id)]}
              loading={detailLoading[String(t.id)]}
              tenant={t}
            />
          ) }}
          columns={[
            { title: '租户', dataIndex: 'name' },
            { title: '隔离级别', dataIndex: 'isolation_level',
              render: (v: string) => <Tag color={isoColor[v]}>{v}</Tag> },
            { title: '放置策略', dataIndex: 'placement_policy', render: (v?: string) => v || <Tag>默认</Tag> },
            { title: '资源组', dataIndex: 'resource_group' },
            { title: '关联数据库', key: 'databases', width: 200,
              render: (_, t) => {
                const d = detail[String(t.id)]
                if (!d) return <Text type="secondary" style={{ fontSize: 12 }}>加载中...</Text>
                if (!d.databases || d.databases.length === 0) return <Tag>无</Tag>
                return (
                  <Space wrap size={4}>
                    {d.databases.map(db => <Tag key={db.name} style={{ fontSize: 11 }}>{db.name}</Tag>)}
                  </Space>
                )
              } },
            { title: 'TiKV 实例', key: 'stores', width: 120,
              render: (_, t) => {
                const d = detail[String(t.id)]
                if (!d) return <Text type="secondary" style={{ fontSize: 12 }}>...</Text>
                if (d.store_match_mode === 'all' || !d.stores || d.stores.length === 0)
                  return <Tag>共享（{d.total_store_count || 0}）</Tag>
                return <Tag color="green">{d.stores.length} 节点</Tag>
              } },
            { title: '状态', dataIndex: 'status',
              render: (v: string) => <Tag color={statusColor[v]}>{v}</Tag> },
            { title: '操作', width: 280, render: (_, t) => (
              <Space>
                <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(t)}>编辑</Button>
                {t.status === 'ACTIVE' &&
                  <Popconfirm title="挂起？将把资源组 RU 降到 1（近乎停摆）" onConfirm={async () => {
                    await api.suspendTenant(t.id); message.success('已挂起'); refresh()
                  }}><Button size="small">挂起</Button></Popconfirm>}
                {t.status === 'SUSPENDED' &&
                  <Button size="small" onClick={async () => {
                    await api.activateTenant(t.id); message.success('已恢复'); refresh()
                  }}>恢复</Button>}
                <Popconfirm
                  title={`确定删除租户 ${t.name}？`}
                  description="将删除关联数据库、用户、资源组、放置策略（不可恢复）"
                  onConfirm={() => handleDelete(t)}
                  okText="删除" cancelText="取消" okButtonProps={{ danger: true }}
                >
                  <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
                </Popconfirm>
              </Space>
            )},
          ]}
        />
      )}

      {/* Edit Modal */}
      <Modal
        title={`编辑租户 - ${editTenant?.name || ''}`}
        open={editOpen}
        onCancel={() => setEditOpen(false)}
        onOk={handleEdit}
        confirmLoading={editSubmitting}
        okText="保存"
        cancelText="取消"
      >
        <Form form={editForm} layout="vertical">
          <Form.Item name="ru_per_sec" label="RU/s 配额"
            tooltip="每秒可使用的 Request Unit 数量">
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="priority" label="优先级"
            tooltip="资源争用时优先分配的级别">
            <Select options={[
              { value: 'LOW', label: 'LOW' },
              { value: 'MEDIUM', label: 'MEDIUM' },
              { value: 'HIGH', label: 'HIGH' },
            ]} />
          </Form.Item>
          <Form.Item name="burstable" label="允许突发 (BURSTABLE)"
            tooltip="允许短时超出 RU 配额" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Alert type="info" showIcon style={{ marginTop: 8 }}
            message="修改将通过 ALTER RESOURCE GROUP 实时生效。" />
        </Form>
      </Modal>
    </div>
  )
}

function TenantAssoc({ detail, resource, loading, tenant }: {
  detail?: TenantDetail; resource?: TenantResourceDetail; loading?: boolean; tenant: Tenant
}) {
  if (loading) return <div style={{ padding: 16 }}><Spin /></div>
  if (!detail) return <div style={{ padding: 16, color: '#94a3b8' }}>暂无数据</div>

  const byLabel = detail.store_match_mode === 'label'
  const totalDB = detail.databases.length
  const totalSize = detail.databases.reduce((a, d) => a + (d.size_mb || 0), 0)
  const rg = resource?.resource_group
  const pp = resource?.placement_policy
  const isPhysical = tenant.isolation_level === 'PHYSICAL' || tenant.isolation_level === 'HYBRID'
  const isLogical = tenant.isolation_level === 'LOGICAL' || tenant.isolation_level === 'HYBRID'

  return (
    <div style={{ padding: '8px 8px 4px' }}>
      <Row gutter={16}>
        {/* 资源上限概览 */}
        {(isLogical && rg) && (
          <Col span={24} style={{ marginBottom: 16 }}>
            <Card size="small" title={<><Text strong>⚡ 资源管控 (Resource Group)</Text> <Tag color="blue">{rg.name}</Tag></>}>
              <Descriptions size="small" column={4}>
                <Descriptions.Item label="RU/s">{rg.ru_per_sec}</Descriptions.Item>
                <Descriptions.Item label="优先级">
                  <Tag color={rg.priority === 'HIGH' ? 'red' : rg.priority === 'LOW' ? 'default' : 'orange'}>{rg.priority}</Tag>
                </Descriptions.Item>
                <Descriptions.Item label="突发">{rg.burstable ? <Tag color="green">允许</Tag> : <Tag>不允许</Tag>}</Descriptions.Item>
                <Descriptions.Item label="隔离模式">
                  <Tag color={isoColor[tenant.isolation_level]}>{tenant.isolation_level}</Tag>
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
        )}

        {/* Placement Policy 详情 */}
        {(isPhysical && pp) && (
          <Col span={24} style={{ marginBottom: 16 }}>
            <Card size="small" title={<><Text strong>📍 放置策略 (Placement Policy)</Text> <Tag color="purple">{pp.policy_name}</Tag></>}>
              <Descriptions size="small" column={3}>
                {pp.primary_region && <Descriptions.Item label="PRIMARY_REGION">{pp.primary_region}</Descriptions.Item>}
                {pp.regions && <Descriptions.Item label="REGIONS">{pp.regions}</Descriptions.Item>}
                {pp.schedule && <Descriptions.Item label="SCHEDULE">{pp.schedule}</Descriptions.Item>}
                {pp.constraints && <Descriptions.Item label="CONSTRAINTS">{pp.constraints}</Descriptions.Item>}
                {pp.leader_constraints && <Descriptions.Item label="LEADER_CONSTRAINTS">{pp.leader_constraints}</Descriptions.Item>}
                {pp.follower_constraints && <Descriptions.Item label="FOLLOWER_CONSTRAINTS">{pp.follower_constraints}</Descriptions.Item>}
                <Descriptions.Item label="Followers">{pp.followers}</Descriptions.Item>
                <Descriptions.Item label="Learners">{pp.learners || 0}</Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
        )}

        {/* 关联数据库 */}
        <Col span={24} style={{ marginBottom: 16 }}>
          <Card size="small" title={
            <Space>
              <Text strong>🗂️ 关联数据库</Text>
              <Tag color="blue">{totalDB} 个</Tag>
              <Tag color="geekblue">合计 {fmtSize(totalSize)}</Tag>
            </Space>
          }>
            {totalDB === 0 ? (
              <Text type="secondary">该租户暂未绑定数据库</Text>
            ) : (
              <Table rowKey="name" size="small" pagination={false} dataSource={detail.databases}
                columns={[
                  { title: '数据库', dataIndex: 'name', render: (v: string) => <code>{v}</code> },
                  { title: '表数量', dataIndex: 'table_count', width: 100, render: (v: number) => v || '—' },
                  { title: '数据大小', dataIndex: 'size_mb', width: 140, render: (v: number) => fmtSize(v) },
                ]}
              />
            )}
          </Card>
        </Col>

        {/* 承载数据的 TiKV 实例（物理隔离） */}
        {isPhysical && (
          <Col span={24}>
            <Card size="small" title={
              <Space>
                <Text strong>💾 承载数据的 TiKV 实例</Text>
                {byLabel ? (
                  <>
                    <Tag color="green">{detail.stores.length} 个 store</Tag>
                    <Tag color="cyan">{new Set(detail.stores.map(s => (s.address || '').split(':')[0])).size} 台主机</Tag>
                    <Tag color="purple">{tenant.label_key}={tenant.label_value}</Tag>
                  </>
                ) : (
                  <Tag color="blue">逻辑隔离：全集群 {detail.total_store_count} 个 store（共享）</Tag>
                )}
              </Space>
            }>
              {detail.store_shared_note && (
                <Alert type="info" showIcon style={{ marginBottom: 8 }} message={detail.store_shared_note} />
              )}
              {detail.stores.length === 0 ? (
                <Text type="secondary">未找到匹配标签 <code>{tenant.label_key}={tenant.label_value}</code> 的 store</Text>
              ) : (
                <Row gutter={[8, 8]}>
                  {detail.stores.map(s => {
                    const up = s.status_name === 'Up'
                    const diskUsed = pctUsed(s.capacity, s.available)
                    return (
                      <Col key={s.id}>
                        <Card size="small" style={{ width: 240, borderColor: up ? '#d9d9d9' : '#ffccc7' }}>
                          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
                            <Text strong>store #{s.id}</Text>
                            <Tag color={up ? 'green' : 'red'} style={{ fontSize: 10 }}>{s.status_name}</Tag>
                          </div>
                          <div style={{ fontSize: 12, color: '#666' }}>{s.address}</div>
                          <div style={{ fontSize: 11, color: '#999', marginTop: 2 }}>
                            磁盘 {diskUsed}% · {fmtB(s.available)} 可用 · {s.region_count} regions
                          </div>
                          {(s.labels || []).filter(l => l.key !== 'engine').length > 0 && (
                            <div style={{ marginTop: 4, display: 'flex', flexWrap: 'wrap', gap: 2 }}>
                              {(s.labels || []).filter(l => l.key !== 'engine').map(l => (
                                <Tag key={l.key}
                                  color={byLabel && l.key === tenant.label_key ? 'green' : 'default'}
                                  style={{ fontSize: 10, margin: 0, padding: '0 4px' }}>{l.key}={l.value}</Tag>
                              ))}
                            </div>
                          )}
                        </Card>
                      </Col>
                    )
                  })}
                </Row>
              )}
            </Card>
          </Col>
        )}
      </Row>
    </div>
  )
}
