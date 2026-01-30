package eureka_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/cosmos/eureka-relayer/db/gen/db"
	mock_eureka "github.com/cosmos/eureka-relayer/mocks/relayer/eureka"
	mock_config "github.com/cosmos/eureka-relayer/mocks/shared/config"
	"github.com/cosmos/eureka-relayer/relayer/eureka"
	"github.com/cosmos/eureka-relayer/shared/config"
	"github.com/cosmos/eureka-relayer/shared/lmt"
)

const (
	evmChainID    = "evmChainID"
	cosmosChainID = "cosmosChainID"
	clientID      = "client-10"
	inputSize     = 100
)

type MockPipeline struct {
	Input  chan *eureka.EurekaTransfer
	Output chan *eureka.EurekaTransfer
}

// newDisconnectedEurekaPipeline creates a eureka pipeline where the input is
// not sent to the output
func newDisconnectedEurekaPipeline() *MockPipeline {
	input := make(chan *eureka.EurekaTransfer, inputSize)
	output := make(chan *eureka.EurekaTransfer)

	return &MockPipeline{
		Input:  input,
		Output: output,
	}
}

func (m *MockPipeline) Push(ctx context.Context, transfer *eureka.EurekaTransfer) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		select {
		case <-ctx.Done():
			return false
		case m.Input <- transfer:
			return true
		}
	}
}

func (m *MockPipeline) Poll() (*eureka.EurekaTransfer, error) {
	t, ok := <-m.Output
	if !ok {
		return t, fmt.Errorf("closed")
	}
	return t, nil
}

func (m *MockPipeline) Close() {
}

func TestRelayDispatcher_Run(t *testing.T) {
	t.Run("submits all waiting transfers until context is cancelled", func(t *testing.T) {
		// this test doenst assert much (besides checking that the error is
		// what we expect), but the real test here is making sure this doesnt
		// dead lock and properly drains the output, which if this returns we
		// did properly

		lmt.ConfigureLogger()
		ctx, cancel := context.WithCancel(lmt.LoggerContext(context.Background()))

		configReader := mock_config.NewMockConfigReader(t)
		configReader.EXPECT().GetChainConfig(mock.Anything).Return(config.ChainConfig{Eureka: &config.EurekaConfig{}}, nil).Maybe()
		ctx = config.ConfigReaderContext(ctx, configReader)

		mockStorage := mock_eureka.NewMockUnfinishedEurekaTransferStorage(t)

		unfinishedTranfsers := []db.EurekaTransfer{
			newDBTransfer(evmChainID, cosmosChainID, clientID, 0),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 1),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 2),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 3),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 4),
		}
		mockStorage.EXPECT().GetUnfinishedEurekaTransfers(ctx).Return(unfinishedTranfsers, nil)

		mockPipeline := newDisconnectedEurekaPipeline()
		mockManager := mock_eureka.NewMockPipelineManager(t)
		mockManager.EXPECT().Close()
		for _, transfer := range unfinishedTranfsers {
			t := eureka.NewEurekaTransfer(ctx, transfer)
			mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
				return arg.PacketSequenceNumber == t.PacketSequenceNumber
			})).Return(mockPipeline, nil)
		}

		poll := time.Minute // only poll once per minute
		pusher := eureka.NewRelayDispatcher(mockStorage, poll, mockManager, true)

		// cancel context after waiting .5 seconds
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()

		assert.ErrorIs(t, pusher.Run(ctx), context.Canceled)
	})
}

