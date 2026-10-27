package router

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/middleware"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A11:按 IDL 枚举全部已声明路由,逐条发起真实请求,
// 断言管理路由必须要求会话(含伪造身份头),公开路由不因鉴权改造被锁住。
func TestDeclaredRoutesRequireSessionOrArePublic(t *testing.T) {
	t.Chdir(t.TempDir())
	engine := newRouteTestEngine(t)
	routes := parseDeclaredRoutes(t)
	if len(routes) < 25 {
		t.Fatalf("expected the IDL to declare a meaningful number of routes, got %d", len(routes))
	}

	management := 0
	for _, route := range routes {
		path := strings.ReplaceAll(route.path, "*filepath", "sample/id")
		path = strings.ReplaceAll(path, ":file_id", "sample")

		switch route.bucket() {
		case bucketPublic:
			response := ut.PerformRequest(engine.Engine, route.method, path, nil)
			if response.Code == 401 {
				t.Fatalf("public route must stay readable without a session: %s %s -> %d %s",
					route.method, route.path, response.Code, response.Body.String())
			}
		case bucketAuth:
			// auth 接口自带会话规则:只要求是 JSON 契约,不吞成 5xx
			response := ut.PerformRequest(engine.Engine, route.method, path, nil)
			if response.Code >= 500 {
				t.Fatalf("auth route must answer with a contract error, got %d %s", response.Code, response.Body.String())
			}
			if route.path == "/api/v1/auth/me" && response.Code != 401 {
				t.Fatalf("me without a session must be 401, got %d", response.Code)
			}
		default:
			management++
			response := ut.PerformRequest(engine.Engine, route.method, path, nil)
			if response.Code != 401 && response.Code != 403 {
				t.Fatalf("management route must require a session: %s %s -> %d %s",
					route.method, route.path, response.Code, response.Body.String())
			}
			// 伪造身份头与旧式 Bearer 都不能放行
			forged := ut.PerformRequest(engine.Engine, route.method, path, nil,
				ut.Header{Key: "X-User-Id", Value: "1"},
				ut.Header{Key: "X-User-Role", Value: "admin"},
				ut.Header{Key: "Authorization", Value: "Bearer forged-management-token"},
			)
			if forged.Code != 401 && forged.Code != 403 {
				t.Fatalf("forged identity must not pass: %s %s -> %d %s",
					route.method, route.path, forged.Code, forged.Body.String())
			}
		}
	}
	if management < 20 {
		t.Fatalf("expected at least 20 management routes, enumerated %d", management)
	}
	t.Logf("enumerated %d routes (%d management)", len(routes), management)
}

type routeBucket int

const (
	bucketManagement routeBucket = iota
	bucketPublic
	bucketAuth
)

type declaredRoute struct {
	method string
	path   string
}

// bucket 判定该路由属于公开、auth 自带规则还是管理范围。
func (r declaredRoute) bucket() routeBucket {
	if strings.HasPrefix(r.path, "/api/v1/auth/") {
		return bucketAuth
	}
	if middleware.IsPublicRequest(r.method, r.path) {
		return bucketPublic
	}
	return bucketManagement
}

var declaredRouteRegexp = regexp.MustCompile(`option\s+\(api\.(get|post)\)\s*=\s*"([^"]+)"`)

// repoRootForTest 从本测试文件的编译期路径推导仓库根(t.Chdir 后相对路径不可用)。
func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// parseDeclaredRoutes 从 IDL 读取全部 HTTP 路由声明。
func parseDeclaredRoutes(t *testing.T) []declaredRoute {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRootForTest(t), "idl", "biz", "*.proto"))
	if err != nil || len(files) == 0 {
		t.Fatalf("read IDL routes: %v (%d files)", err, len(files))
	}
	routes := make([]declaredRoute, 0, 64)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range declaredRouteRegexp.FindAllStringSubmatch(string(content), -1) {
			routes = append(routes, declaredRoute{method: strings.ToUpper(match[1]), path: match[2]})
		}
	}
	return routes
}

// newRouteTestEngine 装配最小可运行的引擎:真实中间件 + 生成路由 + 空库服务。
func newRouteTestEngine(t *testing.T) *server.Hertz {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "routes.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := conn.AutoMigrate(
		&model.Config{}, &model.Asset{}, &model.Environment{}, &model.Pipeline{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{},
		&model.ConfigReleaseVersion{}, &model.ConfigReleaseAsset{}, &model.ConfigReleaseHead{}, &model.ConfigReleaseEvent{},
		&model.AuthUser{}, &model.AuthSession{},
	); err != nil {
		t.Fatal(err)
	}
	svc := service.NewService(conn, nil, "", nil)
	svc.SetAuthSettings(service.AuthSettings{
		CookieName:      "rb_admin_auth_dev",
		IdleTimeout:     2,
		AbsoluteTimeout: 8,
		TrustedOrigins:  []string{"http://127.0.0.1:18081"},
	})
	if err := svc.EnsureFreezeBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	InitHandlers(svc)
	engine := server.New()
	engine.Use(middleware.AdminSession(svc, ""))
	GeneratedRegister(engine)
	return engine
}
