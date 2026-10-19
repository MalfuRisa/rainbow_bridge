package service

import (
	"errors"

	"github.com/redis/go-redis/v9"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"gorm.io/gorm"
)

var (
	ErrResourceNotFound  = errors.New("resource not found")
	ErrAssetNotFound     = errors.New("asset not found")
	ErrConfigAliasExists = db.ErrConfigAliasExists
)

// Logic contains business rules on top of data persistence.
type Logic struct {
	db             *gorm.DB
	redisClient    *redis.Client
	configDAO      *db.ConfigDAO
	assetDAO       *db.AssetDAO
	environmentDAO *db.EnvironmentDAO
	pipelineDAO    *db.PipelineDAO
}

func NewLogic(dbConn *gorm.DB, redisClient *redis.Client) *Logic {
	return &Logic{
		db:             dbConn,
		redisClient:    redisClient,
		configDAO:      db.NewConfigDAO(),
		assetDAO:       db.NewAssetDAO(),
		environmentDAO: db.NewEnvironmentDAO(),
		pipelineDAO:    db.NewPipelineDAO(),
	}
}
