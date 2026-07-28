package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/adapter/redislimit"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/adapter/runtimepolicy"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/apidocs"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/gateway"
	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
	platformhealth "github.com/dangthobach/bpmp-platform/go/platform/health"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
	platformtelemetry "github.com/dangthobach/bpmp-platform/go/platform/telemetry"
)

func main() {
	path := flag.String("config", os.Getenv("API_GATEWAY_CONFIG"), "path to API gateway JSON configuration")
	flag.Parse()
	if *path == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*path); err != nil {
		slog.Error("api-gateway stopped", "error", err)
		os.Exit(1)
	}
}
func run(path string) error {
	value, err := config.Load(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tracerProvider, err := platformtelemetry.Start(ctx, platformtelemetry.Config{
		ServiceName:    value.Telemetry.ServiceName,
		ServiceVersion: value.Telemetry.ServiceVersion,
		Endpoint:       value.Telemetry.Endpoint,
		Insecure:       value.Telemetry.Insecure,
		SampleRatio:    value.Telemetry.SampleRatio,
		ExportTimeout:  value.Telemetry.ExportTimeout(),
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), value.Telemetry.ExportTimeout())
		defer cancel()
		if shutdownErr := tracerProvider.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Error("flush OpenTelemetry", "error", shutdownErr)
		}
	}()
	clientCertificate, err := tls.LoadX509KeyPair(value.UpstreamTLS.Certificate, value.UpstreamTLS.PrivateKey)
	if err != nil {
		return err
	}
	roots, err := certPool(value.UpstreamTLS.CA)
	if err != nil {
		return err
	}
	engineConn, err := grpc.NewClient(value.EngineAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: value.UpstreamTLS.EngineServerName, RootCAs: roots, Certificates: []tls.Certificate{clientCertificate}})), grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes), grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes)))
	if err != nil {
		return err
	}
	defer engineConn.Close()
	humanConn, err := grpc.NewClient(value.HumanAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: value.UpstreamTLS.HumanServerName, RootCAs: roots, Certificates: []tls.Certificate{clientCertificate}})), grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes), grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes)))
	if err != nil {
		return err
	}
	defer humanConn.Close()
	configurationConn, err := grpc.NewClient(
		value.RuntimeConfig.ResolverAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS13, ServerName: value.UpstreamTLS.ConfigurationServerName,
			RootCAs: roots, Certificates: []tls.Certificate{clientCertificate},
		})),
		grpc.WithStatsHandler(platformtelemetry.GRPCClientStatsHandler()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes),
			grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes),
		),
	)
	if err != nil {
		return err
	}
	defer configurationConn.Close()
	configurationConn.Connect()
	configurationKafka, err := runtimeconfig.NewKafkaClient(value.RuntimeConfig.Kafka)
	if err != nil {
		return err
	}
	defer configurationKafka.Close()
	configurationCache, err := runtimeconfig.NewCache(
		configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
	)
	if err != nil {
		return err
	}
	tenantIDs := append([]string(nil), value.RuntimeConfig.InitialTenantIDs...)
	sort.Strings(tenantIDs)
	configurationReloader, err := runtimeconfig.New(runtimeconfig.Config{
		Owner:                configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY,
		PlatformReference:    value.RuntimeConfig.PlatformReference,
		EnvironmentReference: value.RuntimeConfig.EnvironmentReference,
		InitialTenantIDs:     tenantIDs,
		ResolveTimeout:       value.RuntimeConfig.ResolveTimeout(),
		Kafka:                value.RuntimeConfig.Kafka,
	}, configurationv1.NewConfigurationResolverServiceClient(configurationConn),
		configurationKafka, configurationCache)
	if err != nil {
		return err
	}
	bootstrapStarted := time.Now()
	slog.Info("bootstrapping API Gateway runtime configuration", "tenant_count", len(tenantIDs))
	if err = configurationReloader.Bootstrap(ctx); err != nil {
		return err
	}
	slog.Info("API Gateway runtime configuration ready", "elapsed", time.Since(bootstrapStarted))
	policyProvider, err := runtimepolicy.New(configurationCache)
	if err != nil {
		return err
	}
	configurationClient := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS13,
				ServerName:   value.UpstreamTLS.ConfigurationServerName,
				RootCAs:      roots,
				Certificates: []tls.Certificate{clientCertificate},
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	redisPassword, err := readOptionalSecret(value.RateLimit.RedisPasswordFile)
	if err != nil {
		return fmt.Errorf("read Redis password: %w", err)
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr:     value.RateLimit.RedisAddress,
		Username: value.RateLimit.RedisUsername,
		Password: redisPassword,
		DB:       value.RateLimit.RedisDatabase,
	})
	defer redisClient.Close()
	rateLimitTimeout := time.Duration(value.RateLimit.OperationTimeoutMS) * time.Millisecond
	rateLimiter, err := redislimit.NewDynamic(
		redisClient,
		value.RateLimit.RedisKeyPrefix,
		rateLimitTimeout,
	)
	if err != nil {
		return err
	}
	pingCtx, cancelPing := context.WithTimeout(ctx, rateLimitTimeout)
	err = redisClient.Ping(pingCtx).Err()
	cancelPing()
	if err != nil {
		return fmt.Errorf("ping rate-limit Redis: %w", err)
	}
	handler, err := gateway.NewWithPolicy(
		enginev1.NewEngineCommandServiceClient(engineConn),
		humanv1.NewHumanRuntimeServiceClient(humanConn),
		rateLimiter,
		configurationClient,
		value,
		policyProvider,
	)
	if err != nil {
		return err
	}
	engineConn.Connect()
	humanConn.Connect()
	healthHandler := platformhealth.Handler(
		value.Health.ReadinessTimeout(),
		connectionReady(engineConn),
		connectionReady(humanConn),
		func(ctx context.Context) error { return redisClient.Ping(ctx).Err() },
		httpReady(configurationClient, value.ConfigurationURL+"/readyz"),
		func(context.Context) error { return configurationCache.Ready(tenantIDs) },
		func(ctx context.Context) error { return configurationKafka.Ping(ctx) },
	)
	routes := http.NewServeMux()
	routes.Handle("/livez", healthHandler)
	routes.Handle("/readyz", healthHandler)
	if value.APIDocs.Enabled {
		apiReference, docsErr := apidocs.New(apidocs.Config{
			OpenAPIPath:     value.APIDocs.OpenAPIPath,
			ReferencePath:   value.APIDocs.ReferencePath,
			ScalarScriptURL: value.APIDocs.ScalarScriptURL,
		})
		if docsErr != nil {
			return docsErr
		}
		apiReference.Register(routes)
	}
	routes.Handle("/", handler.Routes())
	server := &http.Server{Addr: value.ListenAddress, Handler: platformtelemetry.HTTPHandler(value.Telemetry.ServiceName, routes), ReadHeaderTimeout: value.HTTP.ReadHeaderTimeout(), ReadTimeout: value.HTTP.ReadTimeout(), WriteTimeout: value.HTTP.WriteTimeout(), IdleTimeout: value.HTTP.IdleTimeout()}
	errorsChannel := make(chan error, 2)
	go func() {
		errorsChannel <- server.ListenAndServeTLS(value.PublicTLS.Certificate, value.PublicTLS.PrivateKey)
	}()
	go func() {
		errorsChannel <- configurationReloader.Run(ctx)
	}()
	slog.Info("api-gateway started", "listen_address", value.ListenAddress)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), value.HTTP.ShutdownTimeout())
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case runErr := <-errorsChannel:
		if errors.Is(runErr, http.ErrServerClosed) {
			return nil
		}
		return runErr
	}
}

func httpReady(client *http.Client, endpoint string) platformhealth.Check {
	return func(ctx context.Context) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("configuration upstream health returned %s", response.Status)
		}
		return nil
	}
}

func readOptionalSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value = bytes.TrimSuffix(value, []byte("\n"))
	value = bytes.TrimSuffix(value, []byte("\r"))
	return string(value), nil
}

func connectionReady(connection *grpc.ClientConn) platformhealth.Check {
	return func(context.Context) error {
		if connection.GetState() != connectivity.Ready {
			return fmt.Errorf("upstream connection state is %s", connection.GetState())
		}
		return nil
	}
}

func certPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("upstream CA contains no certificates")
	}
	return pool, nil
}
