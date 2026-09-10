// Package service 是 crm-opportunity 的业务规则层：入参校验 + 跨组件
// 校验（mdm-customer/mdm-product 的 BatchGet）+ 给 http/grpc 一个不依赖
// repo 内部细节的稳定入口（同 erp-sales/erp-inventory/erp-finance 的
// 判据）。⚠️ 本组件没有 TCC 包——两条强依赖都是只读 BatchGet，没有
// 需要补偿的远程写操作，校验逻辑直接放在这一层（设计计划 §5）。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	besdk "github.com/brickKit/be-sdk-go"

	"github.com/brickKit/crm-opportunity/backend/internal/client"
	"github.com/brickKit/crm-opportunity/backend/internal/repo"

	customerv1 "github.com/brickKit/crm-opportunity/gen/mdm/customer/v1"
	productv1 "github.com/brickKit/crm-opportunity/gen/mdm/product/v1"
)

var ErrInvalidArgument = errors.New("参数不合法")

type Service struct {
	repo   *repo.Repo
	logger *slog.Logger
}

func New(r *repo.Repo, logger *slog.Logger) *Service {
	return &Service{repo: r, logger: logger}
}

// checkOpportunityInScope 是 UpdateOpportunity/ChangeStage/MarkWon/
// MarkLost 共用的一步：这四个命令在真正执行状态流转之前，先确认这个
// 商机在调用者的 org/owner 范围内——只保护 List/Get 两个读接口、放过
// 写接口，等于给"看不到就该动不了"这条判据留了个后门（同 erp-sales
// checkOrderInScope 的既有判据）。
func (s *Service) checkOpportunityInScope(ctx context.Context, id string) error {
	opp, err := s.repo.GetOpportunity(ctx, id)
	if err != nil {
		return err
	}
	scope := besdk.ScopeOf(ctx)
	if !opp.InScope(scope.Prefix, scope.Owner) {
		return repo.ErrForbidden
	}
	return nil
}

