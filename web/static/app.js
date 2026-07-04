let nodes = [];
let relayMachines = [];
let rules = [];
let events = [];
let ips = [];
let certs = [];
let overviewMetrics = {
  onlineNodes: "0 / 0",
  activeConnections: 0,
  dailyTraffic: "0 B",
  realIPCaptureRate: "0 个"
};

let accountSettings = {
  username: ""
};

const statusText = {
  running: "运行中",
  warning: "告警",
  paused: "已暂停"
};

const strategyText = {
  single: "单目标直连",
  manual: "手动选择",
  fallback: "故障转移优先",
  round_robin: "轮询负载均衡",
  least_conn: "最少连接",
  ip_hash: "来源 IP Hash",
  latency: "最低延迟",
  weighted: "权重分配"
};

const strategyDetail = {
  single: "当前规则只连接配置的目标地址",
  manual: "固定使用人工选择的目标",
  fallback: "主目标异常后切到备用目标",
  round_robin: "按顺序分摊到多个目标",
  least_conn: "优先选择当前连接更少的目标",
  ip_hash: "同一来源 IP 尽量落到同一目标",
  latency: "优先选择健康检查延迟最低的目标",
  weighted: "按权重比例分配到目标组"
};

const navButtons = document.querySelectorAll("[data-view]");
const views = document.querySelectorAll(".view");
const globalSearch = document.querySelector("#globalSearch");
const ruleStatusFilter = document.querySelector("#ruleStatusFilter");
const ruleImportMode = document.querySelector("#ruleImportMode");
const ruleImportFile = document.querySelector("#ruleImportFile");
const ruleModal = document.querySelector("#ruleModal");
const settingsMessage = document.querySelector("#settingsMessage");
const ruleTable = document.querySelector("#ruleTable");
const proxyProtocolSwitch = document.querySelector("#proxyProtocolSwitch");
const commandModal = document.querySelector("#commandModal");
const commandText = document.querySelector("#commandText");
let bootstrapCommands = { panel: "", relay: "", client: "" };
let editingRuleId = "";
let proxyVersion = "v2";

function setView(viewId) {
  navButtons.forEach((button) => {
    button.classList.toggle("active", button.dataset.view === viewId);
  });
  views.forEach((view) => {
    view.classList.toggle("active", view.id === viewId);
  });
}

function statusBadge(status) {
  return `<span class="status ${status}">${statusText[status]}</span>`;
}

function strategyLabel(strategy) {
  return strategyText[strategy] || strategy || "未配置";
}

function emptyState(text) {
  return `<div class="empty-state">${text}</div>`;
}

function emptyTable(message, colspan) {
  return `<tr><td class="empty-cell" colspan="${colspan}">${message}</td></tr>`;
}

function shortName(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  return text.length > 10 ? text.slice(0, 10) : text;
}

function showToast(message, tone = "") {
  let toast = document.querySelector("#appToast");
  if (!toast) {
    toast = document.createElement("div");
    toast.id = "appToast";
    toast.className = "toast";
    document.body.appendChild(toast);
  }
  toast.textContent = message;
  toast.className = `toast open ${tone}`.trim();
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => toast.classList.remove("open"), 2600);
}

function renderEvents() {
  const eventList = document.querySelector("#eventList");
  if (!events.length) {
    eventList.innerHTML = emptyState("暂无事件");
    return;
  }
  eventList.innerHTML = events
    .map(
      (event) => `
        <div class="event-item">
          <span class="event-dot ${event.tone === "warn" ? "warn" : event.tone === "info" ? "info" : ""}"></span>
          <div>
            <strong>${event.title}</strong>
            <span>${event.text}</span>
          </div>
          <span class="event-time">${event.time}</span>
        </div>
      `
    )
    .join("");
}

