package grpcprobe

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestUnaryTrafficUsesReflectionAndTracksActiveCalls(t *testing.T) {
	client, fixture := testClient(t)
	call, err := client.Prepare(context.Background(), "grpc.health.v1.Health/Check", nil, []byte(`{"service":"slow"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := ParseCodes([]string{"OK"})
	if err != nil {
		t.Fatal(err)
	}
	run := StartUnary(context.Background(), UnarySpec{
		Client: client, Call: call, Metadata: map[string]string{"x-draincheck": "unit"},
		ExpectedCodes: expected, Count: 2, Concurrency: 2, Timeout: time.Second,
	})
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := run.WaitStarted(waitCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.checkStarted:
	case <-time.After(time.Second):
		t.Fatal("server did not receive the unary call")
	}
	if active := run.ActiveSnapshot(); len(active) == 0 {
		t.Fatal("no unary calls were active")
	}
	close(fixture.releaseChecks)
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("unary traffic did not finish")
	}
	results := run.Results()
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	for _, result := range results {
		if !result.Success || result.GRPCCode != "OK" || result.ErrorKind != "" {
			t.Fatalf("result = %#v", result)
		}
	}
}

func TestServerStreamUsesDescriptorSetAndRecordsFinalCode(t *testing.T) {
	client, fixture := testClient(t)
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto),
	}}
	descriptorBytes, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	call, err := client.Prepare(context.Background(), "/grpc.health.v1.Health/Watch", descriptorBytes, []byte(`{"service":"stream"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := StartStream(ctx, StreamSpec{Client: client, Call: call, MinimumMessages: 1})
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	snapshot, established := run.WaitEstablished(waitCtx)
	if !established || snapshot.Messages != 1 || !run.Active() {
		t.Fatalf("establishment = %#v, active=%t", snapshot, run.Active())
	}
	close(fixture.releaseStream)
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("stream did not finish")
	}
	snapshot = run.Snapshot()
	if snapshot.Messages != 2 || snapshot.FinalCode != "OK" || snapshot.ErrorKind != "" || snapshot.ClosedAt.IsZero() {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPrepareRejectsWrongMethodKindAndRequest(t *testing.T) {
	client, _ := testClient(t)
	if _, err := client.Prepare(context.Background(), "grpc.health.v1.Health/Watch", nil, []byte(`{}`), false); err == nil {
		t.Fatal("server-streaming method was accepted as unary")
	}
	if _, err := client.Prepare(context.Background(), "grpc.health.v1.Health/Check", nil, []byte(`{"unknown":true}`), false); err == nil {
		t.Fatal("unknown request field was accepted")
	}
}

func TestCanonicalStatusCodeNames(t *testing.T) {
	codes, err := ParseCodes([]string{"OK", "CANCELLED", "UNAVAILABLE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 3 || CodeName(codes[1]) != "CANCELLED" {
		t.Fatalf("codes = %#v", codes)
	}
}

type grpcFixture struct {
	healthpb.UnimplementedHealthServer
	checkStarted  chan struct{}
	releaseChecks chan struct{}
	releaseStream chan struct{}
}

func (f *grpcFixture) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	select {
	case f.checkStarted <- struct{}{}:
	default:
	}
	<-f.releaseChecks
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func (f *grpcFixture) Watch(_ *healthpb.HealthCheckRequest, stream grpc.ServerStreamingServer[healthpb.HealthCheckResponse]) error {
	if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}); err != nil {
		return err
	}
	<-f.releaseStream
	return stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING})
}

func (f *grpcFixture) List(context.Context, *healthpb.HealthListRequest) (*healthpb.HealthListResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func testClient(t *testing.T) (*Client, *grpcFixture) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	fixture := &grpcFixture{
		checkStarted: make(chan struct{}, 2), releaseChecks: make(chan struct{}), releaseStream: make(chan struct{}),
	}
	healthpb.RegisterHealthServer(server, fixture)
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return &Client{connection: connection}, fixture
}