// leafDeptID 从 dept_path 的末段推出 dept_id（同 erp-sales tcc.leafDeptID
// 的既有判据：infra-authz 的 dept_path 本身就是 `parentPath + id + "/"`
// 拼出来的，末段恰好是这个部门自己的 id）。只在本组件内联，不提进
// be-sdk-go——现在只有两个调用方（erp-sales、本组件），SOP-P 的判据
// 还没到"第三个组件也需要它"的门槛。
func leafDeptID(deptPath string) string {
	trimmed := strings.Trim(deptPath, "/")
	if trimmed == "" {
		return ""
	}
	segments := strings.Split(trimmed, "/")
	return segments[len(segments)-1]
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// fetchActiveCustomer 校验客户存在且可用——建档那一刻就要挡住脏数据，
// 事后对账挡不住（设计计划 §3）。
func fetchActiveCustomer(ctx context.Context, customerID string) (*customerv1.Customer, error) {
	conn, closeConn, err := client.Customer(ctx)
	if err != nil {
		return nil, err
	}
	defer closeConn()

	resp, err := conn.BatchGet(ctx, &customerv1.BatchGetRequest{Ids: []string{customerID}})
	if err != nil {
		return nil, fmt.Errorf("校验客户失败: %w", err)
	}
	if len(resp.Customers) == 0 {
		return nil, fmt.Errorf("%w: 客户不存在：customer_id=%s", ErrInvalidArgument, customerID)
	}
	c := resp.Customers[0]
	if c.Status != customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE {
		return nil, fmt.Errorf("%w: 客户不可用：customer_id=%s status=%s", ErrInvalidArgument, customerID, c.Status)
	}
	return c, nil
}

// fetchActiveProducts 校验产品存在且可用，返回按 id 索引的 map 供调用方
// 取展示快照（sku/name/uom）。
func fetchActiveProducts(ctx context.Context, productIDs []string) (map[string]*productv1.Product, error) {
	conn, closeConn, err := client.Product(ctx)
	if err != nil {
		return nil, err
	}
	defer closeConn()

	resp, err := conn.BatchGet(ctx, &productv1.BatchGetRequest{Ids: productIDs})
	if err != nil {
		return nil, fmt.Errorf("校验产品失败: %w", err)
	}
	if len(resp.MissingIds) > 0 {
		return nil, fmt.Errorf("%w: 产品不存在：%v", ErrInvalidArgument, resp.MissingIds)
	}
	byID := make(map[string]*productv1.Product, len(resp.Products))
	for _, p := range resp.Products {
		if p.Status != productv1.ProductStatus_PRODUCT_STATUS_ACTIVE {
			return nil, fmt.Errorf("%w: 产品不可用：product_id=%s status=%s", ErrInvalidArgument, p.Id, p.Status)
		}
		byID[p.Id] = p
	}
	return byID, nil
}

// ── 命令 ──

type CreateOpportunityItem struct {
	ProductID       string
	Qty             string
	QuotedUnitPrice string
}

type CreateOpportunityInput struct {
	IdempotencyKey      string
	Name                string
	CustomerID          string
	Items               []CreateOpportunityItem
	ExpectedAmount      string
	ProbabilityOverride *int
	ExpectedCloseDate   time.Time
	Currency            string
}

func (s *Service) CreateOpportunity(ctx context.Context, in CreateOpportunityInput) (*repo.Opportunity, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	if in.Name == "" {
		return nil, fmt.Errorf("%w: name 不能为空", ErrInvalidArgument)
	}
	if in.CustomerID == "" {
		return nil, fmt.Errorf("%w: customer_id 不能为空", ErrInvalidArgument)
	}
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("%w: items 不能为空", ErrInvalidArgument)
	}
	for _, it := range in.Items {
		if it.ProductID == "" || it.Qty == "" || it.QuotedUnitPrice == "" {
			return nil, fmt.Errorf("%w: product_id/qty/quoted_unit_price 不能为空", ErrInvalidArgument)
		}
	}

	customer, err := fetchActiveCustomer(ctx, in.CustomerID)
	if err != nil {
		return nil, err
	}

	productIDs := make([]string, 0, len(in.Items))
	for _, it := range in.Items {
		productIDs = append(productIDs, it.ProductID)
	}
	products, err := fetchActiveProducts(ctx, uniqueStrings(productIDs))
	if err != nil {
		return nil, err
	}

	items := make([]repo.CreateOpportunityItemInput, 0, len(in.Items))
	for _, it := range in.Items {
		p := products[it.ProductID]
		items = append(items, repo.CreateOpportunityItemInput{
			ProductID: it.ProductID, ProductSKU: p.Sku, ProductName: p.Name, UOMID: p.BaseUomId,
			Qty: it.Qty, QuotedUnitPrice: it.QuotedUnitPrice,
		})
	}

	// dept_id/dept_path/owner_id 是创建时快照（设计计划 §1），从
	// besdk.ScopeOf(ctx) 取真实值。
	scope := besdk.ScopeOf(ctx)
	opp, err := s.repo.CreateOpportunity(ctx, repo.CreateOpportunityInput{
		IdempotencyKey: in.IdempotencyKey, Name: in.Name, CustomerID: in.CustomerID, CustomerName: customer.Name,
		Items: items, ExpectedAmount: in.ExpectedAmount, ProbabilityOverride: in.ProbabilityOverride,
		ExpectedCloseDate: in.ExpectedCloseDate, Currency: in.Currency,
		DeptID: leafDeptID(scope.Prefix), DeptPath: scope.Prefix, OwnerID: scope.Owner,
	})
	if err != nil {
		s.logger.Error("建商机失败", "customer_id", in.CustomerID, "error", err)
		return nil, err
	}
	return opp, nil
}

