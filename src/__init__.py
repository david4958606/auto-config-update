"""auto-update-tool 引擎包。

分层(对应 PLAN.md 的模块划分)：
  model   —— 配置解析/寻址：master+实体装配、IO 逻辑视图、片段读写
  anchor  —— class 路径定位 + 同类多实例 fan-out + where 筛选
  ops     —— 幂等补丁原语：add_node / add_method / remove_method
  feature —— 功能 YAML 加载 + require/bind 变量绑定
  engine  —— steps 编排：逐腔室、逐步、逐实例执行并落盘
  cli     —— plan / apply 命令行
"""
