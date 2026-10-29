package model

import "time"

// 发布版本选择模式与事件类型。
const (
	ReleaseSelectionActive = "active"
	ReleaseSelectionFixed  = "fixed"

	ReleaseEventCreate   = "create"
	ReleaseEventActivate = "activate"
	ReleaseEventDelete   = "delete"
)

// ConfigReleaseVersion 保存一个不可变快照的元数据与内部 payload。
// 版本创建后配置、名称、描述、资源字节、发布说明、来源冻结 revision、创建时间与摘要
// 都不可通过应用修改;逻辑删除仅改变生命周期字段,不修改快照内容。
type ConfigReleaseVersion struct {
	StorageBackend       string     `gorm:"size:16;not null;default:''" json:"-"`
	StorageBucket        string     `gorm:"size:63;not null;default:''" json:"-"`
	StoragePrefix        string     `gorm:"size:255;not null;default:''" json:"-"`
	StorageAttempt       string     `gorm:"size:36;not null;default:''" json:"-"`
	LegacyLocal          bool       `gorm:"not null;default:false" json:"-"`
	ID                   uint       `gorm:"primaryKey" json:"id"`
	SnapshotID           string     `gorm:"size:64;uniqueIndex:uk_release_snapshot;not null" json:"snapshot_id"`
	ScopeID              string     `gorm:"size:36;not null;default:'';index:idx_release_version_scope" json:"scope_id"`
	EnvironmentKey       string     `gorm:"size:191;not null" json:"environment_key"`
	PipelineKey          string     `gorm:"size:191;not null" json:"pipeline_key"`
	Version              string     `gorm:"size:64;not null" json:"version"`
	CreationRequestID    string     `gorm:"size:64;uniqueIndex:uk_release_request;not null" json:"creation_request_id"`
	RequestFingerprint   string     `gorm:"size:64;not null" json:"request_fingerprint"`
	SourceFreezeRevision int64      `gorm:"not null" json:"source_freeze_revision"`
	EnvironmentName      string     `gorm:"size:191;not null;default:''" json:"environment_name"`
	PipelineName         string     `gorm:"size:191;not null;default:''" json:"pipeline_name"`
	Payload              string     `gorm:"type:text;not null" json:"payload"`
	PayloadSha256        string     `gorm:"size:64;not null" json:"payload_sha256"`
	ManifestSha256       string     `gorm:"size:64;not null" json:"manifest_sha256"`
	ConfigCount          int        `gorm:"not null;default:0" json:"config_count"`
	AssetCount           int        `gorm:"not null;default:0" json:"asset_count"`
	AssetBytes           int64      `gorm:"not null;default:0" json:"asset_bytes"`
	ExternalReferences   string     `gorm:"type:text;not null;default:'[]'" json:"external_references"`
	Reason               string     `gorm:"size:500;not null" json:"reason"`
	Actor                string     `gorm:"size:64;not null" json:"actor"`
	CreatedAt            time.Time  `json:"created_at"`
	DeletedAt            *time.Time `gorm:"index" json:"deleted_at"`
	DeletedBy            string     `gorm:"size:64;not null;default:''" json:"deleted_by"`
	DeleteReason         string     `gorm:"size:500;not null;default:''" json:"delete_reason"`
}

func (ConfigReleaseVersion) TableName() string { return "config_release_version" }

// ConfigReleaseAsset 是快照内资源的清单行;存储相对键由服务端 ID 推导,不保存用户绝对路径。
type ConfigReleaseAsset struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	SnapshotID   string    `gorm:"uniqueIndex:uk_release_asset,priority:1;uniqueIndex:uk_release_asset_source,priority:1;size:64;not null" json:"snapshot_id"`
	AssetID      string    `gorm:"uniqueIndex:uk_release_asset,priority:2;size:64;not null" json:"asset_id"`
	SourceFileID string    `gorm:"uniqueIndex:uk_release_asset_source,priority:2;size:64;not null" json:"source_file_id"`
	FileName     string    `gorm:"size:255;not null" json:"file_name"`
	ContentType  string    `gorm:"size:191;not null;default:''" json:"content_type"`
	Size         int64     `gorm:"not null" json:"size"`
	Sha256       string    `gorm:"size:64;not null" json:"sha256"`
	CreatedAt    time.Time `json:"created_at"`
}

func (ConfigReleaseAsset) TableName() string { return "config_release_asset" }

// ConfigReleaseHead 是范围唯一的生效指针;revision 与指针同事务更新。
type ConfigReleaseHead struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	ScopeID           string     `gorm:"size:36;not null;default:'';index:idx_release_head_scope_id" json:"scope_id"`
	EnvironmentKey    string     `gorm:"size:191;not null" json:"environment_key"`
	PipelineKey       string     `gorm:"size:191;not null" json:"pipeline_key"`
	ActiveSnapshotID  string     `gorm:"size:64;not null;default:''" json:"active_snapshot_id"`
	Revision          int64      `gorm:"not null;default:0" json:"revision"`
	LastVersionNumber int64      `gorm:"not null;default:0" json:"last_version_number"`
	ChangedAt         *time.Time `json:"changed_at"`
	ChangedBy         string     `gorm:"size:64;not null;default:''" json:"changed_by"`
	ChangedReason     string     `gorm:"size:500;not null;default:''" json:"changed_reason"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (ConfigReleaseHead) TableName() string { return "config_release_head" }

// ConfigReleaseEvent 是追加式创建／切换审计;唯一 (event_type, request_id) 支撑幂等确认。
type ConfigReleaseEvent struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	EventType          string    `gorm:"uniqueIndex:uk_release_event_request,priority:1;size:16;not null" json:"event_type"`
	RequestID          string    `gorm:"uniqueIndex:uk_release_event_request,priority:2;size:64;not null" json:"request_id"`
	RequestFingerprint string    `gorm:"size:64;not null" json:"request_fingerprint"`
	ScopeID            string    `gorm:"size:36;not null;default:'';index:idx_release_event_scope_id" json:"scope_id"`
	EnvironmentKey     string    `gorm:"index:idx_release_event_scope,priority:1;size:191;not null" json:"environment_key"`
	PipelineKey        string    `gorm:"index:idx_release_event_scope,priority:2;size:191;not null" json:"pipeline_key"`
	SnapshotID         string    `gorm:"size:64;not null" json:"snapshot_id"`
	Version            string    `gorm:"size:64;not null" json:"version"`
	FromActiveSnapshot string    `gorm:"size:64;not null;default:''" json:"from_active_snapshot_id"`
	ToActiveSnapshot   string    `gorm:"size:64;not null;default:''" json:"to_active_snapshot_id"`
	FromHeadRevision   int64     `gorm:"not null;default:0" json:"from_head_revision"`
	ToHeadRevision     int64     `gorm:"not null;default:0" json:"to_head_revision"`
	Reason             string    `gorm:"size:500;not null" json:"reason"`
	Actor              string    `gorm:"size:64;not null" json:"actor"`
	CreatedAt          time.Time `json:"created_at"`
}

func (ConfigReleaseEvent) TableName() string { return "config_release_event" }

// 初版边界(小团队同步处理)。
const (
	ReleaseMaxConfigs     = 5000
	ReleaseMaxPayloadByte = 64 << 20
	ReleaseMaxAssets      = 500
	ReleaseMaxAssetByte   = 32 << 20
	ReleaseMaxTotalByte   = 100 << 20
)