function renderMetrics() {
  document.querySelector("#metricOnlineNodes").textContent = overviewMetrics.onlineNodes;
  document.querySelector("#metricActiveConnections").textContent = Number(overviewMetrics.activeConnections || 0).toLocaleString();
  document.querySelector("#metricDailyTraffic").textContent = overviewMetrics.dailyTraffic || "0 B";
  document.querySelector("#metricRealIPs").textContent = overviewMetrics.realIPCaptureRate || "0 个";
  const totalNodes = [...relayMachines, ...nodes].length;
  const onlineNodes = [...relayMachines, ...nodes].filter((node) => node.status === "running").length;
  document.querySelector("#metricOnlineNodesHint").textContent = totalNodes ? `${onlineNodes} 台在线，${totalNodes - onlineNodes} 台非运行` : "暂无 agent 接入";
}

function renderStrategySummary() {
  const runningRules = rules.filter((rule) => rule.status === "running");
  const strategyCounts = runningRules.reduce((acc, rule) => {
    acc[rule.strategy] = (acc[rule.strategy] || 0) + 1;
    return acc;
  }, {});
  const topStrategy = Object.entries(strategyCounts).sort((a, b) => b[1] - a[1])[0];
  const name = topStrategy ? strategyLabel(topStrategy[0]) : "未配置";
  document.querySelector("#currentStrategyName").textContent = name;
  document.querySelector("#currentStrategyDetail").textContent = topStrategy
    ? `${topStrategy[1]} 条运行规则，${strategyDetail[topStrategy[0]] || "按规则配置执行"}`
    : "暂无运行规则";
}

function renderTopology() {
  const graph = document.querySelector("#topologyGraph");
  const relays = relayMachines.slice(0, 4);
  const clients = nodes.slice(0, 4);
  const strategies = [...new Set(rules.filter((rule) => rule.status === "running").map((rule) => rule.strategy || "single"))].slice(0, 3);
  if (!relays.length && !clients.length && !rules.length) {
    graph.innerHTML = emptyState("暂无节点和规则数据");
    return;
  }
  graph.innerHTML = `
    <div class="topology-col">
      <span class="topology-label">中转</span>
      ${
        relays.length
          ? relays.map((node) => `<div class="node-dot ${node.status === "running" ? "online" : "warning"}">${shortName(node.name)}</div>`).join("")
          : `<div class="node-dot warning">未接入</div>`
      }
    </div>
    <div class="flow-lines">
      <span class="line strong"></span>
      <span class="line"></span>
      <span class="line calm"></span>
    </div>
    <div class="topology-col center">
      <span class="topology-label">策略</span>
      <div class="policy-core">
        <strong>${strategies.length ? strategies.map(strategyLabel).join(" / ") : "单目标"}</strong>
        <small>${rules.length} 条规则</small>
      </div>
    </div>
    <div class="flow-lines">
      <span class="line calm"></span>
      <span class="line strong"></span>
      <span class="line"></span>
    </div>
    <div class="topology-col">
      <span class="topology-label">客户端/目标</span>
      ${
        clients.length
          ? clients.map((node) => `<div class="node-dot ${node.status === "running" ? "online" : "warning"}">${shortName(node.name)}</div>`).join("")
          : `<div class="node-dot warning">直连目标</div>`
      }
    </div>
  `;
}

async function apiFetch(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options
  });
  if (response.status === 401) {
    window.location.href = "/login";
    return null;
  }
  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(error.error || response.statusText);
  }
  return response.json();
}

function proxyLabel(proxyProtocol) {
  if (!proxyProtocol || proxyProtocol.mode === "off") return "关闭";
  const version = proxyProtocol.version || "v2";
  const modeMap = {
    receive: "接收",
    send: "发送",
    both: "接收+发送"
  };
  return `${version} ${modeMap[proxyProtocol.mode] || proxyProtocol.mode}`;
}

function normalizeRule(rule) {
  return {
    ...rule,
    detail: `${rule.listen} / ${rule.protocol}`,
    proxy: proxyLabel(rule.proxyProtocol),
    strategy: rule.strategy || "single",
    strategyName: strategyLabel(rule.strategy || "single")
  };
}

