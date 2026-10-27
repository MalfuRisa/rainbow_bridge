package service

import (
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

const (
	TransferSourceEditor  = "editor"
	TransferSourceActive  = "active"
	TransferSourceVersion = "version"

	transferArchiveFormatV2   = "rainbow-transfer-v2"
	transferExportTempPrefix  = "rainbow-transfer-"
	transferExportLimitBytes  = int64(192 << 20)
	transferExportDefaultTime = 60 * time.Second
)

// ExportRequest is the normalized input for preview and export.
type ExportRequest struct {
	Format                string
	Source                string
	EnvironmentKey        string
	PipelineKey           string
	ExpectedEnvironmentID string
	ExpectedScopeID       string
	Version               string
	ExpectedSnapshotID    string
	Selections            []*ExportSelection
}

// ExportSelection is one editor-mode selection.
type ExportSelection struct {
	EnvironmentKey        string
	PipelineKey           string
	ResourceKeys          []string
	ExpectedEnvironmentID string
	ExpectedScopeID       string
}

// ExportScopeRef identifies the source generation of one exported scope.
type ExportScopeRef struct {
	EnvironmentKey  string `json:"environment_key"`
	EnvironmentName string `json:"environment_name"`
	PipelineKey     string `json:"pipeline_key"`
	PipelineName    string `json:"pipeline_name"`
	EnvironmentID   string `json:"environment_id"`
	ScopeID         string `json:"scope_id"`
	Version         string `json:"version,omitempty"`
	SnapshotID      string `json:"snapshot_id,omitempty"`
}

type exportAssetData struct {
	metadata model.Asset
	data     []byte
	sha256   string
}

// ExportPlan is a fully resolved migration archive plan.
type ExportPlan struct {
	Source     string
	Format     string
	CapturedAt time.Time
	Scopes     []ExportScopeRef
	Configs    []model.Config
	Assets     []exportAssetData
	Warnings   []string
	Estimated  int64
	Version    string
	SnapshotID string
}

// ExportPreview is the read-only preview payload.
type ExportPreview struct {
	Source     string
	CapturedAt string
	Scopes     []ExportScopeRef
	Configs    int
	Assets     int
	Bytes      int64
	Warnings   []string
	Version    string
	SnapshotID string
}

// ExportResult is a generated temporary archive.
type ExportResult struct {
	TempPath   string
	FileName   string
	Source     string
	Version    string
	SnapshotID string
	Size       int64
}

// TransferExportError is the transfer-specific error surface.
type TransferExportError struct {
	HTTPStatus int
	Code       int
	ErrorType  string
	Message    string
}

func (e *TransferExportError) Error() string { return e.Message }
