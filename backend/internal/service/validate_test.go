package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
)

// 金额、数量、概率不合法时，CreateOpportunity 在调 mdm-customer / mdm-product
// 之前就答参数错误（REST 400）：这里两个依赖都没有地址，一旦先发起调用，拿到
// 的就是"依赖不可用"而不是参数错误。
func TestCreateOpportunity_数字入参不合法时不调用依赖直接拒绝(t *testing.T) {
	svc := New(nil, client.New(besdk.NewConfig(nil), client.DefaultTimeout), slog.Default())
	ctx := besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: "u_validate", DeptPath: "/1/"})
	base := func() CreateOpportunityInput {
		return CreateOpportunityInput{
			IdempotencyKey: "k", Name: "n", CustomerID: "1", ExpectedAmount: "100.00",
			Items: []CreateOpportunityItem{{ProductID: "1", Qty: "1", QuotedUnitPrice: "1.00"}},
		}
	}
	over := 101
	cases := map[string]func(*CreateOpportunityInput){
		"expected_amount=NaN":   func(in *CreateOpportunityInput) { in.ExpectedAmount = "NaN" },
		"expected_amount=1e3":   func(in *CreateOpportunityInput) { in.ExpectedAmount = "1e3" },
		"qty=NaN":               func(in *CreateOpportunityInput) { in.Items[0].Qty = "NaN" },
		"quoted_unit_price=Inf": func(in *CreateOpportunityInput) { in.Items[0].QuotedUnitPrice = "Inf" },
		"probability=101":       func(in *CreateOpportunityInput) { in.ProbabilityOverride = &over },
	}
	for name, mutate := range cases {
		in := base()
		mutate(&in)
		_, err := svc.CreateOpportunity(ctx, in)
		if !errors.Is(err, repo.ErrInvalidArgument) {
			t.Errorf("%s：期望不调依赖、直接 ErrInvalidArgument，实际 %v", name, err)
		}
	}
}
