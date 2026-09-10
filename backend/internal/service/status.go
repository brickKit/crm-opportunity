package service

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/brickKit/crm-opportunity/backend/internal/repo"
)

// ToStatus 把 repo 层的哨兵错误翻成 gRPC status——HTTP 与 gRPC 两条对外
// 接口共用同一套业务错误类型（同 erp-sales/erp-finance/erp-inventory
// 的判据）。
//
// ⚠️ err 可能是"透传"自两条依赖边（mdm-customer/mdm-product）某一次
// 远程调用本身返回的 gRPC status（如 InvalidArgument：客户/产品不存在
// 已经在 service 层被包成 ErrInvalidArgument，但底层网络错误——如
// context.DeadlineExceeded——不应该被压成一个看起来像系统内部错误的
// 500）。所以在真正走到 default 之前，先检查 err 是不是已经带着一个
// 真实的 gRPC status，是的话原样透传。
func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, repo.ErrOpportunityTerminal):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, repo.ErrVersionConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, repo.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, repo.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}
