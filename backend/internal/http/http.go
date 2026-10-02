// Package http 是 crm-opportunity 的 REST 面（对外路径前缀
// /crm/opportunity，与 assembly.yaml 的 edge_routes 一致）。⚠️ 不暴露
// BatchGetOpportunities——那是组件之间的 gRPC 调用，给 BFF 一次取多条防 N+1。
//
// 赢单单独一个权限键 crm.opportunity.win，不与普通编辑合并——它会触发
// 建单、锁库存、生成应收一整条链路，是本组件唯一有下游财务影响的动作。
// org+owner 两维数据范围（"本部门及下级"/"我的商机"）与 erp/sales 相同：
// ListOpportunities 走 besdk.ScopeOf(ctx) 的 Prefix/Owner 两个字段。
package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/service"
)

// parseTimeRFC3339OrEmpty 把空字符串当"未传"（返回零值 time.Time，同
// repo 层 ExpectedCloseDate.IsZero() 的既有判据），非空则严格按 RFC3339
// 解析——前端下拉/日期选择器统一产出这个格式。
func parseTimeRFC3339OrEmpty(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

func RegisterRoutes(eng *gin.Engine, svc *service.Service) {
	g := eng.Group("/crm/opportunity")
	besdk.POST(g, "/opportunities", "crm.opportunity.edit", createOpportunityHandler(svc))
	besdk.GET(g, "/opportunities", "crm.opportunity.view", listOpportunitiesHandler(svc))
	besdk.GET(g, "/opportunities/funnel", "crm.opportunity.view", funnelHandler(svc))
	besdk.GET(g, "/opportunities/:id", "crm.opportunity.view", getOpportunityHandler(svc))
	besdk.GET(g, "/opportunities/:id/stage-history", "crm.opportunity.view", stageHistoryHandler(svc))
	besdk.PATCH(g, "/opportunities/:id", "crm.opportunity.edit", updateOpportunityHandler(svc))
	besdk.POST(g, "/opportunities/:id/stage", "crm.opportunity.edit", changeStageHandler(svc))
	besdk.POST(g, "/opportunities/:id/win", "crm.opportunity.win", markWonHandler(svc))
	besdk.POST(g, "/opportunities/:id/lose", "crm.opportunity.edit", markLostHandler(svc))
	besdk.GET(g, "/stages", "crm.opportunity.view", listStagesHandler(svc))
}

const rfc3339 = "2006-01-02T15:04:05.999999999Z07:00"

func toOpportunityDTO(o *repo.Opportunity) gin.H {
	items := make([]gin.H, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, gin.H{
			"product_id": it.ProductID, "product_sku": it.ProductSKU, "product_name": it.ProductName,
			"uom_id": it.UOMID, "qty": it.Qty, "quoted_unit_price": it.QuotedUnitPrice, "subtotal": it.Subtotal,
		})
	}
	dto := gin.H{
		"id": o.ID, "name": o.Name, "customer_id": o.CustomerID, "customer_name": o.CustomerName,
		"owner_id": o.OwnerID, "dept_id": o.DeptID, "dept_path": o.DeptPath,
		"stage_id": o.StageID, "stage_name": o.StageName, "items": items,
		"expected_amount": o.ExpectedAmount, "probability": o.Probability, "weighted_amount": o.WeightedAmount,
		"status": o.Status, "currency": o.Currency, "lost_reason": o.LostReason, "order_id": o.OrderID,
		"version": o.Version, "created_at": o.CreatedAt.Format(rfc3339), "updated_at": o.UpdatedAt.Format(rfc3339),
	}
	if !o.ExpectedCloseDate.IsZero() {
		dto["expected_close_date"] = o.ExpectedCloseDate.Format(rfc3339)
	}
	if !o.WonAt.IsZero() {
		dto["won_at"] = o.WonAt.Format(rfc3339)
	}
	return dto
}

type createOpportunityItemDTO struct {
	ProductID       string `json:"product_id" binding:"required"`
	Qty             string `json:"qty" binding:"required"`
	QuotedUnitPrice string `json:"quoted_unit_price" binding:"required"`
}

type createOpportunityRequest struct {
	IdempotencyKey    string                     `json:"idempotency_key" binding:"required"`
	Name              string                     `json:"name" binding:"required"`
	CustomerID        string                     `json:"customer_id" binding:"required"`
	Items             []createOpportunityItemDTO `json:"items" binding:"required"`
	ExpectedAmount    string                     `json:"expected_amount" binding:"required"`
	Probability       *int                       `json:"probability"`
	ExpectedCloseDate string                     `json:"expected_close_date"`
	Currency          string                     `json:"currency"`
}

func createOpportunityHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createOpportunityRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		items := make([]service.CreateOpportunityItem, 0, len(req.Items))
		for _, it := range req.Items {
			items = append(items, service.CreateOpportunityItem{
				ProductID: it.ProductID, Qty: it.Qty, QuotedUnitPrice: it.QuotedUnitPrice,
			})
		}
		closeDate, err := parseTimeRFC3339OrEmpty(req.ExpectedCloseDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected_close_date 格式不对：" + err.Error()})
			return
		}
		opp, err := svc.CreateOpportunity(c.Request.Context(), service.CreateOpportunityInput{
			IdempotencyKey: req.IdempotencyKey, Name: req.Name, CustomerID: req.CustomerID, Items: items,
			ExpectedAmount: req.ExpectedAmount, ProbabilityOverride: req.Probability,
			ExpectedCloseDate: closeDate, Currency: req.Currency,
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toOpportunityDTO(opp))
	}
}

