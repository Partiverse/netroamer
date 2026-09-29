// netroamerd 增强脚本（Clash Verge Rev Script 增强项）
// 职责：把 netroamerd 自有直连 provider 挂到内核规则最前。
//
// 为什么必须用 Script 而不是 Merge：Verge 的 Merge 增强只认自己支持的键，
// 写 `prepend-rules` 会被原样透传给 mihomo —— 配置里有这行，但内核 rules
// 列表里没有（实测）。规则列表只能由 Script 增强（或用户手动规则 prepend）
// 注入。
//
// 幂等：重复执行不产生重复行；未改动任何用户规则。
// 卸载：删除本脚本（或其中 netroamerd 段）即完全回滚。
// 前置：Merge 中已声明 netroamer-autodirect provider（path 为相对路径，
// 须在内核 -d 目录内，见 rules-merge.yaml 指纹段）。

function main(config, profileName) {
  const PROVIDER = "netroamer-autodirect";
  const RULE = "RULE-SET," + PROVIDER + ",DIRECT";

  // 1) 兜底声明 provider（Merge 已配则跳过）
  config["rule-providers"] = config["rule-providers"] || {};
  if (!config["rule-providers"][PROVIDER]) {
    config["rule-providers"][PROVIDER] = {
      type: "file",
      behavior: "domain",
      interval: 300,
      path: "netroamer/autodirect.yaml",
    };
  }

  // 2) 挂载行置于 rules 最前（先于用户任何代理类规则）
  config.rules = config.rules || [];
  if (!config.rules.includes(RULE)) {
    config.rules.unshift(RULE);
  }

  return config;
}
