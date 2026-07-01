"""feature —— 功能 YAML 加载 + 变量绑定(require / bind)。

feature 结构(见 features/*.yaml)：
  steps: 有序列表，逐步执行；后一步可依赖前一步产物(如步2依赖步1建的 <IonGauge>)。
  每步：anchor(class 路径) + where? + require? + bind? + 动作(add-node/add-method/remove-method)。

变量作用域：
  - {类名} 由 anchor 逐实例自动绑定(anchor 层完成)。
  - add-node 建对象后，把 {标签} 绑成对象引用 "./标签"，供后续步骤引用(腔室级、跨步)。
  - require: 守卫+绑定。exist —— 逻辑路径(/IO/... 或 /Control/...)必须解析得到，否则跳过；
             命中则把变量绑成"解析后的逻辑路径"。
  - bind:    纯绑定，不做存在性要求(如"删除项"引用的路径不必仍存在)。
"""
from __future__ import annotations

from pathlib import Path

import yaml

from .model import resolve_logical


def load_feature(path) -> dict:
    return yaml.safe_load(Path(path).read_text(encoding="utf-8"))


class Skip(Exception):
    """require 不满足时抛出，携带人类可读原因。"""


def resolve_bindings(step: dict, tags: dict, indexes):
    """处理一个 step 的 require/bind，返回 (新 tags, notes)。

    notes: [(var, logical_path, fragment_path)] —— 供语义 diff 显示"命中于哪个文件"。
    require 不满足 → 抛 Skip(reason)。
    """
    tags = dict(tags)
    notes = []
    for item in step.get("require", []) or []:
        (kind, spec), = item.items()
        if kind == "exist":
            (var, tmpl), = spec.items()
            logical = tmpl.format(**tags)
            fpath, node = resolve_logical(indexes, logical)
            if node is None:
                raise Skip(f"require.exist 不满足 —— 找不到 {logical}")
            tags[var] = logical
            notes.append((var, logical, fpath))
        else:
            raise Skip(f"未知 require 类型: {kind}")
    for item in step.get("bind", []) or []:
        (var, tmpl), = item.items()
        tags[var] = tmpl.format(**tags)
    return tags, notes
