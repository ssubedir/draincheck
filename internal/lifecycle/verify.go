package lifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ssubedir/draincheck/internal/config"
	"github.com/ssubedir/draincheck/internal/grpcprobe"
	"github.com/ssubedir/draincheck/internal/probe"
	"github.com/ssubedir/draincheck/internal/readiness"
	"github.com/ssubedir/draincheck/internal/report"
	containerruntime "github.com/ssubedir/draincheck/internal/runtime"
	"github.com/ssubedir/draincheck/internal/streaming"
	"github.com/ssubedir/draincheck/internal/telemetry"
	"github.com/ssubedir/draincheck/internal/traffic"
)

type Options struct {
	PullPolicy    containerruntime.PullPolicy
	KeepOnFailure bool
	LogLimit      int64
	Profile       config.Profile
}

const preStopOutputLimit = 4 << 10

type trafficExecution interface {
	WaitStarted(context.Context) error
	StopAndSnapshot() []int
	ActiveSnapshot() []int
	Done() <-chan struct{}
	Results() []traffic.Result
	StartedCount() int
}

type verifier struct {
	config            config.Config
	runtime           containerruntime.Runtime
	options           Options
	report            *report.Report
	startedAt         time.Time
	container         containerruntime.Container
	lastState         containerruntime.ContainerState
	created           bool
	telemetryReceiver *telemetry.Receiver
	traceCorrelations []telemetry.Correlation
	grpcTrafficClient *grpcprobe.Client
	grpcStreamClient  *grpcprobe.Client
	grpcUnaryCall     grpcprobe.Call
	grpcStreamCall    grpcprobe.Call
}

func Verify(ctx context.Context, cfg config.Config, runtime containerruntime.Runtime, options Options) (*report.Report, error) {
	runID, err := makeRunID()
	if err != nil {
		return nil, fmt.Errorf("create run ID: %w", err)
	}
	now := time.Now()
	result := report.New(runID, cfg.Target.Image, runtime.Name(), now)
	profile := options.Profile
	if profile == "" {
		profile = config.ProfileGeneric
	}
	result.Profile = string(profile)
	result.Shutdown.DeadlineMS = cfg.Shutdown.Deadline.Value().Milliseconds()
	result.Traffic.Configured = cfg.Traffic.Count
	result.Traffic.Driver = cfg.Traffic.Driver
	result.Traffic.PostSignal.Policy = cfg.Traffic.PostSignal.Policy
	if cfg.Traffic.PostSignal.Policy != config.PostSignalDisabled {
		result.Traffic.PostSignal.Configured = cfg.Traffic.PostSignal.Count
	}
	result.Streaming.SSE.Enabled = cfg.Streaming.SSE.Enabled
	result.Streaming.WebSocket.Enabled = cfg.Streaming.WebSocket.Enabled
	result.Streaming.GRPC.Enabled = cfg.Streaming.GRPC.Enabled
	result.Telemetry.Enabled = cfg.Telemetry.Traces.Enabled
	if cfg.Telemetry.Traces.Enabled || cfg.Telemetry.Metrics.Enabled {
		result.Telemetry.Protocol = telemetry.ProtocolHTTPProtobuf
	}
	if cfg.Telemetry.Traces.Enabled {
		result.Telemetry.MinimumCorrelatedSpans = cfg.Telemetry.Traces.MinimumCorrelatedSpans
	}
	result.Telemetry.Metrics.Enabled = cfg.Telemetry.Metrics.Enabled
	if cfg.Telemetry.Metrics.Enabled {
		result.Telemetry.Metrics.MinimumDataPoints = cfg.Telemetry.Metrics.MinimumDataPoints
	}
	v := &verifier{config: cfg, runtime: runtime, options: options, report: result, startedAt: now}

	runErr := v.run(ctx)
	evidenceCtx, cancelEvidence := context.WithTimeout(context.Background(), 5*time.Second)
	evidenceErr := v.collectEvidence(evidenceCtx)
	cancelEvidence()
	if runErr == nil && evidenceErr != nil {
		runErr = evidenceErr
	}
	if runErr != nil {
		result.AddAssertion("execution.completed", false, runErr.Error())
	}

	result.Finish(time.Now())
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
	cleanupErr := v.cleanup(cleanupCtx)
	cancelCleanup()
	if runErr == nil && cleanupErr != nil {
		runErr = cleanupErr
	}
	if cleanupErr != nil {
		result.AddAssertion("cleanup.completed", false, cleanupErr.Error())
	}
	result.Finish(time.Now())
	return result, runErr
}

