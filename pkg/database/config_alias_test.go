package database

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

func TestConfigAliasUpgrade(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "conflicts"}[conflict], func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := db.DB()
			raw.SetMaxOpenConns(1)
			defer raw.Close()
			if err := db.AutoMigrate(&model.Config{}); err != nil {
				t.Fatal(err)
			}
			row := model.Config{EnvironmentKey: "dev", PipelineKey: "main", Alias: "a", ResourceKey: "one"}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			if conflict {
				row.ID = 0
				row.ResourceKey = "two"
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			err = PrepareConfigAliasConstraint(context.Background(), db)
			if conflict {
				if err == nil {
					t.Fatal("duplicate old data accepted")
				}
				var count int64
				db.Model(&model.Config{}).Count(&count)
				if count != 2 {
					t.Fatal("old data modified")
				}
				return
			}
			for i := 0; i < 2; i++ {
				if err := PrepareConfigAliasConstraint(context.Background(), db); err != nil {
					t.Fatal(err)
				}
			}
			row.ID = 0
			row.ResourceKey = "two"
			if err := db.Create(&row).Error; err == nil {
				t.Fatal("duplicate accepted")
			}
			row.ID = 0
			row.Alias = ""
			row.ResourceKey = "empty-one"
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			row.ID = 0
			row.ResourceKey = "empty-two"
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			row.ID = 0
			row.Alias = "a"
			row.EnvironmentKey = "prod"
			row.ResourceKey = "prod"
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Where("environment_key='dev' AND alias='a'").Delete(&model.Config{}).Error; err != nil {
				t.Fatal(err)
			}
			row.ID = 0
			row.EnvironmentKey = "dev"
			row.ResourceKey = "replacement"
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		})
	}
}
