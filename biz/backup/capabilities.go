package backup

import (
	"encoding/json"

	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// Capabilities is the client-visible support statement. It never contains
// absolute server paths.
type Capabilities struct {
	Supported              bool                  `json:"supported"`
	Reasons                []string              `json:"reasons"`
	Limits                 LimitView             `json:"limits"`
	RestoreRequiresRestart bool                  `json:"restore_requires_restart"`
	Mode                   string                `json:"mode"`
	RollbackRetention      RollbackRetentionView `json:"rollback_retention"`
}

// RollbackRetentionView explains automatic successful-copy retention without
// exposing control-directory paths.
type RollbackRetentionView struct {
	KeepCount int  `json:"keep_count"`
	Managed   bool `json:"managed"`
}

func rollbackRetentionView(cfg *config.Config) RollbackRetentionView {
	keep := config.DefaultBackupRollbackKeepCount
	if cfg != nil {
		if normalized, err := config.NormalizeBackupRollbackKeepCount(cfg.Backup.RollbackKeepCount); err == nil {
			keep = normalized
		}
	}
	return RollbackRetentionView{KeepCount: keep, Managed: true}
}

// CapabilitiesFor evaluates the current deployment without mutating it.
func CapabilitiesFor(cfg *config.Config, limits Limits) Capabilities {
	if cfg == nil {
		return Capabilities{
			Supported: false, Reasons: []string{"backup_disabled"},
			Limits: limits.View(), RestoreRequiresRestart: true, Mode: "business_full",
			RollbackRetention: rollbackRetentionView(nil),
		}
	}
	reasons := unsupportedReasons(cfg)
	if cfg.Backup.Enabled {
		if _, layoutReasons := ResolveLayout(cfg); len(layoutReasons) > 0 {
			reasons = layoutReasons
		}
	}
	if reasons == nil {
		reasons = []string{}
	}
	return Capabilities{
		Supported:              len(reasons) == 0 && cfg.Backup.Enabled,
		Reasons:                reasons,
		Limits:                 limits.View(),
		RestoreRequiresRestart: true,
		Mode:                   "business_full",
		RollbackRetention:      rollbackRetentionView(cfg),
	}
}

func jsonMarshalIndent(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}
