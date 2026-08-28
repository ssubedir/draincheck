package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/soheilhy/cmux"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(readinessExitCode())
	}
	if len(os.Args) > 1 && os.Args[1] == "prestop" {
		os.Exit(preStopExitCode())
	}
	if len(os.Args) > 1 && os.Args[1] == "prestop-fail" {
		fmt.Fprintln(os.Stderr, "intentionally failing pre-stop")
		os.Exit(7)
	}
	port := environment("PORT", "8080")
	mode := environment("DRAINCHECK_FIXTURE_MODE", "graceful")
	var ready atomic.Bool
	var preStopObserved atomic.Bool
	ready.Store(mode != "never-ready" && mode != "exit-before-ready")

	if mode == "exit-before-ready" {
		log.Print("intentionally exiting before readiness")
		time.Sleep(250 * time.Millisecond)
		return
	}

	var tracerProvider *sdktrace.TracerProvider
	if mode == "telemetry-flush" || mode == "telemetry-drop" {
		provider, err := newTracerProvider(context.Background())
		if err != nil {
			log.Fatalf("configure tracing: %v", err)
		}
		tracerProvider = provider
		otel.SetTracerProvider(provider)
		otel.SetTextMapPropagator(propagation.TraceContext{})
		log.Print("OpenTelemetry tracing configured")
	}
	var meterProvider *sdkmetric.MeterProvider
	var completedCounter metric.Int64Counter
	if mode == "metrics-flush" || mode == "metrics-drop" {
		provider, counter, err := newMeterProvider(context.Background())
		if err != nil {
			log.Fatalf("configure metrics: %v", err)
		}
		meterProvider = provider
		completedCounter = counter
		otel.SetMeterProvider(provider)
		log.Print("OpenTelemetry metrics configured")
	}

	mux := http.NewServeMux()
	var activeWork sync.Map
	mux.HandleFunc("/ready", func(writer http.ResponseWriter, _ *http.Request) {
		if mode == "grpc-readiness" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if !ready.Load() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
	})
	workHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if mode == "rich-http" {
			if request.Method != http.MethodPost {
				http.Error(writer, "POST required", http.StatusMethodNotAllowed)
				return
			}
			if request.Header.Get("Content-Type") != "application/json" {
				http.Error(writer, "application/json required", http.StatusUnsupportedMediaType)
				return
			}
			body, err := io.ReadAll(io.LimitReader(request.Body, 1025))
			if err != nil || len(body) > 1024 || string(body) != `{"task":"drain"}` {
				http.Error(writer, "unexpected request body", http.StatusBadRequest)
				return
			}
		}
		delay := 2 * time.Second
		if value := request.URL.Query().Get("delay"); value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil || parsed < 0 || parsed > 30*time.Second {
				http.Error(writer, "invalid delay", http.StatusBadRequest)
				return
			}
			delay = parsed
		}
		requestID := request.URL.Query().Get("id")
		if requestID != "" {
			activeWork.Store(requestID, struct{}{})
			defer activeWork.Delete(requestID)
		}
		log.Printf("work started delay=%s", delay)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			if completedCounter != nil {
				completedCounter.Add(request.Context(), 1)
			}
			writer.Header().Set("Content-Type", "text/plain")
			if mode == "rich-http" {
				writer.WriteHeader(http.StatusConflict)
			}
			fmt.Fprintln(writer, "completed")
			log.Print("work completed")
		case <-request.Context().Done():
			log.Printf("work canceled: %v", request.Context().Err())
		}
	})
	mux.HandleFunc("/active", func(writer http.ResponseWriter, request *http.Request) {
		requestID := request.URL.Query().Get("id")
		if requestID == "" {
			http.Error(writer, "id required", http.StatusBadRequest)
			return
		}
		if _, found := activeWork.Load(requestID); !found {
			http.Error(writer, "work not active", http.StatusNotFound)
			return
		}
		writer.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/prestop", func(writer http.ResponseWriter, _ *http.Request) {
		preStopObserved.Store(true)
		log.Print("pre-stop hook observed")
		time.Sleep(150 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
	})
	if tracerProvider != nil {
		mux.Handle("/work", otelhttp.NewHandler(workHandler, "fixture.work"))
	} else {
		mux.Handle("/work", workHandler)
	}
	var sseShutdownOnce sync.Once
	sseShutdown := make(chan struct{})
	if mode == "sse-graceful" || mode == "sse-drop" {
		mux.HandleFunc("/events", func(writer http.ResponseWriter, _ *http.Request) {
			flusher, ok := writer.(http.Flusher)
			if !ok {
				http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.Header().Set("Cache-Control", "no-cache")
			fmt.Fprint(writer, "event: ready\ndata: connected\n\n")
			flusher.Flush()
			<-sseShutdown
			if mode == "sse-graceful" {
				fmt.Fprint(writer, "event: shutdown\ndata: draining\n\n")
				flusher.Flush()
				log.Print("SSE terminal event emitted")
			} else {
				log.Print("intentionally closing SSE stream without terminal event")
			}
		})
	}
	var webSocketShutdownOnce sync.Once
	webSocketShutdown := make(chan struct{})
	var webSocketDoneOnce sync.Once
	webSocketDone := make(chan struct{})
	if mode == "websocket-graceful" || mode == "websocket-drop" {
		mux.HandleFunc("/ws", func(writer http.ResponseWriter, request *http.Request) {
			connection, err := websocket.Accept(writer, request, nil)
			if err != nil {
				log.Printf("WebSocket handshake failed: %v", err)
				return
			}
			defer connection.CloseNow()
			defer webSocketDoneOnce.Do(func() { close(webSocketDone) })
			log.Print("WebSocket established")
			<-webSocketShutdown
			if mode == "websocket-graceful" {
				if err := connection.Write(request.Context(), websocket.MessageText, []byte("shutdown")); err != nil {
					log.Printf("write WebSocket terminal message: %v", err)
					return
				}
				log.Print("WebSocket terminal message emitted")
			} else {
				log.Print("intentionally closing WebSocket without terminal message")
			}
			if err := connection.Close(websocket.StatusNormalClosure, "draining"); err != nil {
				log.Printf("close WebSocket: %v", err)
			}
		})
	}
	var grpcShutdownOnce sync.Once
	grpcShutdown := make(chan struct{})
	grpcServer := grpc.NewServer()
	healthpb.RegisterHealthServer(grpcServer, &fixtureHealthServer{mode: mode, ready: &ready, shutdown: grpcShutdown})
	reflection.Register(grpcServer)
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("fixture listening port=%s mode=%s", port, mode)
		if mode == "grpc-multiport" {
			grpcPort := environment("GRPC_PORT", "50051")
			listener, err := net.Listen("tcp", ":"+grpcPort)
			if err != nil {
				serverErrors <- fmt.Errorf("listen for gRPC on port %s: %w", grpcPort, err)
				return
			}
			log.Printf("fixture gRPC listening port=%s", grpcPort)
			go func() {
				if err := grpcServer.Serve(listener); err != nil {
					log.Printf("gRPC server stopped: %v", err)
				}
			}()
			serverErrors <- server.ListenAndServe()
			return
		}
		if mode != "grpc-graceful" && mode != "grpc-stream-drop" && mode != "grpc-readiness" {
			serverErrors <- server.ListenAndServe()
			return
		}
		serverErrors <- serveGRPCAndHTTP(server, grpcServer)
	}()

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	for {
		select {
		case received := <-signals:
			log.Printf("received %s", received)
			if mode == "kubernetes-prestop" && !preStopObserved.Load() {
				log.Print("termination signal arrived before pre-stop completed")
				os.Exit(43)
			}
			shutdownDelay := 100 * time.Millisecond
			switch mode {
			case "ignore":
				log.Print("intentionally ignoring termination signal")
				continue
			case "drop-inflight":
				ready.Store(false)
				log.Print("intentionally terminating with active connections")
				os.Exit(0)
			case "stale-readiness":
				log.Print("intentionally keeping readiness healthy during shutdown")
				time.Sleep(1500 * time.Millisecond)
			case "post-signal-accept":
				ready.Store(false)
				shutdownDelay = 750 * time.Millisecond
				log.Print("accepting new work briefly after readiness withdrawal")
			case "post-signal-reject":
				ready.Store(false)
				shutdownDelay = 0
				log.Print("closing the listener immediately after readiness withdrawal")
			case "rich-http":
				ready.Store(false)
				shutdownDelay = 750 * time.Millisecond
				log.Print("accepting body-bearing work briefly after readiness withdrawal")
			}
			ready.Store(false)
			if mode == "sse-graceful" || mode == "sse-drop" {
				sseShutdownOnce.Do(func() { close(sseShutdown) })
			}
			if mode == "websocket-graceful" || mode == "websocket-drop" {
				webSocketShutdownOnce.Do(func() { close(webSocketShutdown) })
				select {
				case <-webSocketDone:
					log.Print("WebSocket handler stopped")
				case <-time.After(2 * time.Second):
					log.Print("WebSocket handler did not stop before shutdown")
				}
			}
			if mode == "grpc-graceful" || mode == "grpc-stream-drop" || mode == "grpc-multiport" || mode == "grpc-readiness" {
				grpcShutdownOnce.Do(func() { close(grpcShutdown) })
				grpcServer.GracefulStop()
				log.Print("gRPC server stopped gracefully")
			}
			time.Sleep(shutdownDelay)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := server.Shutdown(ctx)
			cancel()
			if err != nil {
				log.Fatalf("shutdown failed: %v", err)
			}
			if tracerProvider != nil {
				if mode == "telemetry-drop" {
					log.Print("intentionally skipping OpenTelemetry tracer provider shutdown")
				} else {
					flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
					err := tracerProvider.Shutdown(flushCtx)
					cancelFlush()
					if err != nil {
						log.Fatalf("flush traces: %v", err)
					}
					log.Print("OpenTelemetry tracer provider flushed")
				}
			}
			if meterProvider != nil {
				if mode == "metrics-drop" {
					log.Print("intentionally skipping OpenTelemetry meter provider shutdown")
				} else {
					flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
					err := meterProvider.Shutdown(flushCtx)
					cancelFlush()
					if err != nil {
						log.Fatalf("flush metrics: %v", err)
					}
					log.Print("OpenTelemetry meter provider flushed")
				}
			}
			log.Print("graceful shutdown complete")
			if mode == "nonzero-exit" {
				log.Print("intentionally exiting with status 42")
				os.Exit(42)
			}
			return
		case err := <-serverErrors:
			if !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("server failed: %v", err)
			}
			return
		}
	}
}