type updateOpportunityRequest struct {
	IdempotencyKey    string `json:"idempotency_key" binding:"required"`
	Version           int64  `json:"version"`
	Name              string `json:"name" binding:"required"`
	ExpectedAmount    string `json:"expected_amount" binding:"required"`
	ExpectedCloseDate string `json:"expected_close_date"`
	OwnerID           string `json:"owner_id"`
}

func updateOpportunityHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateOpportunityRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		closeDate, err := parseTimeRFC3339OrEmpty(req.ExpectedCloseDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected_close_date 格式不对：" + err.Error()})
			return
		}
		opp, err := svc.UpdateOpportunity(c.Request.Context(), repo.UpdateOpportunityInput{
			IdempotencyKey: req.IdempotencyKey, ID: c.Param("id"), Version: req.Version,
			Name: req.Name, ExpectedAmount: req.ExpectedAmount, ExpectedCloseDate: closeDate, OwnerID: req.OwnerID,
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toOpportunityDTO(opp))
	}
}

type changeStageRequest struct {
	IdempotencyKey      string `json:"idempotency_key" binding:"required"`
	ToStageID           string `json:"to_stage_id" binding:"required"`
	ProbabilityOverride *int   `json:"probability_override"`
}

func changeStageHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req changeStageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		opp, err := svc.ChangeStage(c.Request.Context(), repo.ChangeStageInput{
			IdempotencyKey: req.IdempotencyKey, ID: c.Param("id"), ToStageID: req.ToStageID,
			ProbabilityOverride: req.ProbabilityOverride,
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toOpportunityDTO(opp))
	}
}

type idempotentRequest struct {
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
}

func markWonHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req idempotentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		opp, err := svc.MarkWon(c.Request.Context(), c.Param("id"), req.IdempotencyKey)
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toOpportunityDTO(opp))
	}
}

type markLostRequest struct {
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	Reason         string `json:"reason" binding:"required"`
}

func markLostHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req markLostRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		opp, err := svc.MarkLost(c.Request.Context(), c.Param("id"), req.IdempotencyKey, req.Reason)
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toOpportunityDTO(opp))
	}
}

func getOpportunityHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		opp, err := svc.GetOpportunity(c.Request.Context(), c.Param("id"))
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toOpportunityDTO(opp))
	}
}

// listOpportunitiesHandler：?view=mine|dept 决定用 owner 维还是 org 维
// （见 repo.ListInput 同名字段注释）。省略时默认 dept——"本部门及下级"
// 是更常见的默认视角。created_after / created_before 不传时由 SDK 注入最近
// 90 天的窗口；格式不对答 400，不悄悄退回默认窗口。
func listOpportunitiesHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		pageSize, _ := strconv.Atoi(c.Query("page_size"))
		after, err := parseTimeRFC3339OrEmpty(c.Query("created_after"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "created_after 格式不对：" + err.Error()})
			return
		}
		before, err := parseTimeRFC3339OrEmpty(c.Query("created_before"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "created_before 格式不对：" + err.Error()})
			return
		}
		out, err := svc.ListOpportunities(c.Request.Context(), repo.ListInput{
			Cursor: c.Query("cursor"), PageSize: pageSize,
			CustomerID: c.Query("customer_id"), StatusFilter: c.Query("status_filter"),
			StageIDFilter: c.Query("stage_id_filter"), ViewMine: c.Query("view") == "mine",
			CreatedAfter: after, CreatedBefore: before,
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		dtos := make([]gin.H, 0, len(out.Opportunities))
		for _, o := range out.Opportunities {
			dtos = append(dtos, toOpportunityDTO(o))
		}
		c.JSON(http.StatusOK, gin.H{"opportunities": dtos, "next_cursor": out.NextCursor})
	}
}

func listStagesHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		stages, err := svc.ListStages(c.Request.Context())
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		dtos := make([]gin.H, 0, len(stages))
		for _, s := range stages {
			dtos = append(dtos, gin.H{
				"id": s.ID, "code": s.Code, "name": s.Name,
				"sort_order": s.SortOrder, "default_probability": s.DefaultProbability, "is_final": s.IsFinal,
			})
		}
		c.JSON(http.StatusOK, gin.H{"stages": dtos})
	}
}

// stageHistoryHandler：GET /opportunities/{id}/stage-history。probability 为 null
// 的行写下时还没有记录概率。
func stageHistoryHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		entries, err := svc.GetStageHistory(c.Request.Context(), c.Param("id"))
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		history := make([]gin.H, 0, len(entries))
		for _, e := range entries {
			row := gin.H{
				"from_stage_id": e.FromStageID, "to_stage_id": e.ToStageID, "probability": nil,
				"changed_by": e.ChangedBy, "changed_at": e.ChangedAt.Format(rfc3339),
			}
			if e.Probability != nil {
				row["probability"] = *e.Probability
			}
			history = append(history, row)
		}
		c.JSON(http.StatusOK, gin.H{"history": history})
	}
}

// funnelHandler：GET /opportunities/funnel。?view=mine|dept 与列表是同一个切换
// （省略时 dept）。每个阶段占一行，按 sort_order；没有商机的阶段计 0。
func funnelHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		stages, err := svc.Funnel(c.Request.Context(), c.Query("view") == "mine")
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		dtos := make([]gin.H, 0, len(stages))
		for _, s := range stages {
			dtos = append(dtos, gin.H{
				"stage_id": s.StageID, "stage_name": s.StageName, "count": s.Count,
				"expected_amount": s.ExpectedAmount, "weighted_amount": s.WeightedAmount,
			})
		}
		c.JSON(http.StatusOK, gin.H{"stages": dtos})
	}
}
