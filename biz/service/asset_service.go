package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
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

	contentType := detectContentType(input.ContentType, input.Data)
	fileSize := int64(len(input.Data))
	var relativePath string

	// 根据配置选择存储方式
	if s.config != nil && s.config.Storage.Type == "minio" {
		// 使用 MinIO 存储
		client, err := s.getMinioClient()
		if err != nil {
			return nil, "", err
		}

		objectName := filepath.Join(input.EnvironmentKey, input.PipelineKey, fileID, fileName)
		_, err = client.PutObject(ctx, s.config.Storage.Minio.Bucket, objectName, strings.NewReader(string(input.Data)), fileSize, minio.PutObjectOptions{
			ContentType: contentType,
		})
		if err != nil {
			return nil, "", err
		}

		relativePath = objectName
	} else {
		// 使用本地存储
		if err := ensureUploadDir(fileID); err != nil {
			return nil, "", err
		}

		relativePath = filepath.Join(uploadDirectory, fileID, fileName)
		fullPath := filepath.Join(dataDirectory, relativePath)
		if err := os.WriteFile(fullPath, input.Data, 0o644); err != nil {
			return nil, "", err
		}
	}

	asset := &model.Asset{
		FileID:         fileID,
		EnvironmentKey: input.EnvironmentKey,
		PipelineKey:    input.PipelineKey,
		FileName:       fileName,
		ContentType:    contentType,
		FileSize:       fileSize,
		Path:           relativePath,
		Remark:         input.Remark,
	}
	if err := s.logic.CreateAsset(ctx, asset); err != nil {
		// 清理已上传的文件
		if s.config != nil && s.config.Storage.Type == "minio" {
			client, _ := s.getMinioClient()
			if client != nil {
				_ = client.RemoveObject(ctx, s.config.Storage.Minio.Bucket, relativePath, minio.RemoveObjectOptions{})
			}
		} else {
			_ = os.Remove(filepath.Join(dataDirectory, relativePath))
		}
		return nil, "", err
	}

	reference := fmt.Sprintf("%s%s", assetScheme, fileID)
	return s.decorateAsset(assetModelToPB(asset)), reference, nil
}

func (s *Service) GetAssetFile(ctx context.Context, fileID string) (*model.Asset, string, error) {
	if fileID == "" {
		return nil, "", ErrAssetNotFound
	}
	asset, err := s.logic.GetAsset(ctx, fileID)
	if err != nil {
		return nil, "", err
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
			if isMissingObject(err) { return nil, "", ErrAssetNotFound }
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
