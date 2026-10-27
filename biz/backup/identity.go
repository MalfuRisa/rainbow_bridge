package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type identityRecord struct {
	InstanceID string `json:"instance_id"`
	CreatedAt  string `json:"created_at"`
}

// loadOrCreateIdentity keeps the target instance id inside the control
// directory. A damaged identity file is never replaced silently, because the
// restore execute endpoint binds a preview to this value.
func loadOrCreateIdentity(controlDir string) (string, error) {
	path := filepath.Join(controlDir, "identity.json")
	payload, err := os.ReadFile(path)
	if err == nil {
		var record identityRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return "", err
		}
		if _, err := uuid.Parse(record.InstanceID); err != nil {
			return "", err
		}
		return record.InstanceID, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	record := identityRecord{
		InstanceID: uuid.NewString(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, encoded, 0o600); err != nil {
		return "", err
	}
	return record.InstanceID, nil
}
