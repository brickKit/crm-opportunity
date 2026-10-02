// Package module 是 crm-opportunity 唯一的装配入口：单跑与合进外壳走同一个
// New 函数，所以它必须能和别的模块同处一个进程。模块只交回零件
// （handler、gRPC 注册函数、迁移、后台循环），谁去 Listen、谁开池、
// 谁 init OTel、谁装信号处理器，全归调用方。
package module

import (
	"context"
	"fmt"
	"regexp"

	besdk "github.com/brickKit/be-sdk-go"
	opportunityv1 "github.com/brickKit/crm-opportunity/gen/crm/opportunity/v1"
	"google.golang.org/grpc"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/consumer"
	grpcapi "github.com/brickKit/crm-opportunity/v2/backend/internal/grpc"
	httpapi "github.com/brickKit/crm-opportunity/v2/backend/internal/http"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/partition"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/service"
)

// New 构造 crm-opportunity 模块。签名不能改：外壳按这个签名注册每个成员。
func New(ctx context.Context, rt *besdk.Runtime) (*besdk.Module, error) {
	// ⚠️ 配置只从 rt.Config 来，模块里零 os.Getenv：进了外壳，一个进程只有一份
	// 环境变量，成员会互相读到对方的 PG_SCHEMA。
	role, schema, err := dbIdentity(rt.Config)
	if err != nil {
		return nil, err
	}

	r := repo.New(rt.DB, role, schema)
	svc := service.New(r, client.New(rt.Config, client.DefaultTimeout), rt.Logger)

	// HTTP：engine 必须用 besdk.NewGinEngine，它已挂好 OTel / request-id /
	// error→status / PII 脱敏日志 / RED 指标 / /healthz / /metrics。
	eng := besdk.NewGinEngine(rt)
	httpapi.RegisterRoutes(eng, svc)

	return &besdk.Module{
		HTTPHandler: eng,

		// ⚠️ gRPC 一个不省，而且由调用方在 extraPorts["grpc"] 上 Listen：
		// 单独部署时别的组件经真实 gRPC 调本组件。
		RegisterGRPC: func(gs *grpc.Server) {
			opportunityv1.RegisterOpportunityServiceServer(gs, grpcapi.New(svc))
		},

		// 后台循环：Outbox 推送 + 周分区维护（event_outbox/event_inbox）+
		// 消费事件。三个循环必须并发跑，不能顺序调用。⚠️ 本组件没有月
		// 分区维护循环——opportunities/opportunity_items/
		// opportunity_stage_history 不分区（一个销售机会一行，量级小）。
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

// dbIdentity 取本模块的数据库身份：每个事务 SET LOCAL ROLE 到 PG_USER，
// search_path 指向 PG_SCHEMA。两个都只从配置来，代码里不写任何角色名、
// schema 名，也不从一个推出另一个——部署者给什么名字就用什么名字。
//
// 单跑时 PG_USER 就是连接池登录的角色；进了外壳，连接池以外壳自己的角色登录，
// 成员配置里的 PG_USER 是它每个事务切换到的角色（外壳的登录角色要是它的成员）。
// 两个键都必须有值：PG_SCHEMA 的默认值由平台按 configSchema 注入，缺了说明
// 配置没生成对，带着空名字跑起来只会在第一个请求时才报错。
func dbIdentity(cfg besdk.Config) (role, schema string, err error) {
	role, _ = cfg.String("PG_USER")
	schema, _ = cfg.String("PG_SCHEMA")
	if role == "" || schema == "" {
		return "", "", fmt.Errorf("数据库身份不全：PG_USER=%q PG_SCHEMA=%q，两个都必须有值", role, schema)
	}
	// 两个名字都要原样拼进 SET LOCAL ROLE / search_path，besdk.WithTx 每个事务都按
	// 同一条规则校验；这里在启动时先查一次，配错了起不来，而不是每个请求都 500。
	for key, v := range map[string]string{"PG_USER": role, "PG_SCHEMA": schema} {
		if !identRe.MatchString(v) {
			return "", "", fmt.Errorf("%s=%q 不能用作数据库标识：只许小写字母开头、小写字母数字下划线", key, v)
		}
	}
	return role, schema, nil
}

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