func (v *verifier) run(ctx context.Context) (runErr error) {
	v.report.AddEvent(time.Now(), "preflight", "checking image and runtime")
	if err := v.runtime.EnsureImage(ctx, v.config.Target.Image, v.options.PullPolicy); err != nil {
		return err
	}
	v.report.AddEvent(time.Now(), "preflight", "image resolved")

	environment := cloneStrings(v.config.Target.Environment)
	hostAliases := make(map[string]string)
	if v.config.Telemetry.Traces.Enabled || v.config.Telemetry.Metrics.Enabled {
		var correlations []telemetry.Correlation
		if v.config.Telemetry.Traces.Enabled {
			var err error
			correlations, err = telemetry.NewCorrelations(v.config.Traffic.Count)
			if err != nil {
				return err
			}
		}
		receiver, err := telemetry.StartReceiver(correlations, v.report.RunID)
		if err != nil {
			return err
		}
		v.telemetryReceiver = receiver
		v.traceCorrelations = correlations
		if v.config.Telemetry.Traces.Enabled {
			for key, value := range receiver.TraceExporterEnvironment() {
				environment[key] = value
			}
		}
		if v.config.Telemetry.Metrics.Enabled {
			for key, value := range receiver.MetricExporterEnvironment(environment["OTEL_RESOURCE_ATTRIBUTES"]) {
				environment[key] = value
			}
		}
		hostAliases[telemetry.GatewayHost] = "host-gateway"
		v.report.AddEvent(time.Now(), "preflight", "authenticated OTLP/HTTP telemetry receiver started")
		defer func() {
			closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
			closeErr := receiver.Close(closeCtx)
			cancelClose()
			if runErr == nil && closeErr != nil {
				runErr = closeErr
			}
		}()
	}

	name := "draincheck-" + v.report.RunID
	container, err := v.runtime.Create(ctx, containerruntime.ContainerSpec{
		Image:          v.config.Target.Image,
		Name:           name,
		RunID:          v.report.RunID,
		ContainerPorts: v.config.RequiredContainerPorts(),
		Environment:    environment,
		HostAliases:    hostAliases,
	})
	if err != nil {
		return err
	}
	v.container = container
	v.created = true
	v.report.Container.ID = container.ID
	v.report.Container.Name = container.Name
	v.report.AddEvent(time.Now(), "creating", "container created")

	if err := v.runtime.Start(ctx, container.ID); err != nil {
		return err
	}
	v.report.AddEvent(time.Now(), "starting", "container started")

	hostPorts := make(map[int]int)
	for _, containerPort := range v.config.RequiredContainerPorts() {
		hostPort, err := v.runtime.HostPort(ctx, container.ID, containerPort)
		if err != nil {
			state, inspectErr := v.runtime.Inspect(ctx, container.ID)
			if inspectErr == nil {
				v.lastState = state
				if !state.Running {
					description := fmt.Sprintf("container exited before readiness (exit code %d)", state.ExitCode)
					v.report.AddAssertion("startup.ready", false, description)
					v.report.AddEvent(time.Now(), "waiting_ready", description)
					return nil
				}
			}
			return err
		}
		hostPorts[containerPort] = hostPort
		v.report.AddEvent(time.Now(), "starting", fmt.Sprintf("mapped container port %d to 127.0.0.1:%d", containerPort, hostPort))
	}
	trafficBaseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPorts[v.config.TrafficPort()])
	sseBaseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPorts[v.config.SSEPort()])
	webSocketBaseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPorts[v.config.WebSocketPort()])
	grpcTrafficTarget := fmt.Sprintf("127.0.0.1:%d", hostPorts[v.config.TrafficPort()])
	grpcStreamTarget := fmt.Sprintf("127.0.0.1:%d", hostPorts[v.config.GRPCStreamPort()])

	var readinessChecker readiness.Checker
	switch v.config.Readiness.Driver {
	case config.ReadinessDriverHTTP:
		readinessBaseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPorts[v.config.ReadinessPort()])
		readinessChecker = readiness.NewHTTP(readinessBaseURL+v.config.Readiness.Path, v.config.Readiness.SuccessStatus)
	case config.ReadinessDriverGRPC:
		readinessGRPCTarget := fmt.Sprintf("127.0.0.1:%d", hostPorts[v.config.ReadinessPort()])
		checker, err := readiness.NewGRPC(readinessGRPCTarget, v.config.Readiness.GRPC.Service)
		if err != nil {
			return err
		}
		readinessChecker = checker
	case config.ReadinessDriverExec:
		readinessChecker = readiness.NewExec(v.runtime, container.ID, v.config.Readiness.Exec.Command)
	default:
		return fmt.Errorf("unsupported readiness driver %q", v.config.Readiness.Driver)
	}
	defer func() { _ = readinessChecker.Close() }()
	ready, description, err := v.waitReady(ctx, readinessChecker)
	if err != nil {
		return err
	}
	if !ready {
		v.report.AddAssertion("startup.ready", false, description)
		v.report.AddEvent(time.Now(), "waiting_ready", description)
		return nil
	}
	v.report.AddAssertion("startup.ready", true, description)
	readyAt := time.Now()
	v.report.Timings.StartupReadyMS = readyAt.Sub(v.startedAt).Milliseconds()
	v.report.AddEvent(readyAt, "ready", description)

	if v.config.Traffic.Driver == config.TrafficDriverGRPC {
		grpcClient, err := grpcprobe.NewClient(grpcTrafficTarget)
		if err != nil {
			return err
		}
		v.grpcTrafficClient = grpcClient
		defer func() { _ = grpcClient.Close() }()
	}
	if v.config.Traffic.Driver == config.TrafficDriverGRPC {
		prepareCtx, cancelPrepare := context.WithTimeout(ctx, v.config.Traffic.RequestTimeout.Value())
		call, err := v.grpcTrafficClient.Prepare(prepareCtx, v.config.Traffic.GRPC.Method, v.config.Traffic.GRPC.DescriptorBytes(), v.config.Traffic.GRPC.RequestBytes(), false)
		cancelPrepare()
		if err != nil {
			message := fmt.Sprintf("prepare gRPC traffic: %v", err)
			v.report.AddAssertion("traffic.inflight_exercised", false, message)
			v.report.AddEvent(time.Now(), "preflight", message)
			return nil
		}
		v.grpcUnaryCall = call
		v.report.AddEvent(time.Now(), "preflight", "gRPC unary method and request resolved")
	}
	if v.config.Streaming.GRPC.Enabled {
		grpcClient, err := grpcprobe.NewClient(grpcStreamTarget)
		if err != nil {
			return err
		}
		v.grpcStreamClient = grpcClient
		defer func() { _ = grpcClient.Close() }()
		prepareCtx, cancelPrepare := context.WithTimeout(ctx, v.config.Streaming.GRPC.EstablishTimeout.Value())
		call, err := v.grpcStreamClient.Prepare(prepareCtx, v.config.Streaming.GRPC.Method, v.config.Streaming.GRPC.DescriptorBytes(), v.config.Streaming.GRPC.RequestBytes(), true)
		cancelPrepare()
		if err != nil {
			message := fmt.Sprintf("prepare gRPC stream: %v", err)
			v.report.AddAssertion("grpc_stream.established", false, message)
			v.report.AddEvent(time.Now(), "preflight", message)
			return nil
		}
		v.grpcStreamCall = call
		v.report.AddEvent(time.Now(), "preflight", "gRPC server-streaming method and request resolved")
	}

	var sseRun *streaming.SSERun
	var cancelSSE context.CancelFunc
	if v.config.Streaming.SSE.Enabled {
		sseCtx, cancel := context.WithCancel(ctx)
		cancelSSE = cancel
		defer cancelSSE()
		sseClient := &http.Client{Transport: &http.Transport{
			DisableCompression: true,
			DisableKeepAlives:  true,
		}}
		sseRun = streaming.StartSSE(sseCtx, sseClient, streaming.SSESpec{
			BaseURL:       sseBaseURL,
			Path:          v.config.Streaming.SSE.Path,
			Headers:       v.config.Streaming.SSE.Headers,
			InitialEvent:  v.config.Streaming.SSE.InitialEvent,
			TerminalEvent: v.config.Streaming.SSE.TerminalEvent,
		})
		establishCtx, cancelEstablish := context.WithTimeout(ctx, v.config.Streaming.SSE.EstablishTimeout.Value())
		snapshot, established := sseRun.WaitEstablished(establishCtx)
		establishErr := establishCtx.Err()
		cancelEstablish()
		v.copySSESnapshot(snapshot)
		message := sseEstablishmentMessage(snapshot, established, establishErr, v.config.Streaming.SSE.EstablishTimeout.Value())
		v.report.AddAssertion("stream.established", established, message)
		v.report.AddEvent(time.Now(), "streaming", message)
		if !established {
			cancelSSE()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
	}

	var webSocketRun *streaming.WebSocketRun
	var cancelWebSocket context.CancelFunc
	if v.config.Streaming.WebSocket.Enabled {
		webSocketCtx, cancel := context.WithCancel(ctx)
		cancelWebSocket = cancel
		defer cancelWebSocket()
		webSocketClient := &http.Client{Transport: &http.Transport{DisableCompression: true}}
		webSocketRun = streaming.StartWebSocket(webSocketCtx, webSocketClient, streaming.WebSocketSpec{
			BaseURL:         webSocketBaseURL,
			Path:            v.config.Streaming.WebSocket.Path,
			Headers:         v.config.Streaming.WebSocket.Headers,
			Subprotocols:    v.config.Streaming.WebSocket.Subprotocols,
			TerminalMessage: v.config.Streaming.WebSocket.TerminalMessage,
		})
		establishCtx, cancelEstablish := context.WithTimeout(ctx, v.config.Streaming.WebSocket.EstablishTimeout.Value())
		snapshot, established := webSocketRun.WaitEstablished(establishCtx)
		establishErr := establishCtx.Err()
		cancelEstablish()
		v.copyWebSocketSnapshot(snapshot)
		message := webSocketEstablishmentMessage(snapshot, established, establishErr, v.config.Streaming.WebSocket.EstablishTimeout.Value())
		v.report.AddAssertion("websocket.established", established, message)
		v.report.AddEvent(time.Now(), "streaming", message)
		if !established {
			cancelWebSocket()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
	}

	var grpcStreamRun *grpcprobe.StreamRun
	var cancelGRPCStream context.CancelFunc
	if v.config.Streaming.GRPC.Enabled {
		streamCtx, cancel := context.WithCancel(ctx)
		cancelGRPCStream = cancel
		defer cancelGRPCStream()
		grpcStreamRun = grpcprobe.StartStream(streamCtx, grpcprobe.StreamSpec{
			Client:          v.grpcStreamClient,
			Call:            v.grpcStreamCall,
			Metadata:        v.config.Streaming.GRPC.Metadata,
			MinimumMessages: v.config.Streaming.GRPC.MinimumMessages,
		})
		establishCtx, cancelEstablish := context.WithTimeout(ctx, v.config.Streaming.GRPC.EstablishTimeout.Value())
		snapshot, established := grpcStreamRun.WaitEstablished(establishCtx)
		establishErr := establishCtx.Err()
		cancelEstablish()
		v.copyGRPCStreamSnapshot(snapshot)
		message := grpcStreamEstablishmentMessage(snapshot, established, establishErr, v.config.Streaming.GRPC.MinimumMessages, v.config.Streaming.GRPC.EstablishTimeout.Value())
		v.report.AddAssertion("grpc_stream.established", established, message)
		v.report.AddEvent(time.Now(), "streaming", message)
		if !established {
			cancelGRPCStream()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
	}

	trafficCtx, cancelTraffic := context.WithCancel(ctx)
	defer cancelTraffic()
	trafficRun := v.startTrafficRun(trafficCtx, trafficBaseURL, probe.PhaseInitial, v.config.Traffic.Count, v.config.Traffic.Concurrency)
	if err := trafficRun.WaitStarted(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		v.report.AddAssertion("traffic.inflight_exercised", false, err.Error())
		return nil
	}
	v.report.AddEvent(time.Now(), "traffic", "traffic started")

	shutdownTimer := time.NewTimer(v.config.Traffic.ShutdownAfter.Value())
	select {
	case <-shutdownTimer.C:
	case <-trafficRun.Done():
		if !shutdownTimer.Stop() {
			<-shutdownTimer.C
		}
	case <-ctx.Done():
		shutdownTimer.Stop()
		return ctx.Err()
	}

	stateBeforeSignal, err := v.runtime.Inspect(ctx, container.ID)
	if err != nil {
		return err
	}
	v.lastState = stateBeforeSignal
	if !stateBeforeSignal.Running {
		inflight := trafficRun.StopAndSnapshot()
		v.report.Traffic.Started = trafficRun.StartedCount()
		v.report.Traffic.Inflight = len(inflight)
		v.report.AddAssertion("shutdown.signal", false, "container exited before the termination signal could be delivered")
		v.waitTraffic(ctx, cancelTraffic, trafficRun)
		v.summarizeTraffic(trafficRun.Results(), inflight)
		return nil
	}

	activeBeforeSignal := trafficRun.StopAndSnapshot()
	v.report.Traffic.Started = trafficRun.StartedCount()
	v.report.AddEvent(time.Now(), "traffic", fmt.Sprintf("%d requests started; %d active before signal request", v.report.Traffic.Started, len(activeBeforeSignal)))

	terminationStartedAt := time.Now()
	shutdownDeadline := terminationStartedAt.Add(v.config.Shutdown.Deadline.Value())
	v.report.AddEvent(terminationStartedAt, "terminating", fmt.Sprintf("%s profile termination grace period started (%s)", v.report.Profile, v.config.Shutdown.Deadline))
	preStopExhausted, err := v.runPreStop(ctx, container.ID, shutdownDeadline)
	if err != nil {
		return err
	}
	if preStopExhausted {
		v.report.Timings.ShutdownTotalMS = time.Since(terminationStartedAt).Milliseconds()
		confirmedInflight := trafficRun.ActiveSnapshot()
		v.report.Traffic.Inflight = len(confirmedInflight)
		v.report.AddAssertion("shutdown.signal", false, fmt.Sprintf("%s was not sent before the shared shutdown deadline expired", v.config.Shutdown.Signal))
		v.report.ForcedCleanup = true
		cancelTraffic()
		v.waitTraffic(ctx, cancelTraffic, trafficRun)
		results := trafficRun.Results()
		v.summarizeTraffic(results, confirmedInflight)
		state, inspectErr := v.runtime.Inspect(ctx, container.ID)
		if inspectErr != nil {
			return inspectErr
		}
		v.lastState = state
		withdrawn := withdrawalResult{message: "readiness withdrawal was not evaluated because pre-stop exhausted the shutdown deadline", at: time.Now()}
		v.evaluateAssertions(false, state, withdrawn, results, confirmedInflight)
		return nil
	}

	signalRequestedAt := time.Now()
	if sseRun != nil {
		v.report.Streaming.SSE.ActiveAtSignal = sseRun.Active()
	}
	if webSocketRun != nil {
		v.report.Streaming.WebSocket.ActiveAtSignal = webSocketRun.Active()
	}
	if grpcStreamRun != nil {
		v.report.Streaming.GRPC.ActiveAtSignal = grpcStreamRun.Active()
	}
	v.report.AddEvent(signalRequestedAt, "terminating", v.config.Shutdown.Signal+" requested")
	withdrawCtx, cancelWithdraw := context.WithDeadline(ctx, signalRequestedAt.Add(v.config.Assertions.ReadinessWithdrawnWithin.Value()))
	withdrawChannel := make(chan withdrawalResult, 1)
	go func() {
		withdrawChannel <- waitWithdrawn(withdrawCtx, readinessChecker, v.config.Readiness.Interval.Value(), signalRequestedAt)
	}()
	if err := v.runtime.Signal(ctx, container.ID, v.config.Shutdown.Signal); err != nil {
		cancelWithdraw()
		return err
	}
	signalConfirmedAt := time.Now()
	signalLatency := signalConfirmedAt.Sub(signalRequestedAt).Round(time.Millisecond)
	v.report.Timings.SignalDeliveryMS = signalConfirmedAt.Sub(signalRequestedAt).Milliseconds()
	confirmedInflight := trafficRun.ActiveSnapshot()
	v.report.Traffic.Inflight = len(confirmedInflight)
	v.report.AddAssertion("shutdown.signal", true, fmt.Sprintf("%s delivery confirmed after %s", v.config.Shutdown.Signal, signalLatency))
	v.report.AddEvent(signalConfirmedAt, "terminating", fmt.Sprintf("%s delivery confirmed after %s; %d requests still active", v.config.Shutdown.Signal, signalLatency, len(confirmedInflight)))

	postSignalRun, cancelPostSignal, err := v.startPostSignalTraffic(ctx, trafficBaseURL, shutdownDeadline, signalConfirmedAt)
	if err != nil {
		cancelWithdraw()
		return err
	}
	if cancelPostSignal != nil {
		defer cancelPostSignal()
	}
	var sseCloseTimer *time.Timer
	if sseRun != nil {
		closeAfter := time.Until(signalRequestedAt.Add(v.config.Streaming.SSE.CloseTimeout.Value()))
		if closeAfter < 0 {
			closeAfter = 0
		}
		sseCloseTimer = time.AfterFunc(closeAfter, cancelSSE)
		defer sseCloseTimer.Stop()
	}
	var webSocketCloseTimer *time.Timer
	if webSocketRun != nil {
		closeAfter := time.Until(signalRequestedAt.Add(v.config.Streaming.WebSocket.CloseTimeout.Value()))
		if closeAfter < 0 {
			closeAfter = 0
		}
		webSocketCloseTimer = time.AfterFunc(closeAfter, cancelWebSocket)
		defer webSocketCloseTimer.Stop()
	}
	var grpcStreamCloseTimer *time.Timer
	if grpcStreamRun != nil {
		closeAfter := time.Until(signalRequestedAt.Add(v.config.Streaming.GRPC.CloseTimeout.Value()))
		if closeAfter < 0 {
			closeAfter = 0
		}
		grpcStreamCloseTimer = time.AfterFunc(closeAfter, cancelGRPCStream)
		defer grpcStreamCloseTimer.Stop()
	}

	state, exited, err := v.waitExit(ctx, container.ID, shutdownDeadline)
	if err != nil {
		cancelWithdraw()
		return err
	}
	v.lastState = state
	if exited {
		exitedAt := time.Now()
		v.report.Timings.ContainerExitMS = exitedAt.Sub(signalRequestedAt).Milliseconds()
		v.report.Timings.ShutdownTotalMS = exitedAt.Sub(terminationStartedAt).Milliseconds()
		v.report.AddEvent(exitedAt, "exited", fmt.Sprintf("container exited with code %d", state.ExitCode))
	} else {
		v.report.ForcedCleanup = true
		v.report.Timings.ShutdownTotalMS = time.Since(terminationStartedAt).Milliseconds()
		v.report.AddEvent(time.Now(), "draining", "shutdown deadline exceeded; forced cleanup required")
	}

	v.waitTraffic(ctx, cancelTraffic, trafficRun)
	results := trafficRun.Results()
	v.summarizeTraffic(results, confirmedInflight)
	if postSignalRun != nil {
		v.waitTraffic(ctx, cancelPostSignal, postSignalRun)
		v.summarizePostSignalTraffic(postSignalRun.Results())
	}
	if err := v.summarizeTelemetry(ctx, confirmedInflight, results, signalConfirmedAt); err != nil {
		cancelWithdraw()
		return err
	}
	if sseRun != nil {
		if err := v.waitAndSummarizeSSE(ctx, sseRun, cancelSSE, signalRequestedAt); err != nil {
			cancelWithdraw()
			return err
		}
	}
	if webSocketRun != nil {
		if err := v.waitAndSummarizeWebSocket(ctx, webSocketRun, cancelWebSocket, signalRequestedAt); err != nil {
			cancelWithdraw()
			return err
		}
	}
	if grpcStreamRun != nil {
		if err := v.waitAndSummarizeGRPCStream(ctx, grpcStreamRun, cancelGRPCStream, signalRequestedAt); err != nil {
			cancelWithdraw()
			return err
		}
	}

	var withdrawn withdrawalResult
	select {
	case withdrawn = <-withdrawChannel:
	case <-ctx.Done():
		cancelWithdraw()
		return ctx.Err()
	}
	cancelWithdraw()
	if withdrawn.withdrawn {
		v.report.Timings.ReadinessWithdrawalMS = withdrawn.at.Sub(signalRequestedAt).Milliseconds()
		v.report.AddEvent(withdrawn.at, "draining", withdrawn.message)
	}
	v.evaluateAssertions(exited, state, withdrawn, results, confirmedInflight)
	return nil
}

func (v *verifier) startPostSignalTraffic(ctx context.Context, baseURL string, shutdownDeadline, signalConfirmedAt time.Time) (trafficExecution, context.CancelFunc, error) {
	policy := v.config.Traffic.PostSignal.Policy
	if policy == config.PostSignalDisabled {
		return nil, nil, nil
	}

	delay := v.config.Traffic.PostSignal.Delay.Value()
	if !signalConfirmedAt.Add(delay).Before(shutdownDeadline) {
		v.report.AddEvent(time.Now(), "post_signal", "post-signal traffic could not start before the shutdown deadline")
		return nil, nil, nil
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if !time.Now().Before(shutdownDeadline) {
		v.report.AddEvent(time.Now(), "post_signal", "post-signal traffic could not start before the shutdown deadline")
		return nil, nil, nil
	}

	postSignalCtx, cancelPostSignal := context.WithCancel(ctx)
	concurrency := v.config.Traffic.PostSignal.Count
	run := v.startTrafficRun(postSignalCtx, baseURL, probe.PhasePostSignal, v.config.Traffic.PostSignal.Count, concurrency)
	if err := run.WaitStarted(ctx); err != nil {
		cancelPostSignal()
		v.waitTraffic(ctx, cancelPostSignal, run)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("start post-signal traffic: %w", err)
	}
	v.report.Traffic.PostSignal.Started = run.StartedCount()
	v.report.AddEvent(time.Now(), "post_signal", fmt.Sprintf("started %d requests for %s policy %s after signal confirmation", run.StartedCount(), policy, time.Since(signalConfirmedAt).Round(time.Millisecond)))
	return run, cancelPostSignal, nil
}

func (v *verifier) startTrafficRun(ctx context.Context, baseURL, phase string, count, concurrency int) trafficExecution {
	if v.config.Traffic.Driver == config.TrafficDriverCommand {
		return probe.Start(ctx, probe.Spec{
			Executable:  v.config.Traffic.Command.ResolvedExecutable(),
			Args:        v.config.Traffic.Command.Args,
			Directory:   v.config.Traffic.Command.ResolvedDirectory(),
			Environment: v.config.Traffic.Command.Environment,
			BaseURL:     baseURL,
			RunID:       v.report.RunID,
			Phase:       phase,
			Count:       count,
			Concurrency: concurrency,
			Timeout:     v.config.Traffic.RequestTimeout.Value(),
		})
	}
	if v.config.Traffic.Driver == config.TrafficDriverGRPC {
		expectedCodes, _ := grpcprobe.ParseCodes(v.config.Traffic.GRPC.ExpectedCodes)
		var metadataForRequest func(int) map[string]string
		if phase == probe.PhaseInitial {
			metadataForRequest = telemetry.CorrelationHeaders(v.traceCorrelations)
		}
		return grpcprobe.StartUnary(ctx, grpcprobe.UnarySpec{
			Client:             v.grpcTrafficClient,
			Call:               v.grpcUnaryCall,
			Metadata:           v.config.Traffic.GRPC.Metadata,
			MetadataForRequest: metadataForRequest,
			ExpectedCodes:      expectedCodes,
			Count:              count,
			Concurrency:        concurrency,
			Timeout:            v.config.Traffic.RequestTimeout.Value(),
		})
	}
	client := &http.Client{Transport: &http.Transport{
		DisableKeepAlives:   phase == probe.PhasePostSignal,
		MaxIdleConns:        concurrency,
		MaxIdleConnsPerHost: concurrency,
	}}
	var headersForRequest func(int) map[string]string
	if phase == probe.PhaseInitial {
		headersForRequest = telemetry.CorrelationHeaders(v.traceCorrelations)
	}
	return traffic.Start(ctx, client, traffic.Spec{
		BaseURL:           baseURL,
		Method:            strings.ToUpper(v.config.Traffic.Request.Method),
		Path:              v.config.Traffic.Request.Path,
		Headers:           v.config.Traffic.Request.Headers,
		Body:              v.config.Traffic.Request.BodyBytes(),
		SuccessStatuses:   v.config.Traffic.Request.SuccessStatuses,
		Count:             count,
		Concurrency:       concurrency,
		Timeout:           v.config.Traffic.RequestTimeout.Value(),
		HeadersForRequest: headersForRequest,
	})
}

func (v *verifier) waitReady(ctx context.Context, checker readiness.Checker) (bool, string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, v.config.Readiness.StartupTimeout.Value())
	defer cancel()
	var last string
	for {
		probeBudget := min(v.config.Readiness.StartupTimeout.Value(), time.Second)
		probeCtx, cancelProbe := context.WithTimeout(waitCtx, probeBudget)
		observation := checker.Check(probeCtx)
		cancelProbe()
		last = "readiness returned " + observation.Description
		if observation.Err == nil && observation.Ready {
			return true, last, nil
		}

		state, err := v.runtime.Inspect(waitCtx, v.container.ID)
		if err != nil && waitCtx.Err() == nil {
			return false, last, err
		}
		if err == nil {
			v.lastState = state
			if !state.Running {
				return false, fmt.Sprintf("container exited before readiness (exit code %d)", state.ExitCode), nil
			}
		}
		if observation.Terminal {
			return false, last, nil
		}

		select {
		case <-time.After(v.config.Readiness.Interval.Value()):
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return false, last, ctx.Err()
			}
			return false, fmt.Sprintf("startup timeout exceeded: %s", last), nil
		}
	}
}

func (v *verifier) runPreStop(ctx context.Context, id string, shutdownDeadline time.Time) (bool, error) {
	hook := v.config.Shutdown.PreStop
	if hook == nil {
		return false, nil
	}
	v.report.Shutdown.PreStop.Configured = true
	startedAt := time.Now()
	v.report.AddEvent(startedAt, "pre_stop", "container exec pre-stop command started")
	hookCtx, cancelHook := context.WithDeadline(ctx, shutdownDeadline)
	result, err := v.runtime.Exec(hookCtx, id, hook.Exec.Command, preStopOutputLimit)
	hookErr := hookCtx.Err()
	cancelHook()
	finishedAt := time.Now()
	duration := finishedAt.Sub(startedAt)
	v.report.Shutdown.PreStop.DurationMS = duration.Milliseconds()
	v.report.Timings.PreStopMS = duration.Milliseconds()

	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(hookErr, context.DeadlineExceeded) {
		v.report.Shutdown.PreStop.TimedOut = true
		message := fmt.Sprintf("pre-stop command exceeded the shared %s shutdown deadline", v.config.Shutdown.Deadline)
		v.report.AddAssertion("shutdown.pre_stop", false, message)
		v.report.AddEvent(finishedAt, "pre_stop", message)
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("run pre-stop command: %w", err)
	}
	v.report.Shutdown.PreStop.ExitCode = result.ExitCode
	passed := result.ExitCode == 0
	message := fmt.Sprintf("pre-stop command exited with code %d after %s", result.ExitCode, duration.Round(time.Millisecond))
	v.report.AddAssertion("shutdown.pre_stop", passed, message)
	v.report.AddEvent(finishedAt, "pre_stop", message)
	return !finishedAt.Before(shutdownDeadline), nil
}

func (v *verifier) waitExit(ctx context.Context, id string, shutdownDeadline time.Time) (containerruntime.ContainerState, bool, error) {
	deadlineCtx, cancel := context.WithDeadline(ctx, shutdownDeadline)
	defer cancel()
	var last containerruntime.ContainerState
	for {
		state, err := v.runtime.Inspect(deadlineCtx, id)
		if err != nil {
			if deadlineCtx.Err() != nil {
				if ctx.Err() != nil {
					return last, false, ctx.Err()
				}
				return last, false, nil
			}
			return last, false, err
		}
		last = state
		if !state.Running {
			return state, true, nil
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-deadlineCtx.Done():
			if ctx.Err() != nil {
				return last, false, ctx.Err()
			}
			return last, false, nil
		}
	}
}

func (v *verifier) waitTraffic(ctx context.Context, cancel context.CancelFunc, run trafficExecution) {
	wait := time.NewTimer(v.config.Traffic.RequestTimeout.Value() + time.Second)
	defer wait.Stop()
	select {
	case <-run.Done():
		return
	case <-wait.C:
		cancel()
	case <-ctx.Done():
		cancel()
	}
	select {
	case <-run.Done():
	case <-time.After(time.Second):
	}
}

func (v *verifier) summarizeTraffic(results []traffic.Result, inflight []int) {
	byID := make(map[int]traffic.Result, len(results))
	for _, result := range results {
		byID[result.ID] = result
		v.report.Traffic.Completed++
		if result.Success {
			v.report.Traffic.Successful++
		} else {
			v.report.Traffic.Failed++
		}
	}
	for _, id := range inflight {
		result, found := byID[id]
		if found && result.Success {
			v.report.Traffic.InflightOK++
		} else {
			v.report.Traffic.InflightBad++
		}
	}
	v.report.AddEvent(time.Now(), "draining", fmt.Sprintf("%d/%d in-flight requests completed successfully", v.report.Traffic.InflightOK, len(inflight)))
}

func (v *verifier) summarizePostSignalTraffic(results []traffic.Result) {
	invalid := 0
	for _, result := range results {
		v.report.Traffic.PostSignal.Completed++
		if result.Success {
			v.report.Traffic.PostSignal.Accepted++
		} else if (v.config.Traffic.Driver == config.TrafficDriverCommand && result.ErrorKind != "probe_result") ||
			(v.config.Traffic.Driver == config.TrafficDriverGRPC && result.ErrorKind != "grpc_status") {
			invalid++
		} else {
			v.report.Traffic.PostSignal.Rejected++
		}
	}
	v.report.AddEvent(time.Now(), "post_signal", fmt.Sprintf(
		"%d accepted, %d rejected, and %d invalid after signal delivery",
		v.report.Traffic.PostSignal.Accepted,
		v.report.Traffic.PostSignal.Rejected,
		invalid,
	))
}

func (v *verifier) summarizeTelemetry(ctx context.Context, eligibleRequestIDs []int, results []traffic.Result, signalConfirmedAt time.Time) error {
	if v.telemetryReceiver == nil {
		return nil
	}
	v.report.Telemetry.EligibleInflight = len(eligibleRequestIDs)
	if v.config.Telemetry.Traces.Enabled {
		if len(eligibleRequestIDs) > 0 {
			_, err := v.telemetryReceiver.WaitForTraces(
				ctx,
				eligibleRequestIDs,
				signalConfirmedAt,
				v.config.Telemetry.Traces.MinimumCorrelatedSpans,
				v.config.Telemetry.Traces.FlushTimeout.Value(),
			)
			if err != nil {
				return fmt.Errorf("wait for correlated OTLP spans: %w", err)
			}
		}
	}
	metricBoundary := completedInflightBoundary(signalConfirmedAt, eligibleRequestIDs, results)
	if v.config.Telemetry.Metrics.Enabled {
		_, err := v.telemetryReceiver.WaitForMetrics(
			ctx,
			metricBoundary,
			v.config.Telemetry.Metrics.MinimumDataPoints,
			v.config.Telemetry.Metrics.FlushTimeout.Value(),
		)
		if err != nil {
			return fmt.Errorf("wait for correlated OTLP metrics: %w", err)
		}
	}
	snapshot := v.telemetryReceiver.Snapshot(eligibleRequestIDs, signalConfirmedAt)
	v.report.Telemetry.CorrelatedSpans = snapshot.CorrelatedSpans
	v.report.Telemetry.MatchedRequests = snapshot.MatchedRequests
	v.report.Telemetry.ExportRequests = snapshot.ExportRequests
	v.report.Telemetry.RejectedExportRequests = snapshot.RejectedExportRequests
	if v.config.Telemetry.Traces.Enabled {
		v.report.AddEvent(time.Now(), "telemetry", fmt.Sprintf(
			"received %d correlated spans from %d confirmed in-flight requests",
			snapshot.CorrelatedSpans,
			snapshot.MatchedRequests,
		))
	}
	if v.config.Telemetry.Metrics.Enabled {
		metricSnapshot := v.telemetryReceiver.Snapshot(nil, metricBoundary)
		v.report.Telemetry.Metrics.DataPoints = metricSnapshot.MetricDataPoints
		v.report.Telemetry.Metrics.ExportRequests = metricSnapshot.MetricExportRequests
		v.report.Telemetry.RejectedExportRequests = metricSnapshot.RejectedExportRequests
		v.report.AddEvent(time.Now(), "telemetry", fmt.Sprintf(
			"received %d run-correlated metric data points after in-flight work completed",
			metricSnapshot.MetricDataPoints,
		))
	}
	return nil
}

func completedInflightBoundary(signalConfirmedAt time.Time, eligibleRequestIDs []int, results []traffic.Result) time.Time {
	eligible := make(map[int]struct{}, len(eligibleRequestIDs))
	for _, requestID := range eligibleRequestIDs {
		eligible[requestID] = struct{}{}
	}
	boundary := signalConfirmedAt
	for _, result := range results {
		if _, found := eligible[result.ID]; !found {
			continue
		}
		completedAt := result.StartedAt.Add(result.Duration)
		if completedAt.After(boundary) {
			boundary = completedAt
		}
	}
	return boundary
}

func (v *verifier) copySSESnapshot(snapshot streaming.SSESnapshot) {
	summary := &v.report.Streaming.SSE
	summary.Established = snapshot.Established
	summary.Status = snapshot.Status
	summary.ContentType = snapshot.ContentType
	summary.InitialEventReceived = snapshot.InitialEventReceived
	summary.TerminalEventReceived = snapshot.TerminalEventReceived
	summary.Events = snapshot.Events
	summary.CleanEOF = snapshot.CleanEOF
	summary.ErrorKind = snapshot.ErrorKind
	summary.Error = snapshot.Error
}

func (v *verifier) copyWebSocketSnapshot(snapshot streaming.WebSocketSnapshot) {
	summary := &v.report.Streaming.WebSocket
	summary.Established = snapshot.Established
	summary.Status = snapshot.Status
	summary.NegotiatedSubprotocol = snapshot.NegotiatedSubprotocol
	summary.Messages = snapshot.Messages
	summary.TerminalMessageReceived = snapshot.TerminalMessageReceived
	summary.CloseFrameReceived = snapshot.CloseFrameReceived
	summary.CloseCode = snapshot.CloseCode
	summary.CloseReason = snapshot.CloseReason
	summary.ErrorKind = snapshot.ErrorKind
	summary.Error = snapshot.Error
}

func (v *verifier) copyGRPCStreamSnapshot(snapshot grpcprobe.StreamSnapshot) {
	summary := &v.report.Streaming.GRPC
	summary.Established = snapshot.Established
	summary.Messages = snapshot.Messages
	summary.FinalCode = snapshot.FinalCode
	summary.ErrorKind = snapshot.ErrorKind
	summary.Error = snapshot.Error
}

func (v *verifier) waitAndSummarizeSSE(ctx context.Context, run *streaming.SSERun, cancel context.CancelFunc, signalRequestedAt time.Time) error {
	closeDeadline := signalRequestedAt.Add(v.config.Streaming.SSE.CloseTimeout.Value())
	if run.Active() {
		remaining := time.Until(closeDeadline)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-run.Done():
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				cancel()
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		} else {
			cancel()
		}
	}
	select {
	case <-run.Done():
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		cancel()
	}

	snapshot := run.Snapshot()
	v.copySSESnapshot(snapshot)
	summary := &v.report.Streaming.SSE
	if snapshot.ClosedAt.IsZero() {
		summary.ErrorKind = "cancel_timeout"
		summary.Error = "SSE observation did not stop after cancellation"
	} else {
		summary.ClosedAfterSignal = !snapshot.ClosedAt.Before(signalRequestedAt)
		summary.ClosedWithinTimeout = summary.ClosedAfterSignal && !snapshot.ClosedAt.After(closeDeadline)
	}
	terminalObserved := v.config.Streaming.SSE.TerminalEvent == "" || snapshot.TerminalEventReceived
	summary.ClosedGracefully = summary.ActiveAtSignal && summary.ClosedAfterSignal && summary.ClosedWithinTimeout && snapshot.CleanEOF && terminalObserved && snapshot.ErrorKind == ""
	v.report.AddEvent(time.Now(), "streaming", sseClosureMessage(*summary, v.config.Streaming.SSE.TerminalEvent != "", v.config.Streaming.SSE.CloseTimeout.Value()))
	return nil
}

