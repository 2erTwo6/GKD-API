package db

import "time"

type Admin struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"uniqueIndex;size:64" json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type APIKey struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Key        string     `gorm:"uniqueIndex;size:80" json:"key"`
	Name       string     `json:"name"`
	Enabled    bool       `gorm:"default:true" json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type VirtualModel struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:64" json:"name"`
	BatchSize   int       `json:"batch_size"`
	BatchWaitMs int       `json:"batch_wait_ms"`
	PickMode    string    `gorm:"default:fastest" json:"pick_mode"`
	MaxWaitMs   int       `json:"max_wait_ms"`
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`

	RealModels []RealModel `json:"real_models,omitempty" gorm:"foreignKey:VirtualModelID"`
}

type RealModel struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	VirtualModelID uint   `gorm:"index" json:"virtual_model_id"`
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	Protocol       string `gorm:"default:openai" json:"protocol"`
	Weight         int    `gorm:"default:1" json:"weight"`
	OverrideJSON   string `gorm:"type:text" json:"override_json"`
	Enabled        bool   `gorm:"default:true" json:"enabled"`
}

type RequestLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	KeyName      string    `json:"key_name"`
	VirtualModel string    `json:"virtual_model"`
	Winner       string    `json:"winner"`
	BatchSize    int       `json:"batch_size"`
	PickMode     string    `json:"pick_mode"`
	LatencyMs    int64     `json:"latency_ms"`
	Status       int       `json:"status"`
	Detail       string    `gorm:"type:text" json:"detail"`
}

type Setting struct {
	Key   string `gorm:"primaryKey;size:64"`
	Value string `gorm:"size:512"`
}
