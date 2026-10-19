package service

import (
	"context"
	"errors"

	"github.com/yi-nology/rainbow_bridge/biz/model/common"
)

// --------------------- Config operations ---------------------

func (s *Service) AddConfig(ctx context.Context, req *common.ResourceConfig) (*common.ResourceConfig, error) {
	if req == nil {
		return nil, errors.New("config payload required")
	}
	model := pbConfigToModel(req)
	s.normalizeStoredConfig(model)
	if err := s.logic.AddConfig(ctx, model); err != nil {
		return nil, err
	}
	return s.decorateConfig(modelConfigToPB(model)), nil
}

func (s *Service) UpdateConfig(ctx context.Context, req *common.ResourceConfig) (*common.ResourceConfig, error) {
	if req == nil {
		return nil, errors.New("config payload required")
	}
	model := pbConfigToModel(req)
	s.normalizeStoredConfig(model)
	if err := s.logic.UpdateConfig(ctx, model); err != nil {
		return nil, err
	}
	updated, err := s.logic.GetConfig(ctx, model.EnvironmentKey, model.PipelineKey, model.ResourceKey)
	if err != nil {
		return nil, err
	}
	return s.decorateConfig(modelConfigToPB(updated)), nil
}

func (s *Service) DeleteConfig(ctx context.Context, environmentKey, pipelineKey, resourceKey string) error {
	return s.logic.DeleteConfig(ctx, environmentKey, pipelineKey, resourceKey)
}

func (s *Service) ListConfigs(ctx context.Context, environmentKey, pipelineKey, typ, minVer, maxVer string, isLatest bool) ([]*common.ResourceConfig, error) {
	configs, err := s.logic.ListConfigs(ctx, environmentKey, pipelineKey, minVer, maxVer, typ, isLatest)
	if err != nil {
		return nil, err
	}
	return s.decorateConfigList(configSliceToPB(configs)), nil
}

func (s *Service) GetConfigDetail(ctx context.Context, environmentKey, pipelineKey, resourceKey string) (*common.ResourceConfig, error) {
	cfg, err := s.logic.GetConfig(ctx, environmentKey, pipelineKey, resourceKey)
	if err != nil {
		return nil, err
	}
	return s.decorateConfig(modelConfigToPB(cfg)), nil
}
