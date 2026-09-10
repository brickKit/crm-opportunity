// Package grpc 实现 crm.opportunity.v1.OpportunityService——内部 gRPC 面
// （§2.1）。HTTP 与 gRPC 共用同一个 service.Service，业务逻辑只写一遍。
package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	opportunityv1 "github.com/brickKit/crm-opportunity/gen/crm/opportunity/v1"

	"github.com/brickKit/crm-opportunity/backend/internal/repo"
	"github.com/brickKit/crm-opportunity/backend/internal/service"
)

type server struct {
	opportunityv1.UnimplementedOpportunityServiceServer
	svc *service.Service
}

func New(svc *service.Service) opportunityv1.OpportunityServiceServer {
	return &server{svc: svc}
}

func toProtoStatus(s string) opportunityv1.OpportunityStatus {
	switch s {
	case repo.StatusOpen:
		return opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_OPEN
	case repo.StatusWon:
		return opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_WON
	case repo.StatusLost:
		return opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_LOST
	case repo.StatusCancelled:
		return opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_CANCELLED
	default:
		return opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_UNSPECIFIED
	}
}

// fromProtoStatus 是 toProtoStatus 的逆映射，ListOpportunities 的
// status_filter 用——不能用 pb 枚举的 .String()（那会得到
// "OPPORTUNITY_STATUS_WON" 这种带前缀的名字，repo 层的状态常量是不带
// 前缀的裸值 "WON"，同 erp-sales fromProtoStatus 的既有判据）。
func fromProtoStatus(s opportunityv1.OpportunityStatus) string {
	switch s {
	case opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_OPEN:
		return repo.StatusOpen
	case opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_WON:
		return repo.StatusWon
	case opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_LOST:
		return repo.StatusLost
	case opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_CANCELLED:
		return repo.StatusCancelled
	default:
		return ""
	}
}

func toProtoOpportunity(o *repo.Opportunity) *opportunityv1.Opportunity {
	items := make([]*opportunityv1.OpportunityItem, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, &opportunityv1.OpportunityItem{
			ProductId: it.ProductID, ProductSku: it.ProductSKU, ProductName: it.ProductName,
			UomId: it.UOMID, Qty: it.Qty, QuotedUnitPrice: it.QuotedUnitPrice, Subtotal: it.Subtotal,
		})
	}
	out := &opportunityv1.Opportunity{
		Id: o.ID, Name: o.Name, CustomerId: o.CustomerID, CustomerName: o.CustomerName,
		OwnerId: o.OwnerID, DeptId: o.DeptID, DeptPath: o.DeptPath,
		StageId: o.StageID, StageName: o.StageName, Items: items,
		ExpectedAmount: o.ExpectedAmount, Probability: int32(o.Probability), WeightedAmount: o.WeightedAmount,
		Status: toProtoStatus(o.Status), Currency: o.Currency, LostReason: o.LostReason,
		Version: o.Version, CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
	if !o.ExpectedCloseDate.IsZero() {
		out.ExpectedCloseDate = timestamppb.New(o.ExpectedCloseDate)
	}
	if !o.WonAt.IsZero() {
		out.WonAt = timestamppb.New(o.WonAt)
	}
	return out
}

func toProtoStage(s *repo.Stage) *opportunityv1.OpportunityStage {
	return &opportunityv1.OpportunityStage{
		Id: s.ID, Code: s.Code, Name: s.Name,
		SortOrder: int32(s.SortOrder), DefaultProbability: int32(s.DefaultProbability), IsFinal: s.IsFinal,
	}
}

func (s *server) CreateOpportunity(ctx context.Context, req *opportunityv1.CreateOpportunityRequest) (*opportunityv1.CreateOpportunityResponse, error) {
	items := make([]service.CreateOpportunityItem, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, service.CreateOpportunityItem{
			ProductID: it.ProductId, Qty: it.Qty, QuotedUnitPrice: it.QuotedUnitPrice,
		})
	}
	in := service.CreateOpportunityInput{
		IdempotencyKey: req.IdempotencyKey, Name: req.Name, CustomerID: req.CustomerId, Items: items,
		ExpectedAmount: req.ExpectedAmount, Currency: req.Currency,
	}
	if req.Probability != nil {
		v := int(*req.Probability)
		in.ProbabilityOverride = &v
	}
	if req.ExpectedCloseDate != nil {
		in.ExpectedCloseDate = req.ExpectedCloseDate.AsTime()
	}
	opp, err := s.svc.CreateOpportunity(ctx, in)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &opportunityv1.CreateOpportunityResponse{Opportunity: toProtoOpportunity(opp)}, nil
}

