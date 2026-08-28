//lint:file-ignore SA1019 gRPC reflection v1alpha is a deliberate compatibility fallback for older servers.

package grpcprobe

import (
	"context"
	"errors"
	"fmt"

	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

func (c *Client) reflectV1Alpha(ctx context.Context, serviceName string) ([][]byte, error) {
	client := reflectionv1alpha.NewServerReflectionClient(c.connection)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("start gRPC reflection v1alpha: %w", err)
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: serviceName},
	}); err != nil {
		return nil, fmt.Errorf("request gRPC service descriptor with reflection v1alpha: %w", err)
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("receive gRPC service descriptor with reflection v1alpha: %w", err)
	}
	if reflectionError := response.GetErrorResponse(); reflectionError != nil {
		return nil, fmt.Errorf("gRPC reflection v1alpha rejected %q: code %d: %s", serviceName, reflectionError.ErrorCode, reflectionError.ErrorMessage)
	}
	fileResponse := response.GetFileDescriptorResponse()
	if fileResponse == nil || len(fileResponse.FileDescriptorProto) == 0 {
		return nil, errors.New("gRPC reflection v1alpha returned no file descriptors")
	}
	return fileResponse.FileDescriptorProto, nil
}