function protocolToRelayProtocol(protocol) {
  const normalized = protocol.replace(/\s+/g, "").toUpperCase();
  if (normalized === "TCP") return "direct_tcp";
  if (normalized === "UDP") return "direct_udp";
  if (normalized === "TCPTUNNEL" || normalized === "TCP隧道") return "tcp_tunnel";
  if (normalized === "TCP+TLS") return "tcp_tls_tunnel";
  if (normalized === "WS") return "ws";
  if (normalized === "WS+TLS") return "ws_tls";
  if (normalized === "GOSTTCP") return "gost_tcp";
  if (normalized === "GOSTWS") return "gost_ws";
  if (normalized === "GOSTWSS" || normalized === "GOSTWS+TLS") return "gost_wss";
  if (normalized === "SOCKS5") return "socks5";
  return "tcp_tunnel";
}

async function loadData() {
  const [overview, relayMachineItems, onlineIps, certificates, account, bootstrap] = await Promise.all([
    apiFetch("/api/overview"),
    apiFetch("/api/relay-machines"),
    apiFetch("/api/online-ips"),
    apiFetch("/api/certificates"),
    apiFetch("/api/settings/account"),
    apiFetch("/api/agent/bootstrap")
  ]);
  if (!overview) return;
  overviewMetrics = {
    onlineNodes: overview.onlineNodes || "0 / 0",
    activeConnections: overview.activeConnections || 0,
    dailyTraffic: overview.dailyTraffic || "0 B",
    realIPCaptureRate: overview.realIPCaptureRate || "0 个"
  };
  nodes = overview.nodes || [];
  relayMachines = relayMachineItems || [];
  rules = (overview.rules || []).map(normalizeRule);
  events = (overview.events || []).map((event) => ({
    tone: event.level || "info",
    title: event.title,
    text: event.body,
    time: event.time
  }));
  ips = (onlineIps || []).map((item, index) => ({
    ip: item.ip,
    rule: item.ruleName,
    entry: item.entryNode,
    conns: item.connections,
    active: item.lastActive,
    pct: Math.max(16, 92 - index * 17)
  }));
  certs = (certificates || []).map((cert) => ({
    name: cert.domain,
    issuer: cert.issuer,
    days: cert.daysLeft,
    used: cert.usedBy
  }));
  accountSettings = account || accountSettings;
  bootstrapCommands = bootstrap || bootstrapCommands;
}

function filteredRules() {
  const keyword = globalSearch.value.trim().toLowerCase();
  const status = ruleStatusFilter ? ruleStatusFilter.value : "all";

  return rules.filter((rule) => {
    const haystack = `${rule.name} ${rule.listen} ${rule.target} ${rule.protocol} ${rule.proxy}`.toLowerCase();
    const keywordMatch = !keyword || haystack.includes(keyword);
    const statusMatch = status === "all" || rule.status === status;
    return keywordMatch && statusMatch;
  });
}

function renderOverviewRules() {
  const visibleRules = rules
    .slice()
    .sort((a, b) => b.connections - a.connections)
    .slice(0, 3);
  if (!visibleRules.length) {
    document.querySelector("#overviewRules").innerHTML = emptyTable("暂无规则数据", 7);
    return;
  }
  document.querySelector("#overviewRules").innerHTML = visibleRules
    .map(
      (rule) => `
        <tr>
          <td><strong>${rule.name}</strong></td>
          <td>${rule.listen}</td>
          <td>${rule.target}</td>
          <td><span class="protocol">${rule.protocol}</span></td>
          <td><span class="tag">${rule.proxy}</span></td>
          <td>${rule.connections.toLocaleString()}</td>
          <td>${statusBadge(rule.status)}</td>
        </tr>
      `
    )
    .join("");
}

