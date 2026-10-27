package backup

import "time"

// Limits are the fixed first-version ceilings. Tests may inject a smaller
// Limits value; the production HTTP surface cannot change them.
type Limits struct {
	MaxPackageBytes        int64
	MaxExtractedBytes      int64
	MaxFiles               int
	MaxFileBytes           int64
	MaxManifestBytes       int64
	CreateTimeout          time.Duration
	ValidateTimeout        time.Duration
	PrepareTimeout         time.Duration
	MaintenanceWait        time.Duration
	MaxBackups             int
	MaxTerminalJobs        int
	MaxConcurrentDownloads int
	SpaceReserveBytes      int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxPackageBytes:        2 << 30,
		MaxExtractedBytes:      10 << 30,
		MaxFiles:               50000,
		MaxFileBytes:           2 << 30,
		MaxManifestBytes:       16 << 20,
		CreateTimeout:          10 * time.Minute,
		ValidateTimeout:        10 * time.Minute,
		PrepareTimeout:         10 * time.Minute,
		MaintenanceWait:        30 * time.Second,
		MaxBackups:             10,
		MaxTerminalJobs:        100,
		MaxConcurrentDownloads: 2,
		SpaceReserveBytes:      256 << 20,
	}
}

// LimitView is the stable JSON shape returned by GET /backup/capabilities.
type LimitView struct {
	PackageBytes      int64 `json:"package_bytes"`
	ExtractedBytes    int64 `json:"extracted_bytes"`
	Files             int   `json:"files"`
	FileBytes         int64 `json:"file_bytes"`
	ManifestBytes     int64 `json:"manifest_bytes"`
	MaintenanceWaitMS int64 `json:"maintenance_wait_ms"`
	MaxBackups        int   `json:"max_backups"`
	MaxTerminalJobs   int   `json:"max_terminal_jobs"`
}

func (l Limits) View() LimitView {
	return LimitView{
		PackageBytes:      l.MaxPackageBytes,
		ExtractedBytes:    l.MaxExtractedBytes,
		Files:             l.MaxFiles,
		FileBytes:         l.MaxFileBytes,
		ManifestBytes:     l.MaxManifestBytes,
		MaintenanceWaitMS: l.MaintenanceWait.Milliseconds(),
		MaxBackups:        l.MaxBackups,
		MaxTerminalJobs:   l.MaxTerminalJobs,
	}
}
