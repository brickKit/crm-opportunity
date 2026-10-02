ID      := crm/opportunity
REPO    := crm-opportunity
VERSION := $(shell grep -m1 -E '^  version:' component.yaml | awk '{print $$2}')
MAJOR   := $(firstword $(subst ., ,$(VERSION)))
# 项目根：本组件在项目里固定挂在 components/<scope>/<name>/ 下
ROOT    := ../../..

.DEFAULT_GOAL := help
.PHONY: help all check-version test migrate-idempotent dag-check contract-check import-scan module-check docs-check smoke image seed seed-clean

help:  ## 列出所有目标
	@awk 'BEGIN{FS=":.*##"; printf "\n用法: make <目标>\n\n"} \
	     /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} \
	     /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0,5)}' $(MAKEFILE_LIST)
	@echo ""

##@ 汇总
all: check-version test migrate-idempotent dag-check contract-check import-scan module-check docs-check  ## 全部门禁（不含要起容器的 smoke / image）

##@ 门禁
check-version:  ## metadata.version、go.mod 的主版本、HEAD 上的 tag 三者一致
	@test -n "$(VERSION)" || { echo "✗ component.yaml 里读不到 metadata.version"; exit 1; }
	@mod="$$(head -1 go.mod | awk '{print $$2}')"; \
	 if [ "$(MAJOR)" -ge 2 ] && [ "$${mod##*/}" != "v$(MAJOR)" ]; then \
	   echo "✗ version $(VERSION) 的主版本是 $(MAJOR)，go.mod 的模块路径应以 /v$(MAJOR) 结尾（实际 $$mod）"; exit 1; fi
	@tags="$$(git tag --points-at HEAD | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$$' || true)"; \
	 if [ -n "$$tags" ]; then \
	   for want in "$(VERSION)" "v$(VERSION)"; do \
	     echo "$$tags" | grep -qx "$$want" || { echo "✗ HEAD 有版本 tag（$$(echo $$tags)），但缺 $$want：Go 组件要 $(VERSION) 与 v$(VERSION) 两个 tag 在同一提交"; exit 1; }; \
	   done; \
	   extra="$$(echo "$$tags" | grep -vx -e "$(VERSION)" -e "v$(VERSION)" || true)"; \
	   [ -z "$$extra" ] || { echo "✗ HEAD 上的版本 tag $$extra 与 component.yaml 的 $(VERSION) 不一致"; exit 1; }; \
	 fi
	@echo "✓ version=$(VERSION)（go.mod 主版本一致；HEAD 上没有 tag，或 $(VERSION) 与 v$(VERSION) 都在）"

test:  ## L1–L3 测试。需要 TEST_PG_DSN（指向 brickkit_test_db）；TEST_NATS_URL 可选
	@# 连库测试在没有 TEST_PG_DSN 时 t.Skip，go test 照样打印 ok、退出 0——全部跳过却
	@# 显示通过。所以这里先拦住，大声失败。
	@test -n "$$TEST_PG_DSN" || { echo "✗ 没设 TEST_PG_DSN：连库测试会全部 SKIP 而显示 ok（设法见 AGENTS.md 的 Build and test）"; exit 1; }
	go test ./... -race -count=1

migrate-idempotent:  ## 同一份迁移连跑两次都成功。需要 PG_HOST PG_PORT PG_DATABASE PG_USER PG_PASSWORD PG_SCHEMA（以 crm_opportunity_rw 登录）
	@# 例：PG_HOST=localhost PG_PORT=5432 PG_DATABASE=brickkit_test_db PG_USER=crm_opportunity_rw
	@#     PG_PASSWORD=… PG_SCHEMA=crm_opportunity make migrate-idempotent
	@mkdir -p build && go build -o build/migrate-probe ./backend/cmd/migrate
	@build/migrate-probe up && build/migrate-probe up && echo "✓ 迁移幂等"

dag-check:  ## 依赖恰好是 mdm/customer 与 mdm/product，都钉 @2.0.0；不依赖 erp/* 与 infra/workflow
	@# CRM 与 ERP 之间只有事件：赢单发事件，订单号由订单事件回填，从不同步调用对方。
	@# 一条同步边意味着 erp/sales 不在时商机就建不了、赢不了。
	@python3 -c 'import sys, yaml; \
	  deps = (yaml.safe_load(open("component.yaml")).get("dependencies") or {}).get("components") or []; \
	  ids = [d.split("@")[0] for d in deps]; want = {"mdm/customer", "mdm/product"}; \
	  bad = [d for d in deps if "@" not in d or d.split("@")[1] != "2.0.0"]; \
	  sync = [i for i in ids if i.startswith("erp/") or i == "infra/workflow"]; \
	  msg = (sync and f"不许同步依赖 {sync}：CRM 与 ERP 之间只有事件") or \
	        (set(ids) != want and f"依赖应恰好是 {sorted(want)}，实际 {ids}") or \
	        (bad and f"依赖必须钉 @2.0.0：{bad}") or \
	        (len(ids) != len(set(ids)) and f"依赖重复：{ids}"); \
	  sys.exit(f"✗ {msg}") if msg else print("✓ 依赖 mdm/customer@2.0.0、mdm/product@2.0.0，无 ERP 同步边，无自环")'

