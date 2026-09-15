#!/usr/bin/env bash
# 灌本组件自己的种子商机（总纲 SOP-W-7，本次重做加量 + 加时间跨度 +
# 加 org 维数据权限真实样本）：
#
# ① seed-opp-1..5（原样保留，下游/历史脚本可能反查过，只增不改）：真实
#    调 REST 接口建 5 条示例商机（3 OPEN 分处不同阶段 + 1 WON + 1
#    LOST），全部用 dev.superuser 的身份建。
# ② seed-opp-6..10（本次新增）：改用 dev.sales.east（华东分部，
#    dev_sales_rep 角色）的身份建，覆盖同样的状态谱系（OPEN 三个不同
#    阶段 + 1 WON + 1 LOST）——dept_id/dept_path/owner_id 是创建时从
#    调用者的 besdk.ScopeOf(ctx) 快照的（service.go 明文），必须真的用
#    这个身份的 JWT 去调接口，不能建完再补写归属字段。这是"华东销售
#    看不到别的部门商机"这条数据权限边界第一次有真实商机样本可以演示，
#    不只是纯自动化测试里才存在。
#
# ⚠️ seed-opp-9 是本次新增的第二个 WON 商机——赢单会再真实触发一次
# erp-sales 自动建单确认（同 seed-opp-4 的既有效果），这次归属是
# dev.sales.east/华东，用来验证"赢单转订单，归属跟着走"这条判据不只在
# dev.superuser 名下成立。
#
# ⚠️ seed-opp-11（本次新增，第 11 条）：唯一一条刻意会赢单失败的商机——
# 用信用额度只有 ¥5000 的客户下一张远超额度的单，erp-sales 真实拒绝
# 确认，真实建一条 infra-workflow 异常待办。这是给 infra-workflow"不用
# 自己造假种子数据、靠真实业务失败路径就有真实待办可看"的样板，见下方
# 该商机定义处的详细注释。
#
# ⚠️ 前置条件（本脚本自己不建，靠 Makefile 的 seed 目标链式调用）：
#   - infra-iam-casdoor 的种子身份（dev.superuser + dev.sales.east +
#     ROPC 测试应用）
#   - infra-authz 授予 dev.superuser 全权限键 + dev.sales.east 归华东
#     分部 + dev_sales_rep 角色（含 crm.opportunity.view/edit/win）
#   - mdm-customer/mdm-product 各自的种子客户/产品
# 单独跑 `make -C components/crm/opportunity seed` 就会把这些都建好，
# 不需要装配层编排。
#
# 用法：make -C components/crm/opportunity seed
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$DIR/../../.." && pwd)"

C_GRN=$'\033[32m'; C_RED=$'\033[31m'; C_OFF=$'\033[0m'
ok()  { echo "${C_GRN}✓${C_OFF} $*"; }
die() { echo "${C_RED}✗${C_OFF} $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"; }
need python3; need docker

# ⚠️ 真机踩到的坑（阶段四附加 Task 0.5）：brickKit 的 servedBy 合并
# 部署下，本组件/infra-iam-casdoor 都可能被收编进某个外壳，没有独立
# 容器、也没有发布到宿主机的端口（Casdoor 本身是带外容器，不受影响，
# 仍然走宿主机映射端口）——这条脚本因此不再直接从宿主机 curl，改成起
# 一个一次性"工具箱"容器加入 brickkit 自己的 docker 网络，全部 curl
# 改在里面跑；目标地址按 brickKit 自己给依赖方注入 *_ENDPOINT 时用的
# 同一条转换规则拼（componentId+version 转小写、"/"和"."全部替换成
# "-"——brickKit 源码 internal/manifest/servicename.go 的
# ServiceName()，已向 brickKit 确认这条规则不区分部署形态）。
component_version() {
  awk -v id="$1" '$0 ~ "^  - id: "id"$"{f=1;next} f&&/^    version:/{print $2;exit}' "$ROOT/brickkit.yaml"
}
service_name() { echo "$1-$(component_version "$1")" | tr '[:upper:]' '[:lower:]' | tr '/.' '--'; }

NET="${BRICKKIT_NET:-brickkit-$(basename "$ROOT")-net}"
docker network inspect "$NET" >/dev/null 2>&1 || die "docker 网络 $NET 不存在——先把本组件 brickkit up 起来（整套或只装这一个，servedBy 合并部署也可以）"

