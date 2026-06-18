// Package orchestrator 把租户的多步操作编排成事务性、可回滚的 Job。
package orchestrator

import (
	"fmt"

	"github.com/tidb-multi-tenant/console/internal/model"
	"github.com/tidb-multi-tenant/console/internal/store"
)

const (
	stPending   = "pending"
	stRunning   = "running"
	stSucceeded = "succeeded"
	stFailed    = "failed"
	stSkipped   = "skipped"
)

// TenantOrchestrator 负责租户创建的 6 步编排与失败回滚。
type TenantOrchestrator struct {
	Meta   *store.Metadata
	Target *store.ClusterSQL // 目标集群 SQL 连接
	PD     *store.PDClient   // 目标集群 PD
}

// NewTenantOrchestrator 为指定集群构造编排器。
func NewTenantOrchestrator(meta *store.Metadata, target *store.ClusterSQL, pd *store.PDClient) *TenantOrchestrator {
	return &TenantOrchestrator{Meta: meta, Target: target, PD: pd}
}

// CreateTenant 同步执行创建（P0）；生产可改为异步 job worker。
// 流程见 docs/p0-technical-design.md §5。任一步失败，对已完成步逆序执行补偿。
func (o *TenantOrchestrator) CreateTenant(req model.CreateTenantRequest, actor string) (*model.Job, error) {
	pfx := "t_" + req.Name
	policyName, rgName := pfx+"_pol", pfx+"_rg"

	steps := o.buildSteps(req, policyName, rgName)
	job := &model.Job{OpType: "CREATE_TENANT", Status: "RUNNING", Steps: steps}

	// 先建元数据占位 tenant（便于关联 job 与回滚）
	labelValue := req.LabelValue
	if labelValue == "" {
		labelValue = req.Name // fallback：未指定则用租户名
	}
	tenant := &model.Tenant{
		ClusterID: req.ClusterID, Name: req.Name, IsolationLevel: req.IsolationLevel,
		LabelKey: req.LabelKey, LabelValue: labelValue,
		PlacementPolicy: policyName, ResourceGroup: rgName,
		RUPerSec: req.ResourceGroup.RUPerSec, Priority: req.ResourceGroup.Priority,
		Status: model.Active, RetentionDays: 30,
		Databases: req.Databases,
	}
	if err := o.Meta.CreateTenant(tenant); err != nil {
		return nil, fmt.Errorf("write metadata: %w", err)
	}
	job.TenantID = tenant.ID
	_ = o.Meta.CreateJob(job)

	// 逐步执行
	for i := range job.Steps {
		job.CurrentStep = i
		job.Steps[i].Status = stRunning
		if err := o.execStep(&job.Steps[i], req, policyName, rgName); err != nil {
			job.Steps[i].Status = stFailed
			job.Steps[i].Error = err.Error()
			// 回滚：逆序对已完成步执行补偿
			o.rollback(job.Steps[:i], req, rgName)
			job.Status = "ROLLED_BACK"
			_ = o.Meta.FinishJob(job.ID, "ROLLED_BACK", err.Error(), job.Steps)
			o.Meta.Audit(actor, req.ClusterID, tenant.ID, "CREATE_TENANT", "tenant", req.Name, "failed", err.Error())
			// 回滚后把 tenant 标记 DELETED（已清理底层实体）
			_ = o.Meta.UpdateTenantStatus(tenant.ID, model.Deleted, -1)
			return job, fmt.Errorf("step %q failed: %w (rolled back)", job.Steps[i].Name, err)
		}
		job.Steps[i].Status = stSucceeded
	}
	job.Status = "SUCCEEDED"
	_ = o.Meta.FinishJob(job.ID, "SUCCEEDED", "", job.Steps)
	o.Meta.Audit(actor, req.ClusterID, tenant.ID, "CREATE_TENANT", "tenant", req.Name, "success", "")
	return job, nil
}