func (v *verifier) waitAndSummarizeWebSocket(ctx context.Context, run *streaming.WebSocketRun, cancel context.CancelFunc, signalRequestedAt time.Time) error {
	closeDeadline := signalRequestedAt.Add(v.config.Streaming.WebSocket.CloseTimeout.Value())
	if run.Active() {
		remaining := time.Until(closeDeadline)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-run.Done():
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				cancel()
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		} else {
			cancel()
		}
	}
	select {
	case <-run.Done():
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		cancel()
	}

	snapshot := run.Snapshot()
	v.copyWebSocketSnapshot(snapshot)
	summary := &v.report.Streaming.WebSocket
	if snapshot.ClosedAt.IsZero() {
		summary.ErrorKind = "cancel_timeout"
		summary.Error = "WebSocket observation did not stop after cancellation"
	} else {
		summary.ClosedAfterSignal = !snapshot.ClosedAt.Before(signalRequestedAt)
		summary.ClosedWithinTimeout = summary.ClosedAfterSignal && !snapshot.ClosedAt.After(closeDeadline)
	}
	if snapshot.TerminalMessageReceived && !snapshot.TerminalMessageReceivedAt.IsZero() {
		summary.TerminalMessageAfterSignal = !snapshot.TerminalMessageReceivedAt.Before(signalRequestedAt)
	}
	terminalObserved := v.config.Streaming.WebSocket.TerminalMessage == "" || summary.TerminalMessageAfterSignal
	summary.ClosedGracefully = summary.ActiveAtSignal && summary.ClosedAfterSignal && summary.ClosedWithinTimeout && summary.CloseFrameReceived && summary.CloseCode == v.config.Streaming.WebSocket.CloseCode && terminalObserved && snapshot.ErrorKind == ""
	v.report.AddEvent(time.Now(), "streaming", webSocketClosureMessage(*summary, v.config.Streaming.WebSocket.TerminalMessage != "", v.config.Streaming.WebSocket.CloseCode, v.config.Streaming.WebSocket.CloseTimeout.Value()))
	return nil
}

