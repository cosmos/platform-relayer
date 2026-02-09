package gasmonitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cosmos/eureka-relayer/relayer/eureka"
	"github.com/cosmos/eureka-relayer/shared/config"
	"github.com/cosmos/eureka-relayer/shared/lmt"
	"github.com/cosmos/eureka-relayer/shared/metrics"
	"go.uber.org/zap"
)

type GasMonitor struct {
	eurkeaClientManager eureka.BridgeClientManager
}

func NewGasMonitor(
	eurekaClientManager eureka.BridgeClientManager,
) *GasMonitor {
	return &GasMonitor{
		eurkeaClientManager: eurekaClientManager,
	}
}

func (gm *GasMonitor) Start(ctx context.Context) error {
	lmt.Logger(ctx).Info("Starting gas monitor")
	var chains []config.ChainConfig
	evmChains, err := config.GetConfigReader(ctx).GetAllChainConfigsOfType(config.ChainType_EVM)
	if err != nil {
		return fmt.Errorf("error getting EVM chains: %w", err)
	}
	cosmosChains, err := config.GetConfigReader(ctx).GetAllChainConfigsOfType(config.ChainType_COSMOS)
	if err != nil {
		return fmt.Errorf("error getting cosmos chains: %w", err)
	}
	svmChains, err := config.GetConfigReader(ctx).GetAllChainConfigsOfType(config.ChainType_SVM)
	if err != nil {
		return fmt.Errorf("error getting svm chains: %w", err)
	}
	chains = append(chains, evmChains...)
	chains = append(chains, cosmosChains...)
	chains = append(chains, svmChains...)

	ticker := time.NewTicker(1 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, chain := range chains {
				if err = gm.monitorEurekaGasBalances(ctx, chain, chain.ChainID); err != nil && !errors.Is(err, config.ErrNoSignerForBridge) {
					lmt.Logger(ctx).Error("failed to monitor eureka gas balance", zap.String("chain_id", chain.ChainID), zap.Error(err))
				}
			}
		}
	}
}

func (gm *GasMonitor) monitorEurekaGasBalances(ctx context.Context, chain config.ChainConfig, chainID string) error {
	warningThreshold, criticalThreshold, err := config.GetConfigReader(ctx).GetSignerGasAlertThresholds(chainID, config.BridgeType_EUREKA)
	if err != nil {
		if errors.Is(err, config.ErrNoSignerForBridge) {
			return nil
		}
		return fmt.Errorf("getting eureka signer gas alert thresholds on chain %s: %w", chainID, err)
	}

	chainClient, err := gm.eurkeaClientManager.GetClient(ctx, chainID)
	if err != nil {
		return fmt.Errorf("getting eureka client for chain %s: %w", chainID, err)
	}

	balance, err := chainClient.SignerGasTokenBalance(ctx)
	if err != nil {
		return fmt.Errorf("getting gas token balance for eurkea signer on chain %s: %w", chainID, err)
	}

	if balance == nil || warningThreshold == nil || criticalThreshold == nil {
		return fmt.Errorf("gas balance or alert thresholds are nil for chain %s", chainID)
	}
	if balance.Cmp(criticalThreshold) < 0 {
		lmt.Logger(ctx).Error("low balance on eureka signer", zap.String("balance", balance.String()), zap.String("chainID", chainID))
	}
	metrics.FromContext(ctx).SetGasBalance(chainID, chain.ChainName, chain.GasTokenSymbol, string(chain.Environment), *balance, *warningThreshold, *criticalThreshold, chain.GasTokenDecimals, metrics.EurekaBridgeType)
	return nil
}
