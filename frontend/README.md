# TiDB 多租户管控台 — Frontend (React + Vite)

## 技术栈
React 18 + TypeScript + Vite + Ant Design 5 + @antv/g6（拓扑图谱，待接入）+ axios + react-router。

## 目录
```
frontend/
├── index.html
├── vite.config.ts          # dev 代理 /api → :8088
└── src/
    ├── main.tsx            # AntD ConfigProvider + 路由
    ├── App.tsx             # 侧边栏布局 + 路由
    ├── api/client.ts       # REST 封装（集群/拓扑/策略/资源组/租户）
    └── pages/
        ├── Topology.tsx    # TiKV 拓扑（按 zone 分组 store 卡片 + 容量条）
        ├── Tenants.tsx     # 租户列表（状态/隔离级别/挂起恢复）
        └── TenantCreate.tsx# 创建向导（5 步表单 → POST /tenants）
```

## 运行
```bash
cd frontend
npm install
npm run dev      # http://localhost:5180 （需后端 :8088）
```

## 与原型的关系
`../prototype/tenant-wizard.html` 是零依赖的可视原型（含 mock 数据 + 实时 SQL 预览）；
本目录是接真实后端的工程化版本，创建向导逻辑与原型一致。
