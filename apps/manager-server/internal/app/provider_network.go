//go:build !cpa_entry_fixture

package app

import "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"

func providerRuntimeNetwork() balance.NetworkOptions { return balance.NetworkOptions{} }
