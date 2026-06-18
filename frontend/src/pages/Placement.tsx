import { useEffect, useState, useMemo } from 'react'
import {
  Card, Table, Space, Input, InputNumber, Button, Alert, Descriptions, Tag,
  message, Typography, Row, Col, Modal, Form, Select, Switch, Tooltip,
  Popconfirm, Divider, Tabs,
} from 'antd'
import {
  PlusOutlined, ReloadOutlined, EditOutlined, DeleteOutlined, CodeOutlined,
} from '@ant-design/icons'
import { api, DryRunResult, PlacementPolicy, PlacementLabel, CreatePlacementPolicyReq } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text, Paragraph } = Typography
const { TabPane } = Tabs

export default function Placement() {
  const { cid } = useCluster()
  const [policies, setPolicies] = useState<PlacementPolicy[]>([])
  const [labels, setLabels] = useState<PlacementLabel[]>([])
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editMode, setEditMode] = useState(false)
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)
  const [previewSQL, setPreviewSQL] = useState('')

  // dry-run 输入
  const [labelKey, setLabelKey] = useState('')
  const [labelValue, setLabelValue] = useState('')
  const [dbs, setDbs] = useState<string[]>([])
  const [voters, setVoters] = useState(3)
  const [res, setRes] = useState<DryRunResult | null>(null)
  const [runLoading, setRunLoading] = useState(false)

  // 高级模式开关（用于区分常规选项和高级选项）
  const [advancedMode, setAdvancedMode] = useState(false)

  const [dbList, setDbList] = useState<string[]>([])

  const load = () => {
    if (!cid) return
    setLoading(true)
    Promise.all([
      api.listPlacementPolicies(cid).then((d: any) => Array.isArray(d) ? d : []).catch(() => []),
      api.listPlacementLabels(cid).then((d: any) => Array.isArray(d) ? d : []).catch(() => []),
      api.listDatabases(cid).then((d: any) => Array.isArray(d) ? d.map((x: any) => x.name) : []).catch(() => []),
    ]).then(([p, l, dbs]) => {
      setPolicies(p as PlacementPolicy[])
      setLabels(l as PlacementLabel[])
      setDbList(dbs as string[])
    }).finally(() => setLoading(false))
  }

  useEffect(() => {
    if (!cid) return
    load()
  }, [cid])

  // --- 创建/编辑表单：SQL 预览生成 ---
  const formValues = Form.useWatch([], form) || {}
  const generatedSQL = useMemo(() => {
    const v = formValues as any
    if (!v.name) return ''
    const parts: string[] = []
    const hasBasic = v.primary_region || v.regions
    const hasAdvanced = v.constraints || v.leader_constraints || v.follower_constraints ||
      v.learner_constraints || v.survival_preferences
    if (hasBasic && hasAdvanced) return '-- 错误: PRIMARY_REGION/REGIONS 与 CONSTRAINTS 不可同时指定'

    if (v.primary_region) parts.push(`PRIMARY_REGION = "${v.primary_region}"`)
    if (v.regions) parts.push(`REGIONS = "${v.regions}"`)
    if (v.schedule) parts.push(`SCHEDULE = "${v.schedule}"`)
    if (v.constraints) parts.push(`CONSTRAINTS = "${v.constraints}"`)
    if (v.leader_constraints) parts.push(`LEADER_CONSTRAINTS = "${v.leader_constraints}"`)
    if (v.follower_constraints) parts.push(`FOLLOWER_CONSTRAINTS = "${v.follower_constraints}"`)
    if (v.learner_constraints) parts.push(`LEARNER_CONSTRAINTS = "${v.learner_constraints}"`)
    if (v.survival_preferences) parts.push(`SURVIVAL_PREFERENCES = "${v.survival_preferences}"`)
    if (v.followers != null) parts.push(`FOLLOWERS = ${v.followers}`)
    if (v.learners != null) parts.push(`LEARNERS = ${v.learners}`)
    if (parts.length === 0) return ''
    const verb = editMode ? 'ALTER' : 'CREATE'
    return `${verb} PLACEMENT POLICY \`${v.name}\`\n  ${parts.join(',\n  ')}`
  }, [formValues, editMode])

  useEffect(() => {
    setPreviewSQL(generatedSQL)
  }, [generatedSQL])

  // 打开创建弹窗
  const openCreate = () => {
    setEditMode(false)
    setAdvancedMode(false)
    form.resetFields()
    form.setFieldsValue({ followers: 2, schedule: 'EVEN' })
    setModalOpen(true)
  }

  // 打开编辑弹窗（从已有 policy 回填）
  const openEdit = (p: PlacementPolicy) => {
    setEditMode(true)
    setAdvancedMode(!!(p.constraints || p.leader_constraints || p.follower_constraints || p.learner_constraints || p.survival_preferences))
    form.setFieldsValue({
      name: p.policy_name,
      primary_region: p.primary_region,
      regions: p.regions,
      schedule: p.schedule,
      followers: p.followers || undefined,
      learners: p.learners || undefined,
      constraints: p.constraints,
      leader_constraints: p.leader_constraints,
      follower_constraints: p.follower_constraints,
      learner_constraints: p.learner_constraints,
      survival_preferences: p.survival_preferences,
    })
    setModalOpen(true)
  }

  const handleSubmit = async () => {
    try {
      const v = await form.validateFields()
      setSubmitting(true)
      const body: CreatePlacementPolicyReq = { name: v.name }
      if (v.primary_region) body.primary_region = v.primary_region
      if (v.regions) body.regions = v.regions
      if (v.schedule) body.schedule = v.schedule
      if (v.followers != null) body.followers = v.followers
      if (v.constraints) body.constraints = v.constraints
      if (v.leader_constraints) body.leader_constraints = v.leader_constraints
      if (v.follower_constraints) body.follower_constraints = v.follower_constraints
      if (v.learner_constraints) body.learner_constraints = v.learner_constraints
      if (v.learners != null) body.learners = v.learners
      if (v.survival_preferences) body.survival_preferences = v.survival_preferences

      if (editMode) {
        await api.alterPlacementPolicy(cid!, v.name, body)
        message.success(`放置策略 ${v.name} 已修改`)
      } else {
        await api.createPlacementPolicy(cid!, body)
        message.success(`放置策略 ${v.name} 创建成功`)
      }
      setModalOpen(false)
      load()
    } catch (e: any) {
      if (e?.errorFields) return // form validation error
      message.error(e?.response?.data?.error || e?.message || '操作失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleDelete = async (pname: string) => {
    try {
      await api.dropPlacementPolicy(cid!, pname)
      message.success(`放置策略 ${pname} 已删除`)
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '删除失败')
    }
  }

  const runDryRun = async () => {
    if (!cid) return
    setRunLoading(true); setRes(null)
    try {
      const r = await api.dryRunPlacement(cid, {
        label_key: labelKey, label_value: labelValue,
        databases: dbs,
        voters,
      })
      setRes(r)
      if (!r.target_capacity_ok) message.warning('目标节点池容量不足，请查看告警')
      else message.success('预估完成')
    } catch (e: any) {
      message.error(e?.response?.data?.error || e.message)
    } finally {
      setRunLoading(false)
    }
  }

  // 标签 key 选项（从 SHOW PLACEMENT LABELS）
  const labelKeyOptions = labels.map(l => ({ label: l.key, value: l.key }))

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card
        title="放置策略管理"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}
              disabled={!cid}>创建放置策略</Button>
          </Space>
        }
      >
        {!cid
          ? <Alert type="info" showIcon message="请在顶部选择集群后查看放置策略。" />
          : <>
            <Alert type="info" showIcon style={{ marginBottom: 12 }}
              message="放置策略（Placement Policy）用于控制数据在 TiKV 节点上的分布位置，可实现数据隔离、高可用、跨机房部署等场景。"
              description={
                <Space size={4} wrap>
                  <Text type="secondary">可用标签：</Text>
                  {labels.length === 0
                    ? <Text type="secondary">未检测到标签，请先给 TiKV 节点打标签</Text>
                    : labels.map(l => (
                      <Tag key={l.key} color="blue">{l.key}: {l.values.join(', ')}</Tag>
                    ))}
                </Space>
              }
            />
            <Table<PlacementPolicy>
              rowKey="policy_id"
              size="small"
              loading={loading}
              dataSource={policies}
              pagination={false}
              scroll={{ x: 1200 }}
              columns={[
                { title: '策略名', dataIndex: 'policy_name', width: 180, fixed: 'left',
                  render: (v: string) => <Text strong>{v}</Text> },
                { title: 'PRIMARY_REGION', dataIndex: 'primary_region', width: 140,
                  render: (v: string) => v || <Text type="secondary">—</Text> },
                { title: 'REGIONS', dataIndex: 'regions', width: 180,
                  render: (v: string) => v || <Text type="secondary">—</Text> },
                { title: 'SCHEDULE', dataIndex: 'schedule', width: 130,
                  render: (v: string) => v || <Text type="secondary">—</Text> },
                { title: 'CONSTRAINTS', dataIndex: 'constraints', width: 200,
                  render: (v: string) => v || <Text type="secondary">—</Text> },
                { title: 'LEADER_CONSTRAINTS', dataIndex: 'leader_constraints', width: 180,
                  render: (v: string) => v || <Text type="secondary">—</Text> },
                { title: 'FOLLOWER_CONSTRAINTS', dataIndex: 'follower_constraints', width: 180,
                  render: (v: string) => v || <Text type="secondary">—</Text> },
                { title: 'SURVIVAL_PREF', dataIndex: 'survival_preferences', width: 180,
                  render: (v: string) => v || <Text type="secondary">—</Text> },
                { title: 'Followers', dataIndex: 'followers', width: 80, align: 'center' },
                { title: 'Learners', dataIndex: 'learners', width: 80, align: 'center',
                  render: (v: number) => v || <Text type="secondary">0</Text> },
                { title: '操作', key: 'action', width: 140, fixed: 'right', align: 'center',
                  render: (_: any, r: PlacementPolicy) => (
                    <Space size={4}>
                      <Tooltip title="编辑">
                        <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />
                      </Tooltip>
                      <Popconfirm
                        title={`确定删除策略 ${r.policy_name}？`}
                        description="仅可删除未绑定任何表/分区的策略"
                        onConfirm={() => handleDelete(r.policy_name)}
                        okText="删除" cancelText="取消" okButtonProps={{ danger: true }}
                      >
                        <Button size="small" danger icon={<DeleteOutlined />} />
                      </Popconfirm>
                    </Space>
                  ),
                },
              ]}
            />
          </>
        }
      </Card>

      {/* 创建/编辑弹窗 */}
      <Modal
        title={editMode ? `编辑放置策略` : '创建放置策略'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
        confirmLoading={submitting}
        okText={editMode ? '保存' : '创建'}
        cancelText="取消"
        width={760}
        destroyOnClose
      >
        <Form form={form} layout="vertical" size="small">
          <Form.Item
            name="name"
            label="策略名称"
            rules={[
              { required: true, message: '请输入策略名称' },
              { pattern: /^[a-zA-Z_][a-zA-Z0-9_]*$/, message: '只能包含字母、数字和下划线，且不能以数字开头' },
            ]}
            extra="策略名全局唯一，不与任何数据库关联"
          >
            <Input placeholder="例如：app_order_policy" disabled={editMode} />
          </Form.Item>

          <Row align="middle" style={{ marginBottom: 12 }}>
            <Col>
              <Space>
                <Switch checked={advancedMode} onChange={setAdvancedMode} size="small" />
                <Text>高级模式（CONSTRAINTS 约束式）</Text>
                <Tooltip title="关闭时使用 PRIMARY_REGION / REGIONS 常规选项（基于 region label 语法糖）；开启后使用 CONSTRAINTS 约束式，可灵活指定任意 label 条件。两者不可混用。">
                  <Text type="secondary" style={{ cursor: 'help' }}>?</Text>
                </Tooltip>
              </Space>
            </Col>
          </Row>

          {!advancedMode ? (
            <>
              <Row gutter={12}>
                <Col span={8}>
                  <Form.Item name="primary_region" label="PRIMARY_REGION"
                    tooltip="Raft Leader 放置在 region 标签匹配的节点上。只能指定一个 region。">
                    <Select
                      showSearch allowClear
                      placeholder="选择或输入 region"
                      options={labelKeyOptions.find(o => o.value === 'region') ? undefined : undefined}
                      mode="tags" maxCount={1}
                    />
                  </Form.Item>
                </Col>
                <Col span={8}>
                  <Form.Item name="regions" label="REGIONS"
                    tooltip="Raft Followers 放置在 region 标签匹配的节点上，逗号分隔。">
                    <Input placeholder="例如：us-east-1,us-west-1" />
                  </Form.Item>
                </Col>
                <Col span={8}>
                  <Form.Item name="schedule" label="SCHEDULE"
                    tooltip="Follower 调度策略。EVEN(默认)=均匀分布；MAJORITY_IN_PRIMARY=主区域多副本。">
                    <Select defaultValue="EVEN" options={[
                      { label: 'EVEN（均匀分布）', value: 'EVEN' },
                      { label: 'MAJORITY_IN_PRIMARY（主区域优先）', value: 'MAJORITY_IN_PRIMARY' },
                    ]} />
                  </Form.Item>
                </Col>
              </Row>
              <Row gutter={12}>
                <Col span={6}>
                  <Form.Item name="followers" label="Followers 数"
                    tooltip="Follower 副本数。总副本 = Followers + 1(Leader)。例如 FOLLOWERS=2 表示 3 副本。">
                    <InputNumber min={1} max={7} style={{ width: '100%' }} placeholder="默认 2" />
                  </Form.Item>
                </Col>
              </Row>
            </>
          ) : (
            <>
              <Form.Item name="constraints" label="CONSTRAINTS"
                tooltip="适用于所有副本的约束列表。例如：[+zone=us-east-1] 或 [+app=order,-type=fault]">
                <Input placeholder='例如：[+zone=us-east-1a]' />
              </Form.Item>
              <Row gutter={12}>
                <Col span={12}>
                  <Form.Item name="leader_constraints" label="LEADER_CONSTRAINTS"
                    tooltip="仅适用于 Leader 的约束。仅支持列表格式。例如：[+region=us-east-1]">
                    <Input placeholder='例如：[+region=us-east-1]' />
                  </Form.Item>
                </Col>
                <Col span={12}>
                  <Form.Item name="follower_constraints" label="FOLLOWER_CONSTRAINTS"
                    tooltip="仅适用于 Follower 的约束。支持列表或字典格式。">
                    <Input placeholder='例如：{"+region=us-east-1": 2, "+region=us-west-1": 1}' />
                  </Form.Item>
                </Col>
              </Row>
              <Row gutter={12}>
                <Col span={12}>
                  <Form.Item name="learner_constraints" label="LEARNER_CONSTRAINTS"
                    tooltip="仅适用于 Learner 的约束。">
                    <Input placeholder="例如：[+region=us-west-1]" />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="learners" label="Learners 数"
                    tooltip="Learner 副本数（不参与投票，仅同步数据）。">
                    <InputNumber min={0} max={7} style={{ width: '100%' }} placeholder="0" />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item name="followers" label="Followers 数"
                    tooltip="Follower 副本数（使用 CONSTRAINTS 时总副本 = Followers + Learners + 1 Leader）。">
                    <InputNumber min={1} max={7} style={{ width: '100%' }} placeholder="默认 2" />
                  </Form.Item>
                </Col>
              </Row>
              <Form.Item name="survival_preferences" label="SURVIVAL_PREFERENCES"
                tooltip="按 label 容灾等级优先放置副本。例如：[region, zone, host]">
                <Input placeholder="例如：[region, zone, host]" />
              </Form.Item>
            </>
          )}
        </Form>

        {/* SQL 预览 */}
        {previewSQL && (
          <div style={{ marginTop: 8 }}>
            <Divider style={{ margin: '8px 0' }}><Text type="secondary"><CodeOutlined /> SQL 预览</Text></Divider>
            <pre style={{
              background: '#f5f5f5', padding: 12, borderRadius: 6, fontSize: 12,
              maxHeight: 200, overflow: 'auto', margin: 0,
            }}>
              {previewSQL}
            </pre>
          </div>
        )}

        {/* 可用标签提示 */}
        {labels.length > 0 && (
          <div style={{ marginTop: 12 }}>
            <Text type="secondary">集群可用标签（参考填写）：</Text>
            <div style={{ marginTop: 4 }}>
              {labels.map(l => (
                <Tag key={l.key} color="geekblue" style={{ marginBottom: 4 }}>
                  {l.key}: {l.values.join(' / ')}
                </Tag>
              ))}
            </div>
          </div>
        )}
      </Modal>

      {/* Dry-run 保留 */}
      <Card title="Placement 变更影响预估（dry-run）"
        extra={<Text type="secondary">绑定 placement policy 前先评估 Region 调度量</Text>}>
        <Alert type="info" showIcon style={{ marginBottom: 12 }}
          message="输入目标节点池标签 + 受影响库，预估需移动的 Region 数、目标池容量是否足够、调度时长。" />
        <Row gutter={12}>
          <Col span={5}>
            <Text type="secondary">标签键</Text>
            <Select showSearch value={labelKey || undefined} onChange={v => { setLabelKey(v); setLabelValue('') }}
              placeholder="选择标签键"
              options={labels.map(l => ({ value: l.key, label: l.key }))} style={{ width: '100%' }} />
          </Col>
          <Col span={5}>
            <Text type="secondary">标签值（节点池）</Text>
            <Select showSearch value={labelValue || undefined} onChange={setLabelValue}
              placeholder="选择标签值"
              options={(labels.find(l => l.key === labelKey)?.values || []).map(v => ({ value: v, label: v }))}
              style={{ width: '100%' }} />
          </Col>
          <Col span={8}>
            <Text type="secondary">受影响库</Text>
            <Select mode="multiple" showSearch value={dbs} onChange={setDbs}
              placeholder="选择数据库"
              options={dbList.map(d => ({ value: d, label: d }))}
              style={{ width: '100%' }} />
          </Col>
          <Col span={3}><Text type="secondary">副本数</Text><InputNumber min={1} max={7} value={voters} onChange={v => setVoters(v || 3)} style={{ width: '100%' }} /></Col>
          <Col span={3} style={{ display: 'flex', alignItems: 'flex-end' }}><Button type="primary" block loading={runLoading} onClick={runDryRun}>预估</Button></Col>
        </Row>

        {res && (
          <div style={{ marginTop: 16 }}>
            <Descriptions bordered size="small" column={2}>
              <Descriptions.Item label="受影响 Region">{res.affected_regions}</Descriptions.Item>
              <Descriptions.Item label="数据总量">{fmtMB(res.total_size_mb)}</Descriptions.Item>
              <Descriptions.Item label="目标池节点数">
                <Tag color={res.target_pool_count >= res.replication_factor ? 'green' : 'red'}>{res.target_pool_count}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="目标池可用">{fmtMB(res.target_avail_mb)}</Descriptions.Item>
              <Descriptions.Item label={`需承载（×副本 ${res.replication_factor}）`}>{fmtMB(res.needed_mb)}</Descriptions.Item>
              <Descriptions.Item label="容量是否足够">
                <Tag color={res.target_capacity_ok ? 'green' : 'red'}>{res.target_capacity_ok ? '✓ 足够' : '✗ 不足'}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="预估调度时长">{res.est_minutes > 0 ? `约 ${res.est_minutes.toFixed(1)} 分钟` : '—'}</Descriptions.Item>
              <Descriptions.Item label="结论">
                {res.target_capacity_ok && (res.warnings?.length ?? 0) === 0
                  ? <Tag color="green">可安全执行</Tag>
                  : <Tag color="orange">需关注告警 / 灰度</Tag>}
              </Descriptions.Item>
            </Descriptions>
            {res.warnings && res.warnings.length > 0 && (
              <Alert type="warning" showIcon style={{ marginTop: 12 }}
                message="告警" description={<ul style={{ margin: 0, paddingLeft: 18 }}>{res.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>} />
            )}
          </div>
        )}
      </Card>
    </Space>
  )
}

function fmtMB(mb: number): string {
  if (mb >= 1024 * 1024) return (mb / 1024 / 1024).toFixed(2) + ' TB'
  if (mb >= 1024) return (mb / 1024).toFixed(1) + ' GB'
  return mb + ' MB'
}
