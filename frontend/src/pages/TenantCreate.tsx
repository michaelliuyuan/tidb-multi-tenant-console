import { useEffect, useState } from 'react'
import {
  Steps, Form, Input, InputNumber, Select, Switch, Button, Space, Card, Alert,
  message, Typography, Checkbox, Tag, Divider, Radio,
} from 'antd'
import { useNavigate } from 'react-router-dom'
import { api, Store, PlacementPolicy, PlacementLabel } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text } = Typography

export default function TenantCreate() {
  const nav = useNavigate()
  const { clusters, cid } = useCluster()
  const [cur, setCur] = useState(0)
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)

  // 集群已有资源（供选择）
  const [stores, setStores] = useState<Store[]>([])
  const [policies, setPolicies] = useState<PlacementPolicy[]>([])
  const [databases, setDatabases] = useState<string[]>([])
  const [resourceGroups, setResourceGroups] = useState<string[]>([])
  const [placementLabels, setPlacementLabels] = useState<PlacementLabel[]>([])

  // 模式切换
  const [dbMode, setDbMode] = useState<'existing' | 'new'>('new')
  const [policyMode, setPolicyMode] = useState<'existing' | 'new' | 'none'>('new')
  const [rgMode, setRgMode] = useState<'existing' | 'new'>('new')

  const tenantName = Form.useWatch('name', form)

  useEffect(() => {
    if (!cid) return
    Promise.all([
      api.listStores(cid).catch(() => [] as Store[]),
      api.listPlacementPolicies(cid).catch(() => [] as PlacementPolicy[]),
      api.listDatabases(cid).catch(() => []),
      api.listResourceGroups(cid).catch(() => []),
      api.listPlacementLabels(cid).catch(() => [] as PlacementLabel[]),
    ]).then(([s, p, dbs, rgs, lbls]) => {
      setStores(s as Store[])
      setPolicies(p as PlacementPolicy[])
      setDatabases((dbs as any[]).map(d => d.name))
      setResourceGroups((rgs as any[]).map(r => r.name))
      setPlacementLabels(lbls as PlacementLabel[])
    })
  }, [cid])

  useEffect(() => { if (cid) form.setFieldValue('cluster_id', cid) }, [cid, form])
  useEffect(() => {
    if (tenantName) {
      form.setFieldValue('db_name', tenantName)
      form.setFieldValue('username', `${tenantName}_user`)
      form.setFieldValue('policy_name', `t_${tenantName}_pol`)
      form.setFieldValue('rg_name', `t_${tenantName}_rg`)
    }
  }, [tenantName, form])

  const steps = ['创建租户', '数据库与位置', '资源配置', '确认']

  const onFinish = async () => {
    const v = form.getFieldsValue(true)
    setSubmitting(true)
    try {
      const isoLevel = (v.policy_mode === 'existing' || v.policy_mode === 'new') ? 'HYBRID' : 'LOGICAL'
      await api.createTenant({
        name: v.name, cluster_id: v.cluster_id, isolation_level: isoLevel,
        label_key: labelKey,
        label_value: v.label_value || v.name,
        placement: {
          primary_region: v.primary_region || '',
          regions: Array.isArray(v.regions) ? v.regions.join(',') : '',
          voters: v.voters || 3, followers: v.followers || 2,
          survival_preferences: v.survival || 'region,host',
        },
        resource_group: { ru_per_sec: v.ru_per_sec || 2000, burstable: v.burstable ?? true, priority: v.priority || 'MEDIUM' },
        databases: v.db_mode === 'existing' && v.existing_db ? [v.existing_db] : [v.db_name || v.name],
        users: [{ username: v.username || `${v.name}_user`, password: v.password || 'ChangeMe!2026' }],
        gradual: v.gradual || false,
      })
      message.success('租户创建成功')
      nav('/tenants')
    } catch (e: any) {
      message.error('创建失败：' + (e?.response?.data?.error || e.message))
    } finally {
      setSubmitting(false)
    }
  }

  const stepFields: string[][] = [
    ['name', 'cluster_id', 'username', 'password'],
    ['db_mode', 'db_name', 'existing_db', 'policy_mode', 'policy_name', 'label_value', 'existing_policy'],
    ['rg_mode', 'rg_name', 'existing_rg', 'ru_per_sec', 'priority', 'burstable'],
    [],
  ]

  const next = async () => {
    try {
      const fields = stepFields[cur]
      if (fields.length > 0) {
        await form.validateFields(fields)
      }
      setCur(c => Math.min(c + 1, steps.length - 1))
    } catch (e: any) {
      if (e?.errorFields) {
        message.warning('请填写必填项后再继续')
      }
    }
  }
  const prev = () => setCur(c => Math.max(c - 1, 0))

  const policyOptions = policies.map(p => ({ value: p.policy_name, label: p.policy_name }))
  const rgOptions = resourceGroups.map(r => ({ value: r, label: r }))
  const dbOptions = databases.map(d => ({ value: d, label: d }))

  // 从集群已有 placement labels 中提取 "app" 标签的可选值
  const labelKey = 'app'
  const appLabel = placementLabels.find(l => l.key === labelKey)
  const labelValueOptions = (appLabel?.values || []).map(v => ({ value: v, label: v }))
  const hasLabelValues = labelValueOptions.length > 0

  return (
    <Card title="创建租户">
      <Steps current={cur} items={steps.map(s => ({ title: s }))} style={{ marginBottom: 24 }} />

      <Form form={form} layout="vertical" initialValues={{
        db_mode: 'new', policy_mode: 'new', rg_mode: 'new',
        voters: 3, followers: 2, survival: 'region,host',
        ru_per_sec: 2000, priority: 'MEDIUM', burstable: true,
      }}>
        {/* Step 0: 创建租户（数据库用户） */}
        <div style={{ display: cur === 0 ? 'block' : 'none' }}>
          <Alert type="info" showIcon style={{ marginBottom: 12 }}
            message="创建租户的同时会创建对应的数据库用户。" />
          <Form.Item name="name" label="租户名称" rules={[{ required: true, message: '请输入租户名称' }]}>
            <Input placeholder="例如：acme" />
          </Form.Item>
          <Form.Item name="cluster_id" label="集群" rules={[{ required: true }]}>
            <Select placeholder="选择集群" options={clusters.map(c => ({ value: c.id, label: c.name }))} />
          </Form.Item>
          <Form.Item name="username" label="数据库用户名" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input placeholder="自动填充 {租户名}_user" />
          </Form.Item>
          <Form.Item name="password" label="用户密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password placeholder="初始密码" />
          </Form.Item>
        </div>

        {/* Step 1: 绑定数据库 + 放置位置 */}
        <div style={{ display: cur === 1 ? 'block' : 'none' }}>
          {/* 数据库选择 */}
          <Divider orientation="left">数据库</Divider>
          <Form.Item name="db_mode" label="数据库模式">
            <Radio.Group onChange={e => setDbMode(e.target.value)}>
              <Radio.Button value="new">创建新数据库</Radio.Button>
              <Radio.Button value="existing">绑定已有数据库</Radio.Button>
            </Radio.Group>
          </Form.Item>
          {dbMode === 'new' ? (
            <Form.Item name="db_name" label="新数据库名称">
              <Input placeholder="自动填充租户名" />
            </Form.Item>
          ) : (
            <Form.Item name="existing_db" label="选择已有数据库" rules={[{ required: true, message: '请选择数据库' }]}>
              <Select showSearch placeholder="选择数据库" options={dbOptions} />
            </Form.Item>
          )}

          {/* 放置策略 */}
          <Divider orientation="left">放置策略</Divider>
          <Form.Item name="policy_mode" label="放置策略模式">
            <Radio.Group onChange={e => setPolicyMode(e.target.value)}>
              <Radio.Button value="new">创建新策略</Radio.Button>
              <Radio.Button value="existing">绑定已有策略</Radio.Button>
              <Radio.Button value="none">不使用（逻辑隔离）</Radio.Button>
            </Radio.Group>
          </Form.Item>

          {policyMode === 'existing' && (
            <Form.Item name="existing_policy" label="选择已有放置策略">
              <Select showSearch placeholder="选择放置策略" options={policyOptions} />
            </Form.Item>
          )}

          {policyMode === 'new' && (
            <>
              <Form.Item name="policy_name" label="新策略名称">
                <Input placeholder="自动填充 t_{租户名}_pol" />
              </Form.Item>
              <Form.Item name="label_value" label={`匹配的标签值（${labelKey}=）`}
                tooltip={`TiKV 节点上已有的标签值，放置策略将约束数据到这些节点。从集群现有 "${labelKey}" 标签值中选择。`}
                rules={[{ required: true, message: '请选择或输入匹配的标签值' }]}>
                {hasLabelValues ? (
                  <Select showSearch placeholder={`选择 "${labelKey}" 标签值`} options={labelValueOptions} />
                ) : (
                  <Input placeholder={`集群暂无 "${labelKey}" 标签，请手动输入（需先在 TiKV 节点上打标签）`} />
                )}
              </Form.Item>
            </>
          )}

          {policyMode === 'none' && (
            <Alert type="info" showIcon message="逻辑隔离：不使用放置策略，数据分布在所有 TiKV 节点上。" />
          )}
        </div>

        {/* Step 2: 资源配置 */}
        <div style={{ display: cur === 2 ? 'block' : 'none' }}>
          <Divider orientation="left">资源组</Divider>
          <Form.Item name="rg_mode" label="资源组模式">
            <Radio.Group onChange={e => setRgMode(e.target.value)}>
              <Radio.Button value="new">创建新资源组</Radio.Button>
              <Radio.Button value="existing">绑定已有资源组</Radio.Button>
            </Radio.Group>
          </Form.Item>

          {rgMode === 'existing' ? (
            <Form.Item name="existing_rg" label="选择已有资源组" rules={[{ required: true, message: '请选择资源组' }]}>
              <Select showSearch placeholder="选择资源组" options={rgOptions} />
            </Form.Item>
          ) : (
            <>
              <Form.Item name="rg_name" label="新资源组名称">
                <Input placeholder="自动填充 t_{租户名}_rg" />
              </Form.Item>
              <Space.Compact block>
                <Form.Item name="ru_per_sec" label="RU/s 配额" rules={[{ required: true }]} style={{ flex: 1, marginRight: 8 }}>
                  <InputNumber min={1} style={{ width: '100%' }} /></Form.Item>
                <Form.Item name="priority" label="优先级" style={{ flex: 1 }}>
                  <Select options={['LOW', 'MEDIUM', 'HIGH'].map(p => ({ value: p }))} /></Form.Item>
              </Space.Compact>
              <Form.Item name="burstable" label="允许突发 (BURSTABLE)" valuePropName="checked"><Switch /></Form.Item>
            </>
          )}
        </div>

        {/* Step 3: 确认 */}
        <div style={{ display: cur === 3 ? 'block' : 'none' }}>
          <Alert type="success" showIcon
            message="确认创建租户。将执行编排操作（创建用户→建库/绑库→建策略/绑策略→建资源组/绑组→元数据），任一步失败自动回滚。" />
          <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
            确认后调用 POST /api/v1/tenants。</Text>
        </div>
      </Form>

      <Space style={{ marginTop: 24, justifyContent: 'space-between', width: '100%' }}>
        <Button disabled={cur === 0} onClick={prev}>上一步</Button>
        {cur < steps.length - 1
          ? <Button type="primary" onClick={next}>下一步</Button>
          : <Button type="primary" loading={submitting} onClick={onFinish}>创建租户</Button>}
      </Space>
    </Card>
  )
}
