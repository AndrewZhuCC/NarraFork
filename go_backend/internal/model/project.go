// Package model defines the domain entities for NarraFork.
// Each struct maps to a database table; fields use the cross-dialect
// scanner types from internal/database for nullable/bool/time columns
// so the same model works against both SQLite and PostgreSQL.
package model

// Project is a NarraFork project — one git repository with a story network
// of chapters.
type Project struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	Status          string  `json:"status"`   // active | archived
	FlowMode        string  `json:"flowMode"` // classic | ruler
	GitPath         *string `json:"gitPath"`
	RemoteURL       *string `json:"remoteUrl"`
	DefaultBranch   *string `json:"defaultBranch"`
	StartupScript   *string `json:"startupScript"`
	CopyFiles       *string `json:"copyFiles"`
	ChapterSettings *string `json:"chapterSettings"` // raw JSON
	ProxyDomain     *string `json:"proxyDomain"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

// Project status constants.
const (
	ProjectStatusActive   = "active"
	ProjectStatusArchived = "archived"
)

// Project flow mode constants.
const (
	FlowModeClassic = "classic"
	FlowModeRuler   = "ruler"
)