func (v *verifier) waitAndSummarizeGRPCStream(ctx context.Context, run *grpcprobe.StreamRun, cancel context.CancelFunc, signalRequestedAt time.Time) error {
	closeDeadline := signalRequestedAt.Add(v.config.Streaming.GRPC.CloseTimeout.Value())
	if run.Active() {
		remaining := time.Until(closeDeadline)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-run.Done():
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				cancel()
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		} else {
			cancel()
		}
	}
	select {
	case <-run.Done():
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		cancel()
	}

	snapshot := run.Snapshot()
	v.copyGRPCStreamSnapshot(snapshot)
	summary := &v.report.Streaming.GRPC
	if snapshot.ClosedAt.IsZero() {
		summary.ErrorKind = "cancel_timeout"
		summary.Error = "gRPC stream observation did not stop after cancellation"
	} else {
		summary.ClosedAfterSignal = !snapshot.ClosedAt.Before(signalRequestedAt)
		summary.ClosedWithinTimeout = summary.ClosedAfterSignal && !snapshot.ClosedAt.After(closeDeadline)
	}
	summary.ClosedGracefully = summary.ActiveAtSignal && summary.ClosedAfterSignal && summary.ClosedWithinTimeout && summary.FinalCode == v.config.Streaming.GRPC.ExpectedCode && snapshot.ErrorKind == ""
	v.report.AddEvent(time.Now(), "streaming", grpcStreamClosureMessage(*summary, v.config.Streaming.GRPC.ExpectedCode, v.config.Streaming.GRPC.CloseTimeout.Value()))
	return nil
}

