package eureka

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmos/eureka-relayer/db/gen/db"
	"github.com/cosmos/eureka-relayer/shared/lmt"
	"github.com/deliveryhero/pipeline/v2"
	"go.uber.org/zap"
)

type TransferStateStorage interface {
	UpdateTransferState(ctx context.Context, arg db.UpdateTransferStateParams) error
}

// EurekaProcessor defines the methods needed to be a processor in a Eureka
// transfer pipeline and to be wrapped by the EurekaProcessorMW helper.
type EurekaProcessor interface {
	// ShouldProcess returns true if a processor should process some input,
	// false otherwise
	ShouldProcess(input *EurekaTransfer) bool

	// State returns the state that a transfer is in, if it is being processed
	// by this processor
	State() db.EurekaRelayStatus

	// inherit Process and Cancel methods
	pipeline.Processor[*EurekaTransfer, *EurekaTransfer]
}

// EurekaProcessorMW is a wrapper around a pipeline processor that has eureka
// specific helper logic. All eureka processors should be wrapped with this.
type EurekaProcessorMW[Input *EurekaTransfer, Output *EurekaTransfer] struct {
	storage  TransferStateStorage
	internal EurekaProcessor
}

func NewEurekaProcessorMW[Input *EurekaTransfer, Output *EurekaTransfer](
	storage TransferStateStorage,
	internal EurekaProcessor,
) EurekaProcessorMW[Input, Output] {
	return EurekaProcessorMW[Input, Output]{
		storage:  storage,
		internal: internal,
	}
}

// Process is a noop if the input transfer has errored, (i.e. another processor
// returned an error for this transfer). If the interal processor implements a
// ShouldProcess method, that will be invoked to check if the processor should
// be invoked or skipped. Finally, the internal processor is called. If the
// processor returns an error, the internal processor's cancel function will be
// called and the transfer will be marked as an error.
func (processor EurekaProcessorMW[Input, Output]) Process(
	ctx context.Context,
	input *EurekaTransfer,
) (Output, error) {
	if input.Error() != "" || input == nil {
		// if we get input that has errored, pass to the next processor
		// input may be nil when all the input and output channels are closing
		return input, nil
	}
	if input.GetState() == db.EurekaRelayStatusFAILED {
		// do not try and process transfers that have been marked as failed
		return input, nil
	}

	// check that the internal processor should process the input, if not,
	// return the input without processing
	if !processor.internal.ShouldProcess(input) {
		return input, nil
	}

	// this internal processor should process this transfer, update its state
	// to match the processor that is about to process it
	stateUpdate := db.UpdateTransferStateParams{
		Status:               processor.internal.State(),
		SourceChainID:        input.GetSourceChainID(),
		PacketSourceClientID: input.GetPacketSourceClientID(),
		PacketSequenceNumber: int32(input.GetPacketSequenceNumber()),
	}
	if err := processor.storage.UpdateTransferState(ctx, stateUpdate); err != nil {
		wrapped := fmt.Errorf(
			"updating transfer state to %s from state %s: %w",
			processor.internal.State(),
			input.GetState(),
			err,
		)
		processor.Cancel(input, wrapped)
		input.ProcessingError = wrapped
		return input, nil
	}

	// update the transfers state to the processor that is about to process
	// this transfer
	prevState := input.State
	input.State = processor.internal.State()

	input.GetLogger().Debug(
		fmt.Sprintf("processing transfer at state %s", string(input.State)),
		zap.String("state", string(input.State)),
		zap.String("prev_state", string(prevState)),
	)

	output, err := processor.internal.Process(ctx, input)
	if err != nil {
		// if the processor returns an error, call the cancel function and set
		// the transfers ProcessingError to the error.
		//
		// NOTE: we are not actually returning the error that the internal
		// processor returned via this Process call. We do this because if
		// simply let the cancel function run and do nothing else, there is no
		// way to deliver the transfer to the Pipelines output channel. So
		// instead, we set a field on the transfer that signals an error
		// occurred, but still continue to send it through the pipeline (but it
		// will just be ignored by every other step since because of the Error
		// check at the start of this function). The internal Processor cancel
		// functions can still handle the error though, typically this would
		// look like logging it, and updating the state in the db to 'FAILED'
		// if the error is fatal and the transfer should not be retried.

		// if the error is context cancelled, the pipeline is over and we dont
		// care about the oboe, return without calling cancel since the outer
		// processor should do it for us
		if errors.Is(err, context.Canceled) {
			return nil, err
		}

		processor.Cancel(input, err)
		input.ProcessingError = err
		return input, nil
	}

	input.GetLogger().Debug(
		fmt.Sprintf("successfully processed transfer at state %s", string(input.State)),
		zap.String("state", string(input.State)),
		zap.String("prev_state", string(prevState)),
	)

	return output, nil
}

// Cancel calls the internal processors Cancel function
func (processor EurekaProcessorMW[Input, Output]) Cancel(input *EurekaTransfer, err error) {
	if input == nil {
		return
	}
	processor.internal.Cancel(input, err)
}

