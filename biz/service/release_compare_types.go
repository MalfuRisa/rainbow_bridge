package service

// ReleaseCompareInput is the service input for POST /release/compare.
type ReleaseCompareInput struct {
	EnvironmentKey             string
	PipelineKey                string
	ExpectedScopeID            string
	ComparisonMode             string
	Version                    string
	ExpectedSnapshotID         string
	BaselineVersion            string
	ExpectedBaselineSnapshotID string
	ExpectedActiveRevision     string
	ExpectedActiveSnapshotID   string
	Offset                     int
	Limit                      int
}

type releaseCompareVersion struct {
	Version    string
	SnapshotID string
}

// ReleaseCompareSummary counts every configuration key in both snapshots.
type ReleaseCompareSummary struct {
	Added     int
	Modified  int
	Removed   int
	Unchanged int
}

// ReleaseCompareSide is the table summary; full content stays in release/detail.
type ReleaseCompareSide struct {
	Alias string
	Name  string
	Type  string
}

// ReleaseCompareItem is one changed configuration.
type ReleaseCompareItem struct {
	ResourceKey   string
	ChangeType    string
	ChangedFields []string
	Before        *ReleaseCompareSide
	After         *ReleaseCompareSide
}

// ReleaseCompareResult is the paginated comparison result.
type ReleaseCompareResult struct {
	ScopeID        string
	ComparisonMode string
	Target         releaseCompareVersion
	Baseline       *releaseCompareVersion
	ActiveRevision int64
	TargetIsActive bool
	Summary        ReleaseCompareSummary
	Items          []ReleaseCompareItem
	TotalChanges   int
	Offset         int
	Limit          int
	HasMore        bool
	NextOffset     *int
}
