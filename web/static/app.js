let nodes = [
  { name: "HK5", region: "Hong Kong", status: "warning", load: "71%", latency: "18 ms", traffic: "1.42 TB" },
  { name: "SG1", region: "Singapore", status: "running", load: "46%", latency: "36 ms", traffic: "812 GB" },
  { name: "JP2", region: "Tokyo", status: "running", load: "39%", latency: "42 ms", traffic: "698 GB" },
  { name: "US-LA", region: "Los Angeles", status: "running", load: "58%", latency: "142 ms", traffic: "1.08 TB" },
  { name: "DE-FRA", region: "Frankfurt", status: "running", load: "32%", latency: "184 ms", traffic: "436 GB" },
  { name: "NL-AMS", region: "Amsterdam", status: "running", load: "28%", latency: "176 ms", traffic: "391 GB" }
];

let relayMachines = [
  { name: "Relay Demo", region: "Relay", status: "warning", load: "0%", latency: "-", traffic: "0 B" }
];

let rules = [
  {
    name: "HK 游戏端口段",
    detail: "20000-20100 / TCP+TLS",
    listen: "HK5 :20000-20100",
    target: "US-LA :443",
    protocol: "TCP+TLS",
    strategy: "Fallback",
    proxy: "v2 发送",
    traffic: "1.82 TB",
    connections: 4821,
    status: "running"
  },
  {
    name: "SG WebSocket 中转",
    detail: "443 / WSS",
    listen: "SG1 :443",
    target: "DE-FRA :8443",
    protocol: "WSS",
    strategy: "IP Hash",
    proxy: "v2 接收+发送",
    traffic: "944 GB",
    connections: 3194,
    status: "running"
  },
  {
    name: "JP 备用入口",
    detail: "18080 / TCP",
    listen: "JP2 :18080",
    target: "NL-AMS :18080",
    protocol: "TCP",
    strategy: "Round Robin",
    proxy: "关闭",
    traffic: "312 GB",
    connections: 876,
    status: "paused"
  },
  {
    name: "HK5 低延迟组",
    detail: "30000-30100 / TCP",
    listen: "HK5 :30000-30100",
    target: "US-LA, DE-FRA",
    protocol: "TCP",
    strategy: "Least Conn",
    proxy: "v1 发送",
    traffic: "1.14 TB",
    connections: 3955,
    status: "warning"
  }
];

let events = [
  { tone: "warn", title: "HK5 健康检查抖动", text: "3 分钟内出现 2 次重连", time: "刚刚" },
  { tone: "info", title: "证书续期完成", text: "relay.example.com 已部署到 2 条规则", time: "8 分钟前" },
  { tone: "ok", title: "US-LA 出口恢复", text: "Fallback 策略已切回主出口", time: "21 分钟前" },
  { tone: "info", title: "新增在线 IP 峰值", text: "SG1 入口 15 分钟内 1,248 个来源", time: "34 分钟前" }
];

let ips = [
  { ip: "来源 A", rule: "HK 游戏端口段", entry: "HK5", conns: 142, active: "12 秒前", pct: 92 },
  { ip: "来源 B", rule: "SG WebSocket 中转", entry: "SG1", conns: 96, active: "28 秒前", pct: 71 },
  { ip: "来源 C", rule: "HK5 低延迟组", entry: "HK5", conns: 74, active: "1 分钟前", pct: 58 },
  { ip: "来源 D", rule: "SG WebSocket 中转", entry: "SG1", conns: 51, active: "2 分钟前", pct: 39 }
];

let certs = [
  { name: "relay.example.com", issuer: "Let's Encrypt / Cloudflare DNS", days: 72, used: "WSS, TCP+TLS" },
  { name: "*.edge.example.com", issuer: "ZeroSSL / DNSPod", days: 41, used: "多入口规则" },
  { name: "hk5.example.net", issuer: "Let's Encrypt / HTTP-01", days: 19, used: "HK5 入口" }
];

let accountSettings = {
  username: ""
};

const statusText = {
  running: "运行中",
  warning: "告警",
  paused: "已暂停"
};

const navButtons = document.querySelectorAll("[data-view]");
const views = document.querySelectorAll(".view");
const globalSearch = document.querySelector("#globalSearch");
const ruleStatusFilter = document.querySelector("#ruleStatusFilter");
const ruleModal = document.querySelector("#ruleModal");
const settingsMessage = document.querySelector("#settingsMessage");
const ruleTable = document.querySelector("#ruleTable");
const proxyProtocolSwitch = document.querySelector("#proxyProtocolSwitch");
const commandModal = document.querySelector("#commandModal");
const commandText = document.querySelector("#commandText");
let bootstrapCommands = { relay: "", client: "" };
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
  document.querySelector("#eventList").innerHTML = events
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
    strategy: rule.strategy || "single"
  };
}

function protocolToRelayProtocol(protocol) {
  const normalized = protocol.replace(/\s+/g, "").toUpperCase();
  if (normalized === "TCP") return "direct_tcp";
  if (normalized === "UDP") return "direct_udp";
  if (normalized === "TCP+TLS") return "tls";
  if (normalized === "WS") return "ws";
  if (normalized === "WS+TLS") return "ws_tls";
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
  nodes = overview.nodes || [];
  relayMachines = relayMachineItems || [];
  rules = (overview.rules || []).map(normalizeRule);
  events = overview.events || [];
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
  document.querySelector("#overviewRules").innerHTML = rules
    .slice()
    .sort((a, b) => b.connections - a.connections)
    .slice(0, 3)
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

  document.querySelector("#nodeGrid").innerHTML = visibleNodes
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
    .join("");
}

function renderRelayMachines() {
  const keyword = globalSearch.value.trim().toLowerCase();
  const visibleRelays = relayMachines.filter((node) => `${node.name} ${node.region}`.toLowerCase().includes(keyword));

  document.querySelector("#relayMachineGrid").innerHTML = visibleRelays
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
    .join("");
}

function renderRules() {
  document.querySelector("#ruleTable").innerHTML = filteredRules()
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
          <td>${rule.strategy}</td>
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
  document.querySelector("#ipRank").innerHTML = ips
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
    .join("");

  document.querySelector("#ipTable").innerHTML = ips
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
    .join("");
}

function renderCerts() {
  document.querySelector("#certGrid").innerHTML = certs
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
    .join("");
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
    protocol,
    inbound: relayProtocol,
    outbound: relayProtocol,
    strategy: base.strategy || "single",
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
document.querySelector("#viewEventsButton").addEventListener("click", () => showToast("事件列表已按最新时间展示"));
document.querySelector("#addClientButton").addEventListener("click", () => openCommandModal("client"));
document.querySelector("#addRelayButton").addEventListener("click", () => openCommandModal("relay"));
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
    console.warn("Failed to load API data, using local preview data", error);
  })
  .finally(renderAll);
