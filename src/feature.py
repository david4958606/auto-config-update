"""feature —— 功能 YAML 加载 + 变量绑定(require / bind)。

feature 结构(见 features/*.yaml)：
  steps: 有序列表，逐步执行；后一步可依赖前一步产物。
  每步：anchor(class 路径) + where? + require? + bind? + 动作(add-node/add-method/remove-method)。

变量作用域：
  - {类名} 由 anchor 逐实例自动绑定(在 anchor 层完成)。
  - require: 守卫 + 绑定。目前支持 exist —— 逻辑 IO 路径必须解析得到，否则该实例跳过；
             命中则把变量绑成"解析后的逻辑路径"，供动作复用。
  - bind:    纯绑定，不做存在性要求(如"删除项"引用的路径不必仍存在)。
"""
from __future__ import annotations

from pathlib import Path

import yaml

from .model import resolve_io_logical


def load_feature(path) -> dict:
    return yaml.safe_load(Path(path).read_text(encoding="utf-8"))


class Skip(Exception):
    """require 不满足时抛出，携带人类可读原因。"""


def resolve_bindings(step: dict, tags: dict, io_index):
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
            fpath, node = resolve_io_logical(io_index, logical)
            if node is None:
                raise Skip(f"require.exist 不满足 —— 所有 IO 片段中均无 {logical}")
            tags[var] = logical
            notes.append((var, logical, fpath))
        else:
            raise Skip(f"未知 require 类型: {kind}")
    for item in step.get("bind", []) or []:
        (var, tmpl), = item.items()
        tags[var] = tmpl.format(**tags)
    return tags, notes
