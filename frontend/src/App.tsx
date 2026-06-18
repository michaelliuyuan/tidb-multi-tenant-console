import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom'
import { Layout, Menu } from 'antd'
import {
  ClusterOutlined, ApartmentOutlined, PartitionOutlined, ControlOutlined,
  LineChartOutlined, HeartOutlined, UserOutlined, DatabaseOutlined,
} from '@ant-design/icons'
import Topology from './pages/Topology'
import Tenants from './pages/Tenants'
import TenantCreate from './pages/TenantCreate'
import Placement from './pages/Placement'
import Monitor from './pages/Monitor'
import ClusterHealth from './pages/ClusterHealth'
import ResourceControl from './pages/ResourceControl'
import UserManagement from './pages/UserManagement'
import DatabaseManagement from './pages/DatabaseManagement'
import { ClusterProvider, ClusterSelector } from './cluster-context'

const { Header, Sider, Content } = Layout

const items = [
  { key: '/topology', icon: <ApartmentOutlined />, label: <Link to="/topology">TiKV 拓扑</Link> },
  { key: '/placement', icon: <PartitionOutlined />, label: <Link to="/placement">放置策略</Link> },
  { key: '/databases', icon: <DatabaseOutlined />, label: <Link to="/databases">数据库管理</Link> },
  { key: '/resource', icon: <ControlOutlined />, label: <Link to="/resource">资源管控</Link> },
  { key: '/users', icon: <UserOutlined />, label: <Link to="/users">用户管理</Link> },
  { key: '/tenants', icon: <ClusterOutlined />, label: <Link to="/tenants">租户管理</Link> },
  { key: '/monitor', icon: <LineChartOutlined />, label: <Link to="/monitor">监控告警</Link> },
  { key: '/health', icon: <HeartOutlined />, label: <Link to="/health">集群健康</Link> },
]

function ActiveMenu() {
  const loc = useLocation()
  const selected = '/' + (loc.pathname.split('/')[1] || 'tenants')
  return <Menu mode="inline" selectedKeys={[selected]} items={items} />
}

export default function App() {
  return (
    <BrowserRouter>
      <ClusterProvider>
        <Layout style={{ minHeight: '100vh' }}>
          <Header style={{ color: '#fff', fontSize: 18, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 12, justifyContent: 'space-between' }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <img src="/logo.png" alt="logo" style={{ height: 36 }} />
              TiDB 资源池管控平台
            </span>
            {/* 全局集群选择器：所有页面共享，localStorage 持久化 */}
            <ClusterSelector />
          </Header>
          <Layout>
            <Sider width={210} style={{ background: '#fff' }}>
              <ActiveMenu />
            </Sider>
            <Content style={{ padding: 24 }}>
              <Routes>
                <Route path="/" element={<Tenants />} />
                <Route path="/topology" element={<Topology />} />
                <Route path="/placement" element={<Placement />} />
                <Route path="/databases" element={<DatabaseManagement />} />
                <Route path="/resource" element={<ResourceControl />} />
                <Route path="/users" element={<UserManagement />} />
                <Route path="/tenants" element={<Tenants />} />
                <Route path="/tenants/new" element={<TenantCreate />} />
                <Route path="/monitor" element={<Monitor />} />
                <Route path="/health" element={<ClusterHealth />} />
              </Routes>
            </Content>
          </Layout>
        </Layout>
      </ClusterProvider>
    </BrowserRouter>
  )
}