function renderNodes() {
  const keyword = globalSearch.value.trim().toLowerCase();
  const visibleNodes = nodes.filter((node) => `${node.name} ${node.region}`.toLowerCase().includes(keyword));

  document.querySelector("#nodeGrid").innerHTML = visibleNodes.length
    ? visibleNodes
    .map(
      (node) => `
        <article class="node-card">
          <div class="node-card-head">
            <div class="node-title">
              <strong>${node.name}</strong>
              <span>${node.region}</span>
            </div>
            ${statusBadge(node.status)}
          </div>
          <div class="health">
            <div><span>负载</span><strong>${node.load}</strong></div>
            <div><span>延迟</span><strong>${node.latency}</strong></div>
            <div><span>流量</span><strong>${node.traffic}</strong></div>
          </div>
        </article>
      `
    )
    .join("")
    : emptyState("暂无客户端机器接入");
}

function renderRelayMachines() {
  const keyword = globalSearch.value.trim().toLowerCase();
  const visibleRelays = relayMachines.filter((node) => `${node.name} ${node.region}`.toLowerCase().includes(keyword));

  document.querySelector("#relayMachineGrid").innerHTML = visibleRelays.length
    ? visibleRelays
    .map(
      (node) => `
        <article class="node-card">
          <div class="node-card-head">
            <div class="node-title">
              <strong>${node.name}</strong>
              <span>${node.region}</span>
            </div>
            ${statusBadge(node.status)}
          </div>
          <div class="health">
            <div><span>角色</span><strong>中转</strong></div>
            <div><span>延迟</span><strong>${node.latency}</strong></div>
            <div><span>流量</span><strong>${node.traffic}</strong></div>
          </div>
        </article>
      `
    )
    .join("")
    : emptyState("暂无中转机器接入");
}

function renderRules() {
  const visibleRules = filteredRules();
  if (!visibleRules.length) {
    document.querySelector("#ruleTable").innerHTML = emptyTable("暂无中转规则", 9);
    return;
  }
  document.querySelector("#ruleTable").innerHTML = visibleRules
    .map(
      (rule) => `
        <tr data-rule-id="${rule.id}">
          <td class="rule-name">
            <strong>${rule.name}</strong>
            <span>${rule.detail}</span>
          </td>
          <td>${rule.relayNodeId || "未分配"}</td>
          <td>${rule.clientNodeId || "未绑定"}</td>
          <td>${rule.listen}</td>
          <td>${rule.target}</td>
          <td>${rule.strategyName}</td>
          <td><span class="tag">${rule.proxy}</span></td>
          <td>${rule.traffic}</td>
          <td>
            <div class="action-row">
              <button class="mini-button" data-rule-action="toggle" type="button">${rule.status === "paused" ? "启动" : "暂停"}</button>
              <button class="mini-button" data-rule-action="edit" type="button">编辑</button>
              <button class="mini-button danger" data-rule-action="delete" type="button">删除</button>
            </div>
          </td>
        </tr>
      `
    )
    .join("");
}

function renderIps() {
  document.querySelector("#ipRank").innerHTML = ips.length
    ? ips
    .map(
      (item) => `
        <div class="rank-item">
          <div class="rank-top">
            <strong>${item.ip}</strong>
            <span>${item.conns} 连接</span>
          </div>
          <div class="rank-bar"><span style="width: ${item.pct}%"></span></div>
        </div>
      `
    )
    .join("")
    : emptyState("暂无真实 IP 捕获");

  document.querySelector("#ipTable").innerHTML = ips.length
    ? ips
    .map(
      (item) => `
        <tr>
          <td><strong>${item.ip}</strong></td>
          <td>${item.entry}</td>
          <td>${item.rule}</td>
          <td>${item.conns}</td>
          <td>${item.active}</td>
        </tr>
      `
    )
    .join("")
    : emptyTable("暂无真实 IP 记录", 5);
}

function renderCerts() {
  document.querySelector("#certGrid").innerHTML = certs.length
    ? certs
    .map((cert) => {
      const pct = Math.max(0, Math.min(100, Math.round((cert.days / 90) * 100)));
      return `
        <article class="cert-card">
          <div class="cert-card-head">
            <div class="cert-title">
              <strong>${cert.name}</strong>
              <span>${cert.issuer}</span>
            </div>
            <span class="status running">自动续期</span>
          </div>
          <div class="cert-progress"><span style="width: ${pct}%"></span></div>
          <div class="cert-meta">
            <span>剩余 ${cert.days} 天</span>
            <span>${cert.used}</span>
          </div>
        </article>
      `;
    })
    .join("")
    : emptyState("暂无证书");
}

