package eureka

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	"github.com/deliveryhero/pipeline/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/cosmos/eureka-relayer/proto/gen/eurekarelayer"
	"github.com/cosmos/eureka-relayer/shared/clients/coingecko"
	"github.com/cosmos/eureka-relayer/shared/lmt"
	"github.com/cosmos/eureka-relayer/shared/metrics"
)

type EurekaPipeline interface {
	// Push pushes a transfer onto the pipelines input to be relayed, and
	// returns if the transfer was successfully accepted to be relayed or not
	Push(ctx context.Context, transfer *EurekaTransfer) bool
	Poll() (*EurekaTransfer, error)
	Close()
}

var _ EurekaPipeline = (*Pipeline[*EurekaTransfer, *EurekaTransfer])(nil)

// Pipeline represents a pipeline that is input an Input and outputs an Output.
// The user (caller) of the pipeline is responsible for closing the input
// channel, but not the output channel. The output channel will be closed once
// the context is cancelled and all items in the pipeline are finished
// processing.
type Pipeline[Input any, Output any] struct {
	// Input is used to send input to the pipeline. The user of the Pipeline is
	// responsible to close the Input channel.
	Input chan<- Input

	// Output receives processed Outputs from the pipeline. This will be closed
	// once the context used to create the pipeline is cancelled.
	Output <-chan Output
}

// Storage is a wrapper interface around all storage interfaces used throughout
// the pipeline
type Storage interface {
	TransferRecvTxWithTxStorage
	TransferRecvTxStorage
	TransferClearRecvTxStorage
	TransferRecvTxGasCostStorage
	TransferWriteAckTxStorage
	TransferAckTxWithTxStorage
	TransferAckTxStorage
	TransferClearAckTxStorage
	TransferAckTxGasCostStorage
	TransferTimeoutTxStorage
	TransferClearTimeoutTxStorage
	TransferTimeoutTxGasCostStorage
	TransferStateStorage
	TransferSendTxFinalityStorage
	TransferWriteAckFinalityStorage
}

type PriceClient interface {
	coingecko.PriceClient
	GetCoinUsdValue(ctx context.Context, coingeckoID string, decimals uint8, amount *big.Int) (pgtype.Numeric, error)
}

