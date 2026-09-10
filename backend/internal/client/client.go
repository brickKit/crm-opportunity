// Package client 拨号到本组件两条强依赖边的 gRPC 客户端 stub（沿用
// erp-sales 阶段二立的模式：契约只读镜像 + 本地生成客户端 stub，
// contracts/vendor/ 的两份镜像 buf generate 出来的 gen/ 包在这里被
// 组装成可用的 XxxServiceClient）。
//
// ⚠️ 全部走 besdk.UserClient（透传 JWT），不许换成 SystemClient——这是
// 用户请求路径，用 SystemClient 会绕过下游数据权限（设计计划 §3.1、
// 导读第 21 条）。地址走 besdk.Endpoint() 剥 http://，dep 参数用组件 ID、
// extra 参数固定是 "grpc"（对应各组件 component.yaml 的 extraPorts
// {name: grpc}，见 registry/ports.tsv）。
package client

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	besdk "github.com/brickKit/be-sdk-go"

	customerv1 "github.com/brickKit/crm-opportunity/gen/mdm/customer/v1"
	productv1 "github.com/brickKit/crm-opportunity/gen/mdm/product/v1"
)

func dial(ctx context.Context, dep string) (*grpc.ClientConn, error) {
	conn, err := besdk.UserClient(ctx, dep, "grpc")
	if err != nil {
		return nil, fmt.Errorf("拨号 %s: %w", dep, err)
	}
	return conn, nil
}

// Customer 拨一条到 mdm-customer 的连接，返回类型化 stub。
func Customer(ctx context.Context) (customerv1.CustomerServiceClient, func() error, error) {
	conn, err := dial(ctx, "mdm/customer")
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return customerv1.NewCustomerServiceClient(conn), conn.Close, nil
}

// Product 拨一条到 mdm-product 的连接，返回类型化 stub。
func Product(ctx context.Context) (productv1.ProductServiceClient, func() error, error) {
	conn, err := dial(ctx, "mdm/product")
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return productv1.NewProductServiceClient(conn), conn.Close, nil
}
