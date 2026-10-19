package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// 写协调的设计期限:整次写操作 60 秒,取得协调最多等待 10 秒。
// 变量而非常量:仅测试可缩短等待以确定性验证 503 语义(生产保持默认值)。
var (
	writeGateTotalTimeout   = 60 * time.Second
	writeGateAcquireTimeout = 10 * time.Second
)

// withWriteGate 以数据库协调行为唯一写入口:
//  1. 有界等待连接并开启事务(acquire 超时 → 503);
//  2. 事务的第一条语句 UPDATE config_write_gate(id=1),在 PostgreSQL/MySQL 取行锁、在 SQLite 取事务写锁;
//  3. 协调存续期内执行 fn,数据库写入必须使用传入的 txLogic/tx,整体受 60s 总期限约束;
//  4. fn 返回错误或超时则整体回滚(审计写失败则状态不变)。
func (s *Service) withWriteGate(ctx context.Context, fn func(ctx context.Context, txLogic *Logic, tx *gorm.DB) error) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, writeGateTotalTimeout)
		defer cancel()
	}
	// 阶段一:有界等待并独占一个连接(单连接池 SQLite 下并发写在此排队;等待超时 → 503)。
	// gorm 的 Connection 会在回调结束后归还连接,且获取过程受上下文限制。
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, writeGateAcquireTimeout)
	defer cancelAcquire()
	entered := false
	err := s.logic.db.WithContext(acquireCtx).Connection(func(connDB *gorm.DB) error {
		entered = true
		// 阶段二:已独占连接,事务生命周期改用总期限上下文
		tx := connDB.Session(&gorm.Session{Context: ctx}).Begin()
		if tx.Error != nil {
			return classifyGateAcquireError(tx.Error)
		}
		// 事务的第一条语句取得数据库写协调
		if err := s.logic.writeGateDAO.TouchGate(ctx, tx); err != nil {
			_ = tx.Rollback().Error
			return classifyGateAcquireError(err)
		}
		if err := fn(ctx, s.logic.withTx(tx), tx); err != nil {
			_ = tx.Rollback().Error
			return classifyWriteError(err)
		}
		if err := tx.Commit().Error; err != nil {
			_ = tx.Rollback().Error
			return classifyWriteError(err)
		}
		return nil
	})
	if err != nil && !entered {
		// 连接获取/等待阶段的失败归类为 503 等待不可用
		return classifyGateAcquireError(err)
	}
	return err
}

// classifyGateAcquireError 把"取得写协调"阶段的失败归类为 503 等待不可用或内部错误。
func classifyGateAcquireError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return freezeGateBusyError()
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") {
		return freezeGateBusyError()
	}
	return freezeInternalError(err)
}

// classifyWriteError 保留冻结错误与业务错误;超时/取消归类为 503,其余原样上抛。
func classifyWriteError(err error) error {
	if _, ok := AsFreezeError(err); ok {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return freezeWriteTimeoutError()
	}
	return err
}

// withTx 返回使用同一事务连接的 Logic 副本;内部 helper 复用该连接,不再另开 l.db 事务。
func (l *Logic) withTx(tx *gorm.DB) *Logic {
	scoped := *l
	scoped.db = tx
	scoped.inTx = true
	return &scoped
}

// freezeStateOrUnlocked 读取范围状态;状态行不存在时视为初始 unlocked(revision 0)。
func (l *Logic) freezeStateOrUnlocked(ctx context.Context, environmentKey, pipelineKey string) (model.ConfigFreezeState, error) {
	state, err := l.freezeDAO.GetState(ctx, l.db, environmentKey, pipelineKey)
	if err != nil {
		if db.IsStateMissing(err) {
			return model.ConfigFreezeState{
				EnvironmentKey: environmentKey,
				PipelineKey:    pipelineKey,
				State:          model.FreezeStateUnlocked,
				Revision:       0,
			}, nil
		}
		return model.ConfigFreezeState{}, err
	}
	return *state, nil
}

// ensureScopesUnlocked 检查全部目标范围;任一 locked 即返回 409 SCOPE_FROZEN 并列出实际受阻范围。
// 必须在 withWriteGate 事务内调用(协调已持有)。
func (l *Logic) ensureScopesUnlocked(ctx context.Context, scopes []FreezeScopeRef) error {
	blocked := make([]FreezeBlockedScope, 0, len(scopes))
	seen := make(map[FreezeScopeRef]bool, len(scopes))
	for _, scope := range scopes {
		environmentKey := strings.TrimSpace(scope.EnvironmentKey)
		pipelineKey := strings.TrimSpace(scope.PipelineKey)
		if environmentKey == "" || pipelineKey == "" {
			continue
		}
		ref := FreezeScopeRef{EnvironmentKey: environmentKey, PipelineKey: pipelineKey}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		state, err := l.freezeStateOrUnlocked(ctx, environmentKey, pipelineKey)
		if err != nil {
			return err
		}
		if state.State == model.FreezeStateLocked {
			blocked = append(blocked, FreezeBlockedScope{
				EnvironmentKey: environmentKey,
				PipelineKey:    pipelineKey,
				State:          state.State,
				Revision:       state.Revision,
			})
		}
	}
	if len(blocked) > 0 {
		return scopeFrozenError(blocked)
	}
	return nil
}

// ensurePipelineUnlocked 是单范围版本。
func (l *Logic) ensurePipelineUnlocked(ctx context.Context, environmentKey, pipelineKey string) error {
	return l.ensureScopesUnlocked(ctx, []FreezeScopeRef{{EnvironmentKey: environmentKey, PipelineKey: pipelineKey}})
}

// ensureEnvironmentMetadataUnlocked 用于更新/删除环境、以及导入环境元数据:
// 环境内存在 locked 渠道时整体拒绝。
func (l *Logic) ensureEnvironmentMetadataUnlocked(ctx context.Context, environmentKey string) error {
	states, err := l.freezeDAO.ListStatesByEnvironment(ctx, l.db, environmentKey)
	if err != nil {
		return err
	}
	blocked := make([]FreezeBlockedScope, 0, len(states))
	for _, state := range states {
		if state.State == model.FreezeStateLocked {
			blocked = append(blocked, FreezeBlockedScope{
				EnvironmentKey: state.EnvironmentKey,
				PipelineKey:    state.PipelineKey,
				State:          state.State,
				Revision:       state.Revision,
			})
		}
	}
	if len(blocked) > 0 {
		return scopeFrozenError(blocked)
	}
	return nil
}

// ensurePipelineStateExists 在新建渠道后补建初始 unlocked 状态行(幂等)。
func (l *Logic) ensurePipelineStateExists(ctx context.Context, environmentKey, pipelineKey string) error {
	return l.freezeDAO.EnsureState(ctx, l.db, environmentKey, pipelineKey)
}