// NewPipeline creates a pipeline that performs the full Eureka relaying
// life cycle on transfers sent to its Input channel.
//
// Each pipeline instance is for a unique (source chain, source client, dest
// chain, dest client) pair, even though the relaying logic for all pipeline
// steps is the same, no matter the chain or client. They must be unique due to
// batching. When attempting to batch multiple packets together to submit in a
// single recv tx, the pipeline assumes that all packets are bound for the same
// dest chain + dest client (this is the same for acks, and timeouts).
func NewPipeline(
	ctx context.Context,
	storage Storage,
	bridgeClientManager BridgeClientManager,
	relayService eurekarelayer.RelayerServiceClient,
	priceClient PriceClient,
	sourceChainID string,
	sourceClientID string,
	destinationChainID string,
	destinationClientID string,
	pipelineOpts *PipelineOptions,
) *Pipeline[*EurekaTransfer, *EurekaTransfer] {
	// inputBufferSize is a high number we should never hit so we do not block
	// on inputting to the pipeline
	const inputBufferSize = 10000
	const concurrency = 10

	logger := lmt.Logger(ctx).With(
		zap.String("source_chain_id", sourceChainID),
		zap.String("source_client_id", sourceClientID),
		zap.String("destination_chain_id", destinationChainID),
		zap.String("destination_client_id", destinationClientID),
		zap.Bool("should_relay_success_acks", pipelineOpts.ShouldRelaySuccessAcks),
		zap.Bool("should_relay_error_acks", pipelineOpts.ShouldRelayErrorAcks),
		zap.Int("max_recv_batch_size", pipelineOpts.RecvBatchSize),
		zap.Duration("recv_batch_timeout", pipelineOpts.RecvBatchTimeout),
		zap.Int("max_recv_batch_concurrency", pipelineOpts.RecvBatchConcurrency),
		zap.Int("max_ack_batch_size", pipelineOpts.AckBatchSize),
		zap.Duration("ack_batch_timeout", pipelineOpts.AckBatchTimeout),
		zap.Int("max_ack_batch_concurrency", pipelineOpts.AckBatchConcurrency),
	)
	ctx = lmt.WithLogger(ctx, logger)

	input := make(chan *EurekaTransfer, inputBufferSize)

	// pull transfers off the input channel one at a time into pipeline
	output := pipeline.Emitter(ctx, func() *EurekaTransfer {
		return <-input
	})

	// check if the recv packet has already been delivered on the destination
	// chain, if it is, find and populate the tx hash for the recv delivery
	checkRecvPacketDeliveryProcessor := NewCheckRecvPacketDeliveryProcessor(bridgeClientManager, storage)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, checkRecvPacketDeliveryProcessor), output)

	// check if the packet commitment is found on the source chain, if it is
	// not, find and populate the either the ack tx hash or timeout tx hash. we
	// need this step here since this tx may have already received a timeout
	checkInitialPacketCommitmentProcessor := NewCheckPacketCommitmentProcessor(bridgeClientManager, storage)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, checkInitialPacketCommitmentProcessor), output)

	// check if the send packet is finalized on the source chain
	checkSendPacketFinalityProcessor := NewCheckSendFinalityProcessor(bridgeClientManager, sourceChainID, storage, pipelineOpts.SourceFinalityOffset)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, checkSendPacketFinalityProcessor), output)

	// if we need to timeout a packet, check to ensure that the packet timeout
	// timestamp is finalized on the destination chain
	checkTimeoutTimestampFinalityProcessor := NewCheckTimeoutFinalityProcessor(bridgeClientManager, pipelineOpts.DestinationFinalityOffset)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, checkTimeoutTimestampFinalityProcessor), output)

	// timeout a packet if it is past its timeout time stamp
	batchTimeoutPacketProcessor := NewBatchTimeoutPacketProcessor(bridgeClientManager, storage, relayService, sourceChainID, sourceClientID, destinationChainID, destinationClientID)
	output = ConditionallyBatchProcess(metrics.ContextWithRelayType(ctx, metrics.EurekaSendToTimeoutRelayType), pipelineOpts.TimeoutBatchConcurrency, pipelineOpts.TimeoutBatchSize, pipelineOpts.TimeoutBatchTimeout, output, NewEurekaBatchProcessorMW(storage, batchTimeoutPacketProcessor))

	// check if the timeout packet needs to be retried. if it does, remove the tx
	// hash from the db an error the transfer so the timeout is retried on the next
	// run
	retryTimeoutPacketProcessor := NewRetryTimeoutPacketProcessor(bridgeClientManager, storage, sourceChainID)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, retryTimeoutPacketProcessor), output)

	// record the timeout tx gas cost at current gas prices, if the pipeline
	// has all of the necessary info to do so
	if pipelineOpts.SourceChainGasTokenCoingeckoID != "" && pipelineOpts.SourceChainGasTokenDecimals != 0 && priceClient != nil {
		timeoutTxGasCalculator := NewTimeoutTxGasCalculatorProcessor(bridgeClientManager, storage, priceClient, pipelineOpts.SourceChainGasTokenCoingeckoID, pipelineOpts.SourceChainGasTokenDecimals)
		output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, timeoutTxGasCalculator), output)
	}

	// if the recv packet has not been delivered, place the transfer into a
	// batch and wait until either batchTimeout or until batchSize transfers
	// accumulate, then deliver a single recv tx for all transfers in the batch
	// on the destination chain
	batchRecvPacketProcessor := NewBatchRecvPacketProcessor(bridgeClientManager, storage, relayService, sourceChainID, sourceClientID, destinationChainID, destinationClientID)
	output = ConditionallyBatchProcess(metrics.ContextWithRelayType(ctx, metrics.EurekaSendToRecvRelayType), pipelineOpts.RecvBatchConcurrency, pipelineOpts.RecvBatchSize, pipelineOpts.RecvBatchTimeout, output, NewEurekaBatchProcessorMW(storage, batchRecvPacketProcessor))

	// check if the recv packet needs to be retried. if it does, remove the tx
	// hash from the db an error the transfer so the recv is retried on the next
	// run
	retryRecvPacketProcessor := NewRetryRecvPacketProcessor(bridgeClientManager, storage, destinationChainID)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, retryRecvPacketProcessor), output)

	// record the recv tx gas cost at current gas prices, if the pipeline has
	// all of the necessary info to do so
	if pipelineOpts.DestinationChainGasTokenCoingeckoID != "" && pipelineOpts.DestinationChainGasTokenDecimals != 0 && priceClient != nil {
		recvTxGasCalculator := NewRecvTxGasCalculatorProcessor(bridgeClientManager, storage, priceClient, pipelineOpts.DestinationChainGasTokenCoingeckoID, pipelineOpts.DestinationChainGasTokenDecimals)
		output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, recvTxGasCalculator), output)
	}

	// wait on the destination chain to produce a write ack packet
	waitForWriteAckPacketProcessor := NewWaitFoWriteAckPacketProcessor(bridgeClientManager, storage)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, waitForWriteAckPacketProcessor), output)

	// check if the write ack is finalized on the destination chain
	checkWriteAckFinalityProcessor := NewCheckWriteAckFinalityProcessor(bridgeClientManager, destinationChainID, pipelineOpts.ShouldRelaySuccessAcks, pipelineOpts.ShouldRelayErrorAcks, storage, pipelineOpts.DestinationFinalityOffset)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, checkWriteAckFinalityProcessor), output)

	// check if the packet commitment has been found on the source chain, if it
	// is, find and populate the either the ack tx hash or timeout tx hash
	checkPostReceivePacketCommitmentProcessor := NewCheckPacketCommitmentProcessor(bridgeClientManager, storage)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, checkPostReceivePacketCommitmentProcessor), output)

	// if the ack packet has not been delivered, place the transfer into a
	// batch and wait until either batchTimeout or until batchSize transfers
	// accumulate, then deliver a single ack tx for all transfers in the batch
	// on the source chain
	batchAckPacketProcessor := NewBatchAckPacketProcessor(bridgeClientManager, storage, relayService, sourceChainID, sourceClientID, destinationChainID, destinationClientID, pipelineOpts.ShouldRelaySuccessAcks, pipelineOpts.ShouldRelayErrorAcks)
	output = ConditionallyBatchProcess(metrics.ContextWithRelayType(ctx, metrics.EurekaRecvToAckRelayType), pipelineOpts.AckBatchConcurrency, pipelineOpts.AckBatchSize, pipelineOpts.AckBatchTimeout, output, NewEurekaBatchProcessorMW(storage, batchAckPacketProcessor))

	// check if the ack packet needs to be retried. if it does, remove the tx
	// hash from the db an error the transfer so the ack is retried on the next
	// run
	retryAckPacketProcessor := NewRetryAckPacketProcessor(bridgeClientManager, storage, sourceChainID)
	output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, retryAckPacketProcessor), output)

	// record the ack tx gas cost at current gas prices, if the pipeline has
	// all of the necessary info to do so
	if pipelineOpts.SourceChainGasTokenCoingeckoID != "" && pipelineOpts.SourceChainGasTokenDecimals != 0 && priceClient != nil {
		ackTxGasCalculator := NewAckTxGasCalculatorProcessor(bridgeClientManager, storage, priceClient, pipelineOpts.SourceChainGasTokenCoingeckoID, pipelineOpts.SourceChainGasTokenDecimals)
		output = pipeline.ProcessConcurrently(ctx, concurrency, NewEurekaProcessorMW(storage, ackTxGasCalculator), output)
	}

	output = pipeline.ProcessConcurrently(ctx, concurrency, NewStateFinisherProcessor(storage, pipelineOpts.ShouldRelaySuccessAcks, pipelineOpts.ShouldRelayErrorAcks), output)

	return &Pipeline[*EurekaTransfer, *EurekaTransfer]{Input: input, Output: output}
}