func (s *Service) UpdateOpportunity(ctx context.Context, in repo.UpdateOpportunityInput) (*repo.Opportunity, error) {
	if in.ID == "" {
		return nil, fmt.Errorf("%w: id 不能为空", ErrInvalidArgument)
	}
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	if err := s.checkOpportunityInScope(ctx, in.ID); err != nil {
		return nil, err
	}
	opp, err := s.repo.UpdateOpportunity(ctx, in)
	if err != nil {
		s.logger.Error("更新商机失败", "opportunity_id", in.ID, "error", err)
		return nil, err
	}
	return opp, nil
}

func (s *Service) ChangeStage(ctx context.Context, in repo.ChangeStageInput) (*repo.Opportunity, error) {
	if in.ID == "" {
		return nil, fmt.Errorf("%w: id 不能为空", ErrInvalidArgument)
	}
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	if in.ToStageID == "" {
		return nil, fmt.Errorf("%w: to_stage_id 不能为空", ErrInvalidArgument)
	}
	if err := s.checkOpportunityInScope(ctx, in.ID); err != nil {
		return nil, err
	}
	// changed_by 是操作人，不是调用方入参——同 owner_id 的来源，取自
	// JWT 身份而不是信任客户端传值。
	in.ChangedBy = besdk.ScopeOf(ctx).Owner
	opp, err := s.repo.ChangeStage(ctx, in)
	if err != nil {
		s.logger.Error("商机阶段推进失败", "opportunity_id", in.ID, "error", err)
		return nil, err
	}
	return opp, nil
}

func (s *Service) MarkWon(ctx context.Context, id, idempotencyKey string) (*repo.Opportunity, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: id 不能为空", ErrInvalidArgument)
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	if err := s.checkOpportunityInScope(ctx, id); err != nil {
		return nil, err
	}
	opp, err := s.repo.MarkWon(ctx, repo.MarkWonInput{IdempotencyKey: idempotencyKey, ID: id})
	if err != nil {
		s.logger.Error("商机赢单失败", "opportunity_id", id, "error", err)
		return nil, err
	}
	return opp, nil
}

func (s *Service) MarkLost(ctx context.Context, id, idempotencyKey, reason string) (*repo.Opportunity, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: id 不能为空", ErrInvalidArgument)
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	if reason == "" {
		return nil, fmt.Errorf("%w: reason 不能为空", ErrInvalidArgument)
	}
	if err := s.checkOpportunityInScope(ctx, id); err != nil {
		return nil, err
	}
	opp, err := s.repo.MarkLost(ctx, repo.MarkLostInput{IdempotencyKey: idempotencyKey, ID: id, Reason: reason})
	if err != nil {
		s.logger.Error("商机输单失败", "opportunity_id", id, "error", err)
		return nil, err
	}
	return opp, nil
}

// ── 读 ──

func (s *Service) GetOpportunity(ctx context.Context, id string) (*repo.Opportunity, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: id 不能为空", ErrInvalidArgument)
	}
	opp, err := s.repo.GetOpportunity(ctx, id)
	if err != nil {
		return nil, err
	}
	scope := besdk.ScopeOf(ctx)
	if !opp.InScope(scope.Prefix, scope.Owner) {
		return nil, repo.ErrForbidden
	}
	return opp, nil
}

func (s *Service) ListOpportunities(ctx context.Context, in repo.ListInput) (*repo.ListResult, error) {
	scope := besdk.ScopeOf(ctx)
	if in.ViewMine {
		in.ScopeOwner = scope.Owner
	} else {
		in.ScopePrefix = scope.Prefix
	}
	return s.repo.ListOpportunities(ctx, in)
}

func (s *Service) BatchGetOpportunities(ctx context.Context, ids []string) (found []*repo.Opportunity, missing []string, err error) {
	return s.repo.BatchGetOpportunities(ctx, ids)
}

func (s *Service) ListStages(ctx context.Context) ([]*repo.Stage, error) {
	return s.repo.ListStages(ctx)
}
