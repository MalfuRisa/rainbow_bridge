package service

// 发布版本的内部类型与常量。HTTP 层负责把 ID／revision 转成十进制字符串。

const (
	releaseListDefaultLimit = 20
	releaseListMaxLimit     = 100
)

// releaseVersionPattern 与保留字:版本号为 1—64 个小写 ASCII 字符。
const (
	releaseVersionReservedActive  = "active"
	releaseVersionReservedCurrent = "current"
	releaseVersionReservedLatest  = "latest"
)

// ReleaseActiveInfo 是一个范围的生效指针快照。
type ReleaseActiveInfo struct {
	SnapshotID    string
	Version       string
	Revision      int64
	ChangedAt     string // RFC3339 UTC;未设置时为空串
	ChangedBy     string
	ChangedReason string
}

// ReleaseVersionMeta 是版本元数据(不含 payload 正文)。
type ReleaseVersionMeta struct {
	SnapshotID           string
	EnvironmentKey       string
	PipelineKey          string
	Version              string
	EnvironmentName      string
	PipelineName         string
	SourceFreezeRevision int64
	CreationRequestID    string
	ConfigCount          int
	AssetCount           int
	AssetBytes           int64
	PayloadSha256        string
	ManifestSha256       string
	ExternalReferences   []string
	Reason               string
	Actor                string
	CreatedAt            string // RFC3339 UTC
}

// ReleaseVersionListItem 是列表项。
type ReleaseVersionListItem struct {
	Meta     ReleaseVersionMeta
	IsActive bool
}

// ReleasePage 是版本列表页。
type ReleasePage struct {
	Items        []ReleaseVersionListItem
	HasMore      bool
	NextBeforeID uint
	Active       *ReleaseActiveInfo
}

// ReleaseCreateResult 是创建操作结果。
type ReleaseCreateResult struct {
	Meta     ReleaseVersionMeta
	Replayed bool
}

// ReleaseActivateResult 是切换操作结果(operation 与此刻 active 分开表达)。
type ReleaseActivateResult struct {
	RequestID       string
	Replayed        bool
	AppliedRevision int64
	Active          *ReleaseActiveInfo
	Version         string
}

// ReleaseEventItem 是历史查询单项。
type ReleaseEventItem struct {
	EventID      uint
	EventType    string
	RequestID    string
	SnapshotID   string
	Version      string
	FromActiveID string
	ToActiveID   string
	FromRevision int64
	ToRevision   int64
	Reason       string
	Actor        string
	CreatedAt    string
}

// ReleaseHistoryPage 是历史页。
type ReleaseHistoryPage struct {
	Items             []ReleaseEventItem
	HasMore           bool
	NextBeforeEventID uint
}

// ReleaseResourceIssue 是创建预检的问题项。
type ReleaseResourceIssue struct {
	ResourceKey string
	Alias       string
	FileID      string
	Reason      string // missing_metadata / file_missing / cross_scope_reference / unresolvable_reference / external_reference / internal_marker
}

// ReleaseLimitInfo 描述一次限额超界。
type ReleaseLimitInfo struct {
	Item   string
	Limit  int64
	Actual int64
}

// releasePayloadScope 是快照内的范围信息(保存时的名称)。
type releasePayloadScope struct {
	EnvironmentKey  string `json:"environment_key"`
	PipelineKey     string `json:"pipeline_key"`
	EnvironmentName string `json:"environment_name"`
	PipelineName    string `json:"pipeline_name"`
}

// releasePayloadConfig 是快照内的配置;受管引用以 release-asset://<asset_id> 标记保存。
type releasePayloadConfig struct {
	ResourceKey string `json:"resource_key"`
	Alias       string `json:"alias"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	Remark      string `json:"remark"`
	IsPerm      bool   `json:"is_perm"`
	Description string `json:"description"`
}

// releasePayload 是写入配置的确定性有序结构(字段顺序固定,不使用 map)。
type releasePayload struct {
	Scope   releasePayloadScope    `json:"scope"`
	Configs []releasePayloadConfig `json:"configs"`
}

// ReleaseRuntimeConfig 是版本 runtime 的组装结果。
type ReleaseRuntimeConfig struct {
	SnapshotID           string
	Version              string
	SourceFreezeRevision int64
	PayloadSha256        string
	SelectionMode        string
	EnvironmentKey       string
	EnvironmentName      string
	PipelineKey          string
	PipelineName         string
	Configs              []releaseRuntimeConfig
	ActiveRevision       *int64
}

// releaseRuntimeConfig 与既有 runtime 的 content 形态一致(由 handler 决定 JSON 呈现)。
type releaseRuntimeConfig struct {
	ResourceKey string
	Alias       string
	Name        string
	Type        string
	Content     string
	Remark      string
	IsPerm      bool
	Description string
}