TOOLBOX="seed-toolbox-$$"
# ⚠️ 真机踩到的坑：--user 必须跟宿主机当前用户一致——COOKIE_JAR 是
# host 侧 mktemp 建出来的（属主是宿主机用户，权限 0600），curlimages/curl
# 镜像默认用镜像自带的非 root 用户跑，不加 --user 的话容器内的 curl
# 连自己的 cookie jar 都没权限读写。
docker run -d --rm --name "$TOOLBOX" --network "$NET" \
  --user "$(id -u):$(id -g)" --add-host host.docker.internal:host-gateway -v /tmp:/tmp \
  curlimages/curl:latest sleep 3600 >/dev/null
curl() { docker exec -i "$TOOLBOX" curl "$@"; }

CASDOOR_URL="${CASDOOR_URL:-http://host.docker.internal:8000}"
IAM_URL="${IAM_URL:-http://$(service_name infra/iam-casdoor):8200}"
CRM_REST="${CRM_REST:-http://$(service_name crm/opportunity):8102}"
SEED_PASSWORD="DevSeed123!"
SEED_APP="local-dev-seed-app"
COOKIE_JAR="$(mktemp)"
trap 'rm -f "$COOKIE_JAR"; docker rm -f "$TOOLBOX" >/dev/null 2>&1' EXIT

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

