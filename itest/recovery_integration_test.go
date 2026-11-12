//go:build integration

package itest

import (
	"strconv"

	core "github.com/brickKit/crm-opportunity/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/recoverypb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// recoveryChecks: a full recovery asks this member, as the recovery tool only, whether the opportunities its audit
// names are still there at their versions (be-protocol chapter 14, P14.3; docs/design.md, "整套恢复之后").
func (fx *fixture) recoveryChecks() {
	t := fx.t
	dial := func(as string) *grpc.ClientConn {
		conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(fx.w.TLS(t, as).ClientConfig(core.ID))))
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	_, err := recoverypb.NewCheckServiceClient(dial(core.Partner)).Check(fx.ctx, &recoverypb.CheckRequest{Recovery: "r"})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "the recovery tool only: %v", err)
	cs := recoverypb.NewCheckServiceClient(dial("ops/recover"))
	rows, err := fx.admin.Query(fx.ctx, "SELECT DISTINCT ON (target->>'id') target->>'id', target->>'version' FROM "+
		itSchema+".be_audit_log WHERE target->>'type' = $1 ORDER BY target->>'id', ord DESC", core.Opportunities)
	require.NoError(t, err)
	var refs []*recoverypb.ObjectRef
	for rows.Next() {
		var id, version string
		require.NoError(t, rows.Scan(&id, &version))
		refs = append(refs, &recoverypb.ObjectRef{Type: core.Opportunities, Id: id, Version: version})
	}
	rows.Close()
	require.NotEmpty(t, refs, "opportunities are audited")
	present, err := cs.Present(fx.ctx, &recoverypb.PresentRequest{Recovery: "r", Objects: refs})
	require.NoError(t, err)
	for i, a := range present.GetAnswers() {
		assert.Equal(t, "present", a.GetAnswer(), refs[i].GetId())
	}
	v, err := strconv.ParseInt(refs[0].GetVersion(), 10, 64)
	require.NoError(t, err, "the audit names the version")
	present, err = cs.Present(fx.ctx, &recoverypb.PresentRequest{Recovery: "r", Objects: []*recoverypb.ObjectRef{
		{Type: core.Opportunities, Id: refs[0].GetId(), Version: strconv.FormatInt(v+1000, 10)},
		{Type: core.Opportunities, Id: ids.New().String(), Version: "1"}}})
	require.NoError(t, err)
	assert.Equal(t, "older", present.GetAnswers()[0].GetAnswer())
	assert.Equal(t, "absent", present.GetAnswers()[1].GetAnswer())
	res, err := cs.Check(fx.ctx, &recoverypb.CheckRequest{Recovery: "r"})
	require.NoError(t, err)
	assert.NotEmpty(t, res.GetResults(), "the SDK's own checks run")
}
