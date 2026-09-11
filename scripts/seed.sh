#!/usr/bin/env bash
# 灌本组件自己的种子商机：真实调 REST 接口建 5 条示例商机
# （3 OPEN 分处不同阶段 + 1 WON + 1 LOST），覆盖全部商机状态（同
# mdm-customer/mdm-product 的既有判据，总纲 SOP-W-7）。
#
# ⚠️ 前置条件（本脚本自己不建，靠 Makefile 的 seed 目标链式调用）：
#   - infra-iam-casdoor 的种子身份（dev.superuser + ROPC 测试应用）
#   - infra-authz 授予 dev.superuser 的全权限键角色
#   - mdm-customer/mdm-product 各自的种子客户/产品
# 单独跑 `make -C components/crm/opportunity seed` 就会先把这四个都
# 建好，不需要装配层编排。
#
# 用法：make -C components/crm/opportunity seed
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$DIR/../../.." && pwd)"

C_GRN=$'\033[32m'; C_RED=$'\033[31m'; C_OFF=$'\033[0m'
ok()  { echo "${C_GRN}✓${C_OFF} $*"; }
die() { echo "${C_RED}✗${C_OFF} $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"; }
need curl; need python3; need docker

CASDOOR_URL="${CASDOOR_URL:-http://localhost:8000}"
IAM_URL="${IAM_URL:-http://localhost:8200}"
CRM_REST="${CRM_REST:-http://localhost:8102}"
SEED_USER="dev.superuser"
SEED_PASSWORD="DevSeed123!"
SEED_APP="local-dev-seed-app"
COOKIE_JAR="$(mktemp)"
trap 'rm -f "$COOKIE_JAR"' EXIT

curl -sf -o /dev/null "$CRM_REST/healthz" || die "crm-opportunity（$CRM_REST）连不上，先 brickkit up"

psqlx() { docker exec -i be-postgres psql -U postgres -d brickkit_db -v ON_ERROR_STOP=1 "$@"; }
# idfor <schema> <idempotency_key>：反查 mdm-customer/mdm-product 自己
# 的 command_idempotency 表，拿它们各自 make seed 落下的真实行 id——
# 交接协议同总纲 SOP-W-7，不需要对方约定输出格式。
idfor() { psqlx -tA -q -c "SET search_path TO $1; SELECT result_id FROM command_idempotency WHERE idempotency_key = '$2';"; }

# infra-authz 的 bundle 是各组件每 ~15s 轮询一次拉进内存的——Makefile
# 链式调用刚跑完 infra-authz 的 seed，这里立刻拿 JWT 来调自己的 REST
# 接口有真实的竞态窗口，等 18 秒让 bundle 刷新到最新授权（同装配层
# 编排脚本的既有判据，不是新发明）。
echo "   等 18 秒，让本组件的权限 bundle 轮询到最新授权……"
sleep 18

echo "── 换一个真实 JWT，供调自己的 REST 接口用 ──"
curl -c "$COOKIE_JAR" -s -o /dev/null -X POST "$CASDOOR_URL/api/login" \
  -H "Content-Type: application/json" \
  -d '{"application":"app-built-in","organization":"built-in","username":"admin","password":"123","autoSignin":true,"type":"login"}'
APP_JSON="$(curl -b "$COOKIE_JAR" -s "$CASDOOR_URL/api/get-application?id=admin/$SEED_APP")"
# ⚠️ 实测踩坑：Casdoor 的 get-application 响应里 signinItems 的
# customCss 字段会带字面换行符（不是转义的 \n），严格模式的 JSON 解析
# 会报 "Invalid control character"——用 strict=False 放宽，我们只要
# clientId/clientSecret 两个字段，不关心这个 UI 主题字段本身对不对。
CLIENT_ID="$(echo "$APP_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin, strict=False)["data"]["clientId"])')"
CLIENT_SECRET="$(echo "$APP_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin, strict=False)["data"]["clientSecret"])')"

ID_TOKEN="$(curl -s -X POST "$CASDOOR_URL/api/login/oauth/access_token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=password" \
  --data-urlencode "username=$SEED_USER" \
  --data-urlencode "password=$SEED_PASSWORD" \
  --data-urlencode "client_id=$CLIENT_ID" \
  --data-urlencode "client_secret=$CLIENT_SECRET" \
  --data-urlencode "scope=openid profile email" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id_token"])')"
