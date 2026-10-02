#!/usr/bin/env bash
# 种子脚本用：一个组件在本项目里配的 PG_SCHEMA。schema 名只从项目配置来，
# 脚本里不写死（同组件代码与迁移）。用 `source` 引入，调用方先算好 ROOT。
#
#   schema_of <scope>/<name>
#     config/<scope>-<name>.yaml 的 PG_SCHEMA；值是 $var:NAME 时取 config/vars.yaml 的
#     NAME；没写这个键就用组件 component.yaml 里 configSchema 的默认值。拿不到就失败。
schema_of() {
  python3 - "$ROOT" "$1" <<'PY'
import os, sys, yaml
root, cid = sys.argv[1], sys.argv[2]
def load(p):
    try:
        with open(p) as f:
            return yaml.safe_load(f) or {}
    except FileNotFoundError:
        return {}
v = load(f"{root}/config/{cid.replace('/', '-')}.yaml").get("PG_SCHEMA")
if isinstance(v, str) and v.startswith("$var:"):
    v = load(f"{root}/config/vars.yaml").get(v[len("$var:"):])
if not v:
    props = (load(f"{root}/components/{cid}/component.yaml").get("configSchema") or {}).get("properties") or {}
    v = (props.get("PG_SCHEMA") or {}).get("default")
if not v:
    sys.exit(f"✗ 拿不到 {cid} 的 PG_SCHEMA（config/{cid.replace('/', '-')}.yaml 没写，组件也没有默认值）")
print(v)
PY
}
