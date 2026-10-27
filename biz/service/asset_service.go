package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"gorm.io/gorm"
)

// --------------------- MinIO helper functions ---------------------

func (s *Service) getMinioClient() (*minio.Client, error) {
	if s.config == nil || s.config.Storage.Type != "minio" {
		return nil, errors.New("minio storage not configured")
	}

	minioConfig := s.config.Storage.Minio
	client, err := minio.New(minioConfig.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioConfig.AccessKey, minioConfig.SecretKey, ""),
		Secure: minioConfig.UseSSL,
	})
	if err != nil {
		return nil, err
	}

	// 确保 bucket 存在
	exists, err := client.BucketExists(context.Background(), minioConfig.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		err = client.MakeBucket(context.Background(), minioConfig.Bucket, minio.MakeBucketOptions{
			Region: minioConfig.Region,
		})
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}

// minioObjectExists 检查受管对象是否存在;其余错误原样返回(不吞掉存储故障)。
func (s *Service) minioObjectExists(ctx context.Context, objectName string) (bool, error) {
	client, err := s.getMinioClient()
	if err != nil {
		return false, err
	}
	if _, err := client.StatObject(ctx, s.config.Storage.Minio.Bucket, objectName, minio.StatObjectOptions{}); err != nil {
		if isMissingObject(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// --------------------- Asset operations ---------------------

func (s *Service) ListAssets(ctx context.Context, environmentKey, pipelineKey string) ([]*common.FileAsset, error) {
	envKey := strings.TrimSpace(environmentKey)
	pipeKey := strings.TrimSpace(pipelineKey)
	if envKey == "" || pipeKey == "" {
		return nil, errors.New("environment_key and pipeline_key are required")
	}
	assets, err := s.logic.ListAssetsByEnvironmentAndPipeline(ctx, envKey, pipeKey)
	if err != nil {
		return nil, err
	}
	return s.decorateAssetList(assetSliceToPB(assets)), nil
}

func (s *Service) UploadAsset(ctx context.Context, input *FileUploadInput) (*common.FileAsset, string, error) {
	if input == nil {
		return nil, "", errors.New("input required")
	}
	if len(input.Data) == 0 {
		return nil, "", errors.New("file data is empty")
	}
	if input.EnvironmentKey == "" || input.PipelineKey == "" {
		return nil, "", errors.New("environment_key and pipeline_key are required")
	}

	fileID := uuid.NewString()
	fileName := input.FileName
	if fileName == "" {
		fileName = fileID
	}

	var (
		asset      *model.Asset
		reference  string
		createdRel string
	)
	// 冻结检查必须在创建任何目录/文件之前;文件与元数据写入都在同一次协调存续期内。
	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		scopeID := input.ExpectedScopeID
		if scopeID == "" {
			resolved, err := resolveScopeByKeys(gateCtx, txLogic, input.EnvironmentKey, input.PipelineKey)
			if err != nil {
				return err
			}
			scopeID = resolved.ScopeID
		} else if _, err := requireScopeIdentity(gateCtx, txLogic, input.EnvironmentKey, input.PipelineKey, scopeID); err != nil {
			return err
		}
		if err := txLogic.ensurePipelineUnlocked(gateCtx, input.EnvironmentKey, input.PipelineKey); err != nil {
			return err
		}
		write, err := s.writeAssetPayload(gateCtx, fileID, fileName, input)
		if err != nil {
			return err
		}
		createdRel = write.relativePath
		record := &model.Asset{
			FileID:         fileID,
			ScopeID:        scopeID,
			EnvironmentKey: input.EnvironmentKey,
			PipelineKey:    input.PipelineKey,
			FileName:       fileName,
			ContentType:    write.contentType,
			FileSize:       write.fileSize,
			Path:           write.relativePath,
			Remark:         input.Remark,
		}
		if err := txLogic.CreateAsset(gateCtx, record); err != nil {
			// 元数据失败:清理刚写入的文件,不留孤儿
			_ = s.removeAssetPayload(gateCtx, write)
			createdRel = ""
			return err
		}
		asset = record
		reference = fmt.Sprintf("%s%s", assetScheme, fileID)
		return nil
	})
	if err != nil {
		if createdRel != "" {
			// 事务回滚:清理本次新建、未被引用的文件(不触碰既有共享资源)
			_ = s.removeAssetPayload(ctx, assetPayload{relativePath: createdRel})
		}
		return nil, "", err
	}
	return s.decorateAsset(assetModelToPB(asset)), reference, nil
}

// assetPayload 描述一次资源文件写入的落点,用于失败清理。
type assetPayload struct {
	relativePath string
	contentType  string
	fileSize     int64
}

// writeAssetPayload 依据存储配置写入资源字节;本地与对象存储都保持有界单次写入。
// managedObjectName 是 MinIO 模式下受管资源的对象名(含目标范围)。
func managedObjectName(environmentKey, pipelineKey, fileID, fileName string) string {
	return filepath.Join(environmentKey, pipelineKey, fileID, fileName)
}

// writeManagedFileExclusive 以排他方式写入本地受管资源文件:
// 资源 ID 每次新建,不允许覆盖既有共享文件;失败时清理本次半成品。
func writeManagedFileExclusive(fullPath string, data []byte) error {
	handle, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := handle.Write(data); err != nil {
		_ = handle.Close()
		_ = os.Remove(fullPath)
		return err
	}
	if err := handle.Close(); err != nil {
		_ = os.Remove(fullPath)
		return err
	}
	return nil
}

// writeAssetPayload 写入受管资源文件;落点与当前 storage 模式一致。
// 排他写入:file_id 由调用方持有且每次新建,既有一致命名对象会被拒绝而不是被覆盖。
func (s *Service) writeAssetPayload(ctx context.Context, fileID, fileName string, input *FileUploadInput) (assetPayload, error) {
	contentType := detectContentType(input.ContentType, input.Data)
	fileSize := int64(len(input.Data))
	if s.config != nil && s.config.Storage.Type == "minio" {
		client, err := s.getMinioClient()
		if err != nil {
			return assetPayload{}, err
		}
		objectName := managedObjectName(input.EnvironmentKey, input.PipelineKey, fileID, fileName)
		exists, err := s.minioObjectExists(ctx, objectName)
		if err != nil {
			return assetPayload{}, err
		}
		if exists {
			return assetPayload{}, errors.New("managed object already exists")
		}
		if _, err := client.PutObject(ctx, s.config.Storage.Minio.Bucket, objectName, bytes.NewReader(input.Data), fileSize, minio.PutObjectOptions{
			ContentType: contentType,
		}); err != nil {
			return assetPayload{}, err
		}
		return assetPayload{relativePath: objectName, contentType: contentType, fileSize: fileSize}, nil
	}
	if err := ensureUploadDir(fileID); err != nil {
		return assetPayload{}, err
	}
	relativePath := filepath.Join(uploadDirectory, fileID, fileName)
	fullPath := filepath.Join(dataDirectory, relativePath)
	if err := writeManagedFileExclusive(fullPath, input.Data); err != nil {
		return assetPayload{}, err
	}
	return assetPayload{relativePath: relativePath, contentType: contentType, fileSize: fileSize}, nil
}

// removeAssetPayload 清理本次写入产生的文件(仅用于失败回滚;fileID 为新建唯一 ID)。
func (s *Service) removeAssetPayload(ctx context.Context, payload assetPayload) error {
	if payload.relativePath == "" {
		return nil
	}
	if s.config != nil && s.config.Storage.Type == "minio" {
		client, err := s.getMinioClient()
		if err != nil {
			return err
		}
		return client.RemoveObject(ctx, s.config.Storage.Minio.Bucket, payload.relativePath, minio.RemoveObjectOptions{})
	}
	return os.Remove(filepath.Join(dataDirectory, payload.relativePath))
}

func (s *Service) GetAssetFile(ctx context.Context, fileID string) (*model.Asset, string, error) {
	if fileID == "" {
		return nil, "", ErrAssetNotFound
	}
	asset, err := s.logic.assetDAO.GetByFileIDIncludingDeleted(ctx, s.logic.db, fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", ErrAssetNotFound
		}
		return nil, "", err
	}
	if asset.DeletedAt.Valid {
		return nil, "", scopeDeletedError("asset belongs to a deleted scope")
	}
	if asset.ScopeID != "" {
		active, err := db.ScopeIsActive(ctx, s.logic.db, asset.ScopeID)
		if err != nil {
			return nil, "", err
		}
		if !active {
			return nil, "", scopeDeletedError("asset belongs to a deleted scope")
		}
	}

	// 根据配置选择存储方式
	if s.config != nil && s.config.Storage.Type == "minio" {
		// 使用 MinIO 存储，下载到临时文件
		client, err := s.getMinioClient()
		if err != nil {
			return nil, "", err
		}

		// 创建临时文件
		tempFile, err := os.CreateTemp("", "rainbow-bridge-asset-")
		if err != nil {
			return nil, "", err
		}
		tempPath := tempFile.Name()
		_ = tempFile.Close()

		// 下载文件
		err = client.FGetObject(ctx, s.config.Storage.Minio.Bucket, asset.Path, tempPath, minio.GetObjectOptions{})
		if err != nil {
			_ = os.Remove(tempPath)
			if isMissingObject(err) {
				return nil, "", ErrAssetNotFound
			}
			return nil, "", err
		}

		return asset, tempPath, nil
	} else {
		// 使用本地存储
		fullPath := filepath.Join(dataDirectory, asset.Path)
		fmt.Printf("[DEBUG] GetAssetFile: fullPath=%s, dataDirectory=%s, asset.Path=%s\n", fullPath, dataDirectory, asset.Path)
		if _, err := os.Stat(fullPath); err != nil {
			fmt.Printf("[DEBUG] os.Stat failed: %v\n", err)
			if os.IsNotExist(err) {
				return nil, "", ErrAssetNotFound
			}
			return nil, "", err
		}
		fmt.Printf("[DEBUG] os.Stat success, returning fullPath=%s\n", fullPath)
		return asset, fullPath, nil
	}
}
