package domain

type NodeStatus string

const (
	NodeStatusRunning NodeStatus = "running"
	NodeStatusWarning NodeStatus = "warning"
	NodeStatusPaused  NodeStatus = "paused"
	NodeStatusOffline NodeStatus = "offline"
)

type NodeRole string

const (
	NodeRoleClient NodeRole = "client"
	NodeRoleRelay  NodeRole = "relay"
)

type RuleStatus string

const (
	RuleStatusRunning RuleStatus = "running"
	RuleStatusWarning RuleStatus = "warning"
	RuleStatusPaused  RuleStatus = "paused"
)

type ProxyProtocolMode string

const (
	ProxyProtocolOff     ProxyProtocolMode = "off"
	ProxyProtocolReceive ProxyProtocolMode = "receive"
	ProxyProtocolSend    ProxyProtocolMode = "send"
	ProxyProtocolBoth    ProxyProtocolMode = "both"
)

type ProxyProtocolVersion string

const (
	ProxyProtocolVersion1 ProxyProtocolVersion = "v1"
	ProxyProtocolVersion2 ProxyProtocolVersion = "v2"
)

type RelayProtocol string

const (
	RelayProtocolDirectTCP RelayProtocol = "direct_tcp"
	RelayProtocolDirectUDP RelayProtocol = "direct_udp"
	RelayProtocolTLS       RelayProtocol = "tls"
	RelayProtocolWS        RelayProtocol = "ws"
	RelayProtocolWSS       RelayProtocol = "ws_tls"
	RelayProtocolTunnelTCP RelayProtocol = "tcp_tunnel"
	RelayProtocolTunnelTLS RelayProtocol = "tcp_tls_tunnel"
	RelayProtocolGOSTTCP   RelayProtocol = "gost_tcp"
	RelayProtocolGOSTWS    RelayProtocol = "gost_ws"
	RelayProtocolGOSTWSS   RelayProtocol = "gost_wss"
	RelayProtocolSOCKS5    RelayProtocol = "socks5"
)

type StrategyKind string

const (
	StrategySingle     StrategyKind = "single"
	StrategyRoundRobin StrategyKind = "round_robin"
	StrategyIPHash     StrategyKind = "ip_hash"
	StrategyLeastConn  StrategyKind = "least_conn"
	StrategyFallback   StrategyKind = "fallback"
	StrategyManual     StrategyKind = "manual"
	StrategyLatency    StrategyKind = "latency"
	StrategyWeighted   StrategyKind = "weighted"
)

type ProxyProtocolConfig struct {
	Mode         ProxyProtocolMode    `json:"mode"`
	Version      ProxyProtocolVersion `json:"version"`
	TrustedCIDRs []string             `json:"trustedCidrs"`
}

type Node struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Region   string     `json:"region"`
	Role     NodeRole   `json:"role"`
	Status   NodeStatus `json:"status"`
	Load     string     `json:"load"`
	Latency  string     `json:"latency"`
	Traffic  string     `json:"traffic"`
	LastSeen string     `json:"lastSeen"`
}

type RelayRule struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	RelayNodeID    string              `json:"relayNodeId"`
	ClientNodeID   string              `json:"clientNodeId"`
	Listen         string              `json:"listen"`
	Target         string              `json:"target"`
	TunnelEndpoint string              `json:"tunnelEndpoint"`
	Protocol       string              `json:"protocol"`
	Inbound        RelayProtocol       `json:"inbound"`
	Outbound       RelayProtocol       `json:"outbound"`
	Strategy       StrategyKind        `json:"strategy"`
	ProxyProtocol  ProxyProtocolConfig `json:"proxyProtocol"`
	Traffic        string              `json:"traffic"`
	Connections    int                 `json:"connections"`
	Status         RuleStatus          `json:"status"`
	Enabled        bool                `json:"enabled"`
}