function renderSettings() {
  const usernameInput = document.querySelector("#settingsUsername");
  if (usernameInput && document.activeElement !== usernameInput) {
    usernameInput.value = accountSettings.username || "";
  }
}

function renderRelayOptions() {
  const select = document.querySelector("#ruleRelayNodeInput");
  if (!select) return;
  const current = select.value;
  const options = relayMachines
    .map((node) => `<option value="${node.id || node.name}">${node.name} / ${node.region}</option>`)
    .join("");
  select.innerHTML = options || `<option value="">暂无中转机器</option>`;
  if (current) select.value = current;
}

function renderClientOptions() {
  const select = document.querySelector("#ruleClientNodeInput");
  if (!select) return;
  const current = select.value;
  const options = nodes
    .map((node) => `<option value="${node.id || node.name}">${node.name} / ${node.region}</option>`)
    .join("");
  select.innerHTML = `<option value="">不绑定客户端机器</option>${options}`;
  if (current) select.value = current;
}

function renderAll() {
  renderMetrics();
  renderStrategySummary();
  renderTopology();
  renderEvents();
  renderOverviewRules();
  renderNodes();
  renderRelayMachines();
  renderRules();
  renderIps();
  renderCerts();
  renderSettings();
  renderRelayOptions();
  renderClientOptions();
}

function setSettingsMessage(text, tone = "") {
  if (!settingsMessage) return;
  settingsMessage.textContent = text;
  settingsMessage.className = `settings-message ${tone}`.trim();
}

function resetRuleForm() {
  editingRuleId = "";
  document.querySelector("#ruleModalTitle").textContent = "新建中转规则";
  document.querySelector("#ruleNameInput").value = "测试 TCP 转发";
  document.querySelector("#ruleProtocolInput").value = "TCP";
  document.querySelector("#ruleListenInput").value = ":19090";
  document.querySelector("#ruleTargetInput").value = "localhost:8080";
  document.querySelector("#ruleTunnelEndpointInput").value = "";
  document.querySelector("#ruleStrategyInput").value = "single";
  proxyVersion = "v2";
  if (proxyProtocolSwitch) proxyProtocolSwitch.checked = true;
  syncProxyButtons();
  renderRelayOptions();
  renderClientOptions();
}

function syncProxyButtons() {
  document.querySelectorAll("[data-proxy-version]").forEach((button) => {
    const active = proxyVersion === button.dataset.proxyVersion;
    button.classList.toggle("active", active);
  });
  if (proxyProtocolSwitch && proxyVersion === "off") {
    proxyProtocolSwitch.checked = false;
  }
}

function openModal(rule = null) {
  if (rule) {
    editingRuleId = rule.id;
    document.querySelector("#ruleModalTitle").textContent = "编辑中转规则";
    document.querySelector("#ruleNameInput").value = rule.name || "";
    document.querySelector("#ruleProtocolInput").value = rule.protocol || "TCP";
    document.querySelector("#ruleListenInput").value = rule.listen || "";
    document.querySelector("#ruleTargetInput").value = rule.target || "";
    document.querySelector("#ruleTunnelEndpointInput").value = rule.tunnelEndpoint || "";
    document.querySelector("#ruleStrategyInput").value = rule.strategy || "single";
    renderRelayOptions();
    renderClientOptions();
    document.querySelector("#ruleRelayNodeInput").value = rule.relayNodeId || "";
    document.querySelector("#ruleClientNodeInput").value = rule.clientNodeId || "";
    proxyVersion = rule.proxyProtocol?.mode === "off" ? "off" : rule.proxyProtocol?.version || "v2";
    if (proxyProtocolSwitch) proxyProtocolSwitch.checked = rule.proxyProtocol?.mode !== "off";
    syncProxyButtons();
  } else {
    resetRuleForm();
  }
  ruleModal.classList.add("open");
  ruleModal.setAttribute("aria-hidden", "false");
}

