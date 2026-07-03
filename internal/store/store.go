package store

import (
	"context"

	"github.com/SadNoo/gosspanel/internal/domain"
)

type Store interface {
	Close() error
	Overview(context.Context) (domain.Overview, error)
	Nodes(context.Context) ([]domain.Node, error)
	NodesByRole(context.Context, domain.NodeRole) ([]domain.Node, error)
	UpsertNode(context.Context, domain.Node) error
	Rules(context.Context) ([]domain.RelayRule, error)
	EnabledRules(context.Context) ([]domain.RelayRule, error)
	Rule(context.Context, string) (domain.RelayRule, error)
	CreateRule(context.Context, domain.RuleInput) (domain.RelayRule, error)
	UpdateRule(context.Context, string, domain.RuleInput) (domain.RelayRule, error)
	UpdateRuleMetrics(context.Context, []domain.RuleMetric) error
	DeleteRule(context.Context, string) error
	RecordOnlineIP(context.Context, domain.OnlineIP) error
	OnlineIPs(context.Context) ([]domain.OnlineIP, error)
	Certificates(context.Context) ([]domain.Certificate, error)
	Events(context.Context) ([]domain.Event, error)
	AddEvent(context.Context, domain.Event) error
	AdminSettings(context.Context) (domain.AdminSettings, error)
	UpdateAdminSettings(context.Context, domain.AdminSettings) error
}
