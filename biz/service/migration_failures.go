package service

import (
	"errors"
	"strings"
)

// 迁移失败类别(稳定标识,供逐项 message 与测试定位;不随底层错误文本变化)。
const (
	migrationFailureAssetNotFound       = "asset_not_found"
	migrationFailureAssetScopeMismatch  = "asset_scope_mismatch"
	migrationFailureAssetFileMissing    = "asset_file_missing"
	migrationFailureAssetReadFailed     = "asset_read_failed"
	migrationFailureAssetWriteFailed    = "asset_write_failed"
	migrationFailureAssetVerifyFailed   = "asset_verify_failed"
	migrationFailureContentUnparseable  = "content_unparseable"
	migrationFailureInternalMarker      = "internal_marker_unsupported"
	migrationFailureReferenceUnmapped   = "reference_unmapped"
	migrationFailureReferenceUnresolved = "reference_unresolved"
	migrationFailureConfigWriteFailed   = "config_write_failed"
	migrationFailureReadbackFailed      = "readback_verify_failed"
	migrationFailureCleanupResidue      = "cleanup_residue"
)

// migrationFailure 是一次可安全展示的迁移失败:定位信息(alias/file_id)+ 稳定类别。
// 原始错误只作为日志线索保留在 cause 中,不进入返回给调用方的消息,
// 避免把服务器绝对路径、连接信息或密钥带进 items[].message。
type migrationFailure struct {
	alias  string
	fileID string
	kind   string
	cause  error
}

func (e *migrationFailure) Error() string {
	parts := make([]string, 0, 3)
	if e.alias != "" {
		parts = append(parts, "alias="+e.alias)
	}
	if e.fileID != "" {
		parts = append(parts, "file_id="+e.fileID)
	}
	parts = append(parts, "类别="+e.kind)
	return "迁移失败: " + strings.Join(parts, " ")
}

func (e *migrationFailure) Unwrap() error { return e.cause }

// withMigrationAlias 为可展示失败补充配置 alias;非迁移失败统一归入 config_write_failed。
func withMigrationAlias(err error, alias string) error {
	if err == nil {
		return nil
	}
	var failure *migrationFailure
	if errors.As(err, &failure) {
		if failure.alias == "" {
			failure.alias = alias
		}
		return err
	}
	return &migrationFailure{alias: alias, kind: migrationFailureConfigWriteFailed, cause: err}
}

// migrationItemMessage 组装逐项失败消息:定位/类别 + 清理残留提示(如有)。
func migrationItemMessage(err error, cleanupNotes []string) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, note := range cleanupNotes {
		message += ";" + note
	}
	return message
}
