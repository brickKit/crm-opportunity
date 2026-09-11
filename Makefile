IMAGE   := brickenterprise/crm-opportunity
VERSION := $(shell grep -E '^\s+version:' component.yaml | head -1 | awk '{print $$2}')

.DEFAULT_GOAL := help
.PHONY: help all check-version test image migrate-idempotent dag-check contract-check import-scan module-check docs-check smoke seed seed-clean

help:  ## 列出所有目标
	@awk 'BEGIN{FS=":.*##"; printf "\n用法: make <目标>\n\n"} \
	     /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} \
	     /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0,5)}' $(MAKEFILE_LIST)
	@echo ""

##@ 汇总
all: check-version test image migrate-idempotent dag-check contract-check import-scan module-check docs-check  ## 8 个门禁（不含 smoke，它要真起容器）

##@ 9 个门禁
check-version:  ## component.yaml 的 version、git tag、deployment.image 三者不许分叉（§9.1 两个真相源）
	@tag="$$(git describe --tags --exact-match 2>/dev/null || true)"; \
	 if [ -n "$$tag" ] && [ "$$tag" != "v$(VERSION)" ]; then \
	   echo "✗ git tag $$tag 与 component.yaml 的 $(VERSION) 不一致"; exit 1; fi; \
	 img_ver="$$(grep -E '^[[:space:]]+image:' component.yaml | head -1 | sed -E 's#.*:([0-9]+\.[0-9]+\.[0-9]+)[[:space:]]*$$#\1#')"; \
	 if [ "$$img_ver" != "$(VERSION)" ]; then \
	   echo "✗ deployment.image 的 tag ($$img_ver) 与 component.yaml 的 version ($(VERSION)) 不一致——镜像大概率没有跟着这次版本升级重新构建（实测踩坑记录类别 F）"; exit 1; fi; \
	 echo "✓ version=$(VERSION)（git tag 与 deployment.image 一致）"

test:  ## 需要 TEST_PG_DSN 与 TEST_NATS_URL（可选，缺省走 nats.DefaultURL）。跨组件校验测试需要 MDM_CUSTOMER_GRPC_ENDPOINT/MDM_PRODUCT_GRPC_ENDPOINT，缺省自动跳过
	go test ./... -race -count=1

image:  ## 建镜像并确认里面有 sh + wget（§12.3.7 健康检查需要）
	docker build -t $(IMAGE):$(VERSION) .
	@# 镜像里必须有 /bin/sh + wget，否则平台的 CMD-SHELL 健康检查永远失败
	@docker run --rm --entrypoint sh $(IMAGE):$(VERSION) -c 'wget --version >/dev/null' \
	  && echo "✓ 镜像里有 sh + wget"

migrate-idempotent:  ## 同一份迁移连跑两次都必须成功（§13.3 铁律五）
	@# 需要 DATABASE_HOST/PORT/USER/PASSWORD/NAME + PG_SCHEMA（平台真实注入
	@# 的分离变量契约）。本地跑迁移要用 postgres 超级用户（不是
	@# crm_opportunity_rw）：建分区之类的 DDL 需要建表权限。
	@go build -o /tmp/crm-opportunity-migrate-probe ./backend/cmd/migrate
	@/tmp/crm-opportunity-migrate-probe up && /tmp/crm-opportunity-migrate-probe up && echo "✓ 迁移幂等"

dag-check:  ## 强依赖图无环（§4.2）。两条强依赖边硬校验，明确不依赖 erp-*/infra-workflow（§1.4 铁律）
	@if grep -qE '^\s*-\s*(\{ id: )?crm/opportunity@' component.yaml; then \
	   echo "✗ 不许依赖自己"; exit 1; fi
	@for dep in mdm/customer mdm/product; do \
	   grep -qE "^\s*-\s*$$dep@" component.yaml || { echo "✗ 缺少强依赖 $$dep"; exit 1; }; \
	 done
	@if grep -qE '^\s*-\s*(\{ id: )?erp/(sales|inventory|finance)@|infra/workflow@' component.yaml; then \
	   echo "✗ CRM 与 ERP 零同步边——不许对 erp-*/infra-workflow 建依赖边（§1.4 铁律、设计计划 §5）"; exit 1; fi
	@echo "✓ 两条强依赖边齐全、无自环、无违规 ERP 同步边（跨组件整图成环检测由 brickkit up --dry-run 负责，§4.2）"

