package eureka

import (
	"context"
	"fmt"

	"github.com/cosmos/eureka-relayer/db/gen/db"
	"go.uber.org/zap"
)

type TransferTimeoutTxGasCostStorage interface {
	UpdateTransferTimeoutTxGasCostUSD(ctx context.Context, arg db.UpdateTransferTimeoutTxGasCostUSDParams) error
}

type TimeoutTxGasCalculatorProcessor struct {
	bridgeClientManager            BridgeClientManager
	storage                        TransferTimeoutTxGasCostStorage
	priceClient                    PriceClient
	sourceChainGasTokenCoingeckoID string
	sourceChainGasTokenDecimals    uint8
}

func NewTimeoutTxGasCalculatorProcessor(
	bridgeClientManager BridgeClientManager,
	storage TransferTimeoutTxGasCostStorage,
	priceClient PriceClient,
	sourceChainGasTokenCoingeckoID string,
	sourceChainGasTokenDecimals uint8,
) TimeoutTxGasCalculatorProcessor {
	return TimeoutTxGasCalculatorProcessor{
		bridgeClientManager:            bridgeClientManager,
		storage:                        storage,
		priceClient:                    priceClient,
		sourceChainGasTokenCoingeckoID: sourceChainGasTokenCoingeckoID,
		sourceChainGasTokenDecimals:    sourceChainGasTokenDecimals,
	}
}

func (processor TimeoutTxGasCalculatorProcessor) Process(ctx context.Context, transfer *EurekaTransfer) (*EurekaTransfer, error) {
	sourceBridgeClient, err := processor.bridgeClientManager.GetClient(ctx, transfer.GetSourceChainID())
	if err != nil {
		return nil, fmt.Errorf("getting eureka bridge client for transfer source chain %s: %w", transfer.GetSourceChainID(), err)
	}

	recvTxHash, hasTimeoutTxHash := transfer.GetTimeoutTxHash()
	if !hasTimeoutTxHash {
		return nil, fmt.Errorf("this is a bug! transfer does not have a recv tx, violating should process")
	}

	gasCostInGasToken, err := sourceBridgeClient.TxFee(ctx, recvTxHash)
	if err != nil {
		return nil, fmt.Errorf("getting gas cost of timeout tx %s on %s: %w", recvTxHash, transfer.GetSourceChainID(), err)
	}

	gasCostUSD, err := processor.priceClient.GetCoinUsdValue(ctx, processor.sourceChainGasTokenCoingeckoID, processor.sourceChainGasTokenDecimals, gasCostInGasToken)
	if err != nil {
		return nil, fmt.Errorf("getting usd value of timeout tx gas cost on chain %s: %w", transfer.GetSourceChainID(), err)
	}

	update := db.UpdateTransferTimeoutTxGasCostUSDParams{
		TimeoutTxGasCostUsd:  gasCostUSD,
		SourceChainID:        transfer.GetSourceChainID(),
		PacketSourceClientID: transfer.GetPacketSourceClientID(),
		PacketSequenceNumber: int32(transfer.GetPacketSequenceNumber()),
	}
	if err := processor.storage.UpdateTransferTimeoutTxGasCostUSD(ctx, update); err != nil {
		return nil, fmt.Errorf("updating transfer timeout tx gas cost usd value: %w", err)
	}

	return transfer, nil
}

func (processor TimeoutTxGasCalculatorProcessor) Cancel(transfer *EurekaTransfer, err error) {
	timeoutTxHash, _ := transfer.GetTimeoutTxHash()
	timeoutTxTime, _ := transfer.GetTimeoutTxTime()
	transfer.GetLogger().Error("calculating timeout packet gas cost", zap.Error(err), zap.String("timeout_tx_hash", timeoutTxHash), zap.Time("timeout_tx_time", timeoutTxTime))
}

func (processor TimeoutTxGasCalculatorProcessor) ShouldProcess(transfer *EurekaTransfer) bool {
	_, hasTimeoutGasCostUSD := transfer.GetTimeoutGasCostUSD()
	if hasTimeoutGasCostUSD {
		return false
	}

	_, hasTimeoutTxHash := transfer.GetTimeoutTxHash()
	return hasTimeoutTxHash
}

func (processor TimeoutTxGasCalculatorProcessor) State() db.EurekaRelayStatus {
	return db.EurekaRelayStatusCALCULATINGTIMEOUTTXGASCOST
}