function closeModal() {
  ruleModal.classList.remove("open");
  ruleModal.setAttribute("aria-hidden", "true");
}

function openCommandModal(role) {
  if (role === "panel") {
    document.querySelector("#commandModalTitle").textContent = "面板安装命令";
    commandText.textContent = bootstrapCommands.panel || "";
    commandModal.classList.add("open");
    commandModal.setAttribute("aria-hidden", "false");
    return;
  }
  const isRelay = role === "relay";
  document.querySelector("#commandModalTitle").textContent = isRelay ? "中转机器接入命令" : "客户端机器接入命令";
  commandText.textContent = isRelay ? bootstrapCommands.relay || "" : bootstrapCommands.client || "";
  commandModal.classList.add("open");
  commandModal.setAttribute("aria-hidden", "false");
}

function closeCommandModal() {
  commandModal.classList.remove("open");
  commandModal.setAttribute("aria-hidden", "true");
}

async function copyCommand() {
  const text = commandText.textContent.trim();
  if (!text) {
    showToast("命令还没有加载完成", "error");
    return;
  }
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
  } else {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    document.execCommand("copy");
    textarea.remove();
  }
  showToast("接入命令已复制", "success");
}

async function refreshData(message = "数据已刷新") {
  await loadData();
  renderAll();
  showToast(message, "success");
}

function rulePayloadFromForm(base = {}) {
  const protocol = document.querySelector("#ruleProtocolInput").value;
  const relayProtocol = protocolToRelayProtocol(protocol);
  const proxyEnabled = relayProtocol !== "direct_udp" && proxyProtocolSwitch ? proxyProtocolSwitch.checked && proxyVersion !== "off" : false;
  return {
    ...base,
    name: document.querySelector("#ruleNameInput").value.trim(),
    relayNodeId: document.querySelector("#ruleRelayNodeInput").value,
    clientNodeId: document.querySelector("#ruleClientNodeInput").value,
    listen: document.querySelector("#ruleListenInput").value.trim(),
    target: document.querySelector("#ruleTargetInput").value.trim(),
    tunnelEndpoint: document.querySelector("#ruleTunnelEndpointInput").value.trim(),
    protocol,
    inbound: relayProtocol,
    outbound: relayProtocol,
    strategy: document.querySelector("#ruleStrategyInput").value || base.strategy || "single",
    status: base.status || "running",
    enabled: base.enabled ?? true,
    proxyProtocol: {
      mode: proxyEnabled ? "send" : "off",
      version: proxyVersion === "v1" ? "v1" : "v2",
      trustedCidrs: base.proxyProtocol?.trustedCidrs || []
    }
  };
}

function rulePayloadFromRule(rule, overrides = {}) {
  return {
    name: rule.name,
    relayNodeId: rule.relayNodeId,
    clientNodeId: rule.clientNodeId,
    listen: rule.listen,
    target: rule.target,
    tunnelEndpoint: rule.tunnelEndpoint,
    protocol: rule.protocol,
    inbound: rule.inbound,
    outbound: rule.outbound,
    strategy: rule.strategy,
    status: rule.status,
    enabled: rule.enabled,
    proxyProtocol: rule.proxyProtocol || { mode: "off", version: "v2", trustedCidrs: [] },
    ...overrides
  };
}

async function saveRule() {
  const current = editingRuleId ? rules.find((rule) => rule.id === editingRuleId) : null;
  const payload = rulePayloadFromForm(current || {});
  const path = editingRuleId ? `/api/rules/${editingRuleId}` : "/api/rules";
  const method = editingRuleId ? "PUT" : "POST";
  await apiFetch(path, {
    method,
    body: JSON.stringify(payload)
  });
  closeModal();
  await refreshData(editingRuleId ? "规则已保存" : "规则已创建");
}