func (p Pipeline[Input, Output]) Push(_ context.Context, i Input) bool {
	p.Input <- i
	return true
}

func (p Pipeline[Input, Output]) Poll() (Output, error) {
	t, ok := <-p.Output
	if !ok {
		return t, fmt.Errorf("closed")
	}
	return t, nil
}

func (p Pipeline[Input, Output]) Close() {
	close(p.Input)
}

// EurekaPipelineManager creates and gives out pipelines for transfers. Each pipeline
// instance is unique to a (source chain, source client, dest chain, dest
// client) combination.
type EurekaPipelineManager struct {
	storage             Storage
	bridgeClientManager BridgeClientManager
	relayService        eurekarelayer.RelayerServiceClient
	priceClient         PriceClient
	pipelines           map[pipelineKey]EurekaPipeline
}

func NewEurekaPipelineManager(
	storage Storage,
	bridgeClientManager BridgeClientManager,
	relayService eurekarelayer.RelayerServiceClient,
	priceClient PriceClient,
) *EurekaPipelineManager {
	return &EurekaPipelineManager{
		storage:             storage,
		bridgeClientManager: bridgeClientManager,
		relayService:        relayService,
		priceClient:         priceClient,
		pipelines:           make(map[pipelineKey]EurekaPipeline),
	}
}

func (creator *EurekaPipelineManager) Pipeline(ctx context.Context, transfer *EurekaTransfer) (EurekaPipeline, error) {
	key := newPipelineKey(transfer)
	if pipeline, ok := creator.pipelines[key]; ok {
		return pipeline, nil
	}

	pipeline, err := creator.newPipelineForTransfer(ctx, transfer)
	if err != nil {
		return nil, fmt.Errorf("creating new pipeline for transfer from %s to %s: %w", transfer.GetSourceChainID(), transfer.GetDestinationChainID(), err)
	}

	creator.pipelines[key] = pipeline
	return pipeline, nil
}

