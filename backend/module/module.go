// Package module 是 crm-opportunity 唯一的装配入口（全局约束 §K、设计书
// §12.5.1、§13.3 铁律七）。单跑与合并走同一个 New 函数；模块只交回零件
// （handler、gRPC 注册函数、迁移、后台循环），谁去 Listen、谁开池、
// 谁 init OTel、谁装信号处理器，全归调用方。
package module

import (
	"context"

	besdk "github.com/brickKit/be-sdk-go"
	opportunityv1 "github.com/brickKit/crm-opportunity/gen/crm/opportunity/v1"
	"google.golang.org/grpc"

	"github.com/brickKit/crm-opportunity/backend/internal/consumer"
	grpcapi "github.com/brickKit/crm-opportunity/backend/internal/grpc"
	httpapi "github.com/brickKit/crm-opportunity/backend/internal/http"
	"github.com/brickKit/crm-opportunity/backend/internal/partition"
	"github.com/brickKit/crm-opportunity/backend/internal/repo"
	"github.com/brickKit/crm-opportunity/backend/internal/service"
	"github.com/brickKit/crm-opportunity/migrations"
)

// New 构造 crm-opportunity 模块。签名一个字都不许改（§12.5.1）。
func New(ctx context.Context, rt *besdk.Runtime) (*besdk.Module, error) {
	// ⚠️ 配置只从 rt.Config 来，模块里零 os.Getenv（§12.5.3、决策 110）。
	schema := rt.Config.StringOr("pgSchema", "crm_opportunity")
	role := schema + "_rw"

	r := repo.New(rt.DB, role, schema)
	svc := service.New(r, rt.Logger)

	// HTTP：engine 必须用 besdk.NewGinEngine，它已挂好 OTel / request-id /
	// error→status / PII 脱敏日志 / RED 指标 / /healthz / /metrics。
	eng := besdk.NewGinEngine(rt)
	httpapi.RegisterRoutes(eng, svc)

	return &besdk.Module{
		HTTPHandler: eng,

		// ⚠️ gRPC 一个不省，而且由调用方在 extraPorts["grpc"] 上 Listen
		// （§1.5 原则一）。
		RegisterGRPC: func(gs *grpc.Server) {
			opportunityv1.RegisterOpportunityServiceServer(gs, grpcapi.New(svc))
		},

		Migrations: migrations.FS, // 合并态由外壳按拓扑顺序跑（§13.3 铁律五）

		// 后台循环：Outbox 推送 + 周分区维护（event_outbox/event_inbox）+
		// 消费事件。三个循环必须并发跑，不能顺序调用。⚠️ 本组件没有月
		// 分区维护循环——opportunities/opportunity_items/
		// opportunity_stage_history 不分区（设计计划 §2、§7）。
		Start: func(ctx context.Context) error {
			errCh := make(chan error, 3)
			go func() { errCh <- besdk.StartOutboxPump(ctx, rt.DB, schema, rt.NATS, rt.Logger) }()
			go func() { errCh <- partition.Start(ctx, rt.DB, role, schema, rt.Logger) }()
			go func() { errCh <- consumer.Start(ctx, rt.DB, role, schema, rt.NATS, rt.Logger) }()

			select {
			case <-ctx.Done():
				return nil
			case err := <-errCh:
				return err // ⚠️ 返回 error，不许 log.Fatal：一个模块退进程 = 整组组件一起没了
			}
		},
		Stop: func(ctx context.Context) error { return nil }, // 后台循环靠 ctx 退出
	}, nil
}
