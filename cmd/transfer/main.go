package main

import (
	"context"
	"flag"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/cosmos/eureka-relayer/shared/config"
	"github.com/cosmos/eureka-relayer/shared/lmt"
)

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	lmt.ConfigureLogger()
	ctx = lmt.LoggerContext(ctx)

	if len(flag.Args()) < 1 {
		lmt.Logger(ctx).Fatal("too few arguments, expected 'transfer bridge_type'")
	}
	bridge := flag.Arg(0)
	switch bridge {
	case string(config.BridgeType_EUREKA):
		if err := eurekaTransfer(ctx); err != nil {
			lmt.Logger(ctx).Error("error sending eureka transfer", zap.Error(err))
		}
	default:
		lmt.Logger(ctx).Error("unexpected bridge type", zap.String("got", bridge), zap.Any("expected", []string{string(config.BridgeType_EUREKA)}))
	}
}
