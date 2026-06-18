import { useEffect, useState } from 'react'
import {
  Card, Table, Tag, Button, Space, Popconfirm, message, Alert, Modal, Form,
  Input, Select, Empty, Spin, Typography, Divider,
} from 'antd'
import {
  PlusOutlined, ReloadOutlined, EditOutlined, DeleteOutlined, KeyOutlined,
  SafetyOutlined,
} from '@ant-design/icons'
import { api, DBUser, PrivilegeEntry } from '../api/client'
import { useCluster } from '../cluster-context'

const { Text } = Typography

export default function UserManagement() {
  const { cid } = useCluster()
  const [users, setUsers] = useState<DBUser[]>([])
  const [loading, setLoading] = useState(false)
  const [resourceGroups, setResourceGroups] = useState<string[]>([])

  // modals
  const [createOpen, setCreateOpen] = useState(false)
  const [editOpen, setEditOpen] = useState(false)
  const [privOpen, setPrivOpen] = useState(false)
  const [editUser, setEditUser] = useState<DBUser | null>(null)
  const [privUser, setPrivUser] = useState<DBUser | null>(null)
  const [privileges, setPrivileges] = useState<PrivilegeEntry[]>([])
  const [privLoading, setPrivLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const [createForm] = Form.useForm()
  const [editForm] = Form.useForm()
  const [grantForm] = Form.useForm()

  const load = () => {
    if (!cid) return
    setLoading(true)
    Promise.all([
      api.listUsers(cid).catch(() => [] as DBUser[]),
      api.listResourceGroups(cid).catch(() => []),
    ]).then(([u, rg]) => {
      setUsers(Array.isArray(u) ? u : [])
      setResourceGroups(Array.isArray(rg) ? rg.map((r: any) => r.name) : [])
    }).finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [cid])

  // Create user
  const handleCreate = async () => {
    try {
      const v = await createForm.validateFields()
      setSubmitting(true)
      await api.createUser(cid!, { user: v.user, host: v.host || '%', password: v.password, resource_group: v.resource_group })
      message.success(`用户 ${v.user} 创建成功`)
      setCreateOpen(false); createForm.resetFields(); load()
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(e?.response?.data?.error || e?.message || '创建失败')
    } finally { setSubmitting(false) }
  }

  // Edit user
  const openEdit = (u: DBUser) => {
    setEditUser(u)
    editForm.setFieldsValue({ password: '', resource_group: '' })
    setEditOpen(true)
  }
  const handleEdit = async () => {
    try {
      const v = await editForm.validateFields()
      setSubmitting(true)
      const body: any = {}
      if (v.password) body.password = v.password
      if (v.resource_group !== undefined) body.resource_group = v.resource_group
      await api.updateUser(cid!, editUser!.user, editUser!.host, body)
      message.success(`用户 ${editUser!.user} 已修改`)
      setEditOpen(false); load()
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(e?.response?.data?.error || e?.message || '修改失败')
    } finally { setSubmitting(false) }
  }

  // Delete user
  const handleDelete = async (u: DBUser) => {
    try {
      await api.deleteUser(cid!, u.user, u.host)
      message.success(`用户 ${u.user}@${u.host} 已删除`)
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '删除失败')
    }
  }

  // Privileges
  const openPrivileges = (u: DBUser) => {
    setPrivUser(u)
    setPrivOpen(true)
    setPrivLoading(true)
    grantForm.resetFields()
    api.listUserPrivileges(cid!, u.user, u.host)
      .then((d: any) => {
        const parsed = d?.parsed || d || []
        setPrivileges(Array.isArray(parsed) ? parsed : [])
      })
      .catch(() => setPrivileges([]))
      .finally(() => setPrivLoading(false))
  }

  const handleGrant = async () => {
    try {
      const v = await grantForm.validateFields()
      await api.grantPrivilege(cid!, privUser!.user, privUser!.host, {
        privileges: v.privileges, database: v.database, table: v.table || '*',
      })
      message.success('权限已授予')
      grantForm.resetFields()
      // refresh
      api.listUserPrivileges(cid!, privUser!.user, privUser!.host).then((d: any) => setPrivileges(d?.parsed || []))
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(e?.response?.data?.error || e?.message || '授权失败')
    }
  }

  const handleRevoke = async (priv: PrivilegeEntry) => {
    try {
      await api.revokePrivilege(cid!, privUser!.user, privUser!.host, {
        privileges: priv.privilege_type, database: priv.table_schema, table: priv.table_name,
      })
      message.success('权限已撤销')
      api.listUserPrivileges(cid!, privUser!.user, privUser!.host).then((d: any) => setPrivileges(d?.parsed || []))
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '撤销失败')
    }
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card title="用户管理（mysql.user）"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => { createForm.resetFields(); setCreateOpen(true) }}
              disabled={!cid}>创建用户</Button>
          </Space>
        }>
        {!cid ? (
          <Empty description="请先选择集群" />
        ) : (
          <>
            <Alert type="info" showIcon style={{ marginBottom: 12 }}
              message="管理数据库用户的创建、修改、删除和权限分配。通过 TiDB SQL（CREATE/ALTER/DROP USER + GRANT/REVOKE）执行。" />
            <Table<DBUser> rowKey={r => `${r.user}@${r.host}`} size="small" loading={loading}
              dataSource={users} pagination={false}
              locale={{ emptyText: '无数据库用户' }}
              columns={[
                { title: '用户名', dataIndex: 'user', render: (v: string) => <Text strong>{v}</Text> },
                { title: 'Host', dataIndex: 'host', width: 120 },
                { title: '操作', width: 320, render: (_, u) => (
                  <Space>
                    <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(u)}>修改</Button>
                    <Button size="small" icon={<SafetyOutlined />} onClick={() => openPrivileges(u)}>权限</Button>
                    <Popconfirm
                      title={`确定删除用户 ${u.user}@${u.host}？`}
                      onConfirm={() => handleDelete(u)}
                      okText="删除" cancelText="取消" okButtonProps={{ danger: true }}
                    >
                      <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
                    </Popconfirm>
                  </Space>
                )},
              ]}
            />
          </>
        )}
      </Card>

      {/* Create Modal */}
      <Modal title="创建用户" open={createOpen} onCancel={() => setCreateOpen(false)} onOk={handleCreate}
        confirmLoading={submitting} okText="创建" cancelText="取消" destroyOnClose>
        <Form form={createForm} layout="vertical" initialValues={{ host: '%' }}>
          <Form.Item name="user" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input placeholder="例如：app_rw" />
          </Form.Item>
          <Form.Item name="host" label="Host" tooltip="允许连接的主机，% 表示任意">
            <Input placeholder="%" />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password placeholder="密码" />
          </Form.Item>
          <Form.Item name="resource_group" label="资源组（可选）"
            tooltip="绑定到资源组后受 RU 配额限制">
            <Select allowClear placeholder="选择资源组（可选）"
              options={resourceGroups.map(rg => ({ value: rg, label: rg }))} />
          </Form.Item>
        </Form>
      </Modal>

      {/* Edit Modal */}
      <Modal title={`修改用户 - ${editUser?.user}@${editUser?.host}`} open={editOpen}
        onCancel={() => setEditOpen(false)} onOk={handleEdit}
        confirmLoading={submitting} okText="保存" cancelText="取消" destroyOnClose>
        <Form form={editForm} layout="vertical">
          <Alert type="info" showIcon style={{ marginBottom: 12 }}
            message="留空不修改。修改密码通过 ALTER USER 生效；资源组可改绑或解绑。" />
          <Form.Item name="password" label="新密码（留空不改）">
            <Input.Password placeholder="留空不修改" />
          </Form.Item>
          <Form.Item name="resource_group" label="资源组（留空不修改，填 default 解绑）">
            <Select allowClear placeholder="选择资源组"
              options={[
                { value: 'default', label: 'default（无限制）' },
                ...resourceGroups.filter(rg => rg !== 'default').map(rg => ({ value: rg, label: rg })),
              ]} />
          </Form.Item>
        </Form>
      </Modal>

      {/* Privileges Modal */}
      <Modal title={`权限管理 - ${privUser?.user}@${privUser?.host}`} open={privOpen}
        onCancel={() => setPrivOpen(false)} footer={null} width={700} destroyOnClose>
        {privLoading ? <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div> : (
          <>
            <Divider orientation="left"><Text strong><SafetyOutlined /> 当前权限</Text></Divider>
            {privileges.length === 0 ? (
              <Empty description="无授权权限" image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ) : (
              <Table<PrivilegeEntry> rowKey={r => `${r.table_schema}.${r.table_name}.${r.privilege_type}`}
                size="small" pagination={false} dataSource={privileges}
                columns={[
                  { title: '权限', dataIndex: 'privilege_type', width: 120,
                    render: (v: string) => <Tag color="blue">{v}</Tag> },
                  { title: '数据库', dataIndex: 'table_schema', width: 140 },
                  { title: '表', dataIndex: 'table_name', width: 140,
                    render: (v: string) => v === '*' ? '所有表' : v },
                  { title: '操作', width: 80, render: (_, p) => (
                    <Popconfirm title="撤销此权限？" onConfirm={() => handleRevoke(p)}>
                      <Button size="small" danger>撤销</Button>
                    </Popconfirm>
                  )},
                ]}
              />
            )}

            <Divider orientation="left"><Text strong><PlusOutlined /> 授予权限</Text></Divider>
            <Form form={grantForm} layout="inline" initialValues={{ privileges: 'SELECT', database: '*', table: '*' }}>
              <Form.Item name="privileges" rules={[{ required: true }]}>
                <Select style={{ width: 160 }} options={[
                  { value: 'ALL', label: 'ALL（全部权限）' },
                  { value: 'SELECT', label: 'SELECT（查询）' },
                  { value: 'INSERT', label: 'INSERT（插入）' },
                  { value: 'UPDATE', label: 'UPDATE（修改）' },
                  { value: 'DELETE', label: 'DELETE（删除）' },
                  { value: 'SELECT,INSERT,UPDATE,DELETE', label: '读写（CRUD）' },
                  { value: 'CREATE', label: 'CREATE（建表）' },
                  { value: 'DROP', label: 'DROP（删表）' },
                  { value: 'INDEX', label: 'INDEX（索引）' },
                  { value: 'ALTER', label: 'ALTER（修改表）' },
                ]} />
              </Form.Item>
              <Form.Item name="database" rules={[{ required: true }]}>
                <Select style={{ width: 140 }} placeholder="数据库" />
              </Form.Item>
              <Form.Item name="table">
                <Input style={{ width: 100 }} placeholder="表名" defaultValue="*" />
              </Form.Item>
              <Form.Item>
                <Button type="primary" icon={<KeyOutlined />} onClick={handleGrant}>授予</Button>
              </Form.Item>
            </Form>
          </>
        )}
      </Modal>
    </Space>
  )
}