async function toggleRule(rule) {
  const paused = rule.status === "paused";
  await apiFetch(`/api/rules/${rule.id}`, {
    method: "PUT",
    body: JSON.stringify(
      rulePayloadFromRule(rule, {
        status: paused ? "running" : "paused",
        enabled: paused
      })
    )
  });
  await refreshData(paused ? "规则已启动" : "规则已暂停");
}

async function deleteRule(rule) {
  if (!window.confirm(`删除规则「${rule.name}」？`)) return;
  await apiFetch(`/api/rules/${rule.id}`, { method: "DELETE" });
  await refreshData("规则已删除");
}

function exportOnlineIPs() {
  const rows = [["真实 IP", "入口", "规则", "连接", "最近活跃"], ...ips.map((item) => [item.ip, item.entry, item.rule, item.conns, item.active])];
  const csv = rows.map((row) => row.map((cell) => `"${String(cell ?? "").replaceAll('"', '""')}"`).join(",")).join("\n");
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `goss-online-ips-${Date.now()}.csv`;
  link.click();
  URL.revokeObjectURL(url);
  showToast("在线 IP 已导出", "success");
}

function downloadJSON(filename, value) {
  const blob = new Blob([JSON.stringify(value, null, 2)], { type: "application/json;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  URL.revokeObjectURL(url);
}

async function exportRules() {
  const response = await fetch("/api/rules/export");
  if (response.status === 401) {
    window.location.href = "/login";
    return;
  }
  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(error.error || response.statusText);
  }
  const payload = await response.json();
  downloadJSON(`goss-rules-${Date.now()}.json`, payload);
  showToast(`已导出 ${payload.count || 0} 条规则`, "success");
}

function normalizeImportFilePayload(value) {
  if (Array.isArray(value)) {
    return { rules: value };
  }
  if (value && Array.isArray(value.rules)) {
    return { ...value, rules: value.rules };
  }
  throw new Error("导入文件格式不正确");
}

async function importRulesFromFile(file) {
  const text = await file.text();
  const parsed = normalizeImportFilePayload(JSON.parse(text));
  const mode = ruleImportMode ? ruleImportMode.value : "skip";
  const confirmed = window.confirm(`准备导入 ${parsed.rules.length} 条规则，冲突处理为「${ruleImportMode?.selectedOptions[0]?.textContent || "冲突跳过"}」。继续？`);
  if (!confirmed) return;
  const result = await apiFetch("/api/rules/import", {
    method: "POST",
    body: JSON.stringify({
      mode,
      rules: parsed.rules
    })
  });
  await refreshData(`导入完成：新增 ${result.created}，更新 ${result.updated}，副本 ${result.copies}，跳过 ${result.skipped}`);
  if (result.issues?.length) {
    console.table(result.issues);
  }
}

navButtons.forEach((button) => {
  button.addEventListener("click", () => setView(button.dataset.view));
});

document.querySelectorAll("[data-view-jump]").forEach((button) => {
  button.addEventListener("click", () => setView(button.dataset.viewJump));
});

document.querySelectorAll(".segmented button").forEach((button) => {
  if (button.dataset.proxyVersion) return;
  button.addEventListener("click", () => {
    const group = button.closest(".segmented");
    group.querySelectorAll("button").forEach((item) => item.classList.toggle("active", item === button));
    showToast(`已切换为${button.textContent.trim()}视图`);
  });
});

globalSearch.addEventListener("input", () => {
  renderNodes();
  renderRelayMachines();
  renderRules();
});

if (ruleStatusFilter) {
  ruleStatusFilter.addEventListener("change", renderRules);
}

document.querySelector("#refreshDataButton").addEventListener("click", () => {
  refreshData().catch((error) => showToast(error.message, "error"));
});
document.querySelector("#openRuleModal").addEventListener("click", () => openModal());
document.querySelector("#openRuleModal2").addEventListener("click", () => openModal());
document.querySelector("#closeRuleModal").addEventListener("click", closeModal);
document.querySelector("#cancelRuleModal").addEventListener("click", closeModal);
document.querySelector("#closeCommandModal").addEventListener("click", closeCommandModal);
document.querySelector("#closeCommandModal2").addEventListener("click", closeCommandModal);
document.querySelector("#copyCommandButton").addEventListener("click", () => {
  copyCommand().catch((error) => showToast(error.message, "error"));
});
document.querySelector("#saveRuleButton").addEventListener("click", () => {
  saveRule().catch((error) => showToast(error.message, "error"));
});

document.querySelectorAll("[data-proxy-version]").forEach((button) => {
  button.addEventListener("click", () => {
    proxyVersion = button.dataset.proxyVersion;
    if (proxyVersion !== "off" && proxyProtocolSwitch) proxyProtocolSwitch.checked = true;
    syncProxyButtons();
  });
});

if (proxyProtocolSwitch) {
  proxyProtocolSwitch.addEventListener("change", () => {
    if (!proxyProtocolSwitch.checked) proxyVersion = "off";
    if (proxyProtocolSwitch.checked && proxyVersion === "off") proxyVersion = "v2";
    syncProxyButtons();
  });
}

if (ruleTable) {
  ruleTable.addEventListener("click", (event) => {
    const button = event.target.closest("[data-rule-action]");
    if (!button) return;
    const row = button.closest("[data-rule-id]");
    const rule = rules.find((item) => item.id === row?.dataset.ruleId);
    if (!rule) return;
    const action = button.dataset.ruleAction;
    if (action === "edit") openModal(rule);
    if (action === "toggle") toggleRule(rule).catch((error) => showToast(error.message, "error"));
    if (action === "delete") deleteRule(rule).catch((error) => showToast(error.message, "error"));
  });
}

document.querySelector("#exportIpsButton").addEventListener("click", exportOnlineIPs);
document.querySelector("#exportRulesButton").addEventListener("click", () => {
  exportRules().catch((error) => showToast(error.message, "error"));
});
document.querySelector("#importRulesButton").addEventListener("click", () => {
  if (!ruleImportFile) return;
  ruleImportFile.value = "";
  ruleImportFile.click();
});
if (ruleImportFile) {
  ruleImportFile.addEventListener("change", () => {
    const file = ruleImportFile.files?.[0];
    if (!file) return;
    importRulesFromFile(file).catch((error) => showToast(error.message, "error"));
  });
}
document.querySelector("#viewEventsButton").addEventListener("click", () => showToast("事件列表已按最新时间展示"));
document.querySelector("#addClientButton").addEventListener("click", () => openCommandModal("client"));
document.querySelector("#addRelayButton").addEventListener("click", () => openCommandModal("relay"));
document.querySelector("#showPanelInstallButton").addEventListener("click", () => openCommandModal("panel"));
document.querySelector("#addCertButton").addEventListener("click", () => showToast("证书申请和部署功能将在 TLS/WSS 阶段接入"));

document.querySelector("#saveAccountSettings").addEventListener("click", async () => {
  setSettingsMessage("正在保存...");
  const payload = {
    username: document.querySelector("#settingsUsername").value.trim(),
    currentPassword: document.querySelector("#settingsCurrentPassword").value,
    newPassword: document.querySelector("#settingsNewPassword").value
  };
  try {
    const result = await apiFetch("/api/settings/account", {
      method: "PUT",
      body: JSON.stringify(payload)
    });
    if (!result) return;
    accountSettings.username = result.username;
    document.querySelector("#settingsCurrentPassword").value = "";
    document.querySelector("#settingsNewPassword").value = "";
    setSettingsMessage("账号设置已保存", "success");
    renderSettings();
  } catch (error) {
    setSettingsMessage(error.message, "error");
  }
});
ruleModal.addEventListener("click", (event) => {
  if (event.target === ruleModal) closeModal();
});
commandModal.addEventListener("click", (event) => {
  if (event.target === commandModal) closeCommandModal();
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    closeModal();
    closeCommandModal();
  }
});

loadData()
  .catch((error) => {
    console.warn("Failed to load API data", error);
    showToast(error.message, "error");
  })
  .finally(renderAll);
