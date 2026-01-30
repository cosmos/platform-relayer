package eureka

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/cosmos/eureka-relayer/shared/config"
	"github.com/cosmos/eureka-relayer/shared/metrics"
)

func (e *EurekaTransfer) RecordRecvSendLatency(ctx context.Context) {
	recvTxTime, ok := e.GetRecvTxTime()
	if !ok {
		return
	}
	e.RecordLatency(ctx, e.GetSourceChainID(), e.GetDestinationChainID(), e.GetSourceTxTime(), recvTxTime, metrics.EurekaSendToRecvRelayType)
}

func (e *EurekaTransfer) RecordRecvRelayed(ctx context.Context) {
	metrics.FromContext(ctx).AddRelayCompleted(
		e.GetSourceChainID(),
		e.GetPacketSourceClientID(),
		e.GetDestinationChainID(),
		e.GetPacketDestinationClientID(),
		metrics.EurekaSendToRecvRelayType,
	)
}

func (e *EurekaTransfer) RecordSendTimeoutLatency(ctx context.Context) {
	timeoutTxTime, ok := e.GetTimeoutTxTime()
	if !ok {
		return
	}
	e.RecordLatency(ctx, e.GetSourceChainID(), e.GetDestinationChainID(), e.GetSourceTxTime(), timeoutTxTime, metrics.EurekaSendToTimeoutRelayType)
}

func (e *EurekaTransfer) RecordTimeoutRelayed(ctx context.Context) {
	metrics.FromContext(ctx).AddRelayCompleted(
		e.GetSourceChainID(),
		e.GetPacketSourceClientID(),
		e.GetDestinationChainID(),
		e.GetPacketDestinationClientID(),
		metrics.EurekaSendToRecvRelayType,
	)
}

func (e *EurekaTransfer) RecordAckRecvLatency(ctx context.Context) {
	ackTxTime, ok := e.GetAckTxTime()
	if !ok {
		return
	}
	recvTxTime, ok := e.GetRecvTxTime()
	if !ok {
		return
	}
	e.RecordLatency(ctx, e.GetDestinationChainID(), e.GetSourceChainID(), recvTxTime, ackTxTime, metrics.EurekaRecvToAckRelayType)
}

func (e *EurekaTransfer) RecordAckRelayed(ctx context.Context) {
	metrics.FromContext(ctx).AddRelayCompleted(
		e.GetDestinationChainID(),
		e.GetPacketDestinationClientID(),
		e.GetSourceChainID(),
		e.GetPacketSourceClientID(),
		metrics.EurekaRecvToAckRelayType,
	)
}

func (e *EurekaTransfer) RecordLatency(ctx context.Context, sourceChainID, destChainID string, from, to time.Time, relayType metrics.RelayType) {
	sourceConfig, err := config.GetConfigReader(ctx).GetChainConfig(sourceChainID)
	if err != nil {
		return
	}
	destConfig, err := config.GetConfigReader(ctx).GetChainConfig(destChainID)
	if err != nil {
		return
	}

	metrics.FromContext(ctx).RelayLatency(
		sourceChainID,
		destChainID,
		sourceConfig.ChainName,
		destConfig.ChainName,
		string(sourceConfig.Environment),
		relayType,
		to.Sub(from),
	)
}

func (e *EurekaTransfer) RecordTransactionRetried(ctx context.Context, relayType metrics.RelayType) {
	sourceConfig, err := config.GetConfigReader(ctx).GetChainConfig(e.GetSourceChainID())
	if err != nil {
		return
	}

	destConfig, err := config.GetConfigReader(ctx).GetChainConfig(e.GetDestinationChainID())
	if err != nil {
		return
	}

	metrics.FromContext(ctx).AddTransactionRetryAttempt(
		e.GetSourceChainID(),
		e.GetDestinationChainID(),
		sourceConfig.ChainName,
		destConfig.ChainName,
		string(sourceConfig.Environment),
		relayType,
	)
}