func (s *server) UpdateOpportunity(ctx context.Context, req *opportunityv1.UpdateOpportunityRequest) (*opportunityv1.UpdateOpportunityResponse, error) {
	in := repo.UpdateOpportunityInput{
		IdempotencyKey: req.IdempotencyKey, ID: req.Id, Version: req.Version,
		Name: req.Name, ExpectedAmount: req.ExpectedAmount, OwnerID: req.OwnerId,
	}
	if req.ExpectedCloseDate != nil {
		in.ExpectedCloseDate = req.ExpectedCloseDate.AsTime()
	}
	opp, err := s.svc.UpdateOpportunity(ctx, in)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &opportunityv1.UpdateOpportunityResponse{Opportunity: toProtoOpportunity(opp)}, nil
}

func (s *server) ChangeStage(ctx context.Context, req *opportunityv1.ChangeStageRequest) (*opportunityv1.ChangeStageResponse, error) {
	in := repo.ChangeStageInput{
		IdempotencyKey: req.IdempotencyKey, ID: req.Id, ToStageID: req.ToStageId,
	}
	if req.ProbabilityOverride != nil {
		v := int(*req.ProbabilityOverride)
		in.ProbabilityOverride = &v
	}
	opp, err := s.svc.ChangeStage(ctx, in)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &opportunityv1.ChangeStageResponse{Opportunity: toProtoOpportunity(opp)}, nil
}

func (s *server) MarkWon(ctx context.Context, req *opportunityv1.MarkWonRequest) (*opportunityv1.MarkWonResponse, error) {
	opp, err := s.svc.MarkWon(ctx, req.Id, req.IdempotencyKey)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &opportunityv1.MarkWonResponse{Opportunity: toProtoOpportunity(opp)}, nil
}

func (s *server) MarkLost(ctx context.Context, req *opportunityv1.MarkLostRequest) (*opportunityv1.MarkLostResponse, error) {
	opp, err := s.svc.MarkLost(ctx, req.Id, req.IdempotencyKey, req.Reason)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &opportunityv1.MarkLostResponse{Opportunity: toProtoOpportunity(opp)}, nil
}

func (s *server) GetOpportunity(ctx context.Context, req *opportunityv1.GetOpportunityRequest) (*opportunityv1.Opportunity, error) {
	opp, err := s.svc.GetOpportunity(ctx, req.Id)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return toProtoOpportunity(opp), nil
}

func (s *server) ListOpportunities(ctx context.Context, req *opportunityv1.ListOpportunitiesRequest) (*opportunityv1.ListOpportunitiesResponse, error) {
	in := repo.ListInput{Cursor: req.Cursor, PageSize: int(req.PageSize), CustomerID: req.CustomerId, StageIDFilter: req.StageIdFilter}
	if req.StatusFilter != opportunityv1.OpportunityStatus_OPPORTUNITY_STATUS_UNSPECIFIED {
		in.StatusFilter = fromProtoStatus(req.StatusFilter)
	}
	if req.CreatedAfter != nil {
		in.CreatedAfter = req.CreatedAfter.AsTime()
	}
	if req.CreatedBefore != nil {
		in.CreatedBefore = req.CreatedBefore.AsTime()
	}
	out, err := s.svc.ListOpportunities(ctx, in)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	opps := make([]*opportunityv1.Opportunity, 0, len(out.Opportunities))
	for _, o := range out.Opportunities {
		opps = append(opps, toProtoOpportunity(o))
	}
	return &opportunityv1.ListOpportunitiesResponse{Opportunities: opps, NextCursor: out.NextCursor}, nil
}

// BatchGetOpportunities 是防 N+1 的唯一合法调用方式（§3.8）。
func (s *server) BatchGetOpportunities(ctx context.Context, req *opportunityv1.BatchGetOpportunitiesRequest) (*opportunityv1.BatchGetOpportunitiesResponse, error) {
	found, missing, err := s.svc.BatchGetOpportunities(ctx, req.Ids)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	opps := make([]*opportunityv1.Opportunity, 0, len(found))
	for _, o := range found {
		opps = append(opps, toProtoOpportunity(o))
	}
	return &opportunityv1.BatchGetOpportunitiesResponse{Opportunities: opps, MissingIds: missing}, nil
}

func (s *server) ListStages(ctx context.Context, _ *opportunityv1.ListStagesRequest) (*opportunityv1.ListStagesResponse, error) {
	stages, err := s.svc.ListStages(ctx)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	out := make([]*opportunityv1.OpportunityStage, 0, len(stages))
	for _, st := range stages {
		out = append(out, toProtoStage(st))
	}
	return &opportunityv1.ListStagesResponse{Stages: out}, nil
}
