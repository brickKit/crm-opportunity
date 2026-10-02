package client

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	besdk "github.com/brickKit/be-sdk-go"

	customerv1 "github.com/brickKit/mdm-customer/gen/mdm/customer/v1"
)

// slowCustomer 模拟一个进程活着、却迟迟不回包的 mdm-customer：BatchGet 要 3 秒
// 才返回，调用方的 deadline 先到时随之返回。
type slowCustomer struct {
	customerv1.UnimplementedCustomerServiceServer
}

func (slowCustomer) BatchGet(ctx context.Context, _ *customerv1.BatchGetRequest) (*customerv1.BatchGetResponse, error) {
	select {
	case <-time.After(3 * time.Second):
		return &customerv1.BatchGetResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestBatchGetCustomers_下游不回包时按超时返回：建商机时同步校验客户，下游
// 卡住不能让本组件的请求无限挂着；到了超时就以 DeadlineExceeded 返回。
func TestBatchGetCustomers_下游不回包时按超时返回(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	customerv1.RegisterCustomerServiceServer(srv, slowCustomer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	cfg := besdk.NewConfig(map[string]string{"MDM_CUSTOMER_GRPC_ENDPOINT": "http://" + lis.Addr().String()})
	d := New(cfg, 200*time.Millisecond)

	start := time.Now()
	_, err = d.BatchGetCustomers(context.Background(), []string{"1"})
	elapsed := time.Since(start)

	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("期望 DeadlineExceeded，实际 err=%v（用时 %v）", err, elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("超时设的是 200ms，调用却挂了 %v", elapsed)
	}
}