func (e *EurekaTransfer) AlertOnExcessiveRelayLatency(ctx context.Context) {
	sourceConfig, err := config.GetConfigReader(ctx).GetChainConfig(e.GetSourceChainID())
	if err != nil {
		return
	}

	destConfig, err := config.GetConfigReader(ctx).GetChainConfig(e.GetDestinationChainID())
	if err != nil {
		return
	}

	ongoingRelayType, ok := e.ongoingRelayType(sourceConfig.Eureka.ShouldRelaySuccessAcks, sourceConfig.Eureka.ShouldRelayErrorAcks)
	if !ok {
		return
	}

	const (
		evmSourceExcessiveLatency    = 60 * time.Minute
		evmSourceFinality            = 15 * time.Minute
		cosmosSourceExcessiveLatency = 30 * time.Minute
	)

	switch ongoingRelayType {
	case metrics.EurekaSendToRecvRelayType:
		if sourceConfig.Type == config.ChainType_EVM && time.Since(e.GetSourceTxTime()) > evmSourceExcessiveLatency {
			// if the source is evm, alert if we have been waiting for more
			// than evmSourceExcessiveLatency and there is still no recv tx
			// hash
			e.GetLogger().Warn(
				fmt.Sprintf("excessive recv packet relay latency from %s to %s", e.GetSourceChainID(), e.GetDestinationChainID()),
				zap.String("packet_source_chain_id", e.GetSourceChainID()),
				zap.String("packet_destination_chain_id", e.GetDestinationChainID()),
				zap.Duration("latency", time.Since(e.GetSourceTxTime())),
				zap.String("source_tx_hash", e.GetSourceTxHash()),
				zap.Time("source_tx_time", e.GetSourceTxTime()),
			)
			metrics.FromContext(ctx).AddExcessiveRelayLatencyObservation(
				sourceConfig.ChainName,
				destConfig.ChainName,
				string(sourceConfig.Environment),
				ongoingRelayType,
			)
		}
		if sourceConfig.Type == config.ChainType_COSMOS && time.Since(e.GetSourceTxTime()) > cosmosSourceExcessiveLatency {
			// if the source is cosmos, alert if we have been waiting for more
			// than cosmosSourceExcessiveRelay and there is still no recv tx
			// hash
			e.GetLogger().Warn(
				fmt.Sprintf("excessive recv packet relay latency from %s to %s", e.GetSourceChainID(), e.GetDestinationChainID()),
				zap.String("packet_source_chain_id", e.GetSourceChainID()),
				zap.String("packet_destination_chain_id", e.GetDestinationChainID()),
				zap.Duration("latency", time.Since(e.GetSourceTxTime())),
				zap.String("source_tx_hash", e.GetSourceTxHash()),
				zap.Time("source_tx_time", e.GetSourceTxTime()),
			)
			metrics.FromContext(ctx).AddExcessiveRelayLatencyObservation(
				sourceConfig.ChainName,
				destConfig.ChainName,
				string(sourceConfig.Environment),
				ongoingRelayType,
			)
		}
	case metrics.EurekaRecvToAckRelayType:
		writeAckTime, _ := e.GetWriteAckTxTime()
		if destConfig.Type == config.ChainType_EVM && time.Since(writeAckTime) > evmSourceExcessiveLatency {
			// if the dest is evm, alert if we have been waiting for more
			// than evmSourceExcessiveLatency since the write ack and there is
			// still no ack tx hash

			// this metrics is from the perspective of the packet being
			// relayed, so for acks, source and dest are flipped
			writeAckTx, _ := e.GetWriteAckTxHash()
			e.GetLogger().Warn(
				fmt.Sprintf("excessive ack packet relay latency from %s to %s", e.GetDestinationChainID(), e.GetSourceChainID()),
				zap.String("packet_source_chain_id", e.GetDestinationChainID()),
				zap.String("packet_destination_chain_id", e.GetSourceChainID()),
				zap.Duration("latency", time.Since(writeAckTime)),
				zap.String("write_ack_tx_hash", writeAckTx),
				zap.Time("write_ack_tx_time", writeAckTime),
				zap.String("source_tx_hash", e.GetSourceTxHash()),
				zap.Time("source_tx_time", e.GetSourceTxTime()),
			)
			metrics.FromContext(ctx).AddExcessiveRelayLatencyObservation(
				destConfig.ChainName,
				sourceConfig.ChainName,
				string(sourceConfig.Environment),
				ongoingRelayType,
			)
		}
		if destConfig.Type == config.ChainType_COSMOS && time.Since(writeAckTime) > cosmosSourceExcessiveLatency {
			// if the dest is cosmos, alert if we have been waiting for more
			// than cosmosSourceExcessiveRelay since the write ack and there is
			// still no ack tx hash

			// this metrics is from the perspective of the packet being
			// relayed, so for acks, source and dest are flipped
			writeAckTx, _ := e.GetWriteAckTxHash()
			e.GetLogger().Warn(
				fmt.Sprintf("excessive ack packet relay latency from %s to %s", e.GetDestinationChainID(), e.GetSourceChainID()),
				zap.String("packet_source_chain_id", e.GetDestinationChainID()),
				zap.String("packet_destination_chain_id", e.GetSourceChainID()),
				zap.Duration("latency", time.Since(writeAckTime)),
				zap.String("write_ack_tx_hash", writeAckTx),
				zap.Time("write_ack_tx_time", writeAckTime),
				zap.String("source_tx_hash", e.GetSourceTxHash()),
				zap.Time("source_tx_time", e.GetSourceTxTime()),
			)
			metrics.FromContext(ctx).AddExcessiveRelayLatencyObservation(
				destConfig.ChainName,
				sourceConfig.ChainName,
				string(sourceConfig.Environment),
				ongoingRelayType,
			)
		}
	case metrics.EurekaSendToTimeoutRelayType:
		if time.Since(e.GetPacketTimeoutTimestamp()) < 5*time.Minute {
			return
		}

		if sourceConfig.Type == config.ChainType_EVM && (time.Since(e.GetSourceTxTime()) < evmSourceFinality) {
			// if this timeout is for a send from an evm chain, wait until the
			// send is finalized before alerting that the timeout has excessive
			// latency
			return
		}

		// does not matter about source or dest chain here, if we have not
		// submitted a timeout within 5 mins of the timeout timestamp,
		// alert
		e.GetLogger().Warn(
			fmt.Sprintf("excessive timeout packet relay latency from %s to %s", e.GetSourceChainID(), e.GetDestinationChainID()),
			zap.String("packet_source_chain_id", e.GetSourceChainID()),
			zap.String("packet_destination_chain_id", e.GetDestinationChainID()),
			zap.Duration("latency", time.Since(e.GetSourceTxTime())),
			zap.String("source_tx_hash", e.GetSourceTxHash()),
			zap.Time("source_tx_time", e.GetSourceTxTime()),
		)
		metrics.FromContext(ctx).AddExcessiveRelayLatencyObservation(
			sourceConfig.ChainName,
			destConfig.ChainName,
			string(sourceConfig.Environment),
			ongoingRelayType,
		)
	}
}

func (e *EurekaTransfer) ongoingRelayType(shouldRelaySuccessAcks, shouldRelayErrorAcks bool) (metrics.RelayType, bool) {
	if e.IsComplete(shouldRelaySuccessAcks, shouldRelayErrorAcks) {
		return "", false
	}

	_, hasWriteAck := e.GetWriteAckTxHash()
	if !hasWriteAck {
		if time.Now().After(e.GetPacketTimeoutTimestamp()) {
			return metrics.EurekaSendToTimeoutRelayType, true
		}
		return metrics.EurekaSendToRecvRelayType, true
	}
	return metrics.EurekaRecvToAckRelayType, true
}