# get_jwt <username> -> access token：两个测试身份（dev.superuser/
# dev.sales.east）共用同一个 ROPC 应用 + 同一个密码（infra-iam-casdoor
# 的既有约定），只有用户名不同，抽成函数避免整段登录逻辑抄两遍。
get_jwt() {
  local username="$1"
  local id_token access_token
  id_token="$(curl -s -X POST "$CASDOOR_URL/api/login/oauth/access_token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "grant_type=password" \
    --data-urlencode "username=$username" \
    --data-urlencode "password=$SEED_PASSWORD" \
    --data-urlencode "client_id=$CLIENT_ID" \
    --data-urlencode "client_secret=$CLIENT_SECRET" \
    --data-urlencode "scope=openid profile email" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id_token"])')"
  [ -n "$id_token" ] || die "拿不到 $username 的 Casdoor id_token"
  access_token="$(curl -s -X POST "$IAM_URL/api/iam/token" \
    -H "Content-Type: application/json" \
    -d "{\"casdoor_id_token\": \"$id_token\"}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"
  [ -n "$access_token" ] || die "$username 换应用 JWT 失败"
  echo "$access_token"
}

echo "── 换两个真实 JWT：dev.superuser（建 1-5）+ dev.sales.east（建 6-10，体现 org 维数据权限）──"
SUPERUSER_TOKEN="$(get_jwt dev.superuser)"
SALES_TOKEN="$(get_jwt dev.sales.east)"
ok "已换到真实应用 JWT（两个身份）"

CUST1="$(idfor mdm_customer seed-customer-1)"
CUST2="$(idfor mdm_customer seed-customer-2)"
CUST3="$(idfor mdm_customer seed-customer-3)"
CUST4="$(idfor mdm_customer seed-customer-4)"
CUST6="$(idfor mdm_customer seed-customer-6)"
CUST7="$(idfor mdm_customer seed-customer-7)"
CUST8="$(idfor mdm_customer seed-customer-8)"
CUST9="$(idfor mdm_customer seed-customer-9)"
PROD1="$(idfor mdm_product seed-product-1)"
PROD2="$(idfor mdm_product seed-product-2)"
PROD3="$(idfor mdm_product seed-product-3)"
PROD4="$(idfor mdm_product seed-product-4)"
PROD6="$(idfor mdm_product seed-product-6)"
PROD7="$(idfor mdm_product seed-product-7)"
PROD8="$(idfor mdm_product seed-product-8)"
PROD9="$(idfor mdm_product seed-product-9)"
PROD10="$(idfor mdm_product seed-product-10)"
[ -n "$CUST1" ] && [ -n "$CUST6" ] || die "拿不到 mdm-customer 的种子客户 id——先确认 make -C ../../mdm/customer seed 跑成功了（要 1-9 号，含本次新增的 6-9）"
[ -n "$PROD1" ] && [ -n "$PROD6" ] || die "拿不到 mdm-product 的种子产品 id——先确认 make -C ../../mdm/product seed 跑成功了（要 1-10 号，含本次新增的 6-10）"

mkopportunity() { # token key name cust prod qty price amount -> id
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$2\",\"name\":\"$3\",\"customer_id\":\"$4\",\"items\":[{\"product_id\":\"$5\",\"qty\":\"$6\",\"quoted_unit_price\":\"$7\"}],\"expected_amount\":\"$8\"}" \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'
}
stage_opportunity() { # token id key stage
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$2/stage" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$3\",\"to_stage_id\":\"$4\"}" >/dev/null
}
win_opportunity() { # token id key
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$2/win" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$3\"}" >/dev/null
}
lose_opportunity() { # token id key reason
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$2/lose" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$3\",\"reason\":\"$4\"}" >/dev/null
}

echo "── ① dev.superuser：5 条示例商机（3 OPEN + 1 WON + 1 LOST，原样保留）──"
OPP1="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-1 "「本地测试」华南电子·Q1 采购意向" "$CUST1" "$PROD1" 10 100.00 "5000.00")"
OPP2="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-2 "「本地测试」京城机械·设备更新项目" "$CUST2" "$PROD2" 10 100.00 "12000.00")"
stage_opportunity "$SUPERUSER_TOKEN" "$OPP2" seed-opp-2-stage 3
OPP3="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-3 "「本地测试」江南纺织·年度框架合同" "$CUST3" "$PROD3" 10 100.00 "35000.00")"
stage_opportunity "$SUPERUSER_TOKEN" "$OPP3" seed-opp-3-stage 4
OPP4="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-4 "「本地测试」西部矿业·已成交项目" "$CUST4" "$PROD4" 10 100.00 "88000.00")"
win_opportunity "$SUPERUSER_TOKEN" "$OPP4" seed-opp-4-win
# ⚠️ 第 5 个客户/产品在各自组件的 seed 里是 DISABLED 样例（覆盖状态
# 完整度用的），CreateOpportunity 会拒绝非 ACTIVE 的客户/产品
# （service.go 真实校验），所以这里改回用 CUST1/PROD2——纯粹是拿一对
# 已知有效的组合，跟这条商机本身讲的是哪家客户没有关系。
OPP5="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-5 "「本地测试」滨海物流·已流失项目" "$CUST1" "$PROD2" 10 100.00 "15000.00")"
lose_opportunity "$SUPERUSER_TOKEN" "$OPP5" seed-opp-5-lose "「本地测试」价格谈不拢"
ok "商机（dev.superuser）：$OPP1(OPEN) $OPP2(PROPOSAL) $OPP3(NEGOTIATION) $OPP4(WON→已自动转订单) $OPP5(LOST)"

echo "── ② dev.sales.east（华东分部）：5 条新增商机，同样覆盖 OPEN 三阶段/WON/LOST ──"
OPP6="$(mkopportunity "$SALES_TOKEN" seed-opp-6 "「本地测试」华东·都市零售连锁超市·补货意向" "$CUST6" "$PROD6" 50 0.20 "3000.00")"
OPP7="$(mkopportunity "$SALES_TOKEN" seed-opp-7 "「本地测试」华东·云帆软件科技·减速机采购方案" "$CUST7" "$PROD9" 8 1580.00 "120000.00")"
stage_opportunity "$SALES_TOKEN" "$OPP7" seed-opp-7-stage 3
OPP8="$(mkopportunity "$SALES_TOKEN" seed-opp-8 "「本地测试」华东·恒基建筑工程·变频器批量采购谈判" "$CUST9" "$PROD10" 12 2200.00 "250000.00")"
stage_opportunity "$SALES_TOKEN" "$OPP8" seed-opp-8-stage 4
OPP9="$(mkopportunity "$SALES_TOKEN" seed-opp-9 "「本地测试」华东·云帆软件科技·防锈涂料已成交项目" "$CUST7" "$PROD7" 15 68.00 "45000.00")"
win_opportunity "$SALES_TOKEN" "$OPP9" seed-opp-9-win
OPP10="$(mkopportunity "$SALES_TOKEN" seed-opp-10 "「本地测试」华东·鲜达食品饮料·已流失项目" "$CUST8" "$PROD8" 20 15.50 "8000.00")"
lose_opportunity "$SALES_TOKEN" "$OPP10" seed-opp-10-lose "「本地测试」华东：客户资金紧张，暂缓采购"
ok "商机（dev.sales.east/华东）：$OPP6(OPEN) $OPP7(PROPOSAL) $OPP8(NEGOTIATION) $OPP9(WON→已自动转订单) $OPP10(LOST)"
echo "   （$OPP4/$OPP9 赢单后 erp-sales 会真的各自动建一张订单，归属分别是 dev.superuser/无部门 与 dev.sales.east/华东——去 erp_sales.sales_orders 表或前端订单列表看得到）"

# ⚠️ seed-opp-11：本组件唯一一条刻意设计成"赢单会真的失败"的商机——
# infra-workflow 的 CreateTask/CloseTask/CancelTask 明确不暴露 REST
# （contracts/infra/workflow/v1/workflow.proto 顶部注释："人能创建待办
# 只有一种可能'人代表某个业务组件创建'，那等于给了一条绕过业务规则的
# 路"）。种子脚本因此不能也不该直接 grpcurl 伪造一条 CreateTask 命令去
# 给 infra-workflow"造数据"——那正是这条设计禁令想挡住的事。真正诚实
# 的做法是让一条真实业务流程真的失败，产生一条真实的异常待办。
# CUST8（「本地测试」鲜达食品饮料，见 mdm-customer 自己的 seed 注释）
# 信用额度只有 ¥5000，专门留着给这个场景用；这里用大额产品把订单金额
# 做到远超额度，赢单后 erp-sales 的信用校验会真的拒绝确认（同
# AGENTS.md 记载的 Task 14 信用超限 Saga 补偿场景），createOpportunity
# ExceptionTask 会真的建一条 infra-workflow 待办，assignee 取事件里的
# owner_id/dept_path——正好也是"华东"，让 infra-workflow 不需要独立
# make seed 也有一条真实的、org/owner 维归属正确的异常待办可看。
#
# 实测踩坑：qty 太小凑不出超额度的真实订单——erp-sales 的定价引擎
# （backend/internal/repo/pricing.go）不认商机行的 quoted_unit_price，
# 是真的查 pricelist_items 按 PRODUCT>CATEGORY>GLOBAL 规则重算，种子
# 产品都没有专属定价规则，一律落到 GLOBAL 规则的固定单价，跟这里传的
# 报价快照数字无关。qty 要大到"qty × 那个固定单价"真的超过 5000 额度
# 才行，不能照抄别的商机的 qty。
OPP11="$(mkopportunity "$SALES_TOKEN" seed-opp-11 "「本地测试」华东·鲜达食品饮料·超额度已成交项目" "$CUST8" "$PROD10" 100 2200.00 "22000.00")"
win_opportunity "$SALES_TOKEN" "$OPP11" seed-opp-11-win
ok "商机（dev.sales.east/华东）：$OPP11(WON→信用超限，erp-sales 真实拒绝确认，infra-workflow 真的多一条异常待办)"

# ── 时间跨度回填：seed-opp-6..10 是本次新增，回填成"最近 4 周内逐步
# 推进"的样子，跟 1-5（既有契约，不动）拉开新旧对比。opportunities 表
# 不分区（AGENTS.md 既有判据），直接 UPDATE 安全。用 now() - interval
# 写绝对值，不是 created_at - interval——重跑脚本不会越跑越早（同
# mdm-customer/mdm-product/erp-finance 已验证过的既有判据）。
psqlx -q <<SQL
SET search_path TO crm_opportunity;
UPDATE opportunities SET created_at = now() - interval '25 days', updated_at = now() - interval '25 days' WHERE id = $OPP6;
UPDATE opportunities SET created_at = now() - interval '18 days', updated_at = now() - interval '18 days' WHERE id = $OPP7;
UPDATE opportunities SET created_at = now() - interval '10 days', updated_at = now() - interval '10 days' WHERE id = $OPP8;
UPDATE opportunities SET created_at = now() - interval '5 days'  WHERE id = $OPP9;
UPDATE opportunities SET created_at = now() - interval '2 days'  WHERE id = $OPP10;
SQL
ok "已给 5 条新增商机回填历史 created_at（最近 2-25 天内，逐步推进的时间线）"
