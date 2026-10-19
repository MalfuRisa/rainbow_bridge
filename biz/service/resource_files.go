package service

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// resourceRestoreHookForTest 是测试专用确定性阻塞点(生产为 nil)。
// 在资源恢复的每个阶段入口被调用一次,便于测试精确控制资源阶段的时序。
var resourceRestoreHookForTest func(stage string)

// createdResourceFile 记录本轮写操作新建的资源落点,用于回滚后的清理。
// 只清理本次新建的文件;绝不触碰既有共享资源。
type createdResourceFile struct {
	kind string // local | minio
	path string // 本地绝对路径或对象名
}

// cleanupCreatedResourceFiles 尽力清理本次新建的文件并忽略单个失败。
func (s *Service) cleanupCreatedResourceFiles(ctx context.Context, files []createdResourceFile) {
	for _, file := range files {
		switch file.kind {
		case "minio":
			client, err := s.getMinioClient()
			if err != nil {
				continue
			}
			_ = client.RemoveObject(ctx, s.config.Storage.Minio.Bucket, file.path, minio.RemoveObjectOptions{})
		default:
			_ = os.Remove(file.path)
		}
	}
}

// inSavepoint 让一次可失败的写步骤拥有独立保存点(已在协调事务内时);
// 保存点回滚不会毒化外层协调事务,保持归档"配置可提交而部分资源失败"的协议。
func (l *Logic) inSavepoint(fn func(sp *Logic) error) error {
	if !l.inTx {
		return fn(l)
	}
	return l.db.Transaction(func(tx *gorm.DB) error {
		return fn(l.withTx(tx))
	})
}

// readManagedAssetFileBytes 读取受管资源文件字节;文件缺失返回 os.ErrNotExist。
func (s *Service) readManagedAssetFileBytes(ctx context.Context, asset *model.Asset) ([]byte, error) {
	if asset == nil {
		return nil, ErrAssetNotFound
	}
	if s.config != nil && s.config.Storage.Type == "minio" {
		client, err := s.getMinioClient()
		if err != nil {
			return nil, err
		}
		object, err := client.GetObject(ctx, s.config.Storage.Minio.Bucket, asset.Path, minio.GetObjectOptions{})
		if err != nil {
			if isMissingObject(err) {
				return nil, os.ErrNotExist
			}
			return nil, err
		}
		defer object.Close()
		payload, err := io.ReadAll(object)
		if err != nil {
			if isMissingObject(err) {
				return nil, os.ErrNotExist
			}
			return nil, err
		}
		return payload, nil
	}
	fullPath := filepath.Join(dataDirectory, asset.Path)
	payload, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return payload, nil
}

// readManagedAssetBytesByID 读取资源元数据与文件字节(事务内可见自身写入)。
// 文件缺失返回 os.ErrNotExist;元数据缺失返回 ErrAssetNotFound。
func (s *Service) readManagedAssetBytesByID(ctx context.Context, l *Logic, fileID string) (*model.Asset, []byte, error) {
	asset, err := l.GetAsset(ctx, fileID)
	if err != nil {
		return nil, nil, err
	}
	payload, err := s.readManagedAssetFileBytes(ctx, asset)
	if err != nil {
		return asset, nil, err
	}
	return asset, payload, nil
}