func TestRelayDispatcher_SubmitUnfinishedTransfers(t *testing.T) {
	t.Run("submits all unfinished transfers", func(t *testing.T) {
		ctx := context.Background()

		configReader := mock_config.NewMockConfigReader(t)
		configReader.EXPECT().GetChainConfig(mock.Anything).Return(config.ChainConfig{Eureka: &config.EurekaConfig{}}, nil).Maybe()
		ctx = config.ConfigReaderContext(ctx, configReader)

		mockStorage := mock_eureka.NewMockUnfinishedEurekaTransferStorage(t)

		unfinishedTranfsers := []db.EurekaTransfer{
			newDBTransfer(evmChainID, cosmosChainID, clientID, 0),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 1),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 2),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 3),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 4),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 5),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 6),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 7),
		}
		mockStorage.EXPECT().GetUnfinishedEurekaTransfers(ctx).Return(unfinishedTranfsers, nil)

		mockPipeline := newDisconnectedEurekaPipeline()
		mockManager := mock_eureka.NewMockPipelineManager(t)
		for _, transfer := range unfinishedTranfsers {
			t := eureka.NewEurekaTransfer(ctx, transfer)
			mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
				return arg.PacketSequenceNumber == t.PacketSequenceNumber
			})).Return(mockPipeline, nil)
		}

		pusher := eureka.NewRelayDispatcher(mockStorage, 0, mockManager, true)

		assert.NoError(t, pusher.SubmitWaitingUnfinishedTransfers(ctx))
		assert.Len(t, mockPipeline.Input, len(unfinishedTranfsers))
	})

	t.Run("error getting unfinished transfers", func(t *testing.T) {
		ctx := context.Background()

		configReader := mock_config.NewMockConfigReader(t)
		configReader.EXPECT().GetChainConfig(mock.Anything).Return(config.ChainConfig{Eureka: &config.EurekaConfig{}}, nil).Maybe()
		ctx = config.ConfigReaderContext(ctx, configReader)

		mockStorage := mock_eureka.NewMockUnfinishedEurekaTransferStorage(t)

		getTransfersErr := fmt.Errorf("error")
		mockStorage.EXPECT().GetUnfinishedEurekaTransfers(ctx).Return(nil, getTransfersErr)

		pusher := eureka.NewRelayDispatcher(mockStorage, 0, nil, true)

		assert.ErrorIs(t, pusher.SubmitWaitingUnfinishedTransfers(ctx), getTransfersErr)
	})

	t.Run("submitting transfer fails, transfer is marked as failed", func(t *testing.T) {
		ctx := context.Background()

		configReader := mock_config.NewMockConfigReader(t)
		configReader.EXPECT().GetChainConfig(mock.Anything).Return(config.ChainConfig{Eureka: &config.EurekaConfig{}}, nil).Maybe()
		ctx = config.ConfigReaderContext(ctx, configReader)

		mockStorage := mock_eureka.NewMockUnfinishedEurekaTransferStorage(t)
		mockStorage.EXPECT().UpdateTransferState(ctx, db.UpdateTransferStateParams{Status: db.EurekaRelayStatusFAILED, SourceChainID: evmChainID, PacketSourceClientID: clientID, PacketSequenceNumber: 0}).Return(nil)

		unfinishedTranfsers := []db.EurekaTransfer{newDBTransfer(evmChainID, cosmosChainID, clientID, 0)}
		mockStorage.EXPECT().GetUnfinishedEurekaTransfers(ctx).Return(unfinishedTranfsers, nil)

		mockPipeline := newDisconnectedEurekaPipeline()
		mockManager := mock_eureka.NewMockPipelineManager(t)
		for _, transfer := range unfinishedTranfsers {
			t := eureka.NewEurekaTransfer(ctx, transfer)
			mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
				return arg.PacketSequenceNumber == t.PacketSequenceNumber
			})).Return(nil, fmt.Errorf("unexpected error"))
		}

		pusher := eureka.NewRelayDispatcher(mockStorage, 0, mockManager, true)

		assert.NoError(t, pusher.SubmitWaitingUnfinishedTransfers(ctx))
		assert.Empty(t, mockPipeline.Input)
	})

	t.Run("submitting transfer fails, others in batch are still submitted", func(t *testing.T) {
		ctx := context.Background()

		configReader := mock_config.NewMockConfigReader(t)
		configReader.EXPECT().GetChainConfig(mock.Anything).Return(config.ChainConfig{Eureka: &config.EurekaConfig{}}, nil).Maybe()
		ctx = config.ConfigReaderContext(ctx, configReader)

		mockStorage := mock_eureka.NewMockUnfinishedEurekaTransferStorage(t)
		mockStorage.EXPECT().UpdateTransferState(ctx, db.UpdateTransferStateParams{Status: db.EurekaRelayStatusFAILED, SourceChainID: evmChainID, PacketSourceClientID: clientID, PacketSequenceNumber: 0}).Return(nil)

		unfinishedTranfsers := []db.EurekaTransfer{
			newDBTransfer(evmChainID, cosmosChainID, clientID, 0),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 1),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 2),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 3),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 4),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 5),
			newDBTransfer(evmChainID, cosmosChainID, clientID, 6),
		}

		mockStorage.EXPECT().GetUnfinishedEurekaTransfers(ctx).Return(unfinishedTranfsers, nil)

		mockManager := mock_eureka.NewMockPipelineManager(t)
		mockPipeline := newDisconnectedEurekaPipeline()

		transfer := eureka.NewEurekaTransfer(ctx, unfinishedTranfsers[0])
		mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
			return arg.PacketSequenceNumber == transfer.PacketSequenceNumber
		})).Return(nil, fmt.Errorf("unexpected error"))

		for _, transfer := range unfinishedTranfsers[1:] {
			t := eureka.NewEurekaTransfer(ctx, transfer)
			mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
				return arg.PacketSequenceNumber == t.PacketSequenceNumber
			})).Return(mockPipeline, nil)
		}

		pusher := eureka.NewRelayDispatcher(mockStorage, 0, mockManager, true)

		assert.NoError(t, pusher.SubmitWaitingUnfinishedTransfers(ctx))
		assert.Len(t, mockPipeline.Input, len(unfinishedTranfsers)-1)
	})
}