func sseEstablishmentMessage(snapshot streaming.SSESnapshot, established bool, waitErr error, timeout time.Duration) string {
	if established {
		return fmt.Sprintf("SSE stream established after receiving the initial event (HTTP %d, %d event)", snapshot.Status, snapshot.Events)
	}
	if snapshot.Error != "" {
		return snapshot.Error
	}
	if errors.Is(waitErr, context.DeadlineExceeded) {
		return fmt.Sprintf("initial SSE event was not received within %s", timeout)
	}
	return "SSE stream ended before the initial event was received"
}

func sseClosureMessage(summary report.SSESummary, terminalRequired bool, timeout time.Duration) string {
	terminal := "not required"
	if terminalRequired {
		terminal = fmt.Sprintf("received=%t", summary.TerminalEventReceived)
	}
	message := fmt.Sprintf("SSE closure: active at signal=%t, terminal event %s, clean EOF=%t, closed within %s=%t", summary.ActiveAtSignal, terminal, summary.CleanEOF, timeout, summary.ClosedWithinTimeout)
	if summary.Error != "" {
		message += "; " + summary.Error
	}
	return message
}

func webSocketEstablishmentMessage(snapshot streaming.WebSocketSnapshot, established bool, waitErr error, timeout time.Duration) string {
	if established {
		message := fmt.Sprintf("WebSocket established (HTTP %d)", snapshot.Status)
		if snapshot.NegotiatedSubprotocol != "" {
			message += "; negotiated subprotocol " + snapshot.NegotiatedSubprotocol
		}
		return message
	}
	if snapshot.Error != "" {
		return snapshot.Error
	}
	if errors.Is(waitErr, context.DeadlineExceeded) {
		return fmt.Sprintf("WebSocket opening handshake did not complete within %s", timeout)
	}
	return "WebSocket ended before the opening handshake completed"
}