[ -n "$ID_TOKEN" ] || die "拿不到 Casdoor id_token"

ACCESS_TOKEN="$(curl -s -X POST "$IAM_URL/api/iam/token" \
  -H "Content-Type: application/json" \
  -d "{\"casdoor_id_token\": \"$ID_TOKEN\"}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"
[ -n "$ACCESS_TOKEN" ] || die "换应用 JWT 失败"
ok "已换到真实应用 JWT"

CUST1="$(idfor mdm_customer seed-customer-1)"
CUST2="$(idfor mdm_customer seed-customer-2)"
CUST3="$(idfor mdm_customer seed-customer-3)"
CUST4="$(idfor mdm_customer seed-customer-4)"
PROD1="$(idfor mdm_product seed-product-1)"
PROD2="$(idfor mdm_product seed-product-2)"
PROD3="$(idfor mdm_product seed-product-3)"
PROD4="$(idfor mdm_product seed-product-4)"
[ -n "$CUST1" ] || die "拿不到 mdm-customer 的种子客户 id——先确认 make -C ../../mdm/customer seed 跑成功了"
[ -n "$PROD1" ] || die "拿不到 mdm-product 的种子产品 id——先确认 make -C ../../mdm/product seed 跑成功了"

echo "── 真实调 REST 接口建 5 条示例商机（3 OPEN + 1 WON + 1 LOST）──"
mkopportunity() {
  local key="$1" name="$2" cust="$3" prod="$4" amount="$5"
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities" \
    -H "Authorization: Bearer $ACCESS_TOKEN" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$key\",\"name\":\"$name\",\"customer_id\":\"$cust\",\"items\":[{\"product_id\":\"$prod\",\"qty\":\"10\",\"quoted_unit_price\":\"100.00\"}],\"expected_amount\":\"$amount\"}" \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'
}
stage_opportunity() {
  local id="$1" key="$2" stage="$3"
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$id/stage" \
    -H "Authorization: Bearer $ACCESS_TOKEN" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$key\",\"to_stage_id\":\"$stage\"}" >/dev/null
}

OPP1="$(mkopportunity seed-opp-1 "「本地测试」华南电子·Q1 采购意向" "$CUST1" "$PROD1" "5000.00")"
OPP2="$(mkopportunity seed-opp-2 "「本地测试」京城机械·设备更新项目" "$CUST2" "$PROD2" "12000.00")"
stage_opportunity "$OPP2" seed-opp-2-stage 3
OPP3="$(mkopportunity seed-opp-3 "「本地测试」江南纺织·年度框架合同" "$CUST3" "$PROD3" "35000.00")"
stage_opportunity "$OPP3" seed-opp-3-stage 4
OPP4="$(mkopportunity seed-opp-4 "「本地测试」西部矿业·已成交项目" "$CUST4" "$PROD4" "88000.00")"
curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$OPP4/win" \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H "Content-Type: application/json" \
  -d '{"idempotency_key":"seed-opp-4-win"}' >/dev/null
# ⚠️ 第 5 个客户/产品在各自组件的 seed 里是 DISABLED 样例（覆盖状态
# 完整度用的），CreateOpportunity 会拒绝非 ACTIVE 的客户/产品
# （service.go 真实校验），所以这里改回用 CUST1/PROD2——纯粹是拿一对
# 已知有效的组合，跟这条商机本身讲的是哪家客户没有关系。
OPP5="$(mkopportunity seed-opp-5 "「本地测试」滨海物流·已流失项目" "$CUST1" "$PROD2" "15000.00")"
curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$OPP5/lose" \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H "Content-Type: application/json" \
  -d '{"idempotency_key":"seed-opp-5-lose","reason":"「本地测试」价格谈不拢"}' >/dev/null
ok "商机：$OPP1(OPEN) $OPP2(PROPOSAL) $OPP3(NEGOTIATION) $OPP4(WON→已自动转订单) $OPP5(LOST)"
echo "   （$OPP4 赢单后 erp-sales 会真的自动建一张订单——去 erp_sales.sales_orders 表或前端订单列表看得到）"
