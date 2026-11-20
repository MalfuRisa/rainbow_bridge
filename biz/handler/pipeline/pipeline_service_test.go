package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPipelineUpdateCompatibility(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "pipeline.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&model.Environment{}, &model.Pipeline{}, &model.ConfigWriteGate{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}); err != nil {
		t.Fatal(err)
	}
	instance := service.NewService(db, nil, "", nil)
	ctx := context.Background()
	if err := instance.EnsureFreezeBootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if err := instance.AddEnvironment(ctx, &envpb.Environment{EnvironmentKey: "dev", EnvironmentName: "Dev", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	initial, err := instance.GetPipeline(ctx, "dev", "default")
	if err != nil {
		t.Fatal(err)
	}
	scopeID := initial.GetScopeId()
	previous := svc
	SetService(instance)
	t.Cleanup(func() { SetService(previous) })
	engine := server.New()
	const route = "/api/v1/pipeline/update"
	engine.POST(route, Update)

	jsonBody := func(order string) string {
		return fmt.Sprintf(`{"environment_key":"dev","expected_scope_id":%q,"pipeline":{"pipeline_key":"default","pipeline_name":"renamed"%s}}`, scopeID, order)
	}
	form := url.Values{
		"environment_key": {"dev"}, "expected_scope_id": {scopeID},
		"pipeline": {`{"pipeline_key":"default","pipeline_name":"renamed","sort_order":19}`},
	}.Encode()
	binary, err := proto.Marshal(&plpb.UpdatePipelineRequest{EnvironmentKey: "dev", ExpectedScopeId: scopeID,
		Pipeline: &plpb.Pipeline{PipelineKey: "default", PipelineName: "renamed", SortOrder: 23}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, suffix, body, contentType string
		httpStatus, code                int
		order                           int32
		errorType                       string
	}{
		{name: "legacy JSON nonzero", body: jsonBody(`,"sort_order":17`), httpStatus: 200, code: 200, order: 17},
		{name: "legacy JSON omitted", body: jsonBody(""), httpStatus: 200, code: 200, order: 17},
		{name: "legacy JSON zero", body: jsonBody(`,"sort_order":0`), httpStatus: 200, code: 200, order: 17},
		{name: "legacy JSON null", body: jsonBody(`,"sort_order":null`), httpStatus: 200, code: 200, order: 17},
		{name: "legacy form", body: form, contentType: "application/x-www-form-urlencoded", httpStatus: 200, code: 200, order: 19},
		{name: "legacy protobuf", body: string(binary), contentType: "application/x-protobuf", httpStatus: 200, code: 200, order: 23},
		{name: "legacy malformed JSON", body: `{`, httpStatus: 200, code: 400, order: 23},
		{name: "explicit zero", suffix: "?set_sort_order=0", body: jsonBody(""), httpStatus: 200, code: 200, order: 0},
		{name: "explicit invalid", suffix: "?set_sort_order=2147483648", body: jsonBody(""), httpStatus: 400, code: 400, order: 0, errorType: "PIPELINE_INVALID_REQUEST"},
	} {
		t.Run(test.name, func(t *testing.T) {
			contentType := test.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			response := ut.PerformRequest(engine.Engine, "POST", route+test.suffix,
				&ut.Body{Body: strings.NewReader(test.body), Len: len(test.body)}, ut.Header{Key: "Content-Type", Value: contentType})
			var result plpb.PipelineResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.httpStatus || result.GetCode() != int32(test.code) || result.GetErrorType() != test.errorType {
				t.Fatalf("HTTP=%d body=%s", response.Code, response.Body.String())
			}
			stored, err := instance.GetPipeline(ctx, "dev", "default")
			if err != nil || stored.GetSortOrder() != test.order || stored.GetPipelineName() != "renamed" {
				t.Fatalf("stored pipeline=%+v, want name=renamed and order=%d, err=%v", stored, test.order, err)
			}
			if test.code == 200 {
				returned := result.GetData().GetPipeline()
				if returned == nil || returned.GetSortOrder() != test.order || returned.GetPipelineName() != "renamed" {
					t.Fatalf("wrong response pipeline: %s", response.Body.String())
				}
			}
		})
	}
}