func webSocketClosureMessage(summary report.WebSocketSummary, terminalRequired bool, expectedCloseCode int, timeout time.Duration) string {
	terminal := "not required"
	if terminalRequired {
		terminal = fmt.Sprintf("received after signal=%t", summary.TerminalMessageAfterSignal)
	}
	message := fmt.Sprintf("WebSocket closure: active at signal=%t, terminal message %s, close frame=%t, close code=%d (expected %d), closed within %s=%t", summary.ActiveAtSignal, terminal, summary.CloseFrameReceived, summary.CloseCode, expectedCloseCode, timeout, summary.ClosedWithinTimeout)
	if summary.Error != "" {
		message += "; " + summary.Error
	}
	return message
}

func grpcStreamEstablishmentMessage(snapshot grpcprobe.StreamSnapshot, established bool, waitErr error, minimumMessages int, timeout time.Duration) string {
	if established {
		return fmt.Sprintf("gRPC server stream established after %d response message(s)", snapshot.Messages)
	}
	if snapshot.Error != "" {
		return snapshot.Error
	}
	if errors.Is(waitErr, context.DeadlineExceeded) {
		return fmt.Sprintf("gRPC stream did not receive %d response message(s) within %s", minimumMessages, timeout)
	}
	return "gRPC stream ended before the establishment requirement was met"
}

