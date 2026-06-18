import { useEffect, useState } from 'react'
import {
  Card, Table, Tag, Button, Space, Popconfirm, message, Alert, Modal, Select,
  Empty, Spin, Typography, Input,
} from 'antd'
import { ReloadOutlined, DatabaseOutlined, TableOutlined, LinkOutlined } from '@ant-design/icons'
import { api, DatabaseInfo, TableInfo, PlacementPolicy } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text } = Typography

function fmtSize(mb: number): string {
  if (!mb) return '—'
  if (mb >= 1024) return (mb / 1024).toFixed(2) + ' GiB'
  return mb + ' MiB'
}

export default function DatabaseManagement() {
  const { cid } = useCluster()
  const [databases, setDatabases] = useState<DatabaseInfo[]>([])
  const [loading, setLoading] = useState(false)
  const [policies, setPolicies] = useState<PlacementPolicy[]>([])
  const [expandedTables, setExpandedTables] = useState<Record<string, TableInfo[]>>({})
  const [tablesLoading, setTablesLoading] = useState<Record<string, boolean>>({})
  const [bindModalOpen, setBindModalOpen] = useState(false)
  const [bindTarget, setBindTarget] = useState<{ type: 'db' | 'table'; db: string; table?: string; current?: string } | null>(null)
  const [selectedPolicy, setSelectedPolicy] = useState('')

  const load = () => {
    if (!cid) return
    setLoading(true)
    Promise.all([
      api.listDatabases(cid).catch(() => [] as DatabaseInfo[]),
      api.listPlacementPolicies(cid).catch(() => [] as PlacementPolicy[]),
    ]).then(([d, p]) => {
      setDatabases(Array.isArray(d) ? d : [])
      setPolicies(Array.isArray(p) ? p : [])
    }).finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [cid])

  const onExpandDB = (expanded: boolean, db: DatabaseInfo) => {
    if (!expanded) return
    const key = db.name
    if (expandedTables[key]) return
    setTablesLoading(s => ({ ...s, [key]: true }))
    api.listTables(cid!, db.name).then(t => setExpandedTables(s => ({ ...s, [key]: t || [] })))
      .catch(() => message.error('加载表列表失败'))
      .finally(() => setTablesLoading(s => ({ ...s, [key]: false })))
  }

  const openBindModal = (type: 'db' | 'table', db: string, table?: string, current?: string) => {
    setBindTarget({ type, db, table, current })
    setSelectedPolicy(current || '')
    setBindModalOpen(true)
  }

  const handleBind = async () => {
    if (!bindTarget) return
    try {
      if (bindTarget.type === 'db') {
        await api.bindDatabasePolicy(cid!, bindTarget.db, selectedPolicy)
      } else {
        await api.bindTablePolicy(cid!, bindTarget.db, bindTarget.table!, selectedPolicy)
      }
      message.success(`${bindTarget.type === 'db' ? '数据库' : '表'}放置策略已更新`)
      setBindModalOpen(false)
      load()
      // 刷新展开的表
      if (bindTarget.type === 'db') {
        setExpandedTables(s => { const ns = { ...s }; delete ns[bindTarget.db]; return ns })
      }
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '操作失败')
    }
  }

  const policyOptions = [
    { value: '', label: 'default（无策略/解绑）' },
    ...policies.map(p => ({ value: p.policy_name, label: p.policy_name })),
  ]

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card title="数据库管理"
        extra={<Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>}>
        {!cid ? (
          <Empty description="请先选择集群" />
        ) : (
          <>
            <Alert type="info" showIcon style={{ marginBottom: 12 }}
              message="查看数据库和表结构，管理放置策略绑定。通过 ALTER DATABASE/TABLE ... PLACEMENT POLICY 控制。" />
            <Table<DatabaseInfo> rowKey="name" size="small" loading={loading}
              dataSource={databases} pagination={false}
              expandable={{ onExpand: onExpandDB, expandedRowRender: (db) => (
                <DBTables tables={expandedTables[db.name]} loading={tablesLoading[db.name]}
                  onBindTable={(tname, current) => openBindModal('table', db.name, tname, current)} />
              ) }}
              locale={{ emptyText: '无数据库' }}
              columns={[
                { title: '数据库', dataIndex: 'name', render: (v: string) => <Space><DatabaseOutlined /><Text strong>{v}</Text></Space> },
                { title: '表数量', dataIndex: 'table_count', width: 100, align: 'center' },
                { title: '数据大小', dataIndex: 'size_mb', width: 120, render: fmtSize },
                { title: '放置策略', dataIndex: 'placement_policy', width: 180,
                  render: (v?: string) => v ? <Tag color="purple">{v}</Tag> : <Tag>default</Tag> },
                { title: '操作', width: 120, render: (_, db) => (
                  <Button size="small" icon={<LinkOutlined />}
                    onClick={() => openBindModal('db', db.name, undefined, db.placement_policy)}>
                    绑定策略
                  </Button>
                )},
              ]}
            />
          </>
        )}
      </Card>

      {/* Bind Policy Modal */}
      <Modal title={bindTarget?.type === 'db'
        ? `数据库「${bindTarget?.db}」放置策略`
        : `表「${bindTarget?.db}.${bindTarget?.table}」放置策略`}
        open={bindModalOpen}
        onCancel={() => setBindModalOpen(false)}
        onOk={handleBind}
        okText="保存" cancelText="取消"
      >
        <div style={{ marginBottom: 8 }}>
          <Text type="secondary">当前策略：</Text>
          {bindTarget?.current ? <Tag color="purple">{bindTarget.current}</Tag> : <Tag>default</Tag>}
        </div>
        <div style={{ marginBottom: 8 }}>
          <Text type="secondary">选择放置策略：</Text>
        </div>
        <Select
          style={{ width: '100%' }}
          value={selectedPolicy}
          onChange={setSelectedPolicy}
          options={policyOptions}
          showSearch
        />
        <Alert type="info" showIcon style={{ marginTop: 12 }}
          message="选择 default 解除绑定。修改后数据将按新策略调度，可能触发 Region 迁移。" />
      </Modal>
    </Space>
  )
}

function DBTables({ tables, loading, onBindTable }: {
  tables?: TableInfo[]; loading?: boolean;
  onBindTable: (tname: string, current?: string) => void
}) {
  if (loading) return <div style={{ padding: 16 }}><Spin /></div>
  if (!tables || tables.length === 0) return <div style={{ padding: 16, color: '#999' }}>无表</div>
  return (
    <Table<TableInfo> rowKey={r => r.name} size="small" pagination={false} dataSource={tables}
      columns={[
        { title: '表名', dataIndex: 'name', render: (v: string) => <Space><TableOutlined /><code>{v}</code></Space> },
        { title: '引擎', dataIndex: 'engine', width: 100,
          render: (v: string) => v === 'TiFlash' ? <Tag color="orange">TiFlash</Tag> : <Tag>{v}</Tag> },
        { title: '行数', dataIndex: 'row_count', width: 120, render: (v: number) => v?.toLocaleString() || '—' },
        { title: '大小', dataIndex: 'size_mb', width: 100, render: fmtSize },
        { title: '放置策略', dataIndex: 'placement_policy', width: 160,
          render: (v?: string) => v ? <Tag color="purple">{v}</Tag> : <Tag>继承/默认</Tag> },
        { title: '操作', width: 100, render: (_, t) => (
          <Button size="small" icon={<LinkOutlined />} onClick={() => onBindTable(t.name, t.placement_policy)}>
            绑定
          </Button>
        )},
      ]}
    />
  )
}
