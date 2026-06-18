import React, { createContext, useContext, useEffect, useState } from 'react'
import { Select, Tag, Button, Modal, Form, Input, InputNumber, Switch, message } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { api, Cluster, CreateClusterReq } from './api/client'

// 全局集群上下文：listClusters 只调一次；选中的 cid 存 localStorage，
// 顶部 ClusterSelector 统一入口，所有页面共享同一个 cid，免去每页重选。
//
// cluster.id 是 AUTO_RANDOM 大整数（>2^53），这里全程按字符串处理。
interface ClusterCtx {
  clusters: Cluster[]
  cid: string | undefined
  setCid: (id: string) => void
  loading: boolean
  refreshClusters: () => Promise<Cluster[]>
}

const Ctx = createContext<ClusterCtx>({ clusters: [], cid: undefined, setCid: () => {}, loading: true, refreshClusters: () => Promise.resolve([]) })
const LS_KEY = 'tidb-console.cid'

export function ClusterProvider({ children }: { children: React.ReactNode }) {
  const [clusters, setClusters] = useState<Cluster[]>([])
  const [cid, setCidState] = useState<string | undefined>(() => localStorage.getItem(LS_KEY) || undefined)
  const [loading, setLoading] = useState(true)

  const refreshClusters = async (): Promise<Cluster[]> => {
    const cs = await api.listClusters()
    setClusters(cs)
    return cs
  }

  useEffect(() => {
    refreshClusters()
      .then(cs => {
        // 校验本地缓存的 cid 仍存在；否则回落到第一个集群
        setCidState(prev => {
          if (prev && cs.some(c => String(c.id) === prev)) return prev
          const def = cs[0] ? String(cs[0].id) : undefined
          if (def) localStorage.setItem(LS_KEY, def)
          return def
        })
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  const setCid = (id: string) => {
    localStorage.setItem(LS_KEY, id)
    setCidState(id)
  }

  return <Ctx.Provider value={{ clusters, cid, setCid, loading, refreshClusters }}>{children}</Ctx.Provider>
}

export const useCluster = () => useContext(Ctx)

// 顶部全局集群选择器（放在 Header）
export function ClusterSelector() {
  const { clusters, cid, setCid, loading, refreshClusters } = useCluster()
  const current = clusters.find(c => String(c.id) === cid)
  const [open, setOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [form] = Form.useForm<CreateClusterReq>()

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields()
      setSubmitting(true)
      const created = await api.createCluster(values)
      await refreshClusters()
      setCid(String(created.id))
      message.success(`集群「${created.name}」添加成功`)
      setOpen(false)
      form.resetFields()
    } catch (err: any) {
      // 连通性探测失败会返回 400 + field 提示
      const msg = err?.response?.data?.error || err?.message || '添加失败'
      message.error(msg)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <span style={{ fontSize: 13, opacity: 0.85, whiteSpace: 'nowrap' }}>集群</span>
      <Select<string>
        style={{ width: 200 }}
        loading={loading}
        placeholder="选择集群"
        value={cid}
        onChange={setCid}
        options={clusters.map(c => ({ value: String(c.id), label: c.name }))}
      />
      <Button size="small" icon={<PlusOutlined />} onClick={() => setOpen(true)} title="添加集群" />
      {current?.status && (
        <Tag color={current.status === 'ok' ? 'green' : 'blue'} style={{ margin: 0 }}>
          {current.status}
        </Tag>
      )}

      <Modal
        title="添加集群"
        open={open}
        onOk={handleSubmit}
        onCancel={() => { setOpen(false); form.resetFields() }}
        confirmLoading={submitting}
        okText="添加并探测"
        cancelText="取消"
        width={520}
        destroyOnClose
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ tidb_port: 4000, skip_probe: false }}
        >
          <Form.Item name="name" label="集群名称" rules={[{ required: true, message: '请输入集群名称' }]}>
            <Input placeholder="如：241东区" />
          </Form.Item>
          <Form.Item name="tidb_host" label="TiDB 地址" rules={[{ required: true, message: '请输入 TiDB 主机' }]}>
            <Input placeholder="如：pepezzzz.synology.me" />
          </Form.Item>
          <Form.Item name="tidb_port" label="TiDB 端口" rules={[{ required: true, message: '请输入端口' }]}>
            <InputNumber min={1} max={65535} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="tidb_user" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input placeholder="如：root" />
          </Form.Item>
          <Form.Item name="tidb_password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password placeholder="数据库密码" />
          </Form.Item>
          <Form.Item name="pd_endpoint" label="PD 端点（可选）" tooltip="留空则跳过 PD 连通性检查">
            <Input placeholder="如：http://pd:2379" />
          </Form.Item>
          <Form.Item name="prometheus_url" label="Prometheus URL（可选）">
            <Input placeholder="如：http://prometheus:9090" />
          </Form.Item>
          <Form.Item name="skip_probe" label="跳过连通性探测" valuePropName="checked" tooltip="开启后不检测直接入库（明知连不上也要先存档时用）">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
