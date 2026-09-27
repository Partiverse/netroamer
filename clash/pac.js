// netroamer PAC 模板 —— 粘贴进 Clash Verge「PAC 模式」设置
// 原则：直连域名在进代理之前分流（勿开 TUN）；改直连白名单时与 Clash prepend 规则、shell NO_PROXY 三处同步
function FindProxyForURL(url, host) {
  var d = host.toLowerCase();
  // 1) 本机与内网一律直连
  if (isPlainHostName(host) ||
      d === "localhost" ||
      shExpMatch(d, "127.*") ||
      shExpMatch(d, "192.168.*") ||
      shExpMatch(d, "10.*") ||
      shExpMatch(d, "172.16.*") ||
      shExpMatch(d, "172.17.*") ||
      shExpMatch(d, "172.18.*") ||
      shExpMatch(d, "172.19.*") ||
      shExpMatch(d, "172.2?.*") ||
      shExpMatch(d, "172.30.*") ||
      shExpMatch(d, "172.31.*") ||
      shExpMatch(d, "100.1*.") ||          // Tailscale CGNAT 100.x
      shExpMatch(d, "*.local") ||
      shExpMatch(d, "*.lan")) {
    return "DIRECT";
  }
  // 2) 国内与 AI 开发域名直连（ZCode 生命线）
  if (dnsDomainIs(d, ".cn") ||
      dnsDomainIs(d, "bigmodel.cn") ||     // ZCode API
      dnsDomainIs(d, "vectide.cn") ||      // ZCode API 中转
      dnsDomainIs(d, "zhipuai.cn") ||
      dnsDomainIs(d, "z.ai") ||
      dnsDomainIs(d, "hf-mirror.com") ||   // HuggingFace 镜像
      dnsDomainIs(d, "npmmirror.com")) {
    return "DIRECT";
  }
  // 3) 其余走本地混合端口（Tailscale IP 段不在此列则被送代理，需留意）
  return "PROXY 127.0.0.1:7897; SOCKS5 127.0.0.1:7897; DIRECT;";
}