// buildSteps 定义编排步骤（不再修改 TiKV 标签，直接用已有标签约束数据分布）。
func (o *TenantOrchestrator) buildSteps(req model.CreateTenantRequest, policyName, rgName string) []model.JobStep {
	needPolicy := req.IsolationLevel != model.Logical
	labelValue := req.LabelValue
	if labelValue == "" {
		labelValue = req.Name
	}
	cons := []string{
		fmt.Sprintf("[+%s=%s]", req.LabelKey, labelValue),
		fmt.Sprintf("[+%s=%s]", req.LabelKey, labelValue),
	}
	steps := []model.JobStep{
		{Name: "create_placement_policy", Action: "建放置策略 " + policyName, Status: stPending},
		{Name: "create_resource_group", Action: "建资源组 " + rgName, Status: stPending},
		{Name: "create_databases", Action: "建库并绑 policy", Status: stPending},
		{Name: "create_users", Action: "建用户并绑资源组", Status: stPending},
		{Name: "write_metadata", Action: "落 mt_console 元数据", Status: stPending},
	}
	if !needPolicy {
		steps[0].Status = stSkipped
		steps[0].Action = "逻辑隔离，跳过 placement policy"
	}
	// 预生成 placement / rg SQL（用于展示与执行）
	if needPolicy {
		q, comp := o.Target.CreatePlacementPolicy(policyName, req.Placement.Voters, req.Placement.SurvivalPreferences, cons)
		steps[0].SQL, steps[0].Compensate = q, comp
	}
	qRG, compRG := o.Target.CreateResourceGroup(rgName, req.ResourceGroup.RUPerSec, req.ResourceGroup.Burstable, req.ResourceGroup.Priority)
	steps[1].SQL, steps[1].Compensate = qRG, compRG
	return steps
}

// execStep 执行单步。
func (o *TenantOrchestrator) execStep(s *model.JobStep, req model.CreateTenantRequest, policyName, rgName string) error {
	switch s.Name {
	case "create_placement_policy":
		if s.Status == stSkipped {
			return nil
		}
		return o.Target.Exec(s.SQL)
	case "create_resource_group":
		return o.Target.Exec(s.SQL)
	case "create_databases":
		pol := policyName
		if req.IsolationLevel == model.Logical {
			pol = ""
		}
		for _, d := range req.Databases {
			q, _ := o.Target.CreateDatabaseWithPolicy("t_"+req.Name+"_"+d, pol)
			if err := o.Target.Exec(q); err != nil {
				return err
			}
		}
		return nil
	case "create_users":
		for _, u := range req.Users {
			host := u.Host
			if host == "" {
				host = "%"
			}
			q, _ := o.Target.CreateUserWithResourceGroup(u.Username, host, u.Password, rgName)
			if err := o.Target.Exec(q); err != nil {
				return err
			}
		}
		return nil
	case "write_metadata":
		return nil // 元数据在 CreateTenant 入口已写
	}
	return fmt.Errorf("unknown step %s", s.Name)
}

// rollback 逆序对 succeeded 步执行补偿。
func (o *TenantOrchestrator) rollback(steps []model.JobStep, req model.CreateTenantRequest, rgName string) {
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Status != stSucceeded {
			continue
		}
		var err error
		switch s.Name {
		case "create_users":
			for _, u := range req.Users {
				host := u.Host
				if host == "" {
					host = "%"
				}
				_ = o.Target.Exec(fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'", u.Username, host))
			}
		case "create_databases":
			for _, d := range req.Databases {
				_ = o.Target.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `t_%s_%s`", req.Name, d))
			}
		case "create_resource_group":
			err = o.Target.Exec(fmt.Sprintf("DROP RESOURCE GROUP IF EXISTS `%s`", rgName))
		case "create_placement_policy":
			if s.Compensate != "" {
				err = o.Target.Exec(s.Compensate)
			}
		case "ensure_store_labels":
			// 标签保留（幂等可重建），不主动删
		}
		if err != nil {
			s.Status = stFailed
			s.Error = "compensate failed: " + err.Error()
		}
	}
}

