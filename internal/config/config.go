package config

import "os"

type Config struct {
	Addr          string
	DataPath      string
	AdminUser     string
	AdminPassword string
	SessionSecret string
	AgentToken    string
}

func FromEnv() Config {
	addr := os.Getenv("GOSS_ADDR")
	if addr == "" {
		addr = "localhost:8080"
	}
	dataPath := os.Getenv("GOSS_DATA")
	if dataPath == "" {
		dataPath = "goss.db"
	}
	adminUser := os.Getenv("GOSS_ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}
	adminPassword := os.Getenv("GOSS_ADMIN_PASSWORD")
	if adminPassword == "" {
		adminPassword = "admin"
	}
	sessionSecret := os.Getenv("GOSS_SESSION_SECRET")
	if sessionSecret == "" {
		sessionSecret = "change-me"
	}
	agentToken := os.Getenv("GOSS_AGENT_TOKEN")
	if agentToken == "" {
		agentToken = "dev-agent-token"
	}

	return Config{
		Addr:          addr,
		DataPath:      dataPath,
		AdminUser:     adminUser,
		AdminPassword: adminPassword,
		SessionSecret: sessionSecret,
		AgentToken:    agentToken,
	}
}