func grpcStreamClosureMessage(summary report.GRPCStreamSummary, expectedCode string, timeout time.Duration) string {
	message := fmt.Sprintf("gRPC stream closure: active at signal=%t, messages=%d, final code=%s (expected %s), closed within %s=%t", summary.ActiveAtSignal, summary.Messages, summary.FinalCode, expectedCode, timeout, summary.ClosedWithinTimeout)
	if summary.ErrorKind != "" {
		message += fmt.Sprintf("; %s: %s", summary.ErrorKind, summary.Error)
	}
	return message
}

func (v *verifier) evaluateAssertions(exited bool, state containerruntime.ContainerState, withdrawn withdrawalResult, results []traffic.Result, inflight []int) {
	v.report.AddAssertion("traffic.inflight_exercised", len(inflight) > 0, fmt.Sprintf("%d requests were active when signal delivery was confirmed", len(inflight)))
	v.report.AddAssertion("readiness.withdrawn", withdrawn.withdrawn, withdrawn.message)
	v.report.AddAssertion(
		"traffic.failed_requests",
		v.report.Traffic.Failed <= v.config.Assertions.MaxFailedRequests,
		trafficFailureMessage(results, v.report.Traffic.Failed, v.config.Assertions.MaxFailedRequests),
	)
	if v.config.Assertions.InflightRequestsComplete {
		v.report.AddAssertion(
			"traffic.inflight_complete",
			v.report.Traffic.InflightBad == 0,
			fmt.Sprintf("%d of %d in-flight requests failed or did not complete", v.report.Traffic.InflightBad, len(inflight)),
		)
	}
	v.evaluatePostSignalAssertion()
	v.evaluateStreamingAssertions()
	v.evaluateTelemetryAssertions()
	v.report.AddAssertion("shutdown.deadline", exited, fmt.Sprintf("container did not exit within %s", v.config.Shutdown.Deadline))
	v.report.AddAssertion(
		"shutdown.exit_code",
		exited && state.ExitCode == v.config.Assertions.ExitCode,
		exitCodeMessage(exited, state.ExitCode, v.config.Assertions.ExitCode),
	)
	v.report.AddAssertion("shutdown.oom", !state.OOMKilled, "container was OOM-killed")
	if v.config.Assertions.ForbidForceKill {
		v.report.AddAssertion("shutdown.force_kill", !v.report.ForcedCleanup, "forced cleanup was required")
	}
}

