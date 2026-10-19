package db

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

func TestConfigUpdateWhitelistAndDescriptionZero(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err := conn.AutoMigrate(&model.Config{}); err != nil {
		t.Fatal(err)
	}
	dao := NewConfigDAO()
	ctx := context.Background()
	original := model.Config{EnvironmentKey: "dev", PipelineKey: "main", Alias: "alias", Name: "name", Content: "old", Type: "text", Remark: "remark", IsPerm: true, Description: "description"}
	if err := dao.Create(ctx, conn, &original); err != nil {
		t.Fatal(err)
	}
	var before model.Config
	if err := conn.First(&before, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	update := model.Config{ID: original.ID + 10, EnvironmentKey: "different", PipelineKey: "different", ResourceKey: original.ResourceKey, Alias: "mutated", Content: "new", CreatedAt: time.Unix(1, 0), DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true}}
	if err := dao.UpdateByEnvironmentAndPipeline(ctx, conn, "dev", "main", &update); err != nil {
		t.Fatal(err)
	}
	got, err := dao.GetByResourceKey(ctx, conn, "dev", "main", original.ResourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != before.ID || got.Alias != before.Alias || got.EnvironmentKey != before.EnvironmentKey || got.PipelineKey != before.PipelineKey || !got.CreatedAt.Equal(before.CreatedAt) || got.DeletedAt.Valid {
		t.Fatalf("identity/lifecycle fields changed: %+v", got)
	}
	if got.Content != "new" || got.Description != "" {
		t.Fatalf("editable values not updated: %+v", got)
	}
	if got.Name != "name" || got.Type != "text" || got.Remark != "remark" || !got.IsPerm {
		t.Fatalf("existing zero-value rules changed: %+v", got)
	}
	update.Description = "restored"
	if err := dao.UpdateByEnvironmentAndPipeline(ctx, conn, "dev", "main", &update); err != nil {
		t.Fatal(err)
	}
	got, err = dao.GetByResourceKey(ctx, conn, "dev", "main", original.ResourceKey)
	if err != nil || got.Description != "restored" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
