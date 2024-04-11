package grpc_health_check

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

var (
	client = http.Client{
		Timeout: 5 * time.Second,
	}
)

type grpcHealthCheckExtension struct {
	config   Config
	logger   *zap.Logger
	server   *grpc.Server
	stopCh   chan struct{}
	settings component.TelemetrySettings
}

func (gc *grpcHealthCheckExtension) Start(ctx context.Context, host component.Host) error {
	gc.logger.Info("Starting grpc_health_check extension", zap.Any("config", gc.config))

	server, err := gc.config.Grpc.ToServer(ctx, host, gc.settings)
	if err != nil {
		return err
	}
	gc.server = server

	ln, err := gc.config.Grpc.NetAddr.Listen(ctx)
	if err != nil {
		return fmt.Errorf("failed to bind to address %s: %w", gc.config.Grpc.NetAddr.Endpoint, err)
	}

	gc.stopCh = make(chan struct{})
	hs := health.NewServer()

	// Register the health server with the gRPC server
	healthpb.RegisterHealthServer(gc.server, hs)
	reflection.Register(gc.server)

	go func() {
		time.Sleep(gc.config.StartPeriod)

		for {
			status := healthpb.HealthCheckResponse_SERVING
			response, err := client.Get(gc.config.HealthCheckHttpEndpoint)
			if err != nil {
				gc.logger.Error("Failed to get health check status", zap.Error(err))
				status = healthpb.HealthCheckResponse_NOT_SERVING
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				gc.logger.Error("Service seems to be unhealthy", zap.Int("code", response.StatusCode))
				status = healthpb.HealthCheckResponse_NOT_SERVING
			}
			hs.SetServingStatus("", status)

			time.Sleep(gc.config.Interval)
		}
	}()

	go func() {
		defer close(gc.stopCh)

		// The listener ownership goes to the server.
		if err = gc.server.Serve(ln); !errors.Is(err, grpc.ErrServerStopped) && err != nil {
			gc.settings.ReportStatus(component.NewFatalErrorEvent(err))
		}
	}()

	return nil
}

func (gc *grpcHealthCheckExtension) Shutdown(context.Context) error {
	if gc.server == nil {
		return nil
	}
	gc.server.GracefulStop()
	if gc.stopCh != nil {
		<-gc.stopCh
	}
	return nil
}

func newServer(config Config, settings component.TelemetrySettings) *grpcHealthCheckExtension {
	return &grpcHealthCheckExtension{
		config:   config,
		logger:   settings.Logger,
		settings: settings,
	}
}
