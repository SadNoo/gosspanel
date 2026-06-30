package domain

type NodeStatus string

const (
	NodeStatusRunning NodeStatus = "running"
	NodeStatusWarning NodeStatus = "warning"
	NodeStatusPaused  NodeStatus = "paused"
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
	RelayProtocolTLS       RelayProtocol = "tls"
	RelayProtocolWS        RelayProtocol = "ws"
	RelayProtocolWSS       RelayProtocol = "ws_tls"
	RelayProtocolTunnelTCP RelayProtocol = "tcp_tunnel"
	RelayProtocolTunnelTLS RelayProtocol = "tcp_tls_tunnel"
	RelayProtocolSOCKS5    RelayProtocol = "socks5"
)

type StrategyKind string

const (
	StrategySingle     StrategyKind = "single"
	StrategyRoundRobin StrategyKind = "round_robin"
	StrategyIPHash     StrategyKind = "ip_hash"
	StrategyLeastConn  StrategyKind = "least_conn"
	StrategyFallback   StrategyKind = "fallback"
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
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	RelayNodeID   string              `json:"relayNodeId"`
	ClientNodeID  string              `json:"clientNodeId"`
	Listen        string              `json:"listen"`
	Target        string              `json:"target"`
	Protocol      string              `json:"protocol"`
	Inbound       RelayProtocol       `json:"inbound"`
	Outbound      RelayProtocol       `json:"outbound"`
	Strategy      StrategyKind        `json:"strategy"`
	ProxyProtocol ProxyProtocolConfig `json:"proxyProtocol"`
	Traffic       string              `json:"traffic"`
	Connections   int                 `json:"connections"`
	Status        RuleStatus          `json:"status"`
	Enabled       bool                `json:"enabled"`
}

type RuleInput struct {
	Name          string              `json:"name"`
	RelayNodeID   string              `json:"relayNodeId"`
	ClientNodeID  string              `json:"clientNodeId"`
	Listen        string              `json:"listen"`
	Target        string              `json:"target"`
	Protocol      string              `json:"protocol"`
	Inbound       RelayProtocol       `json:"inbound"`
	Outbound      RelayProtocol       `json:"outbound"`
	Strategy      StrategyKind        `json:"strategy"`
	ProxyProtocol ProxyProtocolConfig `json:"proxyProtocol"`
	Status        RuleStatus          `json:"status"`
	Enabled       bool                `json:"enabled"`
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

type AgentOnlineIPReport struct {
	NodeID string     `json:"nodeId"`
	Role   NodeRole   `json:"role"`
	Items  []OnlineIP `json:"items"`
}