func trafficFailureMessage(results []traffic.Result, failed, maximum int) string {
	message := fmt.Sprintf("%d failed requests; maximum allowed is %d", failed, maximum)
	if failed == 0 {
		return message
	}
	counts := make(map[string]int)
	for _, result := range results {
		if result.Success {
			continue
		}
		kind := result.ErrorKind
		if result.GRPCCode != "" {
			kind = "gRPC " + result.GRPCCode
		} else if result.Status != 0 {
			kind = fmt.Sprintf("HTTP %d", result.Status)
		}
		if kind == "" {
			kind = "incomplete"
		}
		counts[kind]++
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	const maxKinds = 8
	details := make([]string, 0, min(len(kinds), maxKinds))
	for _, kind := range kinds[:min(len(kinds), maxKinds)] {
		details = append(details, fmt.Sprintf("%s=%d", kind, counts[kind]))
	}
	if len(kinds) > maxKinds {
		details = append(details, fmt.Sprintf("%d other kinds", len(kinds)-maxKinds))
	}
	if len(details) > 0 {
		message += "; " + strings.Join(details, ", ")
	}
	return message
}

func (v *verifier) evaluateStreamingAssertions() {
	if v.config.Streaming.SSE.Enabled {
		summary := v.report.Streaming.SSE
		v.report.AddAssertion(
			"stream.active_at_signal",
			summary.ActiveAtSignal,
			fmt.Sprintf("SSE stream was active when the %s request began: %t", v.config.Shutdown.Signal, summary.ActiveAtSignal),
		)
		v.report.AddAssertion(
			"stream.closed_gracefully",
			summary.ClosedGracefully,
			sseClosureMessage(summary, v.config.Streaming.SSE.TerminalEvent != "", v.config.Streaming.SSE.CloseTimeout.Value()),
		)
	}
	if v.config.Streaming.WebSocket.Enabled {
		summary := v.report.Streaming.WebSocket
		v.report.AddAssertion(
			"websocket.active_at_signal",
			summary.ActiveAtSignal,
			fmt.Sprintf("WebSocket was active when the %s request began: %t", v.config.Shutdown.Signal, summary.ActiveAtSignal),
		)
		v.report.AddAssertion(
			"websocket.closed_gracefully",
			summary.ClosedGracefully,
			webSocketClosureMessage(summary, v.config.Streaming.WebSocket.TerminalMessage != "", v.config.Streaming.WebSocket.CloseCode, v.config.Streaming.WebSocket.CloseTimeout.Value()),
		)
	}
	if v.config.Streaming.GRPC.Enabled {
		summary := v.report.Streaming.GRPC
		v.report.AddAssertion(
			"grpc_stream.active_at_signal",
			summary.ActiveAtSignal,
			fmt.Sprintf("gRPC stream was active when the %s request began: %t", v.config.Shutdown.Signal, summary.ActiveAtSignal),
		)
		v.report.AddAssertion(
			"grpc_stream.closed_gracefully",
			summary.ClosedGracefully,
			grpcStreamClosureMessage(summary, v.config.Streaming.GRPC.ExpectedCode, v.config.Streaming.GRPC.CloseTimeout.Value()),
		)
	}
}

func (v *verifier) evaluateTelemetryAssertions() {
	summary := v.report.Telemetry
	if v.config.Telemetry.Traces.Enabled {
		minimum := v.config.Telemetry.Traces.MinimumCorrelatedSpans
		passed := summary.CorrelatedSpans >= minimum
		v.report.AddAssertion(
			"telemetry.spans_exported",
			passed,
			fmt.Sprintf(
				"received %d correlated spans from %d of %d confirmed in-flight requests; minimum required is %d; %d OTLP exports were rejected",
				summary.CorrelatedSpans,
				summary.MatchedRequests,
				summary.EligibleInflight,
				minimum,
				summary.RejectedExportRequests,
			),
		)
	}
	if v.config.Telemetry.Metrics.Enabled {
		minimum := v.config.Telemetry.Metrics.MinimumDataPoints
		passed := summary.Metrics.DataPoints >= minimum
		v.report.AddAssertion(
			"telemetry.metrics_exported",
			passed,
			fmt.Sprintf(
				"received %d run-correlated metric data points after in-flight work completed; minimum required is %d; %d OTLP exports were rejected",
				summary.Metrics.DataPoints,
				minimum,
				summary.RejectedExportRequests,
			),
		)
	}
}

func (v *verifier) evaluatePostSignalAssertion() {
	summary := v.report.Traffic.PostSignal
	switch v.config.Traffic.PostSignal.Policy {
	case config.PostSignalDisabled:
		return
	case config.PostSignalAccept:
		passed := summary.Completed == summary.Configured && summary.Accepted == summary.Configured
		v.report.AddAssertion(
			"traffic.post_signal_policy",
			passed,
			fmt.Sprintf("accepted %d of %d post-signal requests; %d completed and %d were rejected", summary.Accepted, summary.Configured, summary.Completed, summary.Rejected),
		)
	case config.PostSignalReject:
		passed := summary.Completed == summary.Configured && summary.Rejected == summary.Configured
		v.report.AddAssertion(
			"traffic.post_signal_policy",
			passed,
			fmt.Sprintf("rejected %d of %d post-signal requests; %d completed and %d were accepted", summary.Rejected, summary.Configured, summary.Completed, summary.Accepted),
		)
	}
}

func (v *verifier) collectEvidence(ctx context.Context) error {
	if !v.created {
		return nil
	}
	state, inspectErr := v.runtime.Inspect(ctx, v.container.ID)
	if inspectErr == nil {
		v.lastState = state
		v.report.Container.Status = state.Status
		v.report.Container.ExitCode = state.ExitCode
		v.report.Container.OOMKilled = state.OOMKilled
		v.report.Container.Error = state.Error
	}
	logs, logsErr := v.runtime.Logs(ctx, v.container.ID, v.options.LogLimit)
	if logsErr == nil {
		v.report.Logs = boundLogs(logs, v.options.LogLimit)
	}
	if inspectErr != nil {
		return inspectErr
	}
	return logsErr
}

func boundLogs(logs []byte, limit int64) string {
	if limit <= 0 {
		return ""
	}
	if int64(len(logs)) > limit {
		logs = logs[:limit]
	}
	return string(logs)
}

func (v *verifier) cleanup(ctx context.Context) error {
	if !v.created {
		return nil
	}
	if v.options.KeepOnFailure && !v.report.Passed {
		v.report.Retained = v.container.Name
		v.report.AddEvent(time.Now(), "cleanup", "container retained for debugging")
		return nil
	}
	if err := v.runtime.Remove(ctx, v.container.ID, true); err != nil {
		return err
	}
	v.report.AddEvent(time.Now(), "cleanup", "container removed")
	return nil
}

type withdrawalResult struct {
	withdrawn bool
	message   string
	at        time.Time
}

func waitWithdrawn(ctx context.Context, checker readiness.Checker, interval time.Duration, signalAt time.Time) withdrawalResult {
	last := "readiness was still ready"
	for {
		probeBudget := time.Second
		probeCtx, cancelProbe := context.WithTimeout(ctx, probeBudget)
		observation := checker.Check(probeCtx)
		cancelProbe()
		if observation.Err != nil {
			if ctx.Err() == nil {
				return withdrawalResult{withdrawn: true, message: "readiness check stopped succeeding: " + observation.Description, at: time.Now()}
			}
		} else if !observation.Ready {
			return withdrawalResult{withdrawn: true, message: fmt.Sprintf("readiness returned %s after %s", observation.Description, time.Since(signalAt).Round(time.Millisecond)), at: time.Now()}
		} else {
			last = "readiness still returned " + observation.Description
		}

		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return withdrawalResult{message: last + " when the withdrawal deadline expired", at: time.Now()}
		}
	}
}

func makeRunID() (string, error) {
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func exitCodeMessage(exited bool, actual, expected int) string {
	if !exited {
		return fmt.Sprintf("container did not exit; expected exit code %d", expected)
	}
	return fmt.Sprintf("exit code was %d; expected %d", actual, expected)
}

func cloneStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+3)
	for key, value := range source {
		result[key] = value
	}
	return result
}