func (creator *EurekaPipelineManager) newPipelineForTransfer(ctx context.Context, transfer *EurekaTransfer) (EurekaPipeline, error) {
	key := newPipelineKey(transfer)

	opts, err := NewPipelineOpts(ctx, transfer)
	if err != nil {
		return nil, fmt.Errorf("creating pipeline options: %w", err)
	}

	ctx = metrics.ContextWithSourceChainID(ctx, transfer.GetSourceChainID())
	ctx = metrics.ContextWithSourceClientID(ctx, transfer.GetPacketSourceClientID())
	ctx = metrics.ContextWithDestChainID(ctx, transfer.GetDestinationChainID())
	ctx = metrics.ContextWithDestClientID(ctx, transfer.GetPacketDestinationClientID())
	ctx = metrics.ContextWithBridgeType(ctx, metrics.EurekaBridgeType)

	return NewPipelineDeduper(NewPipeline(
		ctx,
		creator.storage,
		creator.bridgeClientManager,
		creator.relayService,
		creator.priceClient,
		key.SourceChainID,
		key.SourceClientID,
		key.DestinationChainID,
		key.DestinationClientID,
		opts,
	)), nil
}

func (creator *EurekaPipelineManager) Close() {
	for _, pipeline := range creator.pipelines {
		pipeline.Close()
	}
}

type pipelineKey struct {
	SourceChainID       string
	SourceClientID      string
	DestinationChainID  string
	DestinationClientID string
}

func newPipelineKey(transfer *EurekaTransfer) pipelineKey {
	return pipelineKey{
		SourceChainID:       transfer.GetSourceChainID(),
		SourceClientID:      transfer.GetPacketSourceClientID(),
		DestinationChainID:  transfer.GetDestinationChainID(),
		DestinationClientID: transfer.GetPacketDestinationClientID(),
	}
}

var _ EurekaPipeline = (*PipelineDeduper)(nil)

// PipelineDeduper is a wrapper around a pipeline that should be used to input
// values into the pipeline. If a value is pushed onto a pipeline that has
// already been input into the pipeline and it has not been output by the
// pipeline yet, then that input will be ignored.
type PipelineDeduper struct {
	pipeline       EurekaPipeline
	inPipeline     map[transferKey]struct{}
	inPipelineLock *sync.RWMutex
	once           *sync.Once
	done           chan struct{}
}

func NewPipelineDeduper(pipeline EurekaPipeline) *PipelineDeduper {
	return &PipelineDeduper{
		pipeline:       pipeline,
		inPipeline:     make(map[transferKey]struct{}),
		inPipelineLock: new(sync.RWMutex),
		once:           new(sync.Once),
		done:           make(chan struct{}),
	}
}

func (deduper *PipelineDeduper) Push(ctx context.Context, transfer *EurekaTransfer) bool {
	deduper.once.Do(func() {
		// if this is the first time we are pushing a transfer onto this
		// pipeline, spawn a goroutine to watch the output channel to delete
		// entries from the inPipeline map
		go func() {
			defer func() {
				deduper.done <- struct{}{}
			}()

			for {
				output, err := deduper.pipeline.Poll()
				if err != nil {
					return
				}
				if output == nil {
					return
				}
				deduper.inPipelineLock.Lock()
				delete(deduper.inPipeline, newTransferKey(output))
				deduper.inPipelineLock.Unlock()
			}
		}()
	})

	deduper.inPipelineLock.Lock()
	defer deduper.inPipelineLock.Unlock()

	key := newTransferKey(transfer)

	// check if transfer already is in the pipeline
	if _, ok := deduper.inPipeline[key]; ok {
		return false
	}

	// mark transfer as in pipeline and push
	deduper.inPipeline[key] = struct{}{}
	return deduper.pipeline.Push(ctx, transfer)
}

// Poll does nothing for a pipeline deduper, it overrides the output and consumes it infernally
func (deduper *PipelineDeduper) Poll() (*EurekaTransfer, error) {
	return nil, nil
}

func (deduper *PipelineDeduper) Close() {
	deduper.pipeline.Close()
	<-deduper.done
}

type transferKey struct {
	SourceChainID        string
	PacketSequenceNumber uint32
	PacketSourceClientID string
}

func newTransferKey(transfer *EurekaTransfer) transferKey {
	return transferKey{
		SourceChainID:        transfer.GetSourceChainID(),
		PacketSequenceNumber: transfer.GetPacketSequenceNumber(),
		PacketSourceClientID: transfer.GetPacketSourceClientID(),
	}
}
