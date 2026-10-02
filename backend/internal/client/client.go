// Package client 是本组件到两条依赖边（mdm/customer、mdm/product）的 gRPC
// 调用。全部走 besdk.UserClient：这是用户请求路径，透传调用者的 JWT，下游按
// 调用者身份过滤数据；换成 SystemClient 会绕过下游的数据范围，返回的数据
// 只是"多了一些"，不报任何错。
//
// 地址只从 Config 读（rt.Config.Endpoint 剥掉 http://），dep 用组件 ID、extra
// 固定是 "grpc"（对应依赖方 component.yaml 的 extraPorts {name: grpc}）。
package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	besdk "github.com/brickKit/be-sdk-go"

	customerv1 "github.com/brickKit/mdm-customer/gen/mdm/customer/v1"
	productv1 "github.com/brickKit/mdm-product/gen/mdm/product/v1"
)

// DefaultTimeout 是每次同步校验调用的上限。下游卡住（进程活着、不回包）时，
// 没有上限的调用会让建商机的请求一直挂着、占着连接；超时后返回
// DeadlineExceeded（HTTP 504），调用方可以重试。
const DefaultTimeout = 3 * time.Second

// Deps 持有依赖地址与调用超时，service 层通过它调两个主数据组件。
type Deps struct {
	cfg     besdk.Config
	timeout time.Duration
}

func New(cfg besdk.Config, timeout time.Duration) *Deps {
	return &Deps{cfg: cfg, timeout: timeout}
}

// BatchGetCustomers 按 id 批量取客户（校验存在与状态、取展示快照）。
func (d *Deps) BatchGetCustomers(ctx context.Context, ids []string) (*customerv1.BatchGetResponse, error) {
	conn, err := dial(ctx, d.cfg, "mdm/customer")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := d.withTimeout(ctx)
	defer cancel()
	return customerv1.NewCustomerServiceClient(conn).BatchGet(ctx, &customerv1.BatchGetRequest{Ids: ids})
}

// BatchGetProducts 按 id 批量取产品（校验存在与状态、取 sku / 名称 / 单位快照）。
func (d *Deps) BatchGetProducts(ctx context.Context, ids []string) (*productv1.BatchGetResponse, error) {
	conn, err := dial(ctx, d.cfg, "mdm/product")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := d.withTimeout(ctx)
	defer cancel()
	return productv1.NewProductServiceClient(conn).BatchGet(ctx, &productv1.BatchGetRequest{Ids: ids})
}

func (d *Deps) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d.timeout)
}

func dial(ctx context.Context, cfg besdk.Config, dep string) (*grpc.ClientConn, error) {
	conn, err := besdk.UserClient(ctx, cfg, dep, "grpc")
	if err != nil {
		return nil, fmt.Errorf("拨号 %s: %w", dep, err)
	}
	return conn, nil
}

// Customer / Product 拨一条连接并返回完整的类型化 stub，给跨组件测试在真实的
// 依赖里造数据用（业务代码只用上面两个 BatchGet）。
func Customer(ctx context.Context, cfg besdk.Config) (customerv1.CustomerServiceClient, func() error, error) {
	conn, err := dial(ctx, cfg, "mdm/customer")
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return customerv1.NewCustomerServiceClient(conn), conn.Close, nil
}

func Product(ctx context.Context, cfg besdk.Config) (productv1.ProductServiceClient, func() error, error) {
	conn, err := dial(ctx, cfg, "mdm/product")
	if err != nil {
		return nil, func() error { return nil }, err
	}
	return productv1.NewProductServiceClient(conn), conn.Close, nil
}