func TestRelayDispatcher_SubmitTransfer(t *testing.T) {
	t.Run("gets pipeline and pushes", func(t *testing.T) {
		ctx := context.Background()

		configReader := mock_config.NewMockConfigReader(t)
		configReader.EXPECT().GetChainConfig(mock.Anything).Return(config.ChainConfig{Eureka: &config.EurekaConfig{}}, nil).Maybe()
		ctx = config.ConfigReaderContext(ctx, configReader)

		t1 := newTransfer(evmChainID, cosmosChainID, clientID, 0)
		t2 := newTransfer(evmChainID, cosmosChainID, clientID, 1)

		mockPipeline := newDisconnectedEurekaPipeline()
		mockManager := mock_eureka.NewMockPipelineManager(t)
		mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
			return arg.PacketSequenceNumber == 0
		})).Return(mockPipeline, nil)
		mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
			return arg.PacketSequenceNumber == 1
		})).Return(mockPipeline, nil)

		pusher := eureka.NewRelayDispatcher(nil, 0, mockManager, true)

		assert.NoError(t, pusher.SubmitTransfer(ctx, t1))
		assert.NoError(t, pusher.SubmitTransfer(ctx, t2))
		assert.Len(t, mockPipeline.Input, 2)
	})

	t.Run("returns an error when transfer isnt pushed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		configReader := mock_config.NewMockConfigReader(t)
		configReader.EXPECT().GetChainConfig(mock.Anything).Return(config.ChainConfig{Eureka: &config.EurekaConfig{}}, nil).Maybe()
		ctx = config.ConfigReaderContext(ctx, configReader)

		t1 := newTransfer(evmChainID, cosmosChainID, clientID, 0)
		t2 := newTransfer(evmChainID, cosmosChainID, clientID, 1)

		mockPipeline := newDisconnectedEurekaPipeline()
		mockManager := mock_eureka.NewMockPipelineManager(t)
		mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
			return arg.PacketSequenceNumber == t1.PacketSequenceNumber
		})).Return(mockPipeline, nil)
		mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
			return arg.PacketSequenceNumber == t2.PacketSequenceNumber
		})).Return(mockPipeline, nil)

		pusher := eureka.NewRelayDispatcher(nil, 0, mockManager, true)
		assert.NoError(t, pusher.SubmitTransfer(ctx, t1))
		assert.NoError(t, pusher.SubmitTransfer(ctx, t2))
		assert.Len(t, mockPipeline.Input, 2)

		cancel()

		t3 := newTransfer(evmChainID, cosmosChainID, clientID, 3)
		mockManager.EXPECT().Pipeline(mock.Anything, mock.MatchedBy(func(arg *eureka.EurekaTransfer) bool {
			return arg.PacketSequenceNumber == t3.PacketSequenceNumber
		})).Return(mockPipeline, nil)

		assert.Error(t, pusher.SubmitTransfer(ctx, t3))
		assert.Len(t, mockPipeline.Input, 2)
	})
}

func newTransfer(sourceChainID, destinationChainID, packetSourceClientID string, packetSequenceNumber uint32) *eureka.EurekaTransfer {
	return &eureka.EurekaTransfer{
		SourceChainID:        sourceChainID,
		PacketSequenceNumber: packetSequenceNumber,
		PacketSourceClientID: packetSourceClientID,
		DestinationChainID:   destinationChainID,
	}
}

func newDBTransfer(sourceChainID, destinationChainID, packetSourceClientID string, packetSequenceNumber int32) db.EurekaTransfer {
	return db.EurekaTransfer{
		SourceChainID:        sourceChainID,
		PacketSequenceNumber: packetSequenceNumber,
		PacketSourceClientID: packetSourceClientID,
		DestinationChainID:   destinationChainID,
	}
}
