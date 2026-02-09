package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cosmos/eureka-relayer/db/gen/db"
	"github.com/cosmos/eureka-relayer/db/tx"
	"github.com/cosmos/eureka-relayer/gasmonitor"
	"github.com/cosmos/eureka-relayer/proto/gen/eurekarelayer"
	"github.com/cosmos/eureka-relayer/relayer/eureka"
	"github.com/cosmos/eureka-relayer/relayerapi/server"
	"github.com/cosmos/eureka-relayer/shared/clients/coingecko"
	"github.com/cosmos/eureka-relayer/shared/config"
	"github.com/cosmos/eureka-relayer/shared/database"
	"github.com/cosmos/eureka-relayer/shared/lmt"
	"github.com/cosmos/eureka-relayer/shared/metrics"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	configPath           = flag.String("config", "./config/local/config.yml", "path to relayer config file")
	enableEurekaRelaying = flag.Bool("eureka-relaying", true, "if eureka relaying should be enabled")
)

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	lmt.ConfigureLogger()
	ctx = lmt.LoggerContext(ctx)

	promMetrics := metrics.NewPromMetrics()
	ctx = metrics.ContextWithMetrics(ctx, promMetrics)

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		lmt.Logger(ctx).Fatal("Unable to load config", zap.Error(err))
	}
	ctx = config.ConfigReaderContext(ctx, config.NewConfigReader(cfg))

	dsn := config.GetConfigReader(ctx).GetPostgresConnString()
	pool, err := database.NewDatabase(ctx, dsn, config.GetConfigReader(ctx).PostgresIAMAuthEnabled())
	if err != nil {
		lmt.Logger(ctx).Fatal("Unable to connect to database: %v", zap.Error(err))
	}

	var eurekaClientManager eureka.BridgeClientManager
	var eurekaChainIDToPrivateKey map[string]string
	var signerConn *grpc.ClientConn

	signing := cfg.Signing
	if signing.GRPCAddress != "" {
		lmt.Logger(ctx).Info("Remote signer configured for Eureka relayer",
			zap.String("grpc_address", signing.GRPCAddress),
			zap.String("cosmos_wallet_id", signing.CosmosWalletKey),
			zap.String("evm_wallet_id", signing.EVMWalletKey))

		conn, err := grpc.NewClient(
			signing.GRPCAddress,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			lmt.Logger(ctx).Fatal("failed to connect to remote signer",
				zap.String("address", signing.GRPCAddress),
				zap.Error(err))
		}
		signerConn = conn
		defer signerConn.Close()
	} else if signing.KeysPath != "" {
		keys, err := LoadChainIDToPrivateKeyMap(signing.KeysPath)
		if err != nil {
			lmt.Logger(ctx).Fatal("Failed to load chain id -> private key map for eureka", zap.Error(err))
		}
		eurekaChainIDToPrivateKey = keys
		lmt.Logger(ctx).Info("Using local keys for signing", zap.String("keys_path", signing.KeysPath))
	} else {
		lmt.Logger(ctx).Fatal("No signing configuration: set either signing.grpc_address or signing.keys_path")
	}

	eurekaClientManager, err = eureka.NewClientManagerFromConfig(
		ctx,
		eurekaChainIDToPrivateKey,
		signerConn,
		signing.CosmosWalletKey,
		signing.EVMWalletKey,
		config.GetConfigReader(ctx).GetEurekaChains()...,
	)
	if err != nil {
		lmt.Logger(ctx).Fatal("error creating eureka client manager from config", zap.Error(err))
	}

	var coingeckoClient eureka.PriceClient
	coingeckoConfig := config.GetConfigReader(ctx).GetCoingeckoConfig()
	if coingeckoConfig.APIKey != "" {
		coingeckoClient = coingecko.NewCachedPriceClient(coingecko.DefaultCoingeckoClient(coingeckoConfig), coingeckoConfig.CacheRefreshInterval)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		lmt.Logger(ctx).Info("Starting Prometheus")
		if err := metrics.StartPrometheus(ctx, cfg.Metrics.PrometheusAddress); err != nil {
			return err
		}
		return nil
	})

	eg.Go(func() error {
		apiConfig := config.GetConfigReader(ctx).GetRelayerAPIConfig()
		grpcServer, err := server.NewRelayerGRPCServer(ctx, pool, eurekaClientManager, apiConfig.Address)
		if err != nil {
			return err
		}
		grpcServer.Start(ctx)
		return nil
	})

	eg.Go(func() error {
		gasMonitor := gasmonitor.NewGasMonitor(eurekaClientManager)
		err := gasMonitor.Start(ctx)
		if err != nil {
			return fmt.Errorf("creating gas monitor: %w", err)
		}
		return nil
	})

	// create connection to proof relayer
	proofRelayerConfig := config.GetConfigReader(ctx).GetEurekaProofRelayerConfig()

	var opts []grpc.DialOption
	if !proofRelayerConfig.GRPCTLSEnabled {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})))
	}
	opts = append(opts, grpc.WithUnaryInterceptor(metrics.UnaryClientInterceptor))

	opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1024*1024*10)))

	conn, err := grpc.NewClient(proofRelayerConfig.GRPCAddress, opts...)
	if err != nil {
		lmt.Logger(ctx).Fatal(
			"error creating grpc connection to proof relayer",
			zap.String("address", proofRelayerConfig.GRPCAddress),
			zap.Error(err),
		)
	}

	relayer := eurekarelayer.NewRelayerServiceClient(conn)
	defer conn.Close()

	// create storage for eureka transactions
	storage := tx.New(db.New(pool), pool)

	// create a pipeline manager to create new pipelines for new packet
	// transfer paths
	manager := eureka.NewEurekaPipelineManager(storage, eurekaClientManager, relayer, coingeckoClient)

	// create relay dispatcher to submit relays to the pipeline from storage
	dispatcher := eureka.NewRelayDispatcher(storage, 5*time.Second, manager, *enableEurekaRelaying)

	eg.Go(func() error {
		if err := dispatcher.Run(ctx); err != nil {
			return fmt.Errorf("running eureka relayer: %w", err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		lmt.Logger(ctx).Fatal("Error running Relayer", zap.Error(err))
	}
}

func LoadChainIDToPrivateKeyMap(keysPath string) (map[string]string, error) {
	keysBytes, err := os.ReadFile(keysPath)
	if err != nil {
		return nil, err
	}

	rawKeysMap := make(map[string]map[string]string)
	if err := json.Unmarshal(keysBytes, &rawKeysMap); err != nil {
		return nil, err
	}

	keysMap := make(map[string]string)
	for key, value := range rawKeysMap {
		keysMap[key] = value["private_key"]
	}

	return keysMap, nil
}