func readinessExitCode() int {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+environment("PORT", "8080")+"/ready", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create readiness request:", err)
		return 1
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "readiness request:", err)
		return 1
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "readiness returned", response.Status)
		return 1
	}
	return 0
}

func preStopExitCode() int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+environment("PORT", "8080")+"/prestop", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create pre-stop request:", err)
		return 1
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pre-stop request:", err)
		return 1
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusNoContent {
		fmt.Fprintln(os.Stderr, "pre-stop returned", response.Status)
		return 1
	}
	return 0
}

func serveGRPCAndHTTP(httpServer *http.Server, grpcServer *grpc.Server) error {
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return err
	}
	multiplexer := cmux.New(listener)
	grpcListener := multiplexer.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"))
	httpListener := multiplexer.Match(cmux.Any())
	go func() { _ = grpcServer.Serve(grpcListener) }()
	go func() { _ = httpServer.Serve(httpListener) }()
	return multiplexer.Serve()
}

type fixtureHealthServer struct {
	healthpb.UnimplementedHealthServer
	mode     string
	ready    *atomic.Bool
	shutdown <-chan struct{}
}

func (s *fixtureHealthServer) Check(ctx context.Context, request *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if request.GetService() == "ready" {
		servingStatus := healthpb.HealthCheckResponse_NOT_SERVING
		if s.ready.Load() {
			servingStatus = healthpb.HealthCheckResponse_SERVING
		}
		return &healthpb.HealthCheckResponse{Status: servingStatus}, nil
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
	case <-ctx.Done():
		return nil, status.Error(codes.Canceled, "work canceled")
	}
}

func (s *fixtureHealthServer) Watch(_ *healthpb.HealthCheckRequest, stream grpc.ServerStreamingServer[healthpb.HealthCheckResponse]) error {
	if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}); err != nil {
		return err
	}
	<-s.shutdown
	if s.mode == "grpc-stream-drop" {
		log.Print("intentionally ending gRPC stream with UNAVAILABLE")
		return status.Error(codes.Unavailable, "stream dropped")
	}
	if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}); err != nil {
		return err
	}
	log.Print("gRPC terminal response emitted")
	return nil
}

func newTracerProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(30*time.Second)),
	), nil
}

func newMeterProvider(ctx context.Context) (*sdkmetric.MeterProvider, metric.Int64Counter, error) {
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, nil, err
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
		exporter,
		sdkmetric.WithInterval(30*time.Second),
	)))
	counter, err := provider.Meter("draincheck-fixture").Int64Counter("fixture.work.completed")
	if err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, nil, err
	}
	return provider, counter, nil
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
