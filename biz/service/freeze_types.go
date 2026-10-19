package service

// 冻结范围、状态与事件的内部数据类型。
// HTTP 层负责把 revision/事件 ID 转成十进制字符串。

const (
	FreezeEventLock   = "lock"
	FreezeEventUnlock = "unlock"

	// FreezeAdminActor 是共享管理身份,不冒称真实个人。
	FreezeAdminActor = "freeze-admin"

	freezeReasonMax = 500

	freezeHistoryDefaultLimit = 20
	freezeHistoryMaxLimit     = 100
)

// FreezeScopeRef 表示一个冻结范围。
type FreezeScopeRef struct {
	EnvironmentKey string
	PipelineKey    string
}

// FreezeStatusData 是状态查询与锁定/解锁操作的提交结果。
type FreezeStatusData struct {
	EnvironmentKey  string
	PipelineKey     string
	State           string
	Revision        int64
	LastChangedAt   string // RFC3339 UTC;初始为空串
	LastChangedBy   string
	LastReason      string
	AdminConfigured bool
}

// FreezeEventData 是历史查询的单项。
type FreezeEventData struct {
	EventID   uint
	EventType string
	FromState string
	ToState   string
	Revision  int64
	Reason    string
	Actor     string
	CreatedAt string
}

// FreezeHistoryData 是按事件 ID 倒序的历史页。
type FreezeHistoryData struct {
	Items             []FreezeEventData
	HasMore           bool
	NextBeforeEventID uint
}

// FreezeResourceIssue 是锁定前资源检查发现的问题项。
type FreezeResourceIssue struct {
	ResourceKey string // 配置 resource_key(可能为空)
	Alias       string
	FileID      string
	Reason      string // missing_metadata / file_missing / cross_scope_reference / unresolvable_reference
}

// FreezeLockPrecheck 汇总锁定前资源检查结果。
type FreezeLockPrecheck struct {
	Issues             []FreezeResourceIssue
	ExternalReferences []string
}