// EurekaProcessor defines the methods needed to be a processor in a Eureka
// transfer pipeline and to be wrapped by the EurekaProcessorMW helper.
type EurekaBatchProcessor interface {
	// ShouldProcess returns true if a processor should process some input
	// within a batch, false otherwise
	ShouldProcess(input *EurekaTransfer) bool

	// State returns the state that a transfer is in, if it is being processed
	// by this processor
	State() db.EurekaRelayStatus

	// inherit Process and Cancel methods
	pipeline.Processor[[]*EurekaTransfer, []*EurekaTransfer]
}

// EurekaBatchProcessorMW is a wrapper around a pipeline processor that has eureka
// specific helper logic. All eureka processors should be wrapped with this.
type EurekaBatchProcessorMW[Input []*EurekaTransfer, Output []*EurekaTransfer] struct {
	storage  TransferStateStorage
	internal EurekaBatchProcessor
}

func NewEurekaBatchProcessorMW[Input []*EurekaTransfer, Output []*EurekaTransfer](
	storage TransferStateStorage,
	internal EurekaBatchProcessor,
) EurekaBatchProcessorMW[Input, Output] {
	return EurekaBatchProcessorMW[Input, Output]{
		storage:  storage,
		internal: internal,
	}
}

func (processor EurekaBatchProcessorMW[Input, Output]) Process(
	ctx context.Context,
	batch []*EurekaTransfer,
) ([]*EurekaTransfer, error) {
	lmt.Logger(ctx).Debug(
		fmt.Sprintf("processing %d transfer batch", len(batch)),
		zap.String("state", string(processor.internal.State())),
	)

	var notProcessing []*EurekaTransfer
	var toProcess []*EurekaTransfer
	for _, input := range batch {
		if input.Error() != "" {
			// note that if we return an error here then processing the entire
			// batch will fail, we instead we just ignore this input when
			// processing
			notProcessing = append(notProcessing, input)
			continue
		}
		if input.GetState() == db.EurekaRelayStatusFAILED {
			// do not try and process transfers that have been marked as failed
			notProcessing = append(notProcessing, input)
			continue
		}

		if !processor.internal.ShouldProcess(input) {
			// note that if we return an error here then processing the entire
			// batch will fail, we instead we just ignore this input when
			// processing
			notProcessing = append(notProcessing, input)
			continue
		}

		// this internal processor should process this input in a batch, update
		// its state to match the processor that is about to process it
		stateUpdate := db.UpdateTransferStateParams{
			Status:               processor.internal.State(),
			SourceChainID:        input.GetSourceChainID(),
			PacketSourceClientID: input.GetPacketSourceClientID(),
			PacketSequenceNumber: int32(input.GetPacketSequenceNumber()),
		}
		if err := processor.storage.UpdateTransferState(ctx, stateUpdate); err != nil {
			wrapped := fmt.Errorf(
				"updating transfer state to %s from state %s: %w",
				processor.internal.State(),
				input.GetState(),
				err,
			)
			processor.Cancel([]*EurekaTransfer{input}, wrapped)
			input.ProcessingError = wrapped
			notProcessing = append(notProcessing, input)
		}

		// update the transfers state to the processor that is about to process
		// this transfer
		input.State = processor.internal.State()
		toProcess = append(toProcess, input)
	}

	output, err := processor.internal.Process(ctx, toProcess)
	if err != nil {
		// if the processor returns an error, call the cancel function and set
		// the transfers ProcessingError to the error.
		//
		// NOTE: we are not actually returning the error that the internal
		// processor returned via this Process call. We do this because if
		// simply let the cancel function run and do nothing else, there is no
		// way to deliver the transfer to the Pipelines output channel. So
		// instead, we set a field on the transfer that signals an error
		// occurred, but still continue to send it through the pipeline (but it
		// will just be ignored by every other step since because of the Error
		// check at the start of this function). The internal Processor cancel
		// functions can still handle the error though, typically this would
		// look like logging it, and updating the state in the db to 'FAILED'
		// if the error is fatal and the transfer should not be retried.

		// if the error is context cancelled, the pipeline is over and we dont
		// care about the oboe, return without calling cancel since the outer
		// processor should do it for us
		if errors.Is(err, context.Canceled) {
			return nil, err
		}

		processor.Cancel(toProcess, err)
		for _, input := range toProcess {
			input.ProcessingError = err
		}
		return append(toProcess, notProcessing...), nil
	}

	return append(output, notProcessing...), nil
}

// Cancel calls the internal processors Cancel function
func (processor EurekaBatchProcessorMW[Input, Output]) Cancel(input []*EurekaTransfer, err error) {
	processor.internal.Cancel(input, err)
}

func (processor EurekaBatchProcessorMW[Input, Output]) ShouldProcess(input *EurekaTransfer) bool {
	return processor.internal.ShouldProcess(input)
}

func (processor EurekaBatchProcessorMW[Input, Output]) State() db.EurekaRelayStatus {
	return processor.internal.State()
}
