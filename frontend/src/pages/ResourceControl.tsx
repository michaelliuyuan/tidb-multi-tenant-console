import { useEffect, useState } from 'react'
import {
  Card, Table, Tag, Empty, Spin, Typography, Alert, Space, Statistic, Row, Col,
  Button, Modal, Form, Input, InputNumber, Select, Switch, Popconfirm, message,
} from 'antd'
import { PlusOutlined, ReloadOutlined, EditOutlined, DeleteOutlined } from '@ant-design/icons'
import { api } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text } = Typography

interface RG {
  name: string
  ru_per_sec: number
  priority: string
  burstable: number
}

export default function ResourceControl() {
  const { cid } = useCluster()
  const [groups, setGroups] = useState<RG[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>()
  const [modalOpen, setModalOpen] = useState(false)
  const [editMode, setEditMode] = useState(false)
  const [editName, setEditName] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [form] = Form.useForm()
  const [previewSQL, setPreviewSQL] = useState('')

  const load = () => {
    if (!cid) return
    setLoading(true); setError(undefined)
    api.listResourceGroups(cid)
      .then((d: any) => setGroups(Array.isArray(d) ? d : []))
      .catch(e => { setGroups([]); setError(e?.response?.data?.error || e?.message || '加载失败') })
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [cid])

  // SQL preview
  const formValues = Form.useWatch([], form) || {}
  useEffect(() => {
    const v = formValues as any
    if (!v.name && !editMode) { setPreviewSQL(''); return }
    const parts: string[] = []
    if (v.ru_per_sec != null) parts.push(`RU_PER_SEC = ${v.ru_per_sec}`)
    else parts.push('RU_PER_SEC = UNLIMITED')
    if (v.burstable) parts.push('BURSTABLE')
    if (v.priority) parts.push(`PRIORITY = ${v.priority}`)
    const name = editMode ? editName : v.name
    if (!name) { setPreviewSQL(''); return }
    const verb = editMode ? 'ALTER' : 'CREATE'
    const ifNot = editMode ? '' : 'IF NOT EXISTS '
    setPreviewSQL(`${verb} RESOURCE GROUP ${ifNot}\`${name}\` ${parts.join(' ')}`)
  }, [formValues, editMode, editName])

  const openCreate = () => {
    setEditMode(false); setEditName('')
    form.resetFields()
    form.setFieldsValue({ ru_per_sec: 1000, priority: 'MEDIUM', burstable: false })
    setModalOpen(true)
  }

  const openEdit = (rg: RG) => {
    setEditMode(true); setEditName(rg.name)
    form.setFieldsValue({
      ru_per_sec: rg.ru_per_sec > 0 ? rg.ru_per_sec : undefined,
      priority: rg.priority || 'MEDIUM',
      burstable: !!rg.burstable,
    })
    setModalOpen(true)
  }

  const handleSubmit = async () => {
    try {
      const v = await form.validateFields()
      setSubmitting(true)
      const body: any = {}
      if (v.ru_per_sec != null) body.ru_per_sec = v.ru_per_sec
      if (v.priority) body.priority = v.priority
      body.burstable = !!v.burstable
      if (editMode) {
        await api.alterResourceGroup(cid!, editName, body)
        message.success(`资源组 ${editName} 已修改`)
      } else {
        body.name = v.name
        await api.createResourceGroup(cid!, body)
        message.success(`资源组 ${v.name} 创建成功`)
      }
      setModalOpen(false)
      load()
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(e?.response?.data?.error || e?.message || '操作失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleDelete = async (name: string) => {
    try {
      await api.dropResourceGroup(cid!, name)
      message.success(`资源组 ${name} 已删除`)
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '删除失败')
    }
  }

  const limited = groups.filter(g => g.ru_per_sec > 0)
  const totalRU = limited.reduce((a, g) => a + g.ru_per_sec, 0)
  const burstCount = groups.filter(g => g.burstable).length

  const ruCell = (ru: number) =>
    ru <= 0 ? <Tag color="gold">UNLIMITED</Tag> : <span>{ru.toLocaleString()} RU/s</span>

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card title="资源管控（Resource Control）"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}
              disabled={!cid}>创建资源组</Button>
          </Space>
        }>

        {!cid ? (
          <Empty description="请先在顶部选择集群" style={{ marginTop: 40 }} />
        ) : loading ? (
          <div style={{ textAlign: 'center', padding: 60 }}><Spin size="large" /></div>
        ) : error ? (
          <Alert type="error" showIcon message="加载资源组失败" description={error} />
        ) : (
          <>
            <Row gutter={16} style={{ marginBottom: 16 }}>
              <Col span={6}><Card size="small"><Statistic title="资源组总数" value={groups.length} /></Card></Col>
              <Col span={6}><Card size="small"><Statistic title="有限额组" value={limited.length} /></Card></Col>
              <Col span={6}><Card size="small"><Statistic title="配额总和" value={totalRU} suffix="RU/s" /></Card></Col>
              <Col span={6}><Card size="small"><Statistic title="允许突发(BURSTABLE)" value={burstCount} /></Card></Col>
            </Row>

            <Table<RG> rowKey="name" size="middle" dataSource={groups} pagination={false}
              locale={{ emptyText: '该集群无资源组（需 TiDB v7.1+ 开启 Resource Control）' }}
              columns={[
                { title: '资源组', dataIndex: 'name', render: (v: string) => <code>{v}</code> },
                { title: 'RU/s 配额', dataIndex: 'ru_per_sec', width: 180, render: ruCell },
                {
                  title: '优先级', dataIndex: 'priority', width: 120,
                  render: (v: string) => <Tag color={v === 'HIGH' ? 'red' : v === 'LOW' ? 'default' : 'blue'}>{v || '—'}</Tag>,
                },
                {
                  title: 'BURSTABLE', dataIndex: 'burstable', width: 160,
                  render: (v: number) => v ? <Tag color="green">是（允许短时突发）</Tag> : <Tag>否</Tag>,
                },
                { title: '操作', width: 160, render: (_, rg) => (
                  <Space>
                    <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(rg)}>编辑</Button>
                    {rg.name !== 'default' && (
                      <Popconfirm
                        title={`确定删除资源组 ${rg.name}？`}
                        description="绑定该组的用户将回退到 default 组"
                        onConfirm={() => handleDelete(rg.name)}
                        okText="删除" cancelText="取消" okButtonProps={{ danger: true }}
                      >
                        <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
                      </Popconfirm>
                    )}
                  </Space>
                )},
              ]}
            />
            <Alert type="info" showIcon style={{ marginTop: 12 }}
              message="RU_PER_SEC 为软限流配额；default 组通常为 UNLIMITED 且不可删除。修改通过 ALTER RESOURCE GROUP 实时生效。" />
          </>
        )}
      </Card>

      {/* Create/Edit Modal */}
      <Modal
        title={editMode ? `编辑资源组 - ${editName}` : '创建资源组'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
        confirmLoading={submitting}
        okText={editMode ? '保存' : '创建'}
        cancelText="取消"
        width={520}
        destroyOnClose
      >
        <Form form={form} layout="vertical">
          {!editMode && (
            <Form.Item name="name" label="资源组名称" rules={[
              { required: true, message: '请输入资源组名称' },
              { pattern: /^[a-zA-Z_][a-zA-Z0-9_]*$/, message: '只能包含字母、数字和下划线' },
            ]}>
              <Input placeholder="例如：rg_report" />
            </Form.Item>
          )}

          <Form.Item name="ru_per_sec" label="RU/s 配额"
            tooltip="每秒可使用的 Request Unit 数量。留空或 0 表示 UNLIMITED（无限制）">
            <InputNumber min={0} style={{ width: '100%' }} placeholder="留空 = UNLIMITED" />
          </Form.Item>

          <Form.Item name="priority" label="优先级"
            tooltip="资源争用时优先分配的级别。LOW / MEDIUM / HIGH">
            <Select options={[
              { value: 'LOW', label: 'LOW（低优先级）' },
              { value: 'MEDIUM', label: 'MEDIUM（中优先级，默认）' },
              { value: 'HIGH', label: 'HIGH（高优先级）' },
            ]} />
          </Form.Item>

          <Form.Item name="burstable" label="允许突发 (BURSTABLE)"
            tooltip="允许在系统资源充足时短时超出 RU 配额" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>

        {previewSQL && (
          <div style={{ marginTop: 8 }}>
            <Text type="secondary">SQL 预览：</Text>
            <pre style={{
              background: '#f5f5f5', padding: 8, borderRadius: 4, fontSize: 12,
              marginTop: 4, overflow: 'auto',
            }}>
              {previewSQL}
            </pre>
          </div>
        )}
      </Modal>
    </Space>
  )
}