type RuleExport struct {
	Version    int         `json:"version"`
	ExportedAt string      `json:"exportedAt"`
	Count      int         `json:"count"`
	Rules      []RuleInput `json:"rules"`
}

type RuleImportRequest struct {
	Mode   string      `json:"mode"`
	DryRun bool        `json:"dryRun"`
	Rules  []RuleInput `json:"rules"`
}

type RuleImportIssue struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Listen  string `json:"listen"`
	Message string `json:"message"`
}

type RuleImportResult struct {
	Mode    string            `json:"mode"`
	DryRun  bool              `json:"dryRun"`
	Total   int               `json:"total"`
	Created int               `json:"created"`
	Updated int               `json:"updated"`
	Skipped int               `json:"skipped"`
	Copies  int               `json:"copies"`
	Issues  []RuleImportIssue `json:"issues"`
}

type RuleInput struct {
	Name           string              `json:"name"`
	RelayNodeID    string              `json:"relayNodeId"`
	ClientNodeID   string              `json:"clientNodeId"`
	Listen         string              `json:"listen"`
	Target         string              `json:"target"`
	TunnelEndpoint string              `json:"tunnelEndpoint"`
	Protocol       string              `json:"protocol"`
	Inbound        RelayProtocol       `json:"inbound"`
	Outbound       RelayProtocol       `json:"outbound"`
	Strategy       StrategyKind        `json:"strategy"`
	ProxyProtocol  ProxyProtocolConfig `json:"proxyProtocol"`
	Status         RuleStatus          `json:"status"`
	Enabled        bool                `json:"enabled"`
}

type OnlineIP struct {
	IP          string `json:"ip"`
	EntryNode   string `json:"entryNode"`
	RuleName    string `json:"ruleName"`
	Connections int    `json:"connections"`
	LastActive  string `json:"lastActive"`
}

type Certificate struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`
	Issuer    string `json:"issuer"`
	DaysLeft  int    `json:"daysLeft"`
	UsedBy    string `json:"usedBy"`
	AutoRenew bool   `json:"autoRenew"`
}

type Event struct {
	Level string `json:"level"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Time  string `json:"time"`
}

type Overview struct {
	OnlineNodes       string      `json:"onlineNodes"`
	ActiveConnections int         `json:"activeConnections"`
	DailyTraffic      string      `json:"dailyTraffic"`
	RealIPCaptureRate string      `json:"realIPCaptureRate"`
	Nodes             []Node      `json:"nodes"`
	Rules             []RelayRule `json:"rules"`
	Events            []Event     `json:"events"`
}

type AdminSettings struct {
	Username string `json:"username"`
	Password string `json:"-"`
}

type AccountUpdateRequest struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type AgentRegisterRequest struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Region string   `json:"region"`
	Role   NodeRole `json:"role"`
}

type AgentHeartbeatRequest struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Region   string       `json:"region"`
	Role     NodeRole     `json:"role"`
	Status   NodeStatus   `json:"status"`
	Load     string       `json:"load"`
	Latency  string       `json:"latency"`
	Traffic  string       `json:"traffic"`
	LastSeen string       `json:"lastSeen"`
	Metrics  []RuleMetric `json:"metrics"`
}

type AgentOnlineIPReport struct {
	NodeID string     `json:"nodeId"`
	Role   NodeRole   `json:"role"`
	Items  []OnlineIP `json:"items"`
}

type RuleMetric struct {
	RuleID            string `json:"ruleId"`
	ActiveConnections int    `json:"activeConnections"`
	TodayBytes        int64  `json:"todayBytes"`
}

type AgentBootstrapCommands struct {
	Panel           string `json:"panel"`
	PanelHTTPS      string `json:"panelHttps"`
	PanelHTTPSLocal string `json:"panelHttpsLocal"`
	PanelURL        string `json:"panelUrl"`
	Relay           string `json:"relay"`
	Client          string `json:"client"`
}