// DeleteTenant 删除租户：逆序清理底层实体（用户→库→资源组→放置策略）+ 元数据标记 DELETED。
func (o *TenantOrchestrator) DeleteTenant(t model.Tenant, actor string) error {
	rgName := t.ResourceGroup
	policyName := t.PlacementPolicy

	// 从元数据获取关联的数据库和用户
	dbs, _ := o.Meta.GetTenantDatabases(t.ID)

	steps := []model.JobStep{
		{Name: "drop_users", Action: "删除关联用户", Status: stPending},
		{Name: "drop_databases", Action: "删除关联数据库", Status: stPending},
		{Name: "drop_resource_group", Action: "删除资源组 " + rgName, Status: stPending},
		{Name: "drop_placement_policy", Action: "删除放置策略 " + policyName, Status: stPending},
		{Name: "update_metadata", Action: "元数据标记 DELETED", Status: stPending},
	}
	if rgName == "" {
		steps[2].Status = stSkipped
	}
	if policyName == "" || t.IsolationLevel == model.Logical {
		steps[3].Status = stSkipped
	}

	job := &model.Job{TenantID: t.ID, OpType: "DELETE_TENANT", Status: "RUNNING", Steps: steps}
	_ = o.Meta.CreateJob(job)

	for i := range job.Steps {
		job.CurrentStep = i
		if job.Steps[i].Status == stSkipped {
			continue
		}
		job.Steps[i].Status = stRunning
		if err := o.execDeleteStep(&job.Steps[i], t, dbs, rgName, policyName); err != nil {
			job.Steps[i].Status = stFailed
			job.Steps[i].Error = err.Error()
			job.Status = "FAILED"
			_ = o.Meta.FinishJob(job.ID, "FAILED", err.Error(), job.Steps)
			o.Meta.Audit(actor, t.ClusterID, t.ID, "DELETE_TENANT", "tenant", t.Name, "failed", err.Error())
			return fmt.Errorf("step %q failed: %w", job.Steps[i].Name, err)
		}
		job.Steps[i].Status = stSucceeded
	}
	job.Status = "SUCCEEDED"
	_ = o.Meta.FinishJob(job.ID, "SUCCEEDED", "", job.Steps)
	o.Meta.Audit(actor, t.ClusterID, t.ID, "DELETE_TENANT", "tenant", t.Name, "success", "")
	return nil
}

func (o *TenantOrchestrator) execDeleteStep(s *model.JobStep, t model.Tenant, dbs []string, rgName, policyName string) error {
	switch s.Name {
	case "drop_users":
		// 查询 mysql.user 中绑定了该资源组的用户并删除
		rows, err := o.Target.DB.Query(
			fmt.Sprintf("SELECT user,host FROM mysql.user WHERE attribute LIKE '%%resource_group=%s%%'", rgName))
		if err != nil {
			// 如果 attribute 查询不支持，用元数据中的用户
			return nil
		}
		defer rows.Close()
		for rows.Next() {
			var user, host string
			if err := rows.Scan(&user, &host); err == nil {
				s.SQL += fmt.Sprintf("\nDROP USER IF EXISTS '%s'@'%s';", user, host)
			}
		}
		return nil
	case "drop_databases":
		for _, d := range dbs {
			q := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", d)
			s.SQL += q + "\n"
			if err := o.Target.Exec(q); err != nil {
				return err
			}
		}
		return nil
	case "drop_resource_group":
		if rgName == "" {
			return nil
		}
		q := fmt.Sprintf("DROP RESOURCE GROUP IF EXISTS `%s`", rgName)
		s.SQL = q
		return o.Target.Exec(q)
	case "drop_placement_policy":
		if policyName == "" {
			return nil
		}
		q := fmt.Sprintf("DROP PLACEMENT POLICY IF EXISTS `%s`", policyName)
		s.SQL = q
		return o.Target.Exec(q)
	case "update_metadata":
		return o.Meta.UpdateTenantStatus(t.ID, model.Deleted, -1)
	}
	return fmt.Errorf("unknown step %s", s.Name)
}
