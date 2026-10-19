package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
)

// ZIP and TAR share the same field and content representation.
func archiveConfigData(configs []model.Config) []map[string]any {
	result := make([]map[string]any, 0, len(configs))
	for _, cfg := range configs {
		var content any = cfg.Content
		switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
		case "object", "keyvalue", "json", "config", "kv", "key-value", "键值对":
			var parsed any
			if json.Unmarshal([]byte(cfg.Content), &parsed) == nil {
				content = parsed
			}
		}
		result = append(result, map[string]any{
			"resource_key": cfg.ResourceKey, "alias": cfg.Alias, "name": cfg.Name,
			"environment_key": cfg.EnvironmentKey, "pipeline_key": cfg.PipelineKey,
			"type": cfg.Type, "remark": cfg.Remark, "is_perm": cfg.IsPerm,
			"description": cfg.Description, "content": content,
		})
	}
	return result
}

// Archived objects are converted back to the string content API protocol.
func decodeArchiveConfigs(data []byte) ([]*common.ResourceConfig, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	result := make([]*common.ResourceConfig, 0, len(rows))
	for i, row := range rows {
		if string(row) == "null" {
			return nil, fmt.Errorf("config %d: payload required", i+1)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(row, &fields); err != nil {
			return nil, err
		}
		if raw, ok := fields["content"]; ok {
			var content string
			if err := json.Unmarshal(raw, &content); err != nil {
				var metadata struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(row, &metadata); err != nil {
					return nil, err
				}
				switch normalizeConfigTypeString(metadata.Type) {
				case "object", "keyvalue":
					fields["content"], _ = json.Marshal(string(raw))
				default:
					return nil, fmt.Errorf("config %d: content must be a string", i+1)
				}
			}
		}
		normalized, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		var cfg common.ResourceConfig
		if err := json.Unmarshal(normalized, &cfg); err != nil {
			return nil, err
		}
		result = append(result, &cfg)
	}
	return result, nil
}
