package store

import (
	"context"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/setting"
)

func (s *Store) LoadRuntimeControls(ctx context.Context) (setting.RuntimeControls, error) {
	return setting.LoadRuntimeControls(ctx, s.db)
}

func (s *Store) SaveRuntimeControls(ctx context.Context, controls setting.RuntimeControls) error {
	return setting.SaveRuntimeControls(ctx, s.db, controls)
}
