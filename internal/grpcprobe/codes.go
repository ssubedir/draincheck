package grpcprobe

import (
	"fmt"

	"google.golang.org/grpc/codes"
)

var configuredCodes = map[string]codes.Code{
	"OK": codes.OK, "CANCELLED": codes.Canceled, "UNKNOWN": codes.Unknown,
	"INVALID_ARGUMENT": codes.InvalidArgument, "DEADLINE_EXCEEDED": codes.DeadlineExceeded,
	"NOT_FOUND": codes.NotFound, "ALREADY_EXISTS": codes.AlreadyExists,
	"PERMISSION_DENIED": codes.PermissionDenied, "RESOURCE_EXHAUSTED": codes.ResourceExhausted,
	"FAILED_PRECONDITION": codes.FailedPrecondition, "ABORTED": codes.Aborted,
	"OUT_OF_RANGE": codes.OutOfRange, "UNIMPLEMENTED": codes.Unimplemented,
	"INTERNAL": codes.Internal, "UNAVAILABLE": codes.Unavailable, "DATA_LOSS": codes.DataLoss,
	"UNAUTHENTICATED": codes.Unauthenticated,
}

func ParseCodes(values []string) ([]codes.Code, error) {
	result := make([]codes.Code, 0, len(values))
	for _, value := range values {
		code, found := configuredCodes[value]
		if !found {
			return nil, fmt.Errorf("unknown gRPC status code %q", value)
		}
		result = append(result, code)
	}
	return result, nil
}

func CodeName(value codes.Code) string {
	for name, code := range configuredCodes {
		if code == value {
			return name
		}
	}
	return "UNKNOWN"
}

func codeAccepted(value codes.Code, expected []codes.Code) bool {
	for _, candidate := range expected {
		if value == candidate {
			return true
		}
	}
	return false
}