contract-check:  ## proto 只增不改（buf lint + buf breaking，对比上一个发布 tag；还没有发布过就对比 main）
	@# 对比 main 抓不住已经提交到 main 上的破坏性改动（在 main 上工作时等于自己比自己），
	@# 所以对比上一个组件版本 tag（契约包的 gen/* tag 不算）。
	buf lint
	@base="$$(git describe --tags --abbrev=0 --exclude 'gen/*' 2>/dev/null)"; \
	 against="$${base:+.git#tag=$$base}"; against="$${against:-.git#branch=main}"; \
	 echo "buf breaking --against '$$against'"; buf breaking --against "$$against"

import-scan:  ## 只 import SDK 与自己；别的组件只能用它发布的 gen/ 契约包
	@# go list 失败（模块解析不了、编译错误）时必须失败：先单独取输出、查退出码，
	@# 再过滤。管道里直接接 grep 会吞掉 go list 的退出码，什么都没扫到也打印 ✓。
	@deps="$$(go list -deps ./...)" || { echo "✗ go list -deps ./... 失败，没法扫 import"; exit 1; }; \
	 bad="$$(printf '%s\n' "$$deps" | awk '/^github\.com\/brickKit\// \
	         && !/^github\.com\/brickKit\/($(REPO)\/v$(MAJOR)|be-sdk-go)(\/|$$)/ \
	         && !/^github\.com\/brickKit\/[^\/]+\/gen\//')"; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ import 了别的组件的代码："; echo "$$bad"; exit 1; fi; \
	 echo "✓ 无组件间 import"

module-check:  ## 模块能被合进外壳：入口签名对、零 os.Getenv、零进程级初始化、栈合规
	@# 只扫 backend/module 与 backend/internal（会被合进外壳的部分）；backend/cmd 是
	@# 独立运行的装配代码，不在范围内。先去掉行内 // 注释再 grep，免得解释"为什么
	@# 不许 log.Fatal"的注释把自己判成违规；_test.go 不扫（测试自己开库是测试设施）。
	@grep -qE 'func New\(ctx context\.Context, rt \*besdk\.Runtime\) \(\*besdk\.Module, error\)' \
	   backend/module/module.go || { echo "✗ module.New 的签名不对"; exit 1; }
	@bad=""; \
	 for f in $$(find backend/module backend/internal -name '*.go' ! -name '*_test.go'); do \
	   hit="$$(sed 's://.*::' "$$f" | grep -nE 'os\.Getenv|os\.LookupEnv|os\.Environ')"; \
	   [ -n "$$hit" ] && bad="$$bad$$f: $$hit\n"; \
	 done; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ 模块代码读了进程环境变量（进外壳后成员互相覆盖，不报错）："; printf '%b' "$$bad"; exit 1; fi
	@bad=""; \
	 for f in $$(find backend/module backend/internal -name '*.go' ! -name '*_test.go'); do \
	   hit="$$(sed 's://.*::' "$$f" | grep -nE 'log\.Fatal|os\.Exit|signal\.Notify|otel\.SetTracerProvider|promauto\.|prometheus\.MustRegister|gin\.New\(|gin\.Default\(|gin\.SetMode|sql\.Open|net\.Listen')"; \
	   [ -n "$$hit" ] && bad="$$bad$$f: $$hit\n"; \
	 done; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ 模块碰了进程级的东西或自己装配："; printf '%b' "$$bad"; exit 1; fi
	@deps="$$(go list -deps ./backend/module/... ./backend/internal/...)" || { echo "✗ go list -deps 失败，没法核对依赖栈"; exit 1; }; \
	 bad="$$(printf '%s\n' "$$deps" | awk '/labstack\/echo|gofiber\/fiber|go-chi\/chi|jinzhu\/gorm|gorm\.io|lib\/pq/')"; \
	 if [ -n "$$bad" ]; then \
	   echo "✗ 用了锁定栈之外的库："; echo "$$bad"; exit 1; fi
	@echo "✓ 入口签名对、零 os.Getenv、零进程级初始化、栈合规"

docs-check:  ## 清单与文档的严格 lint（= 项目根 make docs-check ID=$(ID)）
	@cd $(ROOT) && brickkit lint --strict $(ID)

##@ 要起容器的检查（在项目里跑）
smoke:  ## 项目能为本组件生成部署文件（brickkit up --dry-run）
	@cd $(ROOT) && brickkit up --dry-run >/dev/null && echo "✓ smoke：up --dry-run 通过"

image:  ## brickkit build 本组件，并确认镜像里有 sh + wget（健康检查经 /bin/sh 调 wget）
	@cd $(ROOT) && brickkit build $(ID)
	@docker run --rm --entrypoint sh $(REPO):$(VERSION) -c 'wget --version >/dev/null && test -f /app/component.yaml' \
	  && echo "✓ 镜像 $(REPO):$(VERSION) 里有 sh + wget + component.yaml"

##@ 本地开发数据（只给本地 / 演示用，不进部署与 CI）
seed:  ## 灌本组件的示例商机（幂等，可重复跑）。先链式跑身份、授权、客户、产品的 seed；要求这几个组件与本组件已经 brickkit up 起来
	@$(MAKE) -C ../../infra/iam-casdoor seed
	@$(MAKE) -C ../../infra/authz seed
	@$(MAKE) -C ../../mdm/customer seed
	@$(MAKE) -C ../../mdm/product seed
	@bash scripts/seed.sh

seed-clean:  ## 撤销本组件 seed 灌的商机（上游的数据由它们各自的 seed-clean 撤销）
	@bash scripts/seed-clean.sh
