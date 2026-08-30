package grpcprobe

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const maxReceiveMessageBytes = 1 << 20

// Client owns one plaintext gRPC connection to the mapped container port.
type Client struct {
	connection *grpc.ClientConn
}

// Call contains a resolved method descriptor and a validated dynamic request.
type Call struct {
	fullMethod string
	method     protoreflect.MethodDescriptor
	request    proto.Message
}

func NewClient(target string) (*Client, error) {
	connection, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxReceiveMessageBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("create gRPC client: %w", err)
	}
	return &Client{connection: connection}, nil
}

func (c *Client) Close() error { return c.connection.Close() }

// Prepare resolves a method from a descriptor set or server reflection and validates its request.
func (c *Client) Prepare(ctx context.Context, methodName string, descriptorSet, requestJSON []byte, serverStreaming bool) (Call, error) {
	serviceName, method, err := splitMethod(methodName)
	if err != nil {
		return Call{}, err
	}
	files, err := c.descriptors(ctx, serviceName, descriptorSet)
	if err != nil {
		return Call{}, err
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return Call{}, fmt.Errorf("resolve gRPC service %q: %w", serviceName, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return Call{}, fmt.Errorf("gRPC symbol %q is not a service", serviceName)
	}
	methodDescriptor := service.Methods().ByName(protoreflect.Name(method))
	if methodDescriptor == nil {
		return Call{}, fmt.Errorf("gRPC service %q has no method %q", serviceName, method)
	}
	if methodDescriptor.IsStreamingClient() {
		return Call{}, errors.New("client-streaming and bidirectional gRPC methods are not supported")
	}
	if methodDescriptor.IsStreamingServer() != serverStreaming {
		kind := "unary"
		if serverStreaming {
			kind = "server-streaming"
		}
		return Call{}, fmt.Errorf("gRPC method %q is not %s", strings.TrimPrefix(methodName, "/"), kind)
	}
	request := dynamicpb.NewMessage(methodDescriptor.Input())
	if len(requestJSON) == 0 {
		requestJSON = []byte("{}")
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(requestJSON, request); err != nil {
		return Call{}, fmt.Errorf("decode request JSON for gRPC method %q: %w", strings.TrimPrefix(methodName, "/"), err)
	}
	return Call{
		fullMethod: "/" + serviceName + "/" + method,
		method:     methodDescriptor,
		request:    request,
	}, nil
}

func (c *Client) descriptors(ctx context.Context, serviceName string, descriptorSet []byte) (*protoregistry.Files, error) {
	if len(descriptorSet) > 0 {
		set := &descriptorpb.FileDescriptorSet{}
		if err := proto.Unmarshal(descriptorSet, set); err != nil {
			return nil, fmt.Errorf("decode gRPC descriptor set: %w", err)
		}
		files, err := protodesc.NewFiles(set)
		if err != nil {
			return nil, fmt.Errorf("load gRPC descriptor set: %w", err)
		}
		return files, nil
	}

	rawDescriptors, v1Err := c.reflectV1(ctx, serviceName)
	if v1Err != nil {
		var alphaErr error
		rawDescriptors, alphaErr = c.reflectV1Alpha(ctx, serviceName)
		if alphaErr != nil {
			return nil, fmt.Errorf("resolve gRPC descriptors with reflection v1 or v1alpha: %w", errors.Join(v1Err, alphaErr))
		}
	}
	return filesFromReflection(rawDescriptors)
}

func (c *Client) reflectV1(ctx context.Context, serviceName string) ([][]byte, error) {
	client := reflectionv1.NewServerReflectionClient(c.connection)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("start gRPC reflection v1: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()
	if err := stream.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: serviceName},
	}); err != nil {
		return nil, fmt.Errorf("request gRPC service descriptor with reflection v1: %w", err)
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("receive gRPC service descriptor with reflection v1: %w", err)
	}
	if reflectionError := response.GetErrorResponse(); reflectionError != nil {
		return nil, fmt.Errorf("gRPC reflection v1 rejected %q: code %d: %s", serviceName, reflectionError.ErrorCode, reflectionError.ErrorMessage)
	}
	fileResponse := response.GetFileDescriptorResponse()
	if fileResponse == nil || len(fileResponse.FileDescriptorProto) == 0 {
		return nil, errors.New("gRPC reflection v1 returned no file descriptors")
	}
	return fileResponse.FileDescriptorProto, nil
}

func filesFromReflection(rawDescriptors [][]byte) (*protoregistry.Files, error) {
	set := &descriptorpb.FileDescriptorSet{File: make([]*descriptorpb.FileDescriptorProto, 0, len(rawDescriptors))}
	seen := make(map[string]struct{})
	for _, raw := range rawDescriptors {
		file := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, file); err != nil {
			return nil, fmt.Errorf("decode reflected gRPC descriptor: %w", err)
		}
		if _, found := seen[file.GetName()]; found {
			continue
		}
		seen[file.GetName()] = struct{}{}
		set.File = append(set.File, file)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("load reflected gRPC descriptors: %w", err)
	}
	return files, nil
}

func splitMethod(value string) (string, string, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	service, method, found := strings.Cut(value, "/")
	if !found || service == "" || method == "" || strings.Contains(method, "/") {
		return "", "", errors.New("gRPC method must use package.Service/Method form")
	}
	return service, method, nil
}
