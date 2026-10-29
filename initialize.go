package main

import (
	"context"
	"log"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	hconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/redis/go-redis/v9"
	"github.com/yi-nology/rainbow_bridge/biz/backup"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	versionhandler "github.com/yi-nology/rainbow_bridge/biz/handler/version"
	"github.com/yi-nology/rainbow_bridge/biz/middleware"
	bizrouter "github.com/yi-nology/rainbow_bridge/biz/router"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	appconfig "github.com/yi-nology/rainbow_bridge/pkg/config"
	"github.com/yi-nology/rainbow_bridge/pkg/database"
	"github.com/yi-nology/rainbow_bridge/pkg/lock"
	"github.com/yi-nology/rainbow_bridge/pkg/logging"
	appredis "github.com/yi-nology/rainbow_bridge/pkg/redis"
)

// LoadConfig loads the configuration from the given path
func LoadConfig(configPath string) (*appconfig.Config, error) {
	return appconfig.Load(configPath)
}

// BuildConfig contains build-time information
type BuildConfig struct {
	Version   string
	GitCommit string
	BuildTime string
	BasePath  string
}

// Application contains the initialized application components
type Application struct {
	H        *server.Hertz
	Config   *appconfig.Config
	Service  *service.Service
	BasePath string
}

// Initialize initializes the application with the given configuration.
// backupInstance is nil when the feature is disabled or unsupported.
func Initialize(configPath string, buildConfig BuildConfig, backupInstance *backup.Instance) (application *Application, err error) {
	// Load configuration
	cfg, err := appconfig.Load(configPath)
	if err != nil {
		return nil, err
	}

	// Initialize logging
	if err := logging.Initialize(&cfg.Log); err != nil {
		return nil, err
	}

	// Open database connection
	db, err := database.Open(cfg.Database)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			if sqlDB, dbErr := db.DB(); dbErr == nil {
				_ = sqlDB.Close()
			}
		}
	}()

	// Auto migrate database tables
	if err := db.AutoMigrate(
		&model.Config{}, &model.Asset{}, &model.Environment{}, &model.Pipeline{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{},
		&model.ConfigReleaseVersion{}, &model.ConfigReleaseAsset{}, &model.ConfigReleaseHead{}, &model.ConfigReleaseEvent{},
		&model.ReleaseStorageAttempt{},
		&model.ScopeLifecycleEvent{},
		&model.AuthUser{}, &model.AuthSession{},
	); err != nil {
		return nil, err
	}

	if err := configdb.BackfillConfigRevisions(context.Background(), db); err != nil {
		return nil, err
	}

	// Migrate existing configs with default environment_key and pipeline_key
	// before resolving their scope identities.
	if err := service.MigrateConfigDefaults(db); err != nil {
		return nil, err
	}

	// Assign stable environment/scope identities and replace legacy key
	// uniqueness with generation-aware constraints.
	if err := configdb.MigrateScopeIdentity(context.Background(), db); err != nil {
		return nil, err
	}

	// Seed only a never-initialized database; deleted defaults stay deleted.
	if err := service.EnsureSystemDefaults(context.Background(), db); err != nil {
		return nil, err
	}

	// Backfill per-scope release counters after scopes and seeds exist.
	if err := configdb.EnsureReleaseNumbering(context.Background(), db); err != nil {
		return nil, err
	}

	if err := database.PrepareConfigAliasConstraint(context.Background(), db); err != nil {
		return nil, err
	}

	// Initialize Redis client (optional)
	var redisClient *redis.Client
	if cfg.Redis.Enabled {
		var redisErr error
		redisClient, redisErr = appredis.NewClient(cfg.Redis)
		if redisErr != nil {
			return nil, redisErr
		}
		// Note: We don't defer close here because the service will use it throughout its lifecycle
		writeLock := lock.New(redisClient, "rainbow_bridge:write_lock", 30*time.Second, 60*time.Second)
		middleware.InitWriteLock(writeLock)
		log.Printf("Redis distributed write lock enabled (%s)", cfg.Redis.Address)
	}

	// Use build-time injected BasePath
	basePath := appconfig.NormalizeBasePath(buildConfig.BasePath)
	maxBodyBytes := backup.DefaultLimits().MaxPackageBytes + (64 << 20)
	if maxInt := int64(^uint(0) >> 1); maxBodyBytes > maxInt {
		maxBodyBytes = maxInt
	}
	opts := []hconfig.Option{
		server.WithHostPorts(cfg.Server.Address),
		// Backup uploads are streamed; ordinary handlers still read their
		// bounded bodies through the same request API.
		server.WithStreamBody(true),
		// Keep the raw multipart stream available to the backup uploader;
		// ordinary multipart handlers still read it through FormFile.
		server.WithDisablePreParseMultipartForm(true),
		server.WithMaxRequestBodySize(int(maxBodyBytes)),
	}
	if basePath != "" {
		opts = append(opts, server.WithBasePath(basePath))
	}
	h := server.Default(opts...)

	// Create service instance
	svc := service.NewService(db, redisClient, basePath, cfg)
	svc.SetProducer(buildConfig.Version, buildConfig.GitCommit)
	if backupInstance != nil && backupInstance.Manager != nil {
		backupInstance.Manager.AttachDatabase(db)
		svc.SetBackupManager(backupInstance.Manager)
	}

	// 管理员账号与会话必须在上线前就绪:缺首次密码即失败退出,已存在则不覆盖。
	authOrigins, err := appconfig.ValidateAuthConfig(cfg)
	if err != nil {
		return nil, err
	}
	idleTimeout, absoluteTimeout, err := appconfig.AuthTimeoutPair(cfg)
	if err != nil {
		return nil, err
	}
	svc.SetAuthSettings(service.AuthSettings{
		CookieName:      cfg.Auth.CookieName,
		CookieSecure:    appconfig.AuthCookieSecure(cfg),
		IdleTimeout:     idleTimeout,
		AbsoluteTimeout: absoluteTimeout,
		TrustedOrigins:  authOrigins,
		BasePath:        basePath,
	})
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		return nil, err
	}
	if err := svc.EnsureAuthSessionsReady(context.Background()); err != nil {
		return nil, err
	}

	// 冻结基础结构必须在 HTTP 开放前就绪:协调行 + 存量渠道补初始 unlocked 状态(幂等,不重置既有状态)。
	if err := svc.EnsureFreezeBootstrap(context.Background()); err != nil {
		return nil, err
	}

	// Set version information
	versionhandler.AppVersion = buildConfig.Version
	versionhandler.AppGitCommit = buildConfig.GitCommit
	versionhandler.AppBuildTime = buildConfig.BuildTime
	versionhandler.IsIntranet = cfg.Intranet.Enabled

	// Initialize handlers
	bizrouter.InitHandlers(svc)

	// Migrate to full asset paths
	if err := svc.MigrateToFullAssetPaths(context.Background()); err != nil {
		log.Printf("Warning: failed to migrate asset paths: %v", err)
	}

	// Register middleware
	h.Use(middleware.Recovery())
	h.Use(middleware.Logging())
	h.Use(middleware.CORS(&cfg.CORS, authOrigins, basePath))
	h.Use(middleware.Auth())
	// 管理接口统一鉴权:默认拒绝,公开范围在中间件内按方法 + 路由精确匹配
	h.Use(middleware.AdminSession(svc, basePath))
	// 备份/恢复维护期间拒绝新的业务写入(认证与备份接口本身保持可用)
	h.Use(middleware.BackupMaintenance(svc))

	// Register routes
	bizrouter.GeneratedRegister(h)

	return &Application{
		H:        h,
		Config:   cfg,
		Service:  svc,
		BasePath: basePath,
	}, nil
}