contract-check:  ## 禁破坏性变更（§8.5、决策 33）。只对本组件自己的契约较真，不含 contracts/vendor 只读镜像
	buf lint
	buf breaking --against '.git#branch=main'

import-scan:  ## 铁律六：不许 import 任何其他组件仓库（§13.3）
	@# ⚠️ contracts/vendor/ 的只读镜像 buf generate 到 gen/ 时物理落在
	@# github.com/brickKit/crm-opportunity/gen/... 下（本组件自己的模块
	@# 路径，沿用 erp-sales 立的 vendored-contract 判据）——天然匹配下面
	@# 的白名单前缀，不需要特殊排除。
	@bad="$$(go list -deps ./... 2>/dev/null | grep -E '^github.com/brickKit/' \
	         | grep -vE '^github.com/brickKit/(crm-opportunity|be-sdk-go)(/|$$)' || true)"; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ 铁律六违规，import 了其他组件仓库："; echo "$$bad"; exit 1; fi; \
	 echo "✓ 无组件间 import（含两份 vendor 镜像生成的 stub 在内）"

module-check:  ## 铁律七：模块能被合进外壳（§12.5、§13.3 铁律七）
	@# 同 erp-sales/erp-finance 既有判据：只扫 backend/module 与
	@# backend/internal，排除 _test.go；banned-libs 检查收窄到
	@# module+internal，不扫 cmd。
	@grep -qE 'func New\(ctx context\.Context, rt \*besdk\.Runtime\) \(\*besdk\.Module, error\)' \
	   backend/module/module.go || { echo "✗ module.New 签名与 §12.5.1 不一致"; exit 1; }
	@bad=""; \
	 for f in $$(find backend/module backend/internal -name '*.go' ! -name '*_test.go'); do \
	   hit="$$(sed 's://.*::' "$$f" | grep -nE 'os\.Getenv|os\.LookupEnv')"; \
	   [ -n "$$hit" ] && bad="$$bad$$f: $$hit\n"; \
	 done; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ 模块代码里读了进程环境变量（22 个模块会互相顶掉，不报错）："; \
	   printf '%b' "$$bad"; exit 1; fi
	@bad=""; \
	 for f in $$(find backend/module backend/internal -name '*.go' ! -name '*_test.go'); do \
	   hit="$$(sed 's://.*::' "$$f" | grep -nE 'log\.Fatal|os\.Exit|signal\.Notify|otel\.SetTracerProvider|promauto\.|prometheus\.MustRegister|gin\.New\(|gin\.Default\(|sql\.Open')"; \
	   [ -n "$$hit" ] && bad="$$bad$$f: $$hit\n"; \
	 done; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ 模块碰了进程级的东西或自己装配（§12.5.2）："; printf '%b' "$$bad"; exit 1; fi
	@bad="$$(go list -deps ./backend/module/... ./backend/internal/... 2>/dev/null \
	         | grep -E 'labstack/echo|gofiber/fiber|go-chi/chi|jinzhu/gorm|gorm\.io|lib/pq' || true)"; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ 用了 §12.4 禁掉的库："; echo "$$bad"; exit 1; fi
	@echo "✓ 铁律七：入口签名对、零 os.Getenv、零进程级 init、栈合规"

docs-check:  ## 四份文档结构检查（总纲 §4 SOP-D）
	@bash ../../../infra/scripts/docs-check.sh crm-opportunity

smoke:  ## 原则一：只装这一个组件就能起来（§1.5、§3.11 第 8 条）
	@# brickkit 不向上找 brickkit.yaml，必须从装配仓库根目录跑——本组件
	@# 固定挂在 components/crm/opportunity 下，根目录固定是 ../../..
	@(cd ../../.. && brickkit up --dry-run >/dev/null) && echo "✓ smoke（完整版见 make tier0）"

##@ 本地开发
seed:  ## 灌本组件自己的种子商机（幂等，可重复跑）。链式建好身份/授权/客户/产品（总纲 SOP-W-7：强依赖必须启动，链式调用它们的 seed），单独跑就能拿到完整数据
	@$(MAKE) -C ../../infra/iam-casdoor seed
	@$(MAKE) -C ../../infra/authz seed
	@$(MAKE) -C ../../mdm/customer seed
	@$(MAKE) -C ../../mdm/product seed
	@bash scripts/seed.sh

seed-clean:  ## 撤销本组件自己的种子商机（不清理强依赖/身份数据，各自 seed-clean）
	@bash scripts/seed-clean.sh
