package eureka_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cosmos/eureka-relayer/db/gen/db"
	mock_eureka "github.com/cosmos/eureka-relayer/mocks/relayer/eureka"
	"github.com/cosmos/eureka-relayer/relayer/eureka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEurekaProcessorMW_Process(t *testing.T) {
	sourceChainID := "sourceChainID"
	packetSequenceNumber := 10
	packetSourceClientID := "client-10"

	t.Run("happy path, state updated, processor output returned", func(t *testing.T) {
		ctx := context.Background()
		input := &eureka.EurekaTransfer{
			State:                db.EurekaRelayStatusPENDING,
			SourceChainID:        sourceChainID,
			PacketSequenceNumber: uint32(packetSequenceNumber),
			PacketSourceClientID: packetSourceClientID,
		}

		mockStorage := mock_eureka.NewMockTransferStateStorage(t)

		// expect that a storage update will happen, updating the transfer to
		// the state that is about to be processed
		mockUpdate := db.UpdateTransferStateParams{
			Status:               db.EurekaRelayStatusGETACKPACKET,
			SourceChainID:        input.GetSourceChainID(),
			PacketSequenceNumber: int32(input.GetPacketSequenceNumber()),
			PacketSourceClientID: input.GetPacketSourceClientID(),
		}
		mockStorage.EXPECT().UpdateTransferState(ctx, mockUpdate).Return(nil)

		mockProcessor := mock_eureka.NewMockEurekaProcessor(t)

		// expect that the input to the mock processor will have its state
		// updated to be the state of the mock processor
		processorInput := &eureka.EurekaTransfer{
			State:                db.EurekaRelayStatusGETACKPACKET,
			SourceChainID:        sourceChainID,
			PacketSequenceNumber: uint32(packetSequenceNumber),
			PacketSourceClientID: packetSourceClientID,
		}
		recvTxHash := "0xdeadbeef"
		processorOutput := &eureka.EurekaTransfer{
			State:                db.EurekaRelayStatusGETACKPACKET,
			SourceChainID:        input.GetSourceChainID(),
			PacketSequenceNumber: input.GetPacketSequenceNumber(),
			PacketSourceClientID: input.GetPacketSourceClientID(),
			RecvTxHash:           &recvTxHash,
		}
		mockProcessor.EXPECT().Process(ctx, processorInput).Return(processorOutput, nil)

		// this mock processor should process this input
		mockProcessor.EXPECT().ShouldProcess(input).Return(true)
		// the state that the mock processor should return
		mockProcessor.EXPECT().State().Return(db.EurekaRelayStatusGETACKPACKET)

		output, err := eureka.NewEurekaProcessorMW(mockStorage, mockProcessor).Process(ctx, input)
		assert.NoError(t, err)

		// expect that the output from the processor is returned unchanged
		assert.Equal(t, *processorOutput, *output)
	})

	t.Run("transfer in error state is not processed", func(t *testing.T) {
		ctx := context.Background()

		input := &eureka.EurekaTransfer{ProcessingError: fmt.Errorf("error")}

		mockStorage := mock_eureka.NewMockTransferStateStorage(t)

		mockProcessor := mock_eureka.NewMockEurekaProcessor(t)

		output, err := eureka.NewEurekaProcessorMW(mockStorage, mockProcessor).Process(ctx, input)
		assert.NoError(t, err)

		// the mockProcessor should not be called when the input has errored,
		// so the input should be returned as the output
		assert.Equal(t, *input, *output)
	})

	t.Run("transfer is not processed and state not updated if processor.ShouldProcess returns false", func(t *testing.T) {
		ctx := context.Background()
		input := &eureka.EurekaTransfer{
			State:                db.EurekaRelayStatusPENDING,
			SourceChainID:        sourceChainID,
			PacketSequenceNumber: uint32(packetSequenceNumber),
			PacketSourceClientID: packetSourceClientID,
		}

		mockStorage := mock_eureka.NewMockTransferStateStorage(t)

		mockProcessor := mock_eureka.NewMockEurekaProcessor(t)

		// this mock processor should *NOT* process this input
		mockProcessor.EXPECT().ShouldProcess(input).Return(false)

		output, err := eureka.NewEurekaProcessorMW(mockStorage, mockProcessor).Process(ctx, input)
		assert.NoError(t, err)

		// the mockProcessor should not be called when ShouldProcess has
		// returned false so the input should be returned as output
		assert.Equal(t, *input, *output)
	})

	t.Run("updating transfer state fails, processor is not called, cancel function is called", func(t *testing.T) {
		ctx := context.Background()
		input := &eureka.EurekaTransfer{
			State:                db.EurekaRelayStatusPENDING,
			SourceChainID:        sourceChainID,
			PacketSequenceNumber: uint32(packetSequenceNumber),
			PacketSourceClientID: packetSourceClientID,
		}
		expected := *input

		mockStorage := mock_eureka.NewMockTransferStateStorage(t)

		// expect that a storage update will be attempted and then fail
		stateUpdateErr := fmt.Errorf("update state error")
		mockUpdate := db.UpdateTransferStateParams{
			Status:               db.EurekaRelayStatusGETACKPACKET,
			SourceChainID:        input.GetSourceChainID(),
			PacketSequenceNumber: int32(input.GetPacketSequenceNumber()),
			PacketSourceClientID: input.GetPacketSourceClientID(),
		}
		mockStorage.EXPECT().UpdateTransferState(ctx, mockUpdate).Return(stateUpdateErr)

		mockProcessor := mock_eureka.NewMockEurekaProcessor(t)

		// this mock processor should process this input
		mockProcessor.EXPECT().ShouldProcess(input).Return(true)

		// expect that the processors cancel function is called with an
		// unmodified input (i.e. no state update yet) and the error that
		// occurred.
		// NOTE: using a custom matcher here since the processor will wrap the
		// error before calling cancel, so we have to check if the wrapped
		// error contains our target error, if we simply require 'err' as the
		// arg to Cancel, this will fail
		mockProcessor.EXPECT().Cancel(input, mock.MatchedBy(func(arg error) bool { return errors.Is(arg, stateUpdateErr) }))

		// the state that the mock processor should return
		mockProcessor.EXPECT().State().Return(db.EurekaRelayStatusGETACKPACKET)

		output, err := eureka.NewEurekaProcessorMW(mockStorage, mockProcessor).Process(ctx, input)
		assert.NoError(t, err)

		// expected that the output ProcessingError contains the stateUpdateErr
		assert.ErrorIs(t, output.ProcessingError, stateUpdateErr)

		// have to clear the output ProcessingError since it is wrapped and
		// wont actually match the stateUpdateError when we compare the structs
		output.ProcessingError = nil

		// expect the output is equal to the original input, i.e no state
		// changes happen to the output
		assert.Equal(t, expected, *output)
	})

	t.Run("processing input fails, cancel function is called", func(t *testing.T) {
		ctx := context.Background()
		input := &eureka.EurekaTransfer{
			State:                db.EurekaRelayStatusPENDING,
			SourceChainID:        sourceChainID,
			PacketSequenceNumber: uint32(packetSequenceNumber),
			PacketSourceClientID: packetSourceClientID,
		}
		expected := *input

		mockStorage := mock_eureka.NewMockTransferStateStorage(t)

		// expect that a storage update will happen, updating the transfer to
		// the state that is about to be processed
		mockUpdate := db.UpdateTransferStateParams{
			Status:               db.EurekaRelayStatusGETACKPACKET,
			SourceChainID:        input.GetSourceChainID(),
			PacketSequenceNumber: int32(input.GetPacketSequenceNumber()),
			PacketSourceClientID: input.GetPacketSourceClientID(),
		}
		mockStorage.EXPECT().UpdateTransferState(ctx, mockUpdate).Return(nil)

		mockProcessor := mock_eureka.NewMockEurekaProcessor(t)

		// expect that the input to the mock processor will have its state
		// updated to be the state of the mock processor
		processorInput := &eureka.EurekaTransfer{
			State:                db.EurekaRelayStatusGETACKPACKET,
			SourceChainID:        sourceChainID,
			PacketSequenceNumber: uint32(packetSequenceNumber),
			PacketSourceClientID: packetSourceClientID,
		}
		processingErr := fmt.Errorf("processing error")
		mockProcessor.EXPECT().Process(ctx, processorInput).Return(nil, processingErr)

		// this mock processor should process this input
		mockProcessor.EXPECT().ShouldProcess(input).Return(true)
		// the state that the mock processor should return
		mockProcessor.EXPECT().State().Return(db.EurekaRelayStatusGETACKPACKET)
		// expect that the processors cancel function is called with an
		// unmodified input (i.e. no state update yet) and the error that
		// occurred.
		// NOTE: using a custom matcher here since the processor will wrap the
		// error before calling cancel, so we have to check if the wrapped
		// error contains our target error, if we simply require 'err' as the
		// arg to Cancel, this will fail
		mockProcessor.EXPECT().Cancel(input, mock.MatchedBy(func(arg error) bool { return errors.Is(arg, processingErr) }))

		output, err := eureka.NewEurekaProcessorMW(mockStorage, mockProcessor).Process(ctx, input)
		assert.NoError(t, err)

		// expected that the output ProcessingError contains the processingErr
		assert.ErrorIs(t, output.ProcessingError, processingErr)

		// have to clear the output ProcessingError since it is wrapped and
		// wont actually match the processingErr when we compare the structs
		output.ProcessingError = nil

		// expect that the input is returned as output *WITH* its state updated
		expected.State = db.EurekaRelayStatusGETACKPACKET
		assert.Equal(t, expected, *output)
	})
}
