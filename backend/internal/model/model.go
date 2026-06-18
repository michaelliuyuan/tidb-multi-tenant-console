// Package model 定义租户管控台的领域模型。
package model

type IsolationLevel string

const (
	Physical IsolationLevel = "PHYSICAL"
	Logical  IsolationLevel = "LOGICAL"
	Hybrid   IsolationLevel = "HYBRID"
)

type TenantStatus string

const (
	Active    TenantStatus = "ACTIVE"
	Suspended TenantStatus = "SUSPENDED"
	Migrating TenantStatus = "MIGRATING"
	Deleted   TenantStatus = "DELETED"
)

// Cluster 被管控的 TiDB 集群连接信息（mt_console.cluster）。
type Cluster struct {
	ID            int64  `json:"id,string" yaml:"-"` // AUTO_RANDOM 大整数，序列化为字符串避免前端 JS 精度丢失
	Name          string `json:"name" yaml:"name"`
	TiDBHost      string `json:"tidb_host" yaml:"tidb_host"`
	TiDBPort      int    `json:"tidb_port" yaml:"tidb_port"`
	TiDBUser      string `json:"tidb_user" yaml:"tidb_user"`
	PDEndpoint    string `json:"pd_endpoint" yaml:"pd_endpoint"`
	PrometheusURL string `json:"prometheus_url,omitempty" yaml:"prometheus_url"`
	Version       string `json:"version,omitempty" yaml:"-"`
	Status        string `json:"status" yaml:"-"`
	Password      string `json:"-" yaml:"tidb_password"` // 明文仅内存；持久化为 tidb_pwd_enc（应加密）
}

// CreateClusterRequest 添加集群表单的请求体。
// 专门单独定义而不用 model.Cluster，是因为 Cluster.Password 标了 json:"-"
// （避免回显密码），导致 gin ShouldBindJSON 无法从前端 JSON 绑定密码字段。
type CreateClusterRequest struct {
	Name          string `json:"name" binding:"required"`
	TiDBHost      string `json:"tidb_host" binding:"required"`
	TiDBPort      int    `json:"tidb_port" binding:"required"`
	TiDBUser      string `json:"tidb_user" binding:"required"`
	TiDBPassword  string `json:"tidb_password" binding:"required"`
	PDEndpoint    string `json:"pd_endpoint"`
	PrometheusURL string `json:"prometheus_url"`
	// SkipProbe=true 时跳过连通性探测（用户明知连不上也要先存档时用）。
	SkipProbe bool `json:"skip_probe"`
}

// Tenant 租户聚合根：database 集合 + placement policy + resource group + users。
type Tenant struct {
	ID              int64          `json:"id,string"`        // AUTO_RANDOM，序列化为字符串避免 JS 精度丢失
	ClusterID       int64          `json:"cluster_id,string"` // BIGINT（>2^53），同样序列化为字符串避免精度丢失
	Name            string         `json:"name"`
	IsolationLevel  IsolationLevel `json:"isolation_level"`
	LabelKey        string         `json:"label_key,omitempty"`
	LabelValue      string         `json:"label_value,omitempty"`
	PlacementPolicy string         `json:"placement_policy,omitempty"`
	ResourceGroup   string         `json:"resource_group,omitempty"`
	RUPerSec        int            `json:"ru_per_sec,omitempty"`
	Priority        string         `json:"priority"`
	MaxStorageGB    int64          `json:"max_storage_gb,omitempty"`
	Status          TenantStatus   `json:"status"`
	RetentionDays   int            `json:"retention_days"`
	Databases       []string       `json:"databases,omitempty"`
	Users           []UserRef      `json:"users,omitempty"`
}

type UserRef struct {
	Username string `json:"username"`
	Host     string `json:"host"`
}

// CreateTenantRequest 创建租户请求体（编排入参）。
type CreateTenantRequest struct {
	Name           string         `json:"name" binding:"required"`
	ClusterID      int64          `json:"cluster_id,string" binding:"required"`
	IsolationLevel IsolationLevel `json:"isolation_level" binding:"required,oneof=PHYSICAL LOGICAL HYBRID"`
	LabelKey       string         `json:"label_key"`
	LabelValue     string         `json:"label_value"` // 匹配已有 TiKV 标签值（不再自动打标签）
	Placement      PlacementSpec  `json:"placement"`
	ResourceGroup  ResourceSpec   `json:"resource_group"`
	Databases      []string       `json:"databases"`
	Users          []UserSpec     `json:"users"`
	Priority       string         `json:"priority"`
	Gradual        bool           `json:"gradual"`
}

type PlacementSpec struct {
	PrimaryRegion       string `json:"primary_region"`
	Regions             string `json:"regions"` // 逗号分隔
	Voters              int    `json:"voters"`
	Followers           int    `json:"followers"`
	SurvivalPreferences string `json:"survival_preferences"` // 逗号分隔
}

type ResourceSpec struct {
	RUPerSec  int    `json:"ru_per_sec" binding:"required"`
	Burstable bool   `json:"burstable"`
	Priority  string `json:"priority"`
}

