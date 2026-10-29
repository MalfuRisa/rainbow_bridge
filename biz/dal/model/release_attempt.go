package model

import "time"

// ReleaseStorageAttempt survives rollback of the business transaction. No credentials.
type ReleaseStorageAttempt struct {
	ID          string `gorm:"primaryKey;size:36"`
	SnapshotID  string `gorm:"size:36;not null;index"`
	RequestID   string `gorm:"size:64;not null"`
	Fingerprint string `gorm:"size:64;not null"`
	ScopeID     string `gorm:"size:36;not null"`
	Bucket      string `gorm:"size:63;not null"`
	Prefix      string `gorm:"size:255;not null;uniqueIndex"`
	Kind        string `gorm:"size:16;not null"`
	State       string `gorm:"size:32;not null;index"`
	Diagnostic  string `gorm:"size:64;not null;default:''"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (ReleaseStorageAttempt) TableName() string { return "release_storage_attempt" }