type UserSpec struct {
	Username string `json:"username" binding:"required"`
	Host     string `json:"host"`
	Password string `json:"password" binding:"required"`
}

// Store PD /pd/api/v1/stores 返回的 TiKV 节点。
type Store struct {
	ID            int64  `json:"id"`
	Address       string `json:"address"`
	StatusAddress string `json:"status_address,omitempty"`
	StatusName    string `json:"status_name"`
	Labels        []KV   `json:"labels"`
	Capacity      string `json:"capacity"`
	Available     string `json:"available"`
	RegionCount   int    `json:"region_count"`
}

// StoreResource 单个 TiKV 实例的资源上限（从 systemctl 获取）。
type StoreResource struct {
	MemoryLimit     string  `json:"memory_limit"`      // 如 "2G"
	MemoryCurrent   uint64  `json:"memory_current"`    // 当前内存使用（字节）
	MemoryUsagePct  float64 `json:"memory_usage_pct"`  // 内存使用率百分比
	CPUQuota        int     `json:"cpu_quota"`         // CPUQuota，如 200（代表 200%）
	CPUUsageNSec    uint64  `json:"cpu_usage_nsec"`    // 累计 CPU 纳秒
	ReadBandwidth   string  `json:"read_bandwidth"`    // IOReadBandwidthMax
	WriteBandwidth  string  `json:"write_bandwidth"`   // IOWriteBandwidthMax
	BlockCacheSize  string  `json:"block_cache_size"`  // 保留（未来从 TiKV /config 获取）
	ResourceControl bool    `json:"resource_control"`   // 始终 true（systemd 管控）
}

type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// JobStep 编排步骤：action 执行，compensate 回滚补偿。
type JobStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // pending|running|succeeded|failed|skipped
	Action     string `json:"action"` // human-readable
	SQL        string `json:"sql,omitempty"`
	Compensate string `json:"compensate,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Job struct {
	ID          int64     `json:"id,string"`
	TenantID    int64     `json:"tenant_id,string"`
	OpType      string    `json:"op_type"`
	Status      string    `json:"status"`
	Steps       []JobStep `json:"steps"`
	CurrentStep int       `json:"current_step"`
	Error       string    `json:"error,omitempty"`
}

// PlacementPolicy 对应 information_schema.PLACEMENT_POLICIES 的完整行。
type PlacementPolicy struct {
	PolicyID           int64  `json:"policy_id"`
	CatalogName        string `json:"catalog_name"`
	PolicyName         string `json:"policy_name"`
	PrimaryRegion      string `json:"primary_region"`
	Regions            string `json:"regions"`
	Constraints        string `json:"constraints"`
	LeaderConstraints  string `json:"leader_constraints"`
	FollowerConstraints string `json:"follower_constraints"`
	LearnerConstraints string `json:"learner_constraints"`
	Schedule           string `json:"schedule"`
	Followers          int    `json:"followers"`
	Learners           int    `json:"learners"`
	SurvivalPreferences string `json:"survival_preferences"`
}

// CreatePlacementPolicyRequest 创建放置策略请求体（覆盖常规 + 高级选项）。
type CreatePlacementPolicyRequest struct {
	Name                string `json:"name" binding:"required"`
	PrimaryRegion       string `json:"primary_region"`
	Regions             string `json:"regions"`
	Schedule            string `json:"schedule"`
	Followers           *int   `json:"followers"`
	Constraints         string `json:"constraints"`
	LeaderConstraints   string `json:"leader_constraints"`
	FollowerConstraints string `json:"follower_constraints"`
	LearnerConstraints  string `json:"learner_constraints"`
	Learners            *int   `json:"learners"`
	SurvivalPreferences string `json:"survival_preferences"`
}

// AlterPlacementPolicyRequest 修改放置策略请求体（name 可从 URL 路径参数获取，body 中可省略）。
type AlterPlacementPolicyRequest struct {
	Name                string `json:"name"`
	PrimaryRegion       string `json:"primary_region"`
	Regions             string `json:"regions"`
	Schedule            string `json:"schedule"`
	Followers           *int   `json:"followers"`
	Constraints         string `json:"constraints"`
	LeaderConstraints   string `json:"leader_constraints"`
	FollowerConstraints string `json:"follower_constraints"`
	LearnerConstraints  string `json:"learner_constraints"`
	Learners            *int   `json:"learners"`
	SurvivalPreferences string `json:"survival_preferences"`
}

// PlacementLabel SHOW PLACEMENT LABELS 返回的可用标签。
type PlacementLabel struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// UpdateTenantRequest 修改租户请求体（仅支持修改可变字段）。
type UpdateTenantRequest struct {
	RUPerSec   *int   `json:"ru_per_sec"`
	Priority   string `json:"priority"`
	Burstable  *bool  `json:"burstable"`
	LabelValue *string `json:"label_value"`
}

// TenantResourceDetail 租户资源详情（展示物理隔离 + 逻辑隔离的资源上限）。
type TenantResourceDetail struct {
	// Resource Group 配置（逻辑隔离）
	ResourceGroup *ResourceGroupDetail `json:"resource_group,omitempty"`
	// TiKV 实例列表（物理隔离）
	Stores []Store `json:"stores,omitempty"`
	// Placement Policy 详情（物理隔离）
	PlacementPolicy *PlacementPolicy `json:"placement_policy,omitempty"`
	// 隔离模式说明
	IsolationLevel string `json:"isolation_level"`
	StoreMatchMode string `json:"store_match_mode"` // "label" | "all"
}

// ResourceGroupDetail 从 information_schema.RESOURCE_GROUPS 读取的资源组配置。
type ResourceGroupDetail struct {
	Name      string `json:"name"`
	RUPerSec  int    `json:"ru_per_sec"`
	Priority  string `json:"priority"`
	Burstable int    `json:"burstable"` // 1=YES, 0=NO
}

// CreateResourceGroupRequest 创建资源组请求体。
type CreateResourceGroupRequest struct {
	Name      string `json:"name" binding:"required"`
	RUPerSec  *int   `json:"ru_per_sec"`
	Priority  string `json:"priority"`
	Burstable bool   `json:"burstable"`
}

// AlterResourceGroupRequest 修改资源组请求体（name 从 URL 路径获取）。
type AlterResourceGroupRequest struct {
	RUPerSec  *int   `json:"ru_per_sec"`
	Priority  string `json:"priority"`
	Burstable *bool  `json:"burstable"`
}

// DBUser 数据库用户（mysql.user）。
type DBUser struct {
	User          string `json:"user"`
	Host          string `json:"host"`
	Authentication string `json:"authentication,omitempty"`
	ResourceGroup string `json:"resource_group,omitempty"`
}

// CreateUserRequest 创建数据库用户。
type CreateUserRequest struct {
	User          string `json:"user" binding:"required"`
	Host          string `json:"host"`
	Password      string `json:"password" binding:"required"`
	ResourceGroup string `json:"resource_group"`
}

// UpdateUserRequest 修改数据库用户（密码/资源组）。
type UpdateUserRequest struct {
	Password      *string `json:"password"`
	ResourceGroup *string `json:"resource_group"`
}

// PrivilegeEntry 用户权限项。
type PrivilegeEntry struct {
	Grantee  string `json:"grantee"`
	TableSchema string `json:"table_schema"`
	TableName   string `json:"table_name"`
	PrivilegeType string `json:"privilege_type"`
	IsGrantable  string `json:"is_grantable"`
}

// GrantPrivilegeRequest 授予权限。
type GrantPrivilegeRequest struct {
	Privileges string `json:"privileges" binding:"required"` // 如 "ALL", "SELECT,INSERT", "SELECT"
	Database   string `json:"database" binding:"required"`    // * 或具体库名
	Table      string `json:"table"`                          // * 或具体表名，默认 *
}

// DatabaseInfo 数据库信息（含绑定的放置策略）。
type DatabaseInfo struct {
	Name            string `json:"name"`
	PlacementPolicy string `json:"placement_policy,omitempty"`
	TableCount      int    `json:"table_count"`
	SizeMB          int64  `json:"size_mb"`
}

// TableInfo 表信息（含绑定的放置策略）。
type TableInfo struct {
	Name            string `json:"name"`
	Schema          string `json:"schema"`
	PlacementPolicy string `json:"placement_policy,omitempty"`
	RowCount        int64  `json:"row_count"`
	SizeMB          int64  `json:"size_mb"`
	Engine          string `json:"engine"`
}

// BindPolicyRequest 绑定/解绑放置策略。
type BindPolicyRequest struct {
	Policy string `json:"policy"` // 空字符串或 "default" = 解绑
}

// DryRunRequest placement 变更影响预估入参（见 docs/p0-technical-design.md §4 dry-run）。
type DryRunRequest struct {
	LabelKey   string   `json:"label_key"`
	LabelValue string   `json:"label_value"`
	Databases  []string `json:"databases"` // 要预估的目标库（影响范围）
	Voters     int      `json:"voters"`    // 副本数，默认 3
}

// DryRunResult placement 变更影响预估结果。
type DryRunResult struct {
	AffectedRegions   int      `json:"affected_regions"`   // 需移动的 Region 数
	TotalSizeMB       int64    `json:"total_size_mb"`      // 受影响数据总量
	ReplicationFactor int      `json:"replication_factor"` // 副本数
	NeededMB          int64    `json:"needed_mb"`          // 目标池需承载 = size * 副本
	TargetPoolCount   int      `json:"target_pool_count"`  // 目标标签节点数
	TargetAvailMB     int64    `json:"target_avail_mb"`    // 目标池可用容量
	TargetCapacityOK  bool     `json:"target_capacity_ok"` // 目标池容量是否足够
	EstMinutes        float64  `json:"est_minutes"`        // 预估调度时长（分钟）
	Warnings          []string `json:"warnings"`
}
